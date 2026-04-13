package agent

import (
	"fmt"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/safefile"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tokenutil"
)

// maxReviewFileSize limits individual file size in review context.
const maxReviewFileSize = 6000

// maxReviewTotalContext caps total source context for review agents.
const maxReviewTotalContext = 40000

// buildCompactSourceContext reads source files from disk and formats them
// as a compact block for review agents. This replaces reading RawCoderOutputFile,
// which contains markdown decoration, LLM commentary, and redundant content.
func buildCompactSourceContext(root string, files []string, maxTokens int) string {
	if len(files) == 0 || root == "" {
		return ""
	}

	var sb strings.Builder
	totalSize := 0
	budget := maxReviewTotalContext

	// If a token budget is set, convert to character budget.
	if maxTokens > 0 {
		charBudget := maxTokens * 4 // chars/token heuristic
		if charBudget < budget {
			budget = charBudget
		}
	}

	for _, path := range files {
		if totalSize >= budget {
			break
		}
		content, err := safefile.ReadFile(root, path)
		if err != nil || len(content) == 0 {
			continue
		}
		// Skip files with binary content to avoid corrupting LLM context.
		if isBinaryContent(content) {
			continue
		}

		fileContent := string(content)
		if len(fileContent) > maxReviewFileSize {
			fileContent = fileContent[:maxReviewFileSize] + "\n... (truncated)"
		}

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
