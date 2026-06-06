package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/chunker"
	pkgcontext "github.com/Open-Makers/ai-coding-agents-orchestrator/internal/context"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/logging"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/safefile"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tokenutil"
)

// Selection reasons recorded per included file, used for instrumentation so
// the source-context pipeline can be measured and compared across agents.
const (
	reasonSeed   = "seed"
	reasonImport = "import"
	reasonCaller = "caller"
	reasonFile   = "file"
)

// maxReviewFileSize limits individual file size in review context.
const maxReviewFileSize = 6000

// maxReviewTotalContext caps total source context for review agents.
const maxReviewTotalContext = 40000

// buildCompactSourceContext reads source files from disk and formats them as
// a compact block for review agents. When seeds are provided, the rendering
// list is expanded with each seed, the seed's local imports, and the local
// callers of the seed's primary exported symbol — in that order — before any
// caller-supplied files. Duplicates are removed while preserving order.
//
// Files larger than maxReviewFileSize are truncated at chunk boundaries
// (whole functions/types) instead of mid-statement.
func buildCompactSourceContext(label, root string, files []string, maxTokens int, seeds ...string) string {
	return buildSourceContextSized(label, root, files, seeds, maxReviewTotalContext, maxReviewFileSize, maxTokens)
}

// shadowMeasureScopedContext renders a symbol-scoped version of the same review
// context purely to emit instrumentation under agent="<label>-shadow"; the
// rendered text is discarded. It lets us compare the potential scoped token
// cost against the whole-file context actually sent, without changing what the
// agent receives. No-op when there are no targets.
func shadowMeasureScopedContext(label, root string, files []string, targets map[string][]string, maxTokens int, seeds ...string) {
	if len(targets) == 0 {
		return
	}
	_ = buildScopedSourceContext(label+"-shadow", root, files, seeds, targets, maxReviewTotalContext, maxReviewFileSize, maxTokens)
}

// buildSourceContextSized is the budget-parameterised core of context
// rendering. Used by review agents (small budgets) and by the Coder fix loop
// (larger budget). totalBudget is a hard ceiling on the rendered string;
// perFileBudget is applied per file via chunk-boundary truncation.
// label identifies the calling agent for instrumentation.
func buildSourceContextSized(label, root string, files, seeds []string, totalBudget, perFileBudget, maxTokens int) string {
	return buildScopedSourceContext(label, root, files, seeds, nil, totalBudget, perFileBudget, maxTokens)
}

// buildScopedSourceContext renders source context with optional symbol-level
// scoping. When targets[path] lists symbol names, only the matching
// declarations (plus the file's package/import header and the enclosing type
// of any selected method) are rendered for that file instead of the whole
// file — cutting tokens while keeping the snippet self-describing. Files
// without targets fall back to whole-file chunk-boundary truncation, so
// existing callers (targets == nil) are unaffected.
func buildScopedSourceContext(label, root string, files, seeds []string, targets map[string][]string, totalBudget, perFileBudget, maxTokens int) string {
	if root == "" {
		return ""
	}
	start := time.Now()
	expanded, reasons := expandWithGraph(root, files, seeds)
	if len(expanded) == 0 {
		return ""
	}

	var sb strings.Builder
	totalSize := 0
	budget := totalBudget

	if maxTokens > 0 {
		charBudget := maxTokens * 4 // chars/token heuristic
		if charBudget < budget {
			budget = charBudget
		}
	}

	includedFiles := 0
	truncatedFiles := 0
	reasonCounts := map[string]int{}

	for _, path := range expanded {
		if totalSize >= budget {
			break
		}
		content, err := safefile.ReadFile(root, path)
		if err != nil || len(content) == 0 {
			continue
		}
		if isBinaryContent(content) {
			continue
		}

		var fileContent string
		var truncated bool
		targeted := len(targets[path]) > 0
		if targeted {
			fileContent, truncated = renderSymbolChunks(path, content, targets[path], perFileBudget)
		} else {
			truncated = len(content) > perFileBudget
			fileContent = truncateByChunks(path, content, perFileBudget)
		}

		entry := fmt.Sprintf("**%s**\n```\n%s\n```\n\n", path, fileContent)
		if totalSize+len(entry) > budget {
			// Targeted (symbol-scoped) snippets must stay whole — never cut a
			// scoped declaration mid-body, so skip the file rather than truncate.
			if targeted {
				continue
			}
			remaining := budget - totalSize
			if remaining > 200 {
				fileContent = tokenutil.Truncate(fileContent, remaining/4)
				truncated = true
				entry = fmt.Sprintf("**%s**\n```\n%s\n```\n\n", path, fileContent)
			} else {
				break
			}
		}

		sb.WriteString(entry)
		totalSize += len(entry)
		includedFiles++
		if truncated {
			truncatedFiles++
		}
		reasonCounts[reasonOf(reasons, path)]++
	}

	result := sb.String()
	logSourceContext(label, len(expanded), includedFiles, truncatedFiles, len(result), tokenutil.EstimateTokens(result), reasonCounts, time.Since(start))
	return result
}

// reasonOf returns the recorded selection reason for path, defaulting to
// reasonFile when none was tracked.
func reasonOf(reasons map[string]string, path string) string {
	if r, ok := reasons[path]; ok {
		return r
	}
	return reasonFile
}

// logSourceContext emits one structured record describing what the source
// context assembly produced, so token usage and selection can be measured and
// compared across agents and runs.
func logSourceContext(label string, considered, included, truncated, chars, tokens int, reasons map[string]int, dur time.Duration) {
	logging.ForComponent("source_context").Info("assembled",
		slog.String("agent", label),
		slog.Int("files_considered", considered),
		slog.Int("files_included", included),
		slog.Int("files_truncated", truncated),
		slog.Int("chars", chars),
		slog.Int("est_tokens", tokens),
		slog.Int("seed", reasons[reasonSeed]),
		slog.Int("import", reasons[reasonImport]),
		slog.Int("caller", reasons[reasonCaller]),
		slog.Int("file", reasons[reasonFile]),
		slog.Int64("build_ms", dur.Milliseconds()),
	)
}

// renderSymbolChunks renders only the declarations in content that match the
// requested symbol names, prefixed by the file's package/import header. For a
// requested type, its methods are included; for a requested or included
// method, its enclosing type declaration is included too. Selected chunks are
// emitted in source order. Falls back to whole-file chunk truncation when the
// file is unsupported/unparseable or no symbol matches. The bool return is
// true when budget forced some matched chunks to be omitted.
func renderSymbolChunks(path string, content []byte, symbols []string, budget int) (string, bool) {
	chunks, err := chunker.Split(path, content)
	if err != nil || len(chunks) <= 1 {
		return truncateByChunks(path, content, budget), len(content) > budget
	}

	want := parseSymbolTargets(symbols)
	selected := selectSymbolChunks(chunks, want)
	if len(selected) == 0 {
		return truncateByChunks(path, content, budget), len(content) > budget
	}

	var sb strings.Builder
	header := fileHeader(content, chunks)
	sb.WriteString(header)
	used := len(header)
	omitted := false
	for _, c := range selected {
		body := c.Body
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		if used+len(body)+1 > budget && used > len(header) {
			omitted = true
			break
		}
		sb.WriteString(body)
		sb.WriteString("\n")
		used += len(body) + 1
	}
	if omitted {
		sb.WriteString("// ... chunks omitted")
	}
	return sb.String(), omitted
}

// symbolTargets is a parsed set of requested symbols. names holds bare
// identifiers (functions, types, vars, consts, or bare method names); methods
// holds qualified method requests as recvType -> set of method names, parsed
// from forms like "Server.Start" or "(*Server).Start".
type symbolTargets struct {
	names   map[string]bool
	methods map[string]map[string]bool
}

// parseSymbolTargets splits requested symbol strings into bare names and
// qualified method requests. "(*Server).Start" and "Server.Start" both select
// method Start on receiver type Server; "Foo" selects anything named Foo.
func parseSymbolTargets(symbols []string) symbolTargets {
	t := symbolTargets{names: map[string]bool{}, methods: map[string]map[string]bool{}}
	for _, s := range symbols {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if recv, method, ok := splitQualifiedMethod(s); ok {
			if t.methods[recv] == nil {
				t.methods[recv] = map[string]bool{}
			}
			t.methods[recv][method] = true
			continue
		}
		t.names[s] = true
	}
	return t
}

// splitQualifiedMethod parses "Recv.Method" or "(*Recv).Method" into its
// receiver type and method name. Returns ok=false for unqualified names.
func splitQualifiedMethod(s string) (recv, method string, ok bool) {
	dot := strings.LastIndex(s, ".")
	if dot <= 0 || dot == len(s)-1 {
		return "", "", false
	}
	recv = s[:dot]
	method = s[dot+1:]
	recv = strings.TrimPrefix(strings.TrimSuffix(strings.TrimPrefix(recv, "("), ")"), "*")
	recv = strings.TrimPrefix(recv, "*")
	if recv == "" || strings.ContainsAny(recv, ".()*") || strings.ContainsAny(method, ".()*") {
		return "", "", false
	}
	return recv, method, true
}

// selectSymbolChunks returns the chunks matching the requested targets, in
// source order. It matches by any declared name (covering grouped
// type/const/var blocks), qualified or bare method names, and methods whose
// receiver type was requested. For any selected function/method it also pulls
// in same-file type declarations referenced in the signature, and for any
// selected method it pulls in its enclosing type declaration.
func selectSymbolChunks(chunks []chunker.Chunk, want symbolTargets) []chunker.Chunk {
	included := make([]bool, len(chunks))
	wantType := make(map[string]bool)

	nameMatch := func(c chunker.Chunk) bool {
		for _, n := range chunkNames(c) {
			if want.names[n] {
				return true
			}
		}
		return false
	}
	methodMatch := func(c chunker.Chunk) bool {
		if c.Kind != "method" {
			return false
		}
		if c.Recv != "" && want.names[c.Recv] {
			return true // requesting a type pulls its methods
		}
		if ms, ok := want.methods[c.Recv]; ok && ms[c.Name] {
			return true // qualified Recv.Method
		}
		return false
	}

	for i, c := range chunks {
		if nameMatch(c) || methodMatch(c) {
			included[i] = true
		}
	}
	// Expand: enclosing types of selected methods + signature-referenced types.
	for i, c := range chunks {
		if !included[i] {
			continue
		}
		if c.Kind == "method" && c.Recv != "" {
			wantType[c.Recv] = true
		}
		for _, r := range c.Refs {
			wantType[r] = true
		}
	}
	for i, c := range chunks {
		if c.Kind != "type" {
			continue
		}
		for _, n := range chunkNames(c) {
			if wantType[n] {
				included[i] = true
				break
			}
		}
	}

	var out []chunker.Chunk
	for i, c := range chunks {
		if included[i] {
			out = append(out, c)
		}
	}
	return out
}

// chunkNames returns a chunk's declared names, falling back to its primary name.
func chunkNames(c chunker.Chunk) []string {
	if len(c.Names) > 0 {
		return c.Names
	}
	if c.Name != "" {
		return []string{c.Name}
	}
	return nil
}

// fileHeader returns the bytes preceding the first declaration — the package
// clause and import block — so a symbol-scoped snippet remains self-describing.
func fileHeader(content []byte, chunks []chunker.Chunk) string {
	minStart := 0
	for _, c := range chunks {
		if c.StartLine > 0 && (minStart == 0 || c.StartLine < minStart) {
			minStart = c.StartLine
		}
	}
	if minStart <= 1 {
		return ""
	}
	lines := strings.SplitAfter(string(content), "\n")
	if minStart-1 >= len(lines) {
		return string(content)
	}
	return strings.Join(lines[:minStart-1], "")
}

// truncateByChunks returns a string that fits within budget by selecting whole
// chunks. Exported declarations come first, then unexported, then const/var.
// For files where the chunker produces only one chunk (unsupported language
// or unparseable source), this degrades to a byte-level truncation marked
// with `... (truncated)`.
func truncateByChunks(path string, content []byte, budget int) string {
	if len(content) <= budget {
		return string(content)
	}
	chunks, err := chunker.Split(path, content)
	if err != nil || len(chunks) <= 1 {
		return string(content[:budget]) + "\n... (truncated)"
	}
	chunker.SortByPriority(chunks)

	var sb strings.Builder
	used := 0
	included := 0
	for _, c := range chunks {
		body := c.Body
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		if used+len(body) > budget && included > 0 {
			break
		}
		sb.WriteString(body)
		sb.WriteString("\n")
		used += len(body) + 1
		included++
		if used >= budget {
			break
		}
	}
	if included < len(chunks) {
		sb.WriteString("... (chunks omitted)")
	}
	return sb.String()
}

// expandWithGraph prepends seeds, their imports, and their callers to the
// caller-supplied file list, removing duplicates while preserving order. It
// also returns a map recording, per path, the first reason it was selected
// (seed / import / caller / file) for instrumentation.
func expandWithGraph(root string, files, seeds []string) ([]string, map[string]string) {
	reasons := make(map[string]string)
	record := func(paths []string, reason string) {
		for _, p := range paths {
			if p == "" {
				continue
			}
			if _, ok := reasons[p]; !ok {
				reasons[p] = reason
			}
		}
	}

	if len(seeds) == 0 {
		ordered := dedupePreserveOrder(files)
		record(ordered, reasonFile)
		return ordered, reasons
	}
	ordered := make([]string, 0, len(files)+len(seeds)*4)

	ordered = append(ordered, seeds...)
	record(seeds, reasonSeed)
	for _, s := range seeds {
		if imps, err := pkgcontext.ImportsOf(root, s); err == nil {
			ordered = append(ordered, imps...)
			record(imps, reasonImport)
		}
	}
	for _, s := range seeds {
		sym := pkgcontext.PrimarySymbolOf(root, s)
		if sym == "" {
			continue
		}
		if callers, err := pkgcontext.CallersOf(root, s, sym); err == nil {
			ordered = append(ordered, callers...)
			record(callers, reasonCaller)
		}
	}
	ordered = append(ordered, files...)
	record(files, reasonFile)
	return dedupePreserveOrder(ordered), reasons
}

func dedupePreserveOrder(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// SeedsFromSemanticSearch derives seed file paths for buildCompactSourceContext
// using the semantic index attached to pc, when available. Returns nil when
// the index is disabled, the query is empty, or the search fails — callers can
// fall back to passing no seeds.
func SeedsFromSemanticSearch(ctx context.Context, pc pkgcontext.ProjectContext, stagePrompt string, k int) []string {
	if strings.TrimSpace(stagePrompt) == "" || k <= 0 {
		return nil
	}
	hits, err := pc.SemanticSearch(ctx, stagePrompt, k)
	if err != nil {
		return nil
	}
	return hits
}
