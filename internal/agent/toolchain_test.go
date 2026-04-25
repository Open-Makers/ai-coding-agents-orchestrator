package agent

import "testing"

func TestDetectGoToolchain_ReturnsCurrentGoVersion(t *testing.T) {
	got := detectGoToolchain()
	if got == "" {
		t.Skip("go binary not available in PATH")
	}
	if got[:2] != "go" {
		t.Errorf("expected version to start with 'go', got %q", got)
	}
}

func TestGoVersionRegex_ExtractsVersionToken(t *testing.T) {
	cases := map[string]string{
		"go version go1.26.0 darwin/arm64":  "go1.26.0",
		"go version go1.25 linux/amd64":     "go1.25",
		"go version go1.22.4 windows/amd64": "go1.22.4",
		"unrelated text":                    "",
	}
	for in, want := range cases {
		if got := goVersionRegex.FindString(in); got != want {
			t.Errorf("FindString(%q) = %q, want %q", in, got, want)
		}
	}
}
