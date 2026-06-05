package agent

import (
	"testing"
)

func TestParseReviewSections_StandardFormat(t *testing.T) {
	input := `MUST FIX
- Missing error handling in main()
- SQL injection in query builder
NICE TO HAVE
- Add logging
Approve?: YES`

	s := parseReviewSections(input)
	assertParsed(t, s)
	assertMustFix(t, s, 2)
	assertNiceToHave(t, s, 1)
	if !s.Approved {
		t.Error("expected approved=true")
	}
}

func TestParseReviewSections_MarkdownHeadings(t *testing.T) {
	input := `## Must Fix
- Buffer overflow risk in parser
### Nice to Have
- Consider using sync.Pool
Approve?: NO`

	s := parseReviewSections(input)
	assertParsed(t, s)
	assertMustFix(t, s, 1)
	assertNiceToHave(t, s, 1)
	if s.Approved {
		t.Error("expected approved=false")
	}
}

func TestParseReviewSections_BoldMarkers(t *testing.T) {
	input := `**MUST FIX**
- Race condition in cache
**NICE TO HAVE**
- none
**Approve?**: YES`

	s := parseReviewSections(input)
	assertParsed(t, s)
	assertMustFix(t, s, 1)
	assertNiceToHave(t, s, 0)
}

func TestParseReviewSections_WithColons(t *testing.T) {
	input := `MUST FIX:
- Nil pointer dereference in handler
RECOMMENDATIONS:
- Use structured logging
Approve: YES`

	s := parseReviewSections(input, "RECOMMENDATION")
	assertParsed(t, s)
	assertMustFix(t, s, 1)
	assertNiceToHave(t, s, 1)
}

func TestParseReviewSections_TripleHashAndBold(t *testing.T) {
	input := `### **Must Fix**
- Missing input validation on /api/users endpoint
- Hardcoded database credentials in config.go

### **Recommendations**
- Add request rate limiting
- Implement structured error responses

**Approve?**: NO`

	s := parseReviewSections(input, "RECOMMENDATION")
	assertParsed(t, s)
	assertMustFix(t, s, 2)
	assertNiceToHave(t, s, 2)
	if s.Approved {
		t.Error("expected approved=false")
	}
}

func TestParseReviewSections_AllNone(t *testing.T) {
	input := `MUST FIX
- none
NICE TO HAVE
- none
Approve?: YES`

	s := parseReviewSections(input)
	assertParsed(t, s)
	assertMustFix(t, s, 0)
	assertNiceToHave(t, s, 0)
	if !s.Approved {
		t.Error("expected approved=true")
	}
}

func TestParseReviewSections_NumberedList(t *testing.T) {
	input := `MUST FIX
1. Missing context cancellation check
2. File handle leak in processFile()
NICE TO HAVE
1. Add benchmarks
Approve?: NO`

	s := parseReviewSections(input)
	assertParsed(t, s)
	assertMustFix(t, s, 2)
	assertNiceToHave(t, s, 1)
}

func TestParseReviewSections_Unparsed(t *testing.T) {
	// Truly ambiguous text with no clear signals should remain unparsed.
	input := `The code has some interesting patterns. I noticed a few things
that are worth discussing with the team. Let me elaborate on the design choices.`

	s := parseReviewSections(input)
	if s.Parsed {
		t.Error("expected Parsed=false for ambiguous freeform text")
	}
	if len(s.MustFix) != 0 {
		t.Errorf("expected 0 must-fix, got %d", len(s.MustFix))
	}
}

func TestParseReviewSections_FallbackApproval(t *testing.T) {
	// Freeform text with clear positive signals should be auto-approved.
	input := `The code looks pretty good overall. I noticed a few things that could
be improved but nothing critical. The error handling is solid and the tests
cover the main paths. Ship it!`

	s := parseReviewSections(input)
	if !s.Parsed {
		t.Error("expected Parsed=true for clearly positive text")
	}
	if !s.Approved {
		t.Error("expected Approved=true for positive text with approval signals")
	}
}

func TestParseReviewSections_FallbackRejection(t *testing.T) {
	input := `There is a critical issue with the code:
- SQL injection vulnerability in the query builder
- Race condition in the cache layer
Not approved.`

	s := parseReviewSections(input)
	if !s.Parsed {
		t.Error("expected Parsed=true for text with rejection signals")
	}
	if len(s.MustFix) == 0 {
		t.Error("expected must-fix items from fallback extraction")
	}
}

func TestParseReviewSections_MustFixHyphenated(t *testing.T) {
	input := `MUST-FIX
- Memory leak in connection pool
NICE TO HAVE
- none
Approve?: NO`

	s := parseReviewSections(input)
	assertParsed(t, s)
	assertMustFix(t, s, 1)
}

func TestParseReviewSections_AlternativeHeadings(t *testing.T) {
	tests := []struct {
		name  string
		input string
		fix   int
	}{
		{
			name:  "Issues heading",
			input: "## Issues\n- Missing error handling\nApprove?: NO",
			fix:   1,
		},
		{
			name:  "Critical heading",
			input: "**Critical**\n- Race condition\nSuggestions\n- Add logging\nApprove?: NO",
			fix:   1,
		},
		{
			name:  "Bugs heading",
			input: "### Bugs\n- Nil pointer in handler\nApprove?: NO",
			fix:   1,
		},
		{
			name:  "Verdict heading",
			input: "Verdict: NO\nMUST FIX\n- Missing validation",
			fix:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := parseReviewSections(tt.input)
			assertParsed(t, s)
			assertMustFix(t, s, tt.fix)
		})
	}
}

func TestParseReviewSections_NoneVariants(t *testing.T) {
	variants := []string{"none", "None", "NONE", "none.", "N/A", "n/a", "-"}
	for _, v := range variants {
		input := "MUST FIX\n- " + v + "\nApprove?: YES"
		s := parseReviewSections(input)
		if len(s.MustFix) != 0 {
			t.Errorf("none-variant %q was not filtered: got %d must-fix", v, len(s.MustFix))
		}
	}
}

func TestNormalizeHeading(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"## Must Fix", "Must Fix"},
		{"### **Must Fix**", "Must Fix"},
		{"**MUST FIX**:", "MUST FIX"},
		{"MUST FIX", "MUST FIX"},
		{"  ## MUST FIX ===", "MUST FIX"},
		{"### __Nice to Have__", "Nice to Have"},
	}
	for _, tt := range tests {
		got := normalizeHeading(tt.input)
		if got != tt.want {
			t.Errorf("normalizeHeading(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseArbitrateResult_Fix(t *testing.T) {
	input := `VERDICT: FIX
MUST FIX
- The reviewer found a real SQL injection vulnerability
- Missing authentication check on admin endpoint`

	r := parseArbitrateResult(input)
	if r.Pass {
		t.Error("expected Pass=false")
	}
	if len(r.MustFix) != 2 {
		t.Errorf("expected 2 must-fix, got %d", len(r.MustFix))
	}
}

func TestParseArbitrateResult_Pass(t *testing.T) {
	input := `VERDICT: PASS
MUST FIX
- none`

	r := parseArbitrateResult(input)
	if !r.Pass {
		t.Error("expected Pass=true")
	}
	if len(r.MustFix) != 0 {
		t.Errorf("expected 0 must-fix, got %d", len(r.MustFix))
	}
}

func TestParseArbitrateResult_IssuesOverridePassVerdict(t *testing.T) {
	input := `VERDICT: PASS
MUST FIX
- Actually there is a critical race condition`

	r := parseArbitrateResult(input)
	if r.Pass {
		t.Error("expected Pass=false when must-fix items are present despite PASS verdict")
	}
}

func TestParseQAReview_Delegates(t *testing.T) {
	input := `MUST FIX
- Issue one
NICE TO HAVE
- Suggestion
Approve?: NO`

	r := parseQAReview(input)
	if r.Unparsed {
		t.Error("expected Unparsed=false for valid format")
	}
	if len(r.MustFix) != 1 {
		t.Errorf("expected 1 must-fix, got %d", len(r.MustFix))
	}
	if r.RawOutput != input {
		t.Error("RawOutput should contain original text")
	}
}

func TestParseQAReview_UnparsedOutput(t *testing.T) {
	// Truly ambiguous text should remain unparsed.
	input := "The architecture follows standard patterns. Let me describe the design choices."
	r := parseQAReview(input)
	if !r.Unparsed {
		t.Error("expected Unparsed=true for ambiguous freeform text")
	}
}

func TestParseQAReview_FreeformApproval(t *testing.T) {
	input := "Everything looks fine, great job!"
	r := parseQAReview(input)
	if r.Unparsed {
		t.Error("expected Unparsed=false for clearly positive text")
	}
	if !r.Approved {
		t.Error("expected Approved=true for positive text")
	}
}

func TestParseSecurityReview_Delegates(t *testing.T) {
	input := `MUST FIX
- Hardcoded API key in config.go
RECOMMENDATIONS
- Rotate secrets quarterly
Approve?: NO`

	r := parseSecurityReview(input)
	if r.Unparsed {
		t.Error("expected Unparsed=false")
	}
	if len(r.MustFix) != 1 {
		t.Errorf("expected 1 must-fix, got %d", len(r.MustFix))
	}
}

func TestParseUXReview_Delegates(t *testing.T) {
	input := `### **Must Fix**
- Error messages are not user-friendly
**Nice to Have**
- Add dark mode support
Approve?: NO`

	r := parseUXReview(input)
	if r.Unparsed {
		t.Error("expected Unparsed=false")
	}
	if len(r.MustFix) != 1 {
		t.Errorf("expected 1 must-fix, got %d", len(r.MustFix))
	}
}

// helpers

func assertParsed(t *testing.T, s reviewSections) {
	t.Helper()
	if !s.Parsed {
		t.Error("expected Parsed=true")
	}
}

func assertMustFix(t *testing.T, s reviewSections, want int) {
	t.Helper()
	if len(s.MustFix) != want {
		t.Errorf("expected %d must-fix items, got %d: %v", want, len(s.MustFix), s.MustFix)
	}
}

func assertNiceToHave(t *testing.T, s reviewSections, want int) {
	t.Helper()
	if len(s.NiceToHave) != want {
		t.Errorf("expected %d nice-to-have items, got %d: %v", want, len(s.NiceToHave), s.NiceToHave)
	}
}
