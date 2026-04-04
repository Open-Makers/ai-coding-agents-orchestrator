package agent

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf8"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
)

const maxTestCmdLen = 512

type TesterPayload struct {
	PatchPath    string
	TestCmdsPath string
}

// TestReport is the structured result written to test_report.json.
type TestReport struct {
	Success  bool            `json:"success"`
	Commands []CommandResult `json:"commands"`
}

// CommandResult holds the outcome of a single shell command.
type CommandResult struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// TesterAgent runs test commands — no LLM involved.
type TesterAgent struct {
	BaseAgent
	ws   artifacts.Workspace
	root string
}

func NewTesterAgent(b *bus.Bus, ws artifacts.Workspace, root string) *TesterAgent {
	return &TesterAgent{
		BaseAgent: NewBase(bus.RoleTester, b),
		ws:        ws,
		root:      root,
	}
}

func (a *TesterAgent) Role() bus.AgentRole { return bus.RoleTester }

func (a *TesterAgent) Run(_ context.Context, _ bus.Message) (bus.Message, error) {
	data, err := a.ws.ReadFile(artifacts.TestCmdsFile)
	if err != nil {
		return bus.Message{}, fmt.Errorf("tester: read test_cmds: %w", err)
	}

	cmds := parseCmds(string(data))
	if len(cmds) == 0 {
		report := TestReport{Success: true}
		if err := a.ws.WriteJSON(artifacts.TestReportFile, report); err != nil {
			return bus.Message{}, err
		}
		return bus.NewMessage(bus.RoleTester, "", bus.MsgResponse, report), nil
	}

	report := TestReport{Success: true, Commands: make([]CommandResult, 0, len(cmds))}
	for _, c := range cmds {
		a.emitToken(fmt.Sprintf("$ %s\n", c), false)
		res := a.runCmd(c)
		report.Commands = append(report.Commands, res)
		a.emitToken(res.Stdout+"\n"+res.Stderr+"\n", false)
		if res.ExitCode != 0 {
			report.Success = false
			break
		}
	}
	a.emitToken("", true)

	if err := a.ws.WriteJSON(artifacts.TestReportFile, report); err != nil {
		return bus.Message{}, err
	}
	return bus.NewMessage(bus.RoleTester, "", bus.MsgResponse, report), nil
}

// sanitiseTestCmd rejects commands that are suspiciously long, contain
// non-printable bytes, or embed literal newlines (which would allow injecting
// a second command regardless of the shell's interpretation).
func sanitiseTestCmd(command string) error {
	if utf8.RuneCountInString(command) > maxTestCmdLen {
		return fmt.Errorf("command too long (%d runes, max %d)", utf8.RuneCountInString(command), maxTestCmdLen)
	}
	for i, r := range command {
		if r == '\n' || r == '\r' {
			return fmt.Errorf("command contains embedded newline at position %d", i)
		}
		if r < 0x20 && r != '\t' {
			return fmt.Errorf("command contains control character 0x%02x", r)
		}
	}
	return nil
}

func (a *TesterAgent) runCmd(command string) CommandResult {
	if err := sanitiseTestCmd(command); err != nil {
		return CommandResult{Command: command, ExitCode: 1, Stderr: "rejected: " + err.Error()}
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = a.root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	exitCode := 0
	if err != nil {
		exitCode = 1
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
	}
	return CommandResult{
		Command:  command,
		ExitCode: exitCode,
		Stdout:   strings.TrimSpace(stdout.String()),
		Stderr:   strings.TrimSpace(stderr.String()),
	}
}

func parseCmds(input string) []string {
	var cmds []string
	scanner := bufio.NewScanner(strings.NewReader(input))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			cmds = append(cmds, line)
		}
	}
	return cmds
}
