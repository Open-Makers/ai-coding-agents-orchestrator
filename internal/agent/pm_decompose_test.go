package agent

import (
	"testing"
)

func TestParseSubTasks_ValidArray(t *testing.T) {
	output := `Some preamble the LLM emitted.

===TASKS===
[
  {"key":"T1","title":"Scaffold","description":"Create main.go","priority":1,"depends_on":[]},
  {"key":"T2","title":"Add /health","description":"Wire HTTP handler","priority":2,"depends_on":["T1"]}
]
===END===

Trailing chatter we should ignore.`

	tasks, err := parseSubTasks(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(tasks))
	}
	if tasks[0].Key != "T1" || tasks[0].Priority != 1 {
		t.Errorf("T1 mismatch: %+v", tasks[0])
	}
	if tasks[1].Key != "T2" || len(tasks[1].DependsOn) != 1 || tasks[1].DependsOn[0] != "T1" {
		t.Errorf("T2 mismatch: %+v", tasks[1])
	}
}

func TestParseSubTasks_DefaultsZeroPriority(t *testing.T) {
	output := `===TASKS===[{"key":"T1","title":"x","description":"y"}]===END===`
	tasks, err := parseSubTasks(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tasks[0].Priority != 2 {
		t.Errorf("priority = %d, want default 2", tasks[0].Priority)
	}
}

func TestParseSubTasks_ToleratesMarkdownFence(t *testing.T) {
	output := "===TASKS===\n```json\n[{\"key\":\"T1\",\"title\":\"x\",\"description\":\"y\",\"priority\":2}]\n```\n===END==="
	tasks, err := parseSubTasks(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Key != "T1" {
		t.Errorf("got %+v", tasks)
	}
}

func TestParseSubTasks_MissingStartMarker_FallsBackToJSONArray(t *testing.T) {
	// Some models drop the ===TASKS=== marker and emit the array directly
	// after the TASKSPEC block (this previously crashed the pipeline).
	output := `===TASKSPEC===
TITLE: Remove control prompt
SCOPE: bugfix
===END===
[
  {"key":"T1","title":"Remove prompt","description":"Strip text","priority":1,"depends_on":[]}
]`
	tasks, err := parseSubTasks(output)
	if err != nil {
		t.Fatalf("expected fallback parse to succeed, got %v", err)
	}
	if len(tasks) != 1 || tasks[0].Key != "T1" {
		t.Fatalf("unexpected tasks: %+v", tasks)
	}
}

func TestParseSubTasks_NoJSONAndNoMarker(t *testing.T) {
	output := "completely unrelated chatter without any array"
	if _, err := parseSubTasks(output); err == nil {
		t.Error("expected error when neither marker nor array is present")
	}
}

func TestParseSubTasks_InvalidJSON(t *testing.T) {
	output := `===TASKS===
{not a json array}
===END===`
	if _, err := parseSubTasks(output); err == nil {
		t.Error("expected error on malformed JSON")
	}
}
