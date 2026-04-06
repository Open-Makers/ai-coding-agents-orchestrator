package gitclient

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// FileStatus represents the git status of a single file.
type FileStatus struct {
	Path     string
	Staged   bool
	Unstaged bool
	Code     string // two-char porcelain code, e.g. "M ", " M", "??"
}

// CommitEntry is a single git log line.
type CommitEntry struct {
	Hash    string
	Message string
}

// GitClient wraps exec git calls for a specific repo root.
type GitClient struct {
	Root string
}

func New(root string) *GitClient {
	return &GitClient{Root: root}
}

// Status returns modified/staged/untracked files.
func (g *GitClient) Status() ([]FileStatus, error) {
	out, err := g.run("status", "--porcelain")
	if err != nil {
		return nil, err
	}
	var files []FileStatus
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 3 {
			continue
		}
		code := line[:2]
		path := strings.TrimSpace(line[2:])
		files = append(files, FileStatus{
			Path:     path,
			Code:     code,
			Staged:   code[0] != ' ' && code[0] != '?',
			Unstaged: code[1] != ' ',
		})
	}
	return files, nil
}

// Diff returns the unstaged diff for path.
func (g *GitClient) Diff(path string) (string, error) {
	return g.run("diff", "HEAD", "--", path)
}

// StagedDiff returns the staged diff for path.
func (g *GitClient) StagedDiff(path string) (string, error) {
	return g.run("diff", "--cached", "--", path)
}

// Stage stages a file.
func (g *GitClient) Stage(path string) error {
	_, err := g.run("add", path)
	return err
}

// Unstage removes a file from staging.
func (g *GitClient) Unstage(path string) error {
	_, err := g.run("restore", "--staged", path)
	return err
}

// Commit creates a commit with the given message.
func (g *GitClient) Commit(msg string) error {
	_, err := g.run("commit", "-m", msg)
	return err
}

// ResetFile discards all changes to path (staged + unstaged).
func (g *GitClient) ResetFile(path string) error {
	_, err := g.run("checkout", "HEAD", "--", path)
	return err
}

// Log returns commits on current branch since base (e.g. "main").
func (g *GitClient) Log(base string) ([]CommitEntry, error) {
	ref := base + "..HEAD"
	out, err := g.run("log", ref, "--oneline", "--no-decorate")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	var commits []CommitEntry
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		entry := CommitEntry{Hash: parts[0]}
		if len(parts) == 2 {
			entry.Message = parts[1]
		}
		commits = append(commits, entry)
	}
	return commits, nil
}

// RemoteURL returns the fetch URL of the given remote (e.g. "origin").
func (g *GitClient) RemoteURL(name string) (string, error) {
	return g.run("remote", "get-url", name)
}

func (g *GitClient) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...) //nolint:gosec // #nosec G204 -- args built internally for git operations
	cmd.Dir = g.Root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "),
			err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(stdout.String(), " \t\r\n"), nil
}
