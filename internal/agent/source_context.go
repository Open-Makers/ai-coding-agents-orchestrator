package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/chunker"
	pkgcontext "github.com/Open-Makers/ai-coding-agents-orchestrator/internal/context"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/safefile"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tokenutil"
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
func buildCompactSourceContext(root string, files []string, maxTokens int, seeds ...string) string {
	return buildSourceContextSized(root, files, seeds, maxReviewTotalContext, maxReviewFileSize, maxTokens)
}

// buildSourceContextSized is the budget-parameterised core of context
// rendering. Used by review agents (small budgets) and by the Coder fix loop
// (larger budget). totalBudget is a hard ceiling on the rendered string;
// perFileBudget is applied per file via chunk-boundary truncation.
func buildSourceContextSized(root string, files, seeds []string, totalBudget, perFileBudget, maxTokens int) string {
	if root == "" {
		return ""
	}
	expanded := expandWithGraph(root, files, seeds)
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

		fileContent := truncateByChunks(path, content, perFileBudget)

		entry := fmt.Sprintf("**%s**\n```\n%s\n```\n\n", path, fileContent)
		if totalSize+len(entry) > budget {
			remaining := budget - totalSize
			if remaining > 200 {
				fileContent = tokenutil.Truncate(fileContent, remaining/4)
				entry = fmt.Sprintf("**%s**\n```\n%s\n```\n\n", path, fileContent)
			} else {
				break
			}
		}

		sb.WriteString(entry)
		totalSize += len(entry)
	}

	return sb.String()
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
// caller-supplied file list, removing duplicates while preserving order.
func expandWithGraph(root string, files, seeds []string) []string {
	if len(seeds) == 0 {
		return dedupePreserveOrder(files)
	}
	ordered := make([]string, 0, len(files)+len(seeds)*4)

	ordered = append(ordered, seeds...)
	for _, s := range seeds {
		if imps, err := pkgcontext.ImportsOf(root, s); err == nil {
			ordered = append(ordered, imps...)
		}
	}
	for _, s := range seeds {
		sym := pkgcontext.PrimarySymbolOf(root, s)
		if sym == "" {
			continue
		}
		if callers, err := pkgcontext.CallersOf(root, sym); err == nil {
			ordered = append(ordered, callers...)
		}
	}
	ordered = append(ordered, files...)
	return dedupePreserveOrder(ordered)
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
