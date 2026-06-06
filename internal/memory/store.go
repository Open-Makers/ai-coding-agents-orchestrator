package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/embedder"
)

// Store is the persistent FTS5/BM25 (+ optional vector) index over Markdown
// memory files in a single workspace.
type Store struct {
	db            *sql.DB
	mu            sync.Mutex
	emb           embedder.Embedder // optional; nil means BM25-only
	root          string            // memory dir absolute path
	chunkTokens   int
	overlapTokens int
}

// Options configure Open.
type Options struct {
	// Embedder is optional. When non-nil, Store computes and persists dense
	// vectors for every chunk and Search can perform hybrid ranking.
	Embedder embedder.Embedder
	// ChunkTokens is the target chunk size in tokens (default 400).
	ChunkTokens int
	// OverlapTokens is the overlap between chunks (default 80).
	OverlapTokens int
}

func (o Options) withDefaults() Options {
	if o.ChunkTokens <= 0 {
		o.ChunkTokens = 400
	}
	if o.OverlapTokens < 0 || o.OverlapTokens >= o.ChunkTokens {
		o.OverlapTokens = 80
	}
	return o
}

// Open opens (creating if necessary) the SQLite database at dbPath. memDir
// is the workspace memory root (used to resolve relative paths during reindex).
func Open(dbPath, memDir string, opts Options) (*Store, error) {
	opts = opts.withDefaults()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
		return nil, fmt.Errorf("memory: mkdir: %w", err)
	}
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout=5000&_pragma=journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("memory: open db: %w", err)
	}
	db.SetMaxOpenConns(1) // serialise writes; modernc/sqlite is goroutine-safe but FTS5 contention is real

	s := &Store{db: db, emb: opts.Embedder, root: memDir, chunkTokens: opts.ChunkTokens, overlapTokens: opts.OverlapTokens}
	if err := s.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return s, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) initSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS files(
			path TEXT PRIMARY KEY,
			mtime_ns INTEGER NOT NULL,
			hash TEXT NOT NULL,
			indexed_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS chunks(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			file TEXT NOT NULL,
			ord INTEGER NOT NULL,
			start_line INTEGER NOT NULL,
			end_line INTEGER NOT NULL,
			tokens INTEGER NOT NULL,
			body TEXT NOT NULL,
			FOREIGN KEY(file) REFERENCES files(path) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_file ON chunks(file)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
			body,
			content='chunks',
			content_rowid='id',
			tokenize='unicode61'
		)`,
		`CREATE TRIGGER IF NOT EXISTS chunks_ai AFTER INSERT ON chunks BEGIN
			INSERT INTO chunks_fts(rowid, body) VALUES (new.id, new.body);
		END`,
		`CREATE TRIGGER IF NOT EXISTS chunks_ad AFTER DELETE ON chunks BEGIN
			INSERT INTO chunks_fts(chunks_fts, rowid, body) VALUES('delete', old.id, old.body);
		END`,
		`CREATE TRIGGER IF NOT EXISTS chunks_au AFTER UPDATE ON chunks BEGIN
			INSERT INTO chunks_fts(chunks_fts, rowid, body) VALUES('delete', old.id, old.body);
			INSERT INTO chunks_fts(rowid, body) VALUES (new.id, new.body);
		END`,
		`CREATE TABLE IF NOT EXISTS embeddings(
			chunk_id INTEGER PRIMARY KEY,
			dim INTEGER NOT NULL,
			model TEXT NOT NULL,
			vec BLOB NOT NULL,
			FOREIGN KEY(chunk_id) REFERENCES chunks(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("memory: init schema: %w (stmt: %s)", err, firstLine(q))
		}
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// chunkConfig returns chunking sizes set at Open time.
func (s *Store) chunkConfig() (int, int) {
	if s.chunkTokens == 0 {
		return 400, 80
	}
	return s.chunkTokens, s.overlapTokens
}

// IndexFile reindexes a single memory file. relPath is workspace-memory-root
// relative (e.g. "MEMORY.md", "daily/2026-05-19.md"). Returns true if the
// file was (re)indexed, false if it was already up-to-date.
func (s *Store) IndexFile(ctx context.Context, relPath string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	abs := filepath.Join(s.root, relPath)
	st, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// File deleted — drop from index.
			return false, s.deleteFile(relPath)
		}
		return false, fmt.Errorf("memory: stat %s: %w", relPath, err)
	}
	if st.IsDir() {
		return false, nil
	}

	body, err := os.ReadFile(abs) // #nosec G304 -- caller controls memory dir
	if err != nil {
		return false, fmt.Errorf("memory: read %s: %w", relPath, err)
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	mtime := st.ModTime().UnixNano()

	var prevHash string
	var prevMtime int64
	row := s.db.QueryRowContext(ctx, `SELECT mtime_ns, hash FROM files WHERE path = ?`, relPath)
	if err := row.Scan(&prevMtime, &prevHash); err == nil {
		if prevHash == hash {
			return false, nil
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks WHERE file = ?`, relPath); err != nil {
		return false, fmt.Errorf("memory: delete chunks: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO files(path, mtime_ns, hash, indexed_at) VALUES(?,?,?,?)
		 ON CONFLICT(path) DO UPDATE SET mtime_ns=excluded.mtime_ns, hash=excluded.hash, indexed_at=excluded.indexed_at`,
		relPath, mtime, hash, time.Now().Unix()); err != nil {
		return false, fmt.Errorf("memory: upsert file: %w", err)
	}

	ct, ot := s.chunkConfig()
	chunks := ChunkMarkdown(string(body), ct, ot)

	insertedIDs := make([]int64, 0, len(chunks))
	insertStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO chunks(file, ord, start_line, end_line, tokens, body) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		return false, err
	}
	defer func() { _ = insertStmt.Close() }()

	for _, c := range chunks {
		res, err := insertStmt.ExecContext(ctx, relPath, c.Ord, c.StartLine, c.EndLine, c.Tokens, c.Body)
		if err != nil {
			return false, fmt.Errorf("memory: insert chunk: %w", err)
		}
		id, _ := res.LastInsertId()
		insertedIDs = append(insertedIDs, id)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("memory: commit: %w", err)
	}

	// Embeddings (best-effort, outside the tx). Failure logs but doesn't undo
	// the BM25 index.
	if s.emb != nil && len(chunks) > 0 {
		if err := s.embedChunks(ctx, insertedIDs, chunks); err != nil {
			// non-fatal — BM25 still works
			return true, fmt.Errorf("memory: embed (non-fatal): %w", err)
		}
	}
	return true, nil
}

func (s *Store) deleteFile(relPath string) error {
	_, err := s.db.Exec(`DELETE FROM files WHERE path = ?`, relPath)
	return err
}

func (s *Store) embedChunks(ctx context.Context, ids []int64, chunks []Chunk) error {
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Body
	}
	vecs, err := s.emb.Embed(ctx, texts)
	if err != nil {
		return err
	}
	if len(vecs) != len(ids) {
		return fmt.Errorf("embedder returned %d vectors, expected %d", len(vecs), len(ids))
	}
	model := embedderModelName(s.emb)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO embeddings(chunk_id, dim, model, vec) VALUES(?,?,?,?)
		 ON CONFLICT(chunk_id) DO UPDATE SET dim=excluded.dim, model=excluded.model, vec=excluded.vec`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for i, v := range vecs {
		if _, err := stmt.ExecContext(ctx, ids[i], len(v), model, encodeVec(v)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Search runs a hybrid BM25 + (optional) cosine search.
//
// Strategy:
//  1. SELECT top RerankCandidates by BM25 from FTS5.
//  2. If embedder is available, fetch their vectors, compute cosine vs the
//     query embedding, and blend: score = α·bm25_norm + (1-α)·cosine.
//  3. Truncate combined body to MaxChars (best-first).
func (s *Store) Search(ctx context.Context, query string, opts SearchOptions) ([]Hit, error) {
	opts = opts.withDefaults()
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}

	candidates, err := s.bm25Candidates(ctx, q, opts.RerankCandidates)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	useEmb := s.emb != nil && opts.HybridAlpha < 1.0
	if useEmb {
		qvec, err := s.emb.Embed(ctx, []string{q})
		if err == nil && len(qvec) == 1 && len(qvec[0]) > 0 {
			s.rerankWithCosine(ctx, candidates, qvec[0], opts.HybridAlpha)
		} else {
			// embedder failed — degrade to pure BM25 score
			for i := range candidates {
				candidates[i].Score = candidates[i].BM25
			}
		}
	} else {
		for i := range candidates {
			candidates[i].Score = candidates[i].BM25
		}
	}

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })
	if len(candidates) > opts.K {
		candidates = candidates[:opts.K]
	}
	if opts.MaxChars > 0 {
		candidates = capByChars(candidates, opts.MaxChars)
	}
	return candidates, nil
}

func (s *Store) bm25Candidates(ctx context.Context, query string, k int) ([]Hit, error) {
	// FTS5 MATCH uses its own query syntax. We escape user input by quoting
	// each token to make it a phrase, which avoids syntax errors from `:` etc.
	matchQuery := buildFTSQuery(query)
	if matchQuery == "" {
		return nil, nil
	}
	const sqlQ = `
		SELECT c.id, c.file, c.start_line, c.end_line, c.body, bm25(chunks_fts) AS rank
		FROM chunks_fts
		JOIN chunks c ON c.id = chunks_fts.rowid
		WHERE chunks_fts MATCH ?
		ORDER BY rank ASC
		LIMIT ?`
	rows, err := s.db.QueryContext(ctx, sqlQ, matchQuery, k)
	if err != nil {
		// Likely malformed MATCH expression — return empty hits rather than fail.
		return nil, nil //nolint:nilerr // intentional swallow
	}
	defer func() { _ = rows.Close() }()
	var out []Hit
	// bm25() returns lower=better; normalise to a 0..1 score where higher=better.
	var rawRanks []float64
	var hits []Hit
	for rows.Next() {
		var id int64
		var file, body string
		var start, end int
		var rank float64
		if err := rows.Scan(&id, &file, &start, &end, &body, &rank); err != nil {
			return nil, err
		}
		hits = append(hits, Hit{File: file, StartLine: start, EndLine: end, Body: body})
		rawRanks = append(rawRanks, rank)
		_ = id
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out = normaliseBM25(hits, rawRanks)
	return out, nil
}

func normaliseBM25(hits []Hit, ranks []float64) []Hit {
	if len(hits) == 0 {
		return hits
	}
	// bm25() in sqlite returns negative numbers; smaller (more negative) = better.
	// Convert by min-max normalising the negated values.
	negs := make([]float64, len(ranks))
	minV, maxV := math.Inf(1), math.Inf(-1)
	for i, r := range ranks {
		v := -r
		negs[i] = v
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	span := maxV - minV
	for i := range hits {
		if span > 0 {
			hits[i].BM25 = (negs[i] - minV) / span
		} else {
			hits[i].BM25 = 1.0
		}
	}
	return hits
}

func (s *Store) rerankWithCosine(ctx context.Context, hits []Hit, qvec []float32, alpha float64) {
	// Fetch vectors for these hits (by file+line range — chunk IDs would be
	// more direct but Hit doesn't carry them; we re-resolve by file/start_line).
	ids := make([]int64, 0, len(hits))
	idIndex := make(map[int64]int)
	for i, h := range hits {
		var id int64
		err := s.db.QueryRowContext(ctx,
			`SELECT id FROM chunks WHERE file = ? AND start_line = ? LIMIT 1`,
			h.File, h.StartLine).Scan(&id)
		if err != nil {
			continue
		}
		ids = append(ids, id)
		idIndex[id] = i
	}
	if len(ids) == 0 {
		for i := range hits {
			hits[i].Score = hits[i].BM25
		}
		return
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, v := range ids {
		args[i] = v
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT chunk_id, dim, vec FROM embeddings WHERE chunk_id IN (`+placeholders+`)`, // #nosec G202 -- placeholders is a generated list of bind markers (?,?,...), not user data
		args...)
	if err != nil {
		for i := range hits {
			hits[i].Score = hits[i].BM25
		}
		return
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var dim int
		var raw []byte
		if err := rows.Scan(&id, &dim, &raw); err != nil {
			continue
		}
		idx, ok := idIndex[id]
		if !ok {
			continue
		}
		vec := decodeVec(raw, dim)
		hits[idx].Cosine = cosine(qvec, vec)
	}
	for i := range hits {
		hits[i].Score = alpha*hits[i].BM25 + (1-alpha)*hits[i].Cosine
	}
}

func capByChars(hits []Hit, maxChars int) []Hit {
	total := 0
	for i, h := range hits {
		total += len(h.Body)
		if total > maxChars {
			return hits[:i]
		}
	}
	return hits
}

// AllFiles returns every memory file currently indexed.
func (s *Store) AllFiles(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path FROM files ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Stats reports basic counts for diagnostics.
type Stats struct {
	Files      int
	Chunks     int
	Embeddings int
}

// Stats reports basic counts for diagnostics.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files`).Scan(&st.Files); err != nil {
		return st, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks`).Scan(&st.Chunks); err != nil {
		return st, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM embeddings`).Scan(&st.Embeddings); err != nil {
		return st, err
	}
	return st, nil
}

func buildFTSQuery(q string) string {
	// Tokenise on whitespace; wrap each token in double quotes (FTS5 phrase)
	// to make the query robust against punctuation. Tokens shorter than 2
	// chars are dropped.
	fields := strings.Fields(q)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimFunc(f, func(r rune) bool { return !isWord(r) })
		if len(f) < 2 {
			continue
		}
		out = append(out, `"`+strings.ReplaceAll(f, `"`, ``)+`"`)
	}
	return strings.Join(out, " OR ")
}

func isWord(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
}

func encodeVec(v []float32) []byte {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func decodeVec(b []byte, dim int) []float32 {
	if dim <= 0 || len(b) < 4*dim {
		return nil
	}
	out := make([]float32, dim)
	for i := 0; i < dim; i++ {
		bits := binary.LittleEndian.Uint32(b[i*4:])
		out[i] = math.Float32frombits(bits)
	}
	return out
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		af := float64(a[i])
		bf := float64(b[i])
		dot += af * bf
		na += af * af
		nb += bf * bf
	}
	if na == 0 || nb == 0 {
		return 0
	}
	v := dot / (math.Sqrt(na) * math.Sqrt(nb))
	// cosine is in [-1, 1]; map to [0, 1] for blending with BM25.
	return (v + 1) / 2
}

// embedderModelName best-effort introspection so we can store which model
// produced each vector (helpful for invalidation on model change).
func embedderModelName(e embedder.Embedder) string {
	if e == nil {
		return ""
	}
	type named interface{ Name() string }
	if n, ok := e.(named); ok {
		return n.Name()
	}
	return "unknown"
}
