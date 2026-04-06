package main

import "testing"

func TestResolveUIMode(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "tui"},
		{"auto", "tui"},
		{"tui", "tui"},
		{"plain", "plain"},
		{"tmux", "tui"},
		{"cmux", "tui"},
		{"cmux-internal", "tui"},
		{"unknown", "tui"},
	}
	for _, tt := range tests {
		if got := resolveUIMode(tt.input); got != tt.want {
			t.Errorf("resolveUIMode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
