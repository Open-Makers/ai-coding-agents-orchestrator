package agent

import "testing"

func TestSanitiseTestCmd_OK(t *testing.T) {
	valid := []string{
		"go test ./...",
		"npm run test",
		"python -m pytest tests/",
		"cargo test",
		"make test",
		"./scripts/test.sh",
		"go test -race -timeout 120s ./...",
	}
	for _, cmd := range valid {
		if err := sanitiseTestCmd(cmd); err != nil {
			t.Errorf("expected %q to be valid, got: %v", cmd, err)
		}
	}
}

func TestSanitiseTestCmd_Rejected(t *testing.T) {
	cases := []struct {
		cmd    string
		reason string
	}{
		{"go test ./...\nrm -rf /", "embedded newline"},
		{"go test ./...\rrm -rf /", "embedded carriage return"},
		{"go test " + string(make([]byte, maxTestCmdLen)), "too long"},
		{"echo \x00hello", "null byte (control char)"},
		{"echo \x01hello", "SOH control char"},
	}
	for _, tc := range cases {
		if err := sanitiseTestCmd(tc.cmd); err == nil {
			t.Errorf("expected %q to be rejected (%s)", tc.cmd, tc.reason)
		}
	}
}
