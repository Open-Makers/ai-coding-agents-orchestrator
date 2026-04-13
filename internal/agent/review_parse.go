package agent

import (
	"strings"
)

// reviewSections holds parsed review output split into structured sections.
type reviewSections struct {
	MustFix    []string
	NiceToHave []string
	Approved   bool
	// Parsed indicates whether the parser found any recognisable structure.
	// When false the raw output should be escalated to PM for arbitration.
	Parsed bool
}

// parseReviewSections is a resilient parser for LLM review output.
// It tolerates markdown headings, bold markers, colons, and other formatting
// variants that non-deterministic models may produce.
func parseReviewSections(text string, niceToHaveHeaders ...string) reviewSections {
	if len(niceToHaveHeaders) == 0 {
		niceToHaveHeaders = []string{"NICE TO HAVE", "RECOMMENDATION"}
	}
	lines := strings.Split(text, "\n")
	var mustFix, niceToHave []string
	section := ""
	approved := false
	foundAnySection := false

	for _, line := range lines {
		norm := normalizeHeading(line)

		switch {
		case isMustFixHeading(norm):
			section = "mustfix"
			foundAnySection = true
		case isNiceToHaveHeading(norm, niceToHaveHeaders):
			section = "nicetohave"
			foundAnySection = true
		case isApproveHeading(norm):
			approved = strings.Contains(strings.ToUpper(norm), "YES")
			section = ""
			foundAnySection = true
		default:
			item := extractListItem(line)
			if item == "" || isNoneValue(item) {
				continue
			}
			switch section {
			case "mustfix":
				mustFix = append(mustFix, item)
			case "nicetohave":
				niceToHave = append(niceToHave, item)
			}
		}
	}

	// Fallback: if no structured sections found, attempt keyword-based extraction.
	if !foundAnySection {
		fallback := fallbackParseReview(text)
		if fallback.Parsed {
			return fallback
		}
	}

	return reviewSections{
		MustFix:    mustFix,
		NiceToHave: niceToHave,
		Approved:   approved,
		Parsed:     foundAnySection,
	}
}

// isMustFixHeading returns true if the normalized heading indicates a must-fix section.
func isMustFixHeading(norm string) bool {
	return headingContains(norm,
		"MUST FIX", "MUST-FIX", "MUSTFIX",
		"CRITICAL", "ISSUES", "BUGS", "PROBLEMS",
		"FINDINGS", "ERRORS", "DEFECTS", "BLOCKERS",
	)
}

// isNiceToHaveHeading returns true if the normalized heading indicates a nice-to-have section.
func isNiceToHaveHeading(norm string, extraHeaders []string) bool {
	base := []string{
		"NICE TO HAVE", "NICE-TO-HAVE", "NICETOHA",
		"SUGGESTION", "IMPROVEMENT", "MINOR",
		"NON-CRITICAL", "OPTIONAL", "ENHANCEMENT",
	}
	all := append(base, extraHeaders...)
	return headingContains(norm, all...)
}

// isApproveHeading returns true if the normalized heading is an approval line.
func isApproveHeading(norm string) bool {
	return headingContains(norm,
		"APPROVE", "VERDICT", "DECISION", "RESULT",
		"OVERALL", "SUMMARY",
	)
}

// fallbackParseReview attempts to parse a review from freeform text by looking
// for approval/rejection signals and extracting potential issues from bullet points.
// This prevents escalation to PM for output that is clearly positive or negative.
func fallbackParseReview(text string) reviewSections {
	upper := strings.ToUpper(text)
	lines := strings.Split(text, "\n")

	// Collect any bullet-point items as potential issues.
	var items []string
	for _, line := range lines {
		item := extractListItem(line)
		if item != "" && !isNoneValue(item) && len(item) > 10 {
			items = append(items, item)
		}
	}

	// Check for clear approval signals.
	approvalSignals := []string{
		"LOOKS GOOD", "SHIP IT", "LGTM", "APPROVED",
		"NO ISSUES", "NO CRITICAL", "NO BUGS", "NO PROBLEMS",
		"ALL GOOD", "WELL DONE", "GREAT JOB", "CLEAN CODE",
		"NO MUST-FIX", "NO MUST FIX", "NOTHING CRITICAL",
		"APPROVE: YES", "APPROVED: YES", "VERDICT: PASS",
	}

	hasApprovalSignal := false
	for _, sig := range approvalSignals {
		if strings.Contains(upper, sig) {
			hasApprovalSignal = true
			break
		}
	}

	// Check for clear rejection signals.
	rejectionSignals := []string{
		"MUST FIX", "MUST-FIX", "CRITICAL ISSUE", "CRITICAL BUG",
		"SECURITY VULNERABILITY", "SQL INJECTION", "XSS",
		"RACE CONDITION", "MEMORY LEAK", "NIL POINTER",
		"APPROVE: NO", "APPROVED: NO", "VERDICT: FIX",
		"NOT APPROVED", "REJECTED",
	}

	hasRejectionSignal := false
	for _, sig := range rejectionSignals {
		if strings.Contains(upper, sig) {
			hasRejectionSignal = true
			break
		}
	}

	// Clear approval with no rejection signals → pass.
	if hasApprovalSignal && !hasRejectionSignal {
		return reviewSections{
			Approved: true,
			Parsed:   true,
		}
	}

	// Clear rejection with bullet items → extract as must-fix.
	if hasRejectionSignal && len(items) > 0 {
		return reviewSections{
			MustFix: items,
			Parsed:  true,
		}
	}

	// Ambiguous — let PM handle it.
	return reviewSections{Parsed: false}
}

// normalizeHeading strips markdown heading markers (#), bold markers (**/__)
// and surrounding whitespace so headings can be matched regardless of formatting.
func normalizeHeading(line string) string {
	s := strings.TrimSpace(line)
	// Strip leading markdown heading markers: ##, ###, etc.
	s = strings.TrimLeft(s, "#")
	s = strings.TrimSpace(s)
	// Strip trailing colons and equal signs before bold markers
	// so "**MUST FIX**:" becomes "**MUST FIX**" then "MUST FIX".
	s = strings.TrimRight(s, ":=")
	s = strings.TrimSpace(s)
	// Strip bold / italic markers: **, __, *, _
	s = strings.Trim(s, "*_")
	s = strings.TrimSpace(s)
	// Second pass in case there were colons after bold markers
	s = strings.TrimRight(s, ":=")
	s = strings.TrimSpace(s)
	return s
}

// headingContains checks whether the normalised heading starts with any of the given prefixes (case-insensitive).
func headingContains(normalised string, prefixes ...string) bool {
	upper := strings.ToUpper(normalised)
	for _, p := range prefixes {
		if strings.HasPrefix(upper, p) {
			return true
		}
	}
	return false
}

// extractListItem strips common list markers (-, *, numbered) and surrounding whitespace.
func extractListItem(line string) string {
	s := strings.TrimSpace(line)
	// Strip markdown list markers
	if len(s) > 1 && (s[0] == '-' || s[0] == '*') && s[1] == ' ' {
		s = strings.TrimSpace(s[2:])
	} else {
		// Strip numbered markers: "1.", "1)", "1 -", etc.
		for i, c := range s {
			if c >= '0' && c <= '9' {
				continue
			}
			if i > 0 && (c == '.' || c == ')') {
				rest := s[i+1:]
				s = strings.TrimSpace(rest)
				break
			}
			break
		}
	}
	return strings.TrimSpace(s)
}

// isNoneValue returns true for placeholder "none" values LLMs sometimes emit.
func isNoneValue(s string) bool {
	lower := strings.ToLower(strings.TrimSpace(s))
	return lower == "none" || lower == "none." || lower == "n/a" || lower == "-"
}
