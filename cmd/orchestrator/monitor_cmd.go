package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tui"
)

func monitorCmd(args []string) {
	fs := flag.NewFlagSet("monitor", flag.ExitOnError)
	rootFlag := fs.String("root", "", "repo root")
	busAddrFlag := fs.String("bus-addr", "", "unix socket path of the bus server")
	if err := fs.Parse(args); err != nil {
		fatal(err)
	}

	if *rootFlag == "" || *busAddrFlag == "" {
		_, _ = fmt.Fprintln(os.Stderr, "usage: orchestrator monitor --root=<repo> --bus-addr=<path>")
		os.Exit(2)
	}

	ws, err := artifacts.EnsureWorkspace(*rootFlag)
	if err != nil {
		fatal(err)
	}

	client, err := bus.DialUnix(*busAddrFlag)
	if err != nil {
		fatal(fmt.Errorf("monitor: connect to bus: %w", err))
	}
	defer func() { _ = client.Close() }()

	cfg, _ := config.Load(*rootFlag)

	var chatLLM runner.LLMRunner
	if plannerCfg, ok := cfg.Agents["planner"]; ok {
		if r, err := runner.New(plannerCfg, nil, cfg.PromptLanguage); err == nil {
			chatLLM = r
		}
	}
	if chatLLM == nil {
		chatLLM = runner.NewOllamaRunner("")
	}

	model := tui.NewControl(client.Subscribe(), nil, *rootFlag, ws.Path(""), chatLLM, cfg)
	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fatal(err)
	}
}
