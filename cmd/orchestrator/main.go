package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/orchestrator"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "run":
		runCmd(os.Args[2:])
	case "resume":
		fmt.Fprintln(os.Stderr, "resume not implemented yet")
		os.Exit(2)
	case "report":
		reportCmd(os.Args[2:])
	case "approve":
		approveCmd(os.Args[2:])
	case "clean":
		cleanCmd(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println(`orchestrator <command>

Commands:
  run --requirements requirements.md  Run a full orchestration workflow
  resume                              Resume last run (not implemented)
  report                              Summarize last run artifacts
  approve                             Create approval marker
  clean                               Remove .orchestrator workspace
`)
}

func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	reqPath := fs.String("requirements", "", "path to requirements.md")
	branch := fs.String("branch", "", "git branch to create and checkout")
	dryRun := fs.Bool("dry-run", false, "print intent and exit")
	humanApprove := fs.Bool("human-approve", false, "prompt before applying patches or running tests")
	_ = fs.Parse(args)

	if *reqPath == "" {
		fmt.Fprintln(os.Stderr, "--requirements is required")
		os.Exit(2)
	}

	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if *branch != "" {
		if err := checkoutBranch(root, *branch); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	ws, err := artifacts.EnsureWorkspace(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := copyTask(*reqPath, ws.Path(artifacts.RequirementsFile)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if *dryRun {
		fmt.Println("dry-run: workspace prepared, no agents executed")
		return
	}

	orch := orchestrator.Orchestrator{
		Config:       orchestrator.Config{MaxFixAttempts: 3},
		Runner:       runner.CodexRunner{},
		Tester:       orchestrator.CommandTester{},
		ReviewParser: orchestrator.SimpleReviewParser{},
		Patcher:      humanGate(orchestrator.GitPatcher{}, *humanApprove),
	}

	if err := orch.Run(root, *reqPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func reportCmd(args []string) {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	rootFlag := fs.String("root", "", "repo root (default: cwd)")
	_ = fs.Parse(args)

	root := *rootFlag
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		root = cwd
	}

	ws, err := artifacts.EnsureWorkspace(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	printIfExists(ws, artifacts.PlanFile)
	printIfExists(ws, artifacts.TestReportFile)
	printIfExists(ws, artifacts.ReviewFile)
	printIfExists(ws, artifacts.PRDescriptionFile)
}

func approveCmd(args []string) {
	fs := flag.NewFlagSet("approve", flag.ExitOnError)
	rootFlag := fs.String("root", "", "repo root (default: cwd)")
	_ = fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: orchestrator approve <architecture|plan|prompts|all>")
		os.Exit(2)
	}

	root := *rootFlag
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		root = cwd
	}

	ws, err := artifacts.EnsureWorkspace(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	target := strings.ToLower(fs.Arg(0))
	if target == "all" {
		markers := []string{
			artifacts.ArchitectureApprovedFile,
			artifacts.PlanApprovedFile,
			artifacts.PromptsApprovedFile,
		}
		for _, marker := range markers {
			if err := writeApproval(ws, marker); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
		return
	}

	marker, err := approvalMarker(target)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if err := writeApproval(ws, marker); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func cleanCmd(args []string) {
	fs := flag.NewFlagSet("clean", flag.ExitOnError)
	rootFlag := fs.String("root", "", "repo root (default: cwd)")
	_ = fs.Parse(args)

	root := *rootFlag
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		root = cwd
	}

	dir := filepath.Join(root, artifacts.DirName)
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("removed", dir)
}

func copyTask(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func printIfExists(ws artifacts.Workspace, name string) {
	data, err := ws.ReadFile(name)
	if err != nil {
		return
	}
	fmt.Printf("== %s ==\n%s\n", name, strings.TrimSpace(string(data)))
}

func approvalMarker(target string) (string, error) {
	switch strings.ToLower(target) {
	case "architecture":
		return artifacts.ArchitectureApprovedFile, nil
	case "plan":
		return artifacts.PlanApprovedFile, nil
	case "prompts":
		return artifacts.PromptsApprovedFile, nil
	default:
		return "", fmt.Errorf("unknown approval target: %s", target)
	}
}

func writeApproval(ws artifacts.Workspace, marker string) error {
	path := ws.Path(marker)
	if err := os.WriteFile(path, []byte("approved\n"), 0o644); err != nil {
		return err
	}
	_ = ws.AppendRunLog("approval created: " + marker)
	fmt.Println("created", path)
	return nil
}

func checkoutBranch(root, branch string) error {
	cmd := execCommand(root, "git", "checkout", "-b", branch)
	if err := cmd.Run(); err == nil {
		return nil
	}
	cmd = execCommand(root, "git", "checkout", branch)
	return cmd.Run()
}

func execCommand(root, name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

type gatingPatcher struct {
	inner  orchestrator.Patcher
	prompt bool
}

func humanGate(p orchestrator.Patcher, enabled bool) orchestrator.Patcher {
	if !enabled {
		return p
	}
	return gatingPatcher{inner: p, prompt: true}
}

func (g gatingPatcher) Apply(ctx *orchestrator.Context, patchPath string) error {
	if g.prompt {
		if !confirm("Apply patch?") {
			return fmt.Errorf("patch application declined")
		}
	}
	return g.inner.Apply(ctx, patchPath)
}

func confirm(msg string) bool {
	fmt.Printf("%s [y/N]: ", msg)
	var resp string
	_, _ = fmt.Fscanln(os.Stdin, &resp)
	resp = strings.TrimSpace(strings.ToLower(resp))
	return resp == "y" || resp == "yes"
}
