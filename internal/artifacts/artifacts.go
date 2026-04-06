package artifacts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DirName                  = ".orchestrator"
	ProjectConfigFile        = "project.yaml"
	ModulePathFile           = "module_path"
	RequirementsFile         = "requirements.md"
	VisionFile               = "vision.md"
	VisionApprovedFile       = "vision.approved"
	MoscowFile               = "moscow.md"
	ArchitectureFile         = "architecture.md"
	ArchitectureApprovedFile = "architecture.approved"
	ImplementationPlanFile   = "implementation_plan.md"
	PlanApprovedFile         = "plan.approved"
	PromptsFile              = "prompts.md"
	PromptsApprovedFile      = "prompts.approved"
	PlanFile                 = "plan.json"
	RawCoderOutputFile       = "coder_output.md"
	ChangesFile              = "changes.md"
	TestCmdsFile             = "test_cmds.txt"
	TestReportFile           = "test_report.json"
	ReviewFile               = "review.md"
	UXReviewFile             = "ux_review.md"
	SecurityReviewFile       = "security_review.md"
	QAReviewFile             = "qa_review.md"
	NiceToHaveFile           = "nice_to_have.md"
	SummaryFile              = "summary.md"
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
// Returns an empty string if name is empty.
func (w Workspace) Path(name string) string {
	if name == "" {
		return w.Dir
	}
	return filepath.Join(w.Dir, name)
}

func (w Workspace) WriteFile(name string, data []byte) error {
	if strings.Contains(name, "..") {
		return fmt.Errorf("write %s: path traversal rejected", name)
	}
	path := w.Path(name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

func (w Workspace) ReadFile(name string) ([]byte, error) {
	if strings.Contains(name, "..") {
		return nil, fmt.Errorf("read %s: path traversal rejected", name)
	}
	path := w.Path(name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	return data, nil
}

// FileExists returns true if the artifact file exists and is non-empty.
func (w Workspace) FileExists(name string) bool {
	info, err := os.Stat(w.Path(name))
	return err == nil && info.Size() > 0
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

// generatedArtifacts lists all files produced by PM, planner, and pipeline phases.
// Requirements are intentionally excluded — they are user input.
var generatedArtifacts = []string{
	VisionFile,
	VisionApprovedFile,
	MoscowFile,
	ArchitectureFile,
	ArchitectureApprovedFile,
	ImplementationPlanFile,
	PlanApprovedFile,
	PromptsFile,
	PromptsApprovedFile,
	PlanFile,
	RawCoderOutputFile,
	ChangesFile,
	TestCmdsFile,
	TestReportFile,
	ReviewFile,
	UXReviewFile,
	SecurityReviewFile,
	QAReviewFile,
	NiceToHaveFile,
	SummaryFile,
}

// CleanGeneratedArtifacts removes all generated artifacts from the workspace,
// forcing the pipeline to regenerate everything from scratch.
func (w Workspace) CleanGeneratedArtifacts() {
	for _, name := range generatedArtifacts {
		_ = os.Remove(w.Path(name))
	}
}

// AppendRunLog appends a single line with RFC3339 timestamp.
func (w Workspace) AppendRunLog(message string) error {
	path := w.Path(RunLogFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open runlog: %w", err)
	}
	defer func() { _ = f.Close() }()

	line := fmt.Sprintf("%s %s\n", time.Now().Format(time.RFC3339), message)
	if _, err := f.WriteString(line); err != nil {
		return fmt.Errorf("write runlog: %w", err)
	}
	return nil
}
