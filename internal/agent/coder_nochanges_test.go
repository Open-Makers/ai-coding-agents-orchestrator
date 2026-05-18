package agent

import "testing"

func TestIsNoChangesDeclared(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"empty", "", false},
		{"whitespace only", "   \n\t  ", false},
		{"explicit no changes required", "No changes required.", true},
		{"explicit no changes needed", "No changes needed", true},
		{"sentence with no file changes", "All tests pass. No file changes are needed for this stage.", true},
		{"no modifications required variant", "No modifications required — codebase is complete.", true},
		{"actual code description", "Added new file internal/foo.go with handler logic.", false},
		{"refactor description", "Refactored game.go to extract board logic.", false},
		{"case insensitive", "NO CHANGES REQUIRED", true},
		{"transcript from failing run", "No changes required. All production files already satisfy their sibling *_test.go contracts.", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isNoChangesDeclared(tc.input)
			if got != tc.expected {
				t.Errorf("isNoChangesDeclared(%q) = %v, want %v", tc.input, got, tc.expected)
			}
		})
	}
}
