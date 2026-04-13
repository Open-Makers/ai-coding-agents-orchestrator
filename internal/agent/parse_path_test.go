package agent

import "testing"

func TestParseFilePath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "bold path", input: "**cmd/game/main.go**", expected: "cmd/game/main.go"},
		{name: "heading path", input: "### internal/game/state.go", expected: "internal/game/state.go"},
		{name: "backtick path in prose", input: "Here is `cmd/game/main.go`:", expected: "cmd/game/main.go"},
		{name: "file prefix with backtick", input: "File `internal/game/state.go`:", expected: "internal/game/state.go"},
		{name: "path with trailing description", input: "**cmd/game/main.go** — Main entry point", expected: "cmd/game/main.go"},
		{name: "path with dash description", input: "internal/game/board.go - Board representation", expected: "internal/game/board.go"},
		{name: "path with parenthetical", input: "internal/game/state.go (state management)", expected: "internal/game/state.go"},
		{name: "numbered list", input: "1. cmd/game/main.go", expected: "cmd/game/main.go"},
		{name: "bullet path", input: "- internal/game/game.go", expected: "internal/game/game.go"},
		{name: "plain path", input: "internal/core/game.go", expected: "internal/core/game.go"},
		{name: "create prefix", input: "Create internal/game/state.go", expected: "internal/game/state.go"},
		{name: "empty line", input: "", expected: ""},
		{name: "prose without path", input: "This implements the game logic", expected: ""},
		{name: "dot-slash prefix", input: "./cmd/app/main.go", expected: "cmd/app/main.go"},
		{name: "wrapped in parens", input: "(internal/game/state.go)", expected: "internal/game/state.go"},
		{name: "wrapped in brackets", input: "[internal/game/state.go]", expected: "internal/game/state.go"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseFilePath(tt.input)
			if result != tt.expected {
				t.Errorf("parseFilePath(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestExtractBacktickPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "prose with backtick path", input: "Here is `cmd/game/main.go`:", expected: "cmd/game/main.go"},
		{name: "file prefix", input: "File: `internal/state.go`", expected: "internal/state.go"},
		{name: "no backticks", input: "internal/state.go", expected: ""},
		{name: "backtick without path", input: "Use `fmt.Println` here", expected: ""},
		{name: "backtick with dot-slash", input: "Create `./cmd/app/main.go`", expected: "cmd/app/main.go"},
		{name: "single backtick only", input: "Just a `test", expected: ""},
		{name: "empty backticks", input: "Here ``", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractBacktickPath(tt.input)
			if result != tt.expected {
				t.Errorf("extractBacktickPath(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestExtractLeadingPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "em-dash separator", input: "cmd/game/main.go — Main entry point", expected: "cmd/game/main.go"},
		{name: "en-dash separator", input: "cmd/game/main.go – Entry", expected: "cmd/game/main.go"},
		{name: "hyphen separator", input: "internal/game/board.go - Board representation", expected: "internal/game/board.go"},
		{name: "parenthetical", input: "internal/game/state.go (state management)", expected: "internal/game/state.go"},
		{name: "no file extension", input: "just some words here", expected: ""},
		{name: "no separator", input: "two words", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractLeadingPath(tt.input)
			if result != tt.expected {
				t.Errorf("extractLeadingPath(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestPathFromFenceTag(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "lang and path", input: "go cmd/game/main.go", expected: "cmd/game/main.go"},
		{name: "title attribute", input: "go title=\"internal/game/state.go\"", expected: "internal/game/state.go"},
		{name: "file attribute", input: "file=internal/game/state.go", expected: "internal/game/state.go"},
		{name: "lang colon path", input: "go:cmd/game/main.go", expected: "cmd/game/main.go"},
		{name: "python colon path", input: "python:src/app.py", expected: "src/app.py"},
		{name: "lang only", input: "go", expected: ""},
		{name: "empty", input: "", expected: ""},
		{name: "colon without path", input: "go:", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pathFromFenceTag(tt.input)
			if result != tt.expected {
				t.Errorf("pathFromFenceTag(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
