package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
)

// agentCmd is the "orchestrator agent" sub-command.
// It connects to a remote bus via Unix socket, subscribes for messages
// targeted at its role, and streams them to stdout.
//
// In tmux mode, this process runs in its own pane and displays agent output.
func agentCmd(args []string) {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	roleFlag := fs.String("role", "", "agent role (planner|coder|tester|reviewer|fixer|pr)")
	sessionFlag := fs.String("session", "", "orchestrator session ID")
	busAddrFlag := fs.String("bus-addr", "", "unix socket path of the bus server")
	_ = fs.Parse(args)

	if *roleFlag == "" || *busAddrFlag == "" {
		fmt.Fprintln(os.Stderr, "usage: orchestrator agent --role=<role> --session=<id> --bus-addr=<path>")
		os.Exit(2)
	}

	validRoles := map[string]bool{
		"planner": true, "coder": true, "tester": true,
		"reviewer": true, "fixer": true, "pr": true,
	}
	if !validRoles[*roleFlag] {
		fmt.Fprintf(os.Stderr, "invalid --role %q (must be one of: planner, coder, tester, reviewer, fixer, pr)\n", *roleFlag)
		os.Exit(2)
	}

	role := bus.AgentRole(*roleFlag)

	client, err := bus.DialUnix(*busAddrFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent %s: connect to bus: %v\n", role, err)
		os.Exit(1)
	}
	defer client.Close()

	fmt.Printf("[%s] connected to session %s\n", role, *sessionFlag)

	msgs := client.Subscribe()

	// Handle SIGINT/SIGTERM gracefully.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-sig:
			return
		case m, ok := <-msgs:
			if !ok {
				return
			}
			printAgentMsg(role, m)
		}
	}
}

// printAgentMsg prints relevant bus messages to stdout in the agent pane.
func printAgentMsg(myRole bus.AgentRole, m bus.Message) {
	// Show messages directed at or from this role.
	if m.From != myRole && m.To != myRole && m.To != "" {
		return
	}

	switch m.Type {
	case bus.MsgEvent:
		switch p := m.Payload.(type) {
		case bus.TokenPayload:
			if !p.Done {
				fmt.Print(p.Text)
			}
		case string:
			if p != "" {
				fmt.Printf("\n[%s] %s\n", m.From, p)
			}
		default:
			fmt.Printf("\n[%s] event\n", m.From)
		}
	case bus.MsgHumanGate:
		fmt.Printf("\n[GATE] %v\n", m.Payload)
	case bus.MsgRequest:
		if m.To == myRole {
			fmt.Printf("\n[%s] starting…\n", myRole)
		}
	case bus.MsgResponse:
		if m.From == myRole {
			fmt.Printf("\n[%s] done\n", myRole)
		}
	}
}
