package orchestrator

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
)

type CommandResult struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

type TestReport struct {
	Success  bool            `json:"success"`
	Commands []CommandResult `json:"commands"`
}

// CommandTester executes commands listed in test_cmds.txt.
type CommandTester struct{}

func (t CommandTester) Run(ctx *Context) (TestResult, error) {
	data, err := ctx.Workspace.ReadFile(artifacts.TestCmdsFile)
	if err != nil {
		return TestResult{}, err
	}

	cmds := parseCommands(string(data))
	if len(cmds) == 0 {
		report := TestReport{Success: true, Commands: nil}
		if err := ctx.Workspace.WriteJSON(artifacts.TestReportFile, report); err != nil {
			return TestResult{}, err
		}
		return TestResult{Success: true}, nil
	}

	report := TestReport{Success: true, Commands: make([]CommandResult, 0, len(cmds))}
	for _, c := range cmds {
		res := runCommand(c)
		report.Commands = append(report.Commands, res)
		if res.ExitCode != 0 {
			report.Success = false
			break
		}
	}

	if err := ctx.Workspace.WriteJSON(artifacts.TestReportFile, report); err != nil {
		return TestResult{}, err
	}

	last := ""
	if len(report.Commands) > 0 {
		last = report.Commands[len(report.Commands)-1].Command
	}
	return TestResult{Success: report.Success, Command: last, Output: flattenOutput(report.Commands)}, nil
}

func parseCommands(input string) []string {
	var cmds []string
	scanner := bufio.NewScanner(strings.NewReader(input))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cmds = append(cmds, line)
	}
	return cmds
}

func runCommand(command string) CommandResult {
	cmd := exec.Command("sh", "-c", command)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
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

func flattenOutput(results []CommandResult) string {
	var b strings.Builder
	for i, res := range results {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "$ %s\n%s\n%s", res.Command, res.Stdout, res.Stderr)
	}
	return strings.TrimSpace(b.String())
}
