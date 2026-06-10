package runner

import (
	"testing"
)

func TestCountCodexTokens_Basic(t *testing.T) {
	usage := countCodexTokens("gpt-4o", "hello world system prompt", "hello world response")
	if usage.InputTokens <= 0 {
		t.Errorf("expected positive InputTokens, got %d", usage.InputTokens)
	}
	if usage.OutputTokens <= 0 {
		t.Errorf("expected positive OutputTokens, got %d", usage.OutputTokens)
	}
	if usage.Estimated {
		t.Error("Estimated should be false when tiktoken succeeds")
	}
}

func TestCountCodexTokens_UnknownModel_FallsBackToEstimate(t *testing.T) {
	usage := countCodexTokens("unknown-model-xyz", "hello", "world")
	if usage.InputTokens <= 0 {
		t.Errorf("expected positive InputTokens even on fallback, got %d", usage.InputTokens)
	}
	if !usage.Estimated {
		t.Error("Estimated should be true on tiktoken fallback")
	}
}

func TestParseCodexConfigModel(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "exact model key",
			toml: "model = \"gpt-5.5\"\n",
			want: "gpt-5.5",
		},
		{
			name: "ignores model_provider and similar keys",
			toml: "model_provider = \"omlx\"\nmodel_reasoning_effort = \"high\"\nmodel = \"gpt-5.3-codex\"\n",
			want: "gpt-5.3-codex",
		},
		{
			name: "only foreign model_* keys yields no model",
			toml: "model_provider = \"omlx\"\nmodel_reasoning_effort = \"high\"\n",
			want: "",
		},
		{
			name: "stops at section header",
			toml: "[profiles.foo]\nmodel = \"should-not-read\"\n",
			want: "",
		},
		{
			name: "single quotes and spacing",
			toml: "model   =   'gpt-5.4'\n",
			want: "gpt-5.4",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseCodexConfigModel(tc.toml); got != tc.want {
				t.Errorf("parseCodexConfigModel() = %q, want %q", got, tc.want)
			}
		})
	}
}
