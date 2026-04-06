package agent

import "testing"

func TestSanitizeFilePath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		root     string
		expected string
	}{
		{
			name:     "already relative",
			path:     "internal/core/game.go",
			root:     "/Users/traq/dev/prv/tictactoe",
			expected: "internal/core/game.go",
		},
		{
			name:     "absolute path with project root",
			path:     "Users/traq/dev/prv/tictactoe/internal/cli/cli.go",
			root:     "/Users/traq/dev/prv/tictactoe",
			expected: "internal/cli/cli.go",
		},
		{
			name:     "nested absolute path",
			path:     "Users/traq/dev/prv/tictactoe/Users/traq/dev/prv/tictactoe/internal/core/core.go",
			root:     "/Users/traq/dev/prv/tictactoe",
			expected: "internal/core/core.go",
		},
		{
			name:     "with leading slash",
			path:     "/Users/traq/dev/prv/tictactoe/cmd/game/main.go",
			root:     "/Users/traq/dev/prv/tictactoe",
			expected: "cmd/game/main.go",
		},
		{
			name:     "cmd relative",
			path:     "cmd/game/main.go",
			root:     "",
			expected: "cmd/game/main.go",
		},
		{
			name:     "absolute without root",
			path:     "Users/traq/dev/prv/tictactoe/internal/core/core.go",
			root:     "",
			expected: "internal/core/core.go",
		},
		{
			name:     "home directory linux",
			path:     "home/user/project/internal/pkg/pkg.go",
			root:     "",
			expected: "internal/pkg/pkg.go",
		},
		{
			name:     "dot prefix",
			path:     "./cmd/app/main.go",
			root:     "",
			expected: "cmd/app/main.go",
		},
		{
			name:     "src directory",
			path:     "Users/dev/myproject/src/main.rs",
			root:     "",
			expected: "src/main.rs",
		},
		{
			name:     "config path",
			path:     "Users/traq/dev/prv/tictactoe/config/config.go",
			root:     "/Users/traq/dev/prv/tictactoe",
			expected: "config/config.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeFilePath(tt.path, tt.root)
			if result != tt.expected {
				t.Errorf("sanitizeFilePath(%q, %q) = %q, want %q", tt.path, tt.root, result, tt.expected)
			}
		})
	}
}
