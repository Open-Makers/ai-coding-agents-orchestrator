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
		case headingContains(norm, "MUST FIX", "MUST-FIX", "MUSTFIX"):
			section = "mustfix"
			foundAnySection = true
		case headingContainsAny(norm, niceToHaveHeaders):
			section = "nicetohave"
			foundAnySection = true
		case headingContains(norm, "APPROVE"):
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

	return reviewSections{
		MustFix:    mustFix,
		NiceToHave: niceToHave,
		Approved:   approved,
		Parsed:     foundAnySection,
	}
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

// headingContainsAny is like headingContains but takes a slice.
func headingContainsAny(normalised string, prefixes []string) bool {
	return headingContains(normalised, prefixes...)
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
