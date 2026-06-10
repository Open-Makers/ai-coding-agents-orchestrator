package agent

import "testing"

func TestParseTaskSpec_Pipeline(t *testing.T) {
	output := `===TASKSPEC===
TITLE: Fix the off-by-one in pager
SCOPE: bugfix
PIPELINE: fix
DESCRIPTION:
Correct the boundary check in the pager.
ACCEPTANCE_CRITERIA:
- last page renders fully
===END===`

	spec, ok := parseTaskSpec(output)
	if !ok {
		t.Fatal("expected to parse task spec")
	}
	if spec.Pipeline != "fix" {
		t.Errorf("pipeline = %q, want %q", spec.Pipeline, "fix")
	}
	if spec.Scope != "bugfix" {
		t.Errorf("scope = %q, want %q", spec.Scope, "bugfix")
	}
}

func TestParseTaskSpec_NoPipeline(t *testing.T) {
	output := `===TASKSPEC===
TITLE: Build a thing
SCOPE: greenfield
DESCRIPTION:
Make it.
===END===`

	spec, ok := parseTaskSpec(output)
	if !ok {
		t.Fatal("expected to parse task spec")
	}
	if spec.Pipeline != "" {
		t.Errorf("pipeline = %q, want empty", spec.Pipeline)
	}
}

func TestParseRnDAction(t *testing.T) {
	tests := []struct {
		name      string
		output    string
		wantMsg   string
		wantCoder string
		wantEnd   bool
	}{
		{
			name:    "plain message",
			output:  "What database should we prototype against?",
			wantMsg: "What database should we prototype against?",
		},
		{
			name: "coder directive",
			output: `Let's test the parser idea.
===CODER===
Write a tiny main.go that parses "1+2" and prints 3.
===END===`,
			wantMsg:   "Let's test the parser idea.",
			wantCoder: `Write a tiny main.go that parses "1+2" and prints 3.`,
		},
		{
			name: "propose end",
			output: `The concept works as shown.
===PROPOSE_END===`,
			wantMsg: "The concept works as shown.",
			wantEnd: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			act := parseRnDAction(tc.output)
			if act.Message != tc.wantMsg {
				t.Errorf("message = %q, want %q", act.Message, tc.wantMsg)
			}
			if act.CoderTask != tc.wantCoder {
				t.Errorf("coder = %q, want %q", act.CoderTask, tc.wantCoder)
			}
			if act.ProposeEnd != tc.wantEnd {
				t.Errorf("proposeEnd = %v, want %v", act.ProposeEnd, tc.wantEnd)
			}
		})
	}
}
