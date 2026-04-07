package tokenutil

import (
	"strings"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		input   string
		wantMin int
		wantMax int
	}{
		{"", 0, 0},
		{"hi", 1, 1},
		{"hello world, this is a test", 5, 10},
	}
	for _, tt := range tests {
		got := EstimateTokens(tt.input)
		if got < tt.wantMin || got > tt.wantMax {
			t.Errorf("EstimateTokens(%q) = %d, want [%d, %d]", tt.input, got, tt.wantMin, tt.wantMax)
		}
	}
}

func TestTruncate_NoOp(t *testing.T) {
	text := "short text"
	got := Truncate(text, 1000)
	if got != text {
		t.Errorf("expected no truncation, got %q", got)
	}
}

func TestTruncate_Cuts(t *testing.T) {
	text := strings.Repeat("word ", 1000) // ~5000 chars = ~1250 tokens
	got := Truncate(text, 100)
	if len(got) >= len(text) {
		t.Error("expected truncation")
	}
	if !strings.Contains(got, "truncated") {
		t.Error("expected truncation marker")
	}
}

func TestTruncate_ZeroBudget(t *testing.T) {
	text := "anything"
	got := Truncate(text, 0)
	if got != text {
		t.Error("zero budget should return original text")
	}
}

func TestTruncateSourceFiles(t *testing.T) {
	files := map[string]string{
		"a.go": strings.Repeat("x", 400),  // ~100 tokens
		"b.go": strings.Repeat("y", 400),  // ~100 tokens
		"c.go": strings.Repeat("z", 4000), // ~1000 tokens
	}
	result := TruncateSourceFiles(files, 250)
	if len(result) == 0 {
		t.Fatal("expected at least some files")
	}
	// Not all files should fit in 250 tokens
	totalChars := 0
	for _, v := range result {
		totalChars += len(v)
	}
	if totalChars >= 4800 {
		t.Error("expected truncation to reduce total size")
	}
}
