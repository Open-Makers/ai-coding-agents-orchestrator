package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/agent"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/logging"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/orchestrator"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/skills"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tui"
)

// version is set at build time via -ldflags "-X main.version=<ver>".
var version = "dev"

func main() {
	logging.Setup()
	tui.Version = version
	if len(os.Args) < 2 {
		// No sub-command: open the TUI picker and let the user choose requirements.
		runCmd(nil)
		return
	}

	switch os.Args[1] {
	case "run":
		runCmd(os.Args[2:])
	case "agent":
		agentCmd(os.Args[2:])
	case "resume":
		fmt.Fprintln(os.Stderr, "resume not implemented yet")
		os.Exit(2)
	case "report":
		reportCmd(os.Args[2:])
	case "monitor":
		monitorCmd(os.Args[2:])
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
	  monitor                             Open the control TUI against a running bus
	  approve                             Create approval marker
	  clean                               Remove .orchestrator workspace`)

}

func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	reqPath := fs.String("requirements", "", "path to requirements.md (omit to open picker)")
	branch := fs.String("branch", "", "git branch to create and checkout")
	modulePath := fs.String("module", "", "Go module path (e.g. github.com/user/project)")
	dryRun := fs.Bool("dry-run", false, "print intent and exit")
	ui := fs.String("ui", "auto", "ui mode: auto, tui, or plain")
	_ = fs.Parse(args)

	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	cfg, err := config.Load(root)
	if err != nil {
		fatal(fmt.Errorf("load config: %w", err))
	}

	ws, err := artifacts.EnsureWorkspace(root)
	if err != nil {
		fatal(err)
	}

	if err := logging.SetupFile(ws.Path(logging.LogFileName)); err != nil {
		fmt.Fprintf(os.Stderr, "warn: could not open log file: %v\n", err)
	}
	defer logging.Close()

	// Migrate legacy module_path file into project config.
	if cfg.Project.ModulePath == "" {
		if data, err := ws.ReadFile(artifacts.ModulePathFile); err == nil {
			cfg.Project.ModulePath = strings.TrimSpace(string(data))
			_ = os.Remove(ws.Path(artifacts.ModulePathFile))
		}
	}

	// Override with --module flag if provided.
	if *modulePath != "" {
		cfg.Project.ModulePath = *modulePath
	}

	// Auto-detect from existing go.mod if still empty.
	if cfg.Project.ModulePath == "" {
		cfg.Project.ModulePath = detectGoModulePath(root)
	}

	// Persist module path to project config for future runs.
	if cfg.Project.ModulePath != "" {
		projectCfg := config.LoadProject(root)
		if projectCfg.Project.ModulePath != cfg.Project.ModulePath {
			projectCfg.Project.ModulePath = cfg.Project.ModulePath
			_ = config.Save(root, projectCfg)
		}
	}

	// If module path is still missing for a Go project, prompt the user.
	if tui.NeedsModulePath(root, cfg) && !*dryRun && *ui != "plain" {
		chosen, err := tui.RunModulePathPrompt(root, ws.Path(""))
		if err != nil {
			fatal(fmt.Errorf("module path prompt: %w", err))
		}
		if chosen == "" {
			return // user cancelled
		}
		cfg.Project.ModulePath = chosen
	}

	// No requirements given — open the TUI home/picker (unless plain/dry-run mode).
	if *reqPath == "" && !*dryRun && *ui != "plain" {
		chosen, updatedCfg, err := tui.RunStartup(root, ws.Path(artifacts.RequirementsFile), cfg)
		if err != nil {
			fatal(fmt.Errorf("picker: %w", err))
		}
		if chosen == "" {
			return // user quit
		}
		*reqPath = chosen
		cfg = updatedCfg
	}

	if *reqPath == "" {
		fmt.Fprintln(os.Stderr, "--requirements is required in plain/dry-run mode")
		os.Exit(2)
	}

	if *branch != "" {
		if err := checkoutBranch(root, *branch); err != nil {
			fatal(err)
		}
	}

	if *dryRun {
		fmt.Println("dry-run: workspace prepared, no agents executed")
		return
	}

	resolvedUI := resolveUIMode(*ui)

	b := bus.New()
	if err := b.SetLogPath(ws.Path("runlog.jsonl")); err != nil {
		slog.Warn("could not open bus log", slog.String("error", err.Error()))
	}
	defer b.Close()

	ctx := context.Background()

	if resolvedUI == "plain" {
		agents := buildAgents(b, cfg, ws, root)
		pipeline := orchestrator.NewPipeline(b, agents, cfg, ws, root)
		events := b.Subscribe()
		go plainLogger(events)
		if err := pipeline.Run(ctx, *reqPath); err != nil {
			fatal(err)
		}
		return
	}

	// TUI mode (default) — loop supports returning to main menu after pipeline.
	for {
		events := b.Subscribe()
		wsPath := ws.Path("")
		chatLLM := runner.OpenCodeRunner{}
		tuiModel := tui.New(events, nil, root, wsPath, chatLLM, cfg)
		p := tea.NewProgram(tuiModel, tea.WithAltScreen())

		pipelineCtx, pipelineCancel := context.WithCancel(ctx)

		go func() {
			agents := buildAgents(b, cfg, ws, root)
			pipeline := orchestrator.NewPipeline(b, agents, cfg, ws, root)
			p.Send(tui.PipelineReadyWithCancelMsg(pipeline, pipelineCancel))
			err := pipeline.Run(pipelineCtx, *reqPath)
			if pipelineCtx.Err() != nil {
				err = fmt.Errorf("pipeline cancelled by user")
			}
			p.Send(tui.PipelineDoneMsg{Err: err})
		}()

		result, err := p.Run()
		if err != nil {
			fatal(err)
		}

		// Check if user wants to return to main menu.
		final, ok := result.(tui.Model)
		if !ok || !final.ReturnToMenu() {
			break
		}

		// Re-open the startup picker to choose new requirements.
		chosen, updatedCfg, err := tui.RunStartup(root, ws.Path(artifacts.RequirementsFile), cfg)
		if err != nil {
			fatal(fmt.Errorf("picker: %w", err))
		}
		if chosen == "" {
			break // user quit from menu
		}

		*reqPath = chosen
		cfg = updatedCfg
	}
}

func resolveUIMode(requested string) string {
	switch requested {
	case "", "auto":
		return "tui"
	case "plain", "tui":
		return requested
	case "tmux", "cmux", "cmux-internal":
		slog.Warn("ui mode no longer supported, using tui", slog.String("requested", requested))
		return "tui"
	default:
		slog.Warn("unknown ui mode, falling back to tui", slog.String("requested", requested))
		return "tui"
	}
}

func buildAgents(b *bus.Bus, cfg config.Config, ws artifacts.Workspace, root string) map[bus.AgentRole]agent.Agent {
	skillLoader := skills.New("")
	var allSkills []string
	for _, ac := range cfg.Agents {
		allSkills = append(allSkills, ac.Skills...)
	}
	if len(allSkills) > 0 {
		go func() {
			if err := skillLoader.Prefetch(allSkills); err != nil {
				slog.Warn("skill prefetch failed", slog.String("error", err.Error()))
			}
		}()
	}

	makeRunner := func(role string) runner.LLMRunner {
		ac := cfg.Agents[role]
		r, err := runner.New(ac, skillLoader, cfg.PromptLanguage)
		if err != nil {
			slog.Warn("runner creation failed, falling back to opencode",
				slog.String("role", role),
				slog.String("error", err.Error()),
			)
			return runner.OpenCodeRunner{}
		}
		return r
	}

	agents := make(map[bus.AgentRole]agent.Agent)

	pmCfg := cfg.Agents["pm"]
	agents[bus.RolePM] = agent.NewPMAgent(b, makeRunner("pm"), ws,
		pmCfg.Skills, pmCfg.Model)

	plannerCfg := cfg.Agents["planner"]
	agents[bus.RolePlanner] = agent.NewPlannerAgent(b, makeRunner("planner"), ws,
		plannerCfg.Skills, plannerCfg.Model)

	coderCfg := cfg.Agents["coder"]
	agents[bus.RoleCoder] = agent.NewCoderAgent(b, makeRunner("coder"), ws, root, cfg,
		coderCfg.Skills, coderCfg.Model)

	testerCfg := cfg.Agents["tester"]
	agents[bus.RoleTester] = agent.NewTesterAgent(b, makeRunner("tester"), ws, root, cfg,
		testerCfg.Skills, testerCfg.Model)

	reviewerCfg := cfg.Agents["reviewer"]
	agents[bus.RoleReviewer] = agent.NewReviewerAgent(b, makeRunner("reviewer"), ws,
		reviewerCfg.Skills, reviewerCfg.Model)

	uxCfg := cfg.Agents["ux_reviewer"]
	agents[bus.RoleUXReviewer] = agent.NewUXReviewerAgent(b, makeRunner("ux_reviewer"), ws,
		uxCfg.Skills, uxCfg.Model)

	secCfg := cfg.Agents["security"]
	agents[bus.RoleSecurity] = agent.NewSecurityAgent(b, makeRunner("security"), ws,
		secCfg.Skills, secCfg.Model)

	qaCfg := cfg.Agents["qa"]
	agents[bus.RoleQA] = agent.NewQAAgent(b, makeRunner("qa"), ws,
		qaCfg.Skills, qaCfg.Model)

	return agents
}

func plainLogger(events <-chan bus.Message) {
	for msg := range events {
		switch msg.Type {
		case bus.MsgHumanGate:
			fmt.Printf("\n[GATE] %v\n\n", msg.Payload)
		case bus.MsgEvent:
			if tp, ok := msg.Payload.(bus.TokenPayload); ok {
				if !tp.Done {
					fmt.Print(tp.Text)
				}
			} else {
				fmt.Printf("[%s] %v\n", msg.From, msg.Payload)
			}
		}
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
			fatal(err)
		}
		root = cwd
	}

	ws, err := artifacts.EnsureWorkspace(root)
	if err != nil {
		fatal(err)
	}

	for _, name := range []string{
		artifacts.ImplementationPlanFile,
		artifacts.TestReportFile,
		artifacts.ReviewFile,
	} {
		printIfExists(ws, name)
	}
}

func approveCmd(args []string) {
	fs := flag.NewFlagSet("approve", flag.ExitOnError)
	rootFlag := fs.String("root", "", "repo root (default: cwd)")
	_ = fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: orchestrator approve <vision|architecture|plan|prompts|all>")
		os.Exit(2)
	}

	root := *rootFlag
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fatal(err)
		}
		root = cwd
	}

	ws, err := artifacts.EnsureWorkspace(root)
	if err != nil {
		fatal(err)
	}

	target := strings.ToLower(fs.Arg(0))
	markers := markerList(target)
	if markers == nil {
		fmt.Fprintf(os.Stderr, "unknown approval target: %s\n", target)
		os.Exit(2)
	}
	for _, m := range markers {
		if err := writeApproval(ws, m); err != nil {
			fatal(err)
		}
	}
}

func markerList(target string) []string {
	switch target {
	case "vision":
		return []string{artifacts.VisionApprovedFile}
	case "architecture":
		return []string{artifacts.ArchitectureApprovedFile}
	case "plan":
		return []string{artifacts.PlanApprovedFile}
	case "prompts":
		return []string{artifacts.PromptsApprovedFile}
	case "all":
		return []string{
			artifacts.VisionApprovedFile,
			artifacts.ArchitectureApprovedFile,
			artifacts.PlanApprovedFile,
			artifacts.PromptsApprovedFile,
		}
	}
	return nil
}

func cleanCmd(args []string) {
	fs := flag.NewFlagSet("clean", flag.ExitOnError)
	rootFlag := fs.String("root", "", "repo root (default: cwd)")
	_ = fs.Parse(args)

	root := *rootFlag
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fatal(err)
		}
		root = cwd
	}

	dir := filepath.Join(root, artifacts.DirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("nothing to clean")
			return
		}
		fatal(err)
	}

	preserve := map[string]bool{
		artifacts.RequirementsFile:  true,
		artifacts.ProjectConfigFile: true,
	}
	for _, entry := range entries {
		if preserve[entry.Name()] {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			fatal(err)
		}
	}
	fmt.Printf("cleaned %s (preserved %v)\n", dir, preserve)
}

func printIfExists(ws artifacts.Workspace, name string) {
	data, err := ws.ReadFile(name)
	if err != nil {
		return
	}
	// Pretty-print JSON files.
	if strings.HasSuffix(name, ".json") {
		var v any
		if json.Unmarshal(data, &v) == nil {
			pretty, _ := json.MarshalIndent(v, "", "  ")
			fmt.Printf("== %s ==\n%s\n\n", name, pretty)
			return
		}
	}
	fmt.Printf("== %s ==\n%s\n\n", name, strings.TrimSpace(string(data)))
}

func writeApproval(ws artifacts.Workspace, marker string) error {
	if err := ws.WriteFile(marker, []byte("approved\n")); err != nil {
		return err
	}
	fmt.Println("created", ws.Path(marker))
	return nil
}

func checkoutBranch(root, branch string) error {
	cmd := exec.Command("git", "checkout", "-b", branch)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err == nil {
		return nil
	}
	cmd = exec.Command("git", "checkout", branch)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// detectGoModulePath reads the module path from an existing go.mod file.
func detectGoModulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module"))
		}
	}
	return ""
}
