package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/agent"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/multiplexer"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/orchestrator"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/skills"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tui"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
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
  clean                               Remove .orchestrator workspace`)

}

func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	reqPath := fs.String("requirements", "", "path to requirements.md")
	branch := fs.String("branch", "", "git branch to create and checkout")
	dryRun := fs.Bool("dry-run", false, "print intent and exit")
	ui := fs.String("ui", "tui", "ui mode: tui or plain")
	_ = fs.Parse(args)

	if *reqPath == "" {
		fmt.Fprintln(os.Stderr, "--requirements is required")
		os.Exit(2)
	}

	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	if *branch != "" {
		if err := checkoutBranch(root, *branch); err != nil {
			fatal(err)
		}
	}

	cfg, err := config.Load(root)
	if err != nil {
		fatal(fmt.Errorf("load config: %w", err))
	}

	ws, err := artifacts.EnsureWorkspace(root)
	if err != nil {
		fatal(err)
	}

	if *dryRun {
		fmt.Println("dry-run: workspace prepared, no agents executed")
		return
	}

	b := bus.New()
	if err := b.SetLogPath(ws.Path("runlog.jsonl")); err != nil {
		log.Printf("warn: could not open bus log: %v", err)
	}
	defer b.Close()

	agents := buildAgents(b, cfg, ws, root)
	pipeline := orchestrator.NewPipeline(b, agents, cfg, ws, root)
	ctx := context.Background()

	if *ui == "plain" {
		events := b.Subscribe()
		go plainLogger(events)
		if err := pipeline.Run(ctx, *reqPath); err != nil {
			fatal(err)
		}
		return
	}

	if *ui == "tmux" {
		runTmuxMode(ctx, b, pipeline, ws, root, *reqPath)
		return
	}

	// TUI mode (default).
	events := b.Subscribe()
	wsPath := ws.Path("")
	// Reuse the planner's runner for the chat overlay (best-effort).
	var chatLLM runner.LLMRunner
	if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" {
		chatLLM = runner.NewAnthropicRunner(apiKey, nil)
	}
	tuiModel := tui.New(events, pipeline, root, wsPath, chatLLM)
	p := tea.NewProgram(tuiModel, tea.WithAltScreen())

	go func() {
		if err := pipeline.Run(ctx, *reqPath); err != nil {
			log.Printf("pipeline error: %v", err)
		}
		p.Quit()
	}()

	if _, err := p.Run(); err != nil {
		fatal(err)
	}
}

func buildAgents(b *bus.Bus, cfg config.Config, ws artifacts.Workspace, root string) map[bus.AgentRole]agent.Agent {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")

	skillLoader := skills.New("")
	// Pre-fetch skills used by all agents (best-effort, non-blocking on failure).
	var allSkills []string
	for _, ac := range cfg.Agents {
		allSkills = append(allSkills, ac.Skills...)
	}
	if len(allSkills) > 0 && apiKey != "" {
		go func() {
			if err := skillLoader.Prefetch(allSkills); err != nil {
				log.Printf("warn: skill prefetch: %v", err)
			}
		}()
	}

	makeRunner := func(role string) runner.LLMRunner {
		ac := cfg.Agents[role]
		r, err := runner.New(ac, skillLoader, apiKey)
		if err != nil {
			log.Printf("warn: runner for %s: %v — falling back to codex", role, err)
			return runner.CodexRunner{}
		}
		return r
	}

	agents := make(map[bus.AgentRole]agent.Agent)

	plannerCfg := cfg.Agents["planner"]
	agents[bus.RolePlanner] = agent.NewPlannerAgent(b, makeRunner("planner"), ws,
		plannerCfg.Skills, plannerCfg.Model)

	coderCfg := cfg.Agents["coder"]
	agents[bus.RoleCoder] = agent.NewCoderAgent(b, makeRunner("coder"), ws,
		coderCfg.Skills, coderCfg.Model)

	agents[bus.RoleTester] = agent.NewTesterAgent(b, ws, root)

	reviewerCfg := cfg.Agents["reviewer"]
	agents[bus.RoleReviewer] = agent.NewReviewerAgent(b, makeRunner("reviewer"), ws,
		reviewerCfg.Skills, reviewerCfg.Model)

	fixerCfg := cfg.Agents["fixer"]
	agents[bus.RoleFixer] = agent.NewFixerAgent(b, makeRunner("fixer"), ws,
		fixerCfg.Skills, fixerCfg.Model)

	prCfg := cfg.Agents["pr"]
	agents[bus.RolePR] = agent.NewPRAgent(b, makeRunner("pr"), ws,
		prCfg.Skills, prCfg.Model)

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
		artifacts.PRDescriptionFile,
	} {
		printIfExists(ws, name)
	}
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
	case "architecture":
		return []string{artifacts.ArchitectureApprovedFile}
	case "plan":
		return []string{artifacts.PlanApprovedFile}
	case "prompts":
		return []string{artifacts.PromptsApprovedFile}
	case "all":
		return []string{
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
	if err := os.RemoveAll(dir); err != nil {
		fatal(err)
	}
	fmt.Println("removed", dir)
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

// runTmuxMode starts a tmux session with per-agent panes.
// Each pane runs `orchestrator agent` which subscribes to the bus over a Unix socket.
// The main process runs the pipeline normally; agents' bus events are visible in each pane.
func runTmuxMode(ctx context.Context, b *bus.Bus, pipeline *orchestrator.Pipeline, ws artifacts.Workspace, root, reqPath string) {
	sessionID := fmt.Sprintf("orch-%d", time.Now().UnixNano()%100000)
	socketPath := fmt.Sprintf("/tmp/%s.sock", sessionID)

	// Start bus socket server.
	stop := make(chan struct{})
	defer close(stop)
	if err := b.ServeUnix(socketPath, stop); err != nil {
		fatal(fmt.Errorf("tmux: bus socket: %w", err))
	}
	defer os.Remove(socketPath)

	mx := multiplexer.New(sessionID)
	if err := mx.CreateSession(sessionID); err != nil {
		fatal(fmt.Errorf("tmux: create session: %w", err))
	}
	defer mx.Close()

	agentRoles := []bus.AgentRole{
		bus.RolePlanner, bus.RoleCoder,
		bus.RoleTester, bus.RoleReviewer,
		bus.RoleFixer, bus.RolePR,
	}

	for _, role := range agentRoles {
		pane, err := mx.NewPane(role)
		if err != nil {
			log.Printf("warn: tmux pane for %s: %v", role, err)
			continue
		}
		if err := mx.SpawnAgent(pane, role, sessionID, socketPath); err != nil {
			log.Printf("warn: tmux spawn %s: %v", role, err)
		}
	}

	// Main window: Conversation Panel only (attach to session, window 0).
	events := b.Subscribe()
	tuiModel := tui.NewConvOnly(events, root)
	p := tea.NewProgram(tuiModel, tea.WithAltScreen())

	fmt.Printf("tmux session: %s\n  attach with: tmux attach -t %s\n", sessionID, sessionID)

	go func() {
		if err := pipeline.Run(ctx, reqPath); err != nil {
			log.Printf("pipeline error: %v", err)
		}
		p.Quit()
	}()

	if _, err := p.Run(); err != nil {
		fatal(err)
	}
}
