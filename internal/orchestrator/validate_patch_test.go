package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidatePatch_Clean(t *testing.T) {
	patch := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1 +1,2 @@
 package main
+// added
`
	f := writeTmp(t, patch)
	if err := validatePatch(f); err != nil {
		t.Fatalf("expected clean patch to pass: %v", err)
	}
}

func TestValidatePatch_TraversalRejected(t *testing.T) {
	cases := []string{
		// Path traversal in diff header
		"--- a/../../etc/passwd\n+++ b/../../etc/passwd\n",
		// Attempt via b/ prefix
		"+++ b/../secret.txt\n",
	}
	for _, patch := range cases {
		f := writeTmp(t, patch)
		if err := validatePatch(f); err == nil {
			t.Errorf("expected traversal patch to be rejected:\n%s", patch)
		}
	}
}

func TestValidatePatch_MissingFile(t *testing.T) {
	if err := validatePatch("/tmp/does-not-exist-orchestrator.diff"); err == nil {
		t.Error("expected error for missing patch file")
	}
}

func writeTmp(t *testing.T, content string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "test.patch")
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}
