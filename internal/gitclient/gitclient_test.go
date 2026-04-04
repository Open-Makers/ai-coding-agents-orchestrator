package gitclient

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func initRepo(t *testing.T) (string, *GitClient) {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")

	f := filepath.Join(dir, "hello.go")
	if err := os.WriteFile(f, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "initial")

	return dir, New(dir)
}

func TestStatus_ModifiedFile(t *testing.T) {
	dir, gc := initRepo(t)

	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := gc.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one modified file")
	}
	found := false
	for _, f := range files {
		if f.Path == "hello.go" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("hello.go not in status output: %+v", files)
	}
}

func TestDiff_NonEmpty(t *testing.T) {
	dir, gc := initRepo(t)

	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	diff, err := gc.Diff("hello.go")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff == "" {
		t.Error("expected non-empty diff")
	}
}

func TestStageUnstage(t *testing.T) {
	dir, gc := initRepo(t)

	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n// v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := gc.Stage("hello.go"); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	files, err := gc.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	staged := false
	for _, f := range files {
		if f.Path == "hello.go" && f.Staged {
			staged = true
		}
	}
	if !staged {
		t.Errorf("expected hello.go to be staged: %+v", files)
	}

	if err := gc.Unstage("hello.go"); err != nil {
		t.Fatalf("Unstage: %v", err)
	}

	files, _ = gc.Status()
	for _, f := range files {
		if f.Path == "hello.go" && f.Staged {
			t.Error("expected hello.go to be unstaged")
		}
	}
}

func TestResetFile(t *testing.T) {
	dir, gc := initRepo(t)

	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("broken content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := gc.ResetFile("hello.go"); err != nil {
		t.Fatalf("ResetFile: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "hello.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "package main\n" {
		t.Errorf("file not reset, content: %q", string(data))
	}
}
