package artifacts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	DirName                  = ".orchestrator"
	RequirementsFile         = "requirements.md"
	ArchitectureFile         = "architecture.md"
	ArchitectureApprovedFile = "architecture.approved"
	ImplementationPlanFile   = "implementation_plan.md"
	PlanApprovedFile         = "plan.approved"
	PromptsFile              = "prompts.md"
	PromptsApprovedFile      = "prompts.approved"
	PlanFile                 = "plan.json"
	PatchFile                = "patch.diff"
	ChangesFile              = "changes.md"
	TestCmdsFile             = "test_cmds.txt"
	TestReportFile           = "test_report.json"
	ReviewFile               = "review.md"
	PRDescriptionFile        = "pr_description.md"
	RunLogFile               = "runlog.txt"
)

// Workspace represents the .orchestrator directory inside a repo root.
type Workspace struct {
	Root string
	Dir  string
}

// EnsureWorkspace creates the .orchestrator directory if missing.
func EnsureWorkspace(root string) (Workspace, error) {
	if root == "" {
		return Workspace{}, fmt.Errorf("root is required")
	}
	dir := filepath.Join(root, DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Workspace{}, fmt.Errorf("create workspace: %w", err)
	}
	return Workspace{Root: root, Dir: dir}, nil
}

// Path returns a path to a known artifact file inside the workspace.
func (w Workspace) Path(name string) string {
	return filepath.Join(w.Dir, name)
}

func (w Workspace) WriteFile(name string, data []byte) error {
	path := w.Path(name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

func (w Workspace) ReadFile(name string) ([]byte, error) {
	path := w.Path(name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	return data, nil
}

func (w Workspace) WriteJSON(name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", name, err)
	}
	data = append(data, '\n')
	return w.WriteFile(name, data)
}

func (w Workspace) ReadJSON(name string, v any) error {
	data, err := w.ReadFile(name)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("unmarshal %s: %w", name, err)
	}
	return nil
}

// AppendRunLog appends a single line with RFC3339 timestamp.
func (w Workspace) AppendRunLog(message string) error {
	path := w.Path(RunLogFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open runlog: %w", err)
	}
	defer f.Close()

	line := fmt.Sprintf("%s %s\n", time.Now().Format(time.RFC3339), message)
	if _, err := f.WriteString(line); err != nil {
		return fmt.Errorf("write runlog: %w", err)
	}
	return nil
}
