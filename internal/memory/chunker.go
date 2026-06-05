package memory

import (
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tokenutil"
)

// Chunk is a slice of a Markdown file ready to be indexed.
type Chunk struct {
	Ord       int    // 0-based order within the file
	StartLine int    // 1-based start line in the source file
	EndLine   int    // 1-based end line, inclusive
	Tokens    int    // estimated token count
	Body      string // verbatim content
}

// segment is an intermediate split unit (one or more lines, may exceed
// sizeTokens — packer handles that).
type segment struct {
	startLine int
	endLine   int
	body      string
	tokens    int
}

// ChunkMarkdown splits content into chunks of approximately sizeTokens with
// overlapTokens overlap between successive chunks. The splitter prefers
// heading boundaries (`#`, `##`, ...) and falls back to a sliding window
// when a single segment is larger than sizeTokens.
//
// Mirrors the OpenClaw chunking strategy: 400-token chunks with 80-token
// overlap by default.
func ChunkMarkdown(content string, sizeTokens, overlapTokens int) []Chunk {
	if sizeTokens <= 0 {
		sizeTokens = 400
	}
	if overlapTokens < 0 || overlapTokens >= sizeTokens {
		overlapTokens = sizeTokens / 5
	}
	if strings.TrimSpace(content) == "" {
		return nil
	}

	segments := splitByHeadings(content)
	if len(segments) == 0 {
		return nil
	}

	chunks := packSegments(segments, sizeTokens, overlapTokens)
	if overlapTokens > 0 && len(chunks) > 1 {
		chunks = applyOverlap(chunks, overlapTokens)
	}
	return chunks
}

func splitByHeadings(content string) []segment {
	lines := strings.Split(content, "\n")
	var out []segment
	var buf []string
	start := 1
	flush := func(end int) {
		if len(buf) == 0 {
			return
		}
		body := strings.Join(buf, "\n")
		buf = buf[:0]
		if strings.TrimSpace(body) == "" {
			return
		}
		out = append(out, segment{
			startLine: start,
			endLine:   end,
			body:      body,
			tokens:    tokenutil.EstimateTokens(body),
		})
	}
	for i, ln := range lines {
		lineNo := i + 1
		if strings.HasPrefix(strings.TrimSpace(ln), "#") && len(buf) > 0 {
			flush(lineNo - 1)
			start = lineNo
		}
		if len(buf) == 0 {
			start = lineNo
		}
		buf = append(buf, ln)
	}
	flush(len(lines))
	return out
}

func packSegments(segs []segment, sizeTokens, overlapTokens int) []Chunk {
	var out []Chunk
	emit := func(s segment) {
		out = append(out, Chunk{
			Ord:       len(out),
			StartLine: s.startLine,
			EndLine:   s.endLine,
			Tokens:    s.tokens,
			Body:      s.body,
		})
	}
	var cur segment
	have := false
	for _, s := range segs {
		if s.tokens > sizeTokens {
			if have {
				emit(cur)
				have = false
			}
			for _, w := range slideOversized(s, sizeTokens, overlapTokens) {
				emit(w)
			}
			continue
		}
		if !have {
			cur = s
			have = true
			continue
		}
		if cur.tokens+s.tokens <= sizeTokens {
			cur.body += "\n" + s.body
			cur.endLine = s.endLine
			cur.tokens += s.tokens
			continue
		}
		emit(cur)
		cur = s
	}
	if have {
		emit(cur)
	}
	return out
}

func slideOversized(s segment, sizeTokens, overlap int) []segment {
	lines := strings.Split(s.body, "\n")
	if len(lines) == 0 {
		return nil
	}
	linesPerTok := float64(len(lines)) / float64(max1(s.tokens))
	step := int(float64(sizeTokens) * linesPerTok)
	if step < 1 {
		step = 1
	}
	overlapLines := int(float64(overlap) * linesPerTok)
	if overlapLines < 0 {
		overlapLines = 0
	}

	var out []segment
	for i := 0; i < len(lines); {
		end := i + step
		if end > len(lines) {
			end = len(lines)
		}
		body := strings.Join(lines[i:end], "\n")
		out = append(out, segment{
			startLine: s.startLine + i,
			endLine:   s.startLine + end - 1,
			body:      body,
			tokens:    tokenutil.EstimateTokens(body),
		})
		if end == len(lines) {
			break
		}
		next := end - overlapLines
		if next <= i {
			next = i + 1
		}
		i = next
	}
	return out
}

func applyOverlap(chunks []Chunk, overlapTokens int) []Chunk {
	out := make([]Chunk, len(chunks))
	out[0] = chunks[0]
	for i := 1; i < len(chunks); i++ {
		tail := tailTokens(chunks[i-1].Body, overlapTokens)
		c := chunks[i]
		if tail != "" {
			c.Body = tail + "\n" + c.Body
			c.Tokens = tokenutil.EstimateTokens(c.Body)
		}
		out[i] = c
	}
	return out
}

func tailTokens(s string, tokens int) string {
	if tokens <= 0 || s == "" {
		return ""
	}
	want := tokens * 4
	if want >= len(s) {
		return s
	}
	cut := s[len(s)-want:]
	if idx := strings.Index(cut, "\n"); idx > 0 && idx < len(cut)-1 {
		cut = cut[idx+1:]
	}
	return cut
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
