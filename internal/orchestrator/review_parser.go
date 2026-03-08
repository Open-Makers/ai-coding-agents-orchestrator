package orchestrator

import (
	"fmt"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
)

// SimpleReviewParser parses review.md in the expected template format.
type SimpleReviewParser struct{}

func (p SimpleReviewParser) Parse(ctx *Context) (ReviewResult, error) {
	data, err := ctx.Workspace.ReadFile(artifacts.ReviewFile)
	if err != nil {
		return ReviewResult{}, err
	}
	text := string(data)
	lines := strings.Split(text, "\n")

	var mustFix []string
	inMustFix := false
	approveSet := false
	approve := false

	for _, line := range lines {
		trim := strings.TrimSpace(line)
		upper := strings.ToUpper(trim)

		switch {
		case strings.HasPrefix(upper, "MUST FIX"):
			inMustFix = true
			continue
		case strings.HasPrefix(upper, "NICE TO HAVE"):
			inMustFix = false
			continue
		case strings.HasPrefix(upper, "APPROVE"):
			approveSet = true
			approve = strings.Contains(upper, "YES")
			inMustFix = false
			continue
		}

		if inMustFix {
			item := strings.TrimSpace(strings.TrimPrefix(trim, "-"))
			if item != "" {
				mustFix = append(mustFix, item)
			}
		}
	}

	if !approveSet {
		return ReviewResult{}, fmt.Errorf("review missing approval line")
	}

	return ReviewResult{Approve: approve, MustFix: mustFix}, nil
}
