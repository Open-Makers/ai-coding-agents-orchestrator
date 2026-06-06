package orchestrator

import "testing"

func TestInferBrownfieldScope(t *testing.T) {
	tests := []struct {
		name   string
		inputs []string
		want   string
	}{
		{"english fix", []string{"please fix the login bug"}, "bugfix"},
		{"english bug word", []string{"there is a bug in the parser"}, "bugfix"},
		{"polish poprawka", []string{"trzeba zrobić poprawki w panelu"}, "bugfix"},
		{"polish napraw", []string{"napraw błąd w logowaniu"}, "bugfix"},
		{"english refactor", []string{"refactor the auth module"}, "refactor"},
		{"polish refaktor", []string{"zrób refaktor tego pakietu"}, "refactor"},
		{"neutral feature", []string{"add a new export button"}, "feature"},
		{"empty inputs", []string{"", ""}, "feature"},
		{"description over title", []string{"do something", "fix the crash"}, "bugfix"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := inferBrownfieldScope(tc.inputs...)
			if got != tc.want {
				t.Errorf("inferBrownfieldScope(%v) = %q, want %q", tc.inputs, got, tc.want)
			}
		})
	}
}

