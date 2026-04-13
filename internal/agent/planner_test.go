package agent

import (
	"testing"
)

func TestParseStages_MultipleStages(t *testing.T) {
	input := `===STAGE 1: Must Have — Game logic===
Implement the core game state and rules.
Create internal/game/state.go with board representation.

===STAGE 2: Must Have — Terminal UI===
Implement terminal rendering and input handling.
Create internal/ui/terminal.go.

===STAGE 3: Should Have — AI opponent===
Add computer player with minimax.
`

	stages := ParseStages(input)
	if len(stages) != 3 {
		t.Fatalf("expected 3 stages, got %d", len(stages))
	}

	if stages[0].Index != 1 || stages[0].Name != "Must Have — Game logic" {
		t.Errorf("stage 1: got index=%d name=%q", stages[0].Index, stages[0].Name)
	}
	if stages[1].Index != 2 || stages[1].Name != "Must Have — Terminal UI" {
		t.Errorf("stage 2: got index=%d name=%q", stages[1].Index, stages[1].Name)
	}
	if stages[2].Index != 3 || stages[2].Name != "Should Have — AI opponent" {
		t.Errorf("stage 3: got index=%d name=%q", stages[2].Index, stages[2].Name)
	}

	if stages[0].Prompt == "" {
		t.Error("stage 1 prompt should not be empty")
	}
}

func TestParseStages_NoDelimiters_FallsBackToSingleStage(t *testing.T) {
	input := `Implement the full application.
Create all files as described in the architecture.`

	stages := ParseStages(input)
	if len(stages) != 1 {
		t.Fatalf("expected 1 fallback stage, got %d", len(stages))
	}
	if stages[0].Index != 1 {
		t.Errorf("expected index=1, got %d", stages[0].Index)
	}
	if stages[0].Name != "Full Implementation" {
		t.Errorf("expected name='Full Implementation', got %q", stages[0].Name)
	}
	if stages[0].Prompt == "" {
		t.Error("fallback stage prompt should contain the original content")
	}
}

func TestParseStages_HashDelimiters(t *testing.T) {
	input := `### STAGE 1: Core setup ###
Set up the project structure.

### STAGE 2: Feature implementation ###
Build the main features.
`

	stages := ParseStages(input)
	if len(stages) != 2 {
		t.Fatalf("expected 2 stages, got %d", len(stages))
	}
	if stages[0].Name != "Core setup" {
		t.Errorf("stage 1 name: got %q", stages[0].Name)
	}
}

func TestParseStages_DashSeparator(t *testing.T) {
	input := `===STAGE 1 - Basic structure===
Create the base files.
`

	stages := ParseStages(input)
	if len(stages) != 1 {
		t.Fatalf("expected 1 stage, got %d", len(stages))
	}
	if stages[0].Name != "Basic structure" {
		t.Errorf("expected name='Basic structure', got %q", stages[0].Name)
	}
}

func TestParseStageHeader_ValidFormats(t *testing.T) {
	tests := []struct {
		line          string
		expectedIndex int
		expectedName  string
	}{
		{"===STAGE 1: Must Have — Game logic===", 1, "Must Have — Game logic"},
		{"### STAGE 2: Feature ###", 2, "Feature"},
		{"STAGE 3 - AI opponent", 3, "AI opponent"},
		{"===STAGE 10: Large project===", 10, "Large project"},
		{"### Stage 1: Set Up Project Structure", 1, "Set Up Project Structure"},
		{"### Step 1: Core setup", 1, "Core setup"},
		{"## Step 2 — Feature implementation", 2, "Feature implementation"},
		{"### 1. Set Up Project Structure and Main Entry Point", 1, "Set Up Project Structure and Main Entry Point"},
		{"### 3. Implement Error Handling", 3, "Implement Error Handling"},
		{"not a stage header", 0, ""},
		{"", 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			idx, name := parseStageHeader(tt.line)
			if idx != tt.expectedIndex {
				t.Errorf("index: expected %d, got %d", tt.expectedIndex, idx)
			}
			if name != tt.expectedName {
				t.Errorf("name: expected %q, got %q", tt.expectedName, name)
			}
		})
	}
}

func TestParseStages_MarkdownHeadings(t *testing.T) {
	input := `### Stage 1: Set Up Project Structure and Main Entry Point

- Create cmd/tic-tac-toe/ directory.
- Write the main function in main.go.

### Stage 2: Define Core Game Logic

- Implement game state management (board.go).
- Define player structures.

### Stage 3: Implement Error Handling and Testing

- Wrap errors with context.
- Write initial tests.
`
	stages := ParseStages(input)
	if len(stages) != 3 {
		t.Fatalf("expected 3 stages, got %d", len(stages))
	}
	if stages[0].Index != 1 || stages[0].Name != "Set Up Project Structure and Main Entry Point" {
		t.Errorf("stage 1: got index=%d name=%q", stages[0].Index, stages[0].Name)
	}
	if stages[1].Index != 2 || stages[1].Name != "Define Core Game Logic" {
		t.Errorf("stage 2: got index=%d name=%q", stages[1].Index, stages[1].Name)
	}
	if stages[2].Index != 3 {
		t.Errorf("stage 3: got index=%d", stages[2].Index)
	}
}

func TestParseStages_NumberedHeadings(t *testing.T) {
	input := `### 1. Set Up Project Structure

Create the directory layout.

### 2. Core Game Logic

Implement the board.

### 3. Testing

Write tests.
`
	stages := ParseStages(input)
	if len(stages) != 3 {
		t.Fatalf("expected 3 stages, got %d", len(stages))
	}
	if stages[0].Name != "Set Up Project Structure" {
		t.Errorf("stage 1 name: got %q", stages[0].Name)
	}
}

func TestParseSections_ExactHeaders(t *testing.T) {
	input := `===ARCHITECTURE===
Directory layout goes here.

===PLAN===
Step-by-step plan.

===PROMPTS===
===STAGE 1: Core===
Implement core logic.
`
	sections := parseSections(input, "ARCHITECTURE", "PLAN", "PROMPTS")
	for _, key := range []string{"ARCHITECTURE", "PLAN", "PROMPTS"} {
		if sections[key] == "" {
			t.Errorf("section %q should not be empty", key)
		}
	}
}

func TestParseSections_MarkdownHeaders(t *testing.T) {
	input := `### ARCHITECTURE ###
Directory layout.

### PLAN ###
Implementation steps.

### PROMPTS ###
===STAGE 1: Setup===
Set up files.
`
	sections := parseSections(input, "ARCHITECTURE", "PLAN", "PROMPTS")
	for _, key := range []string{"ARCHITECTURE", "PLAN", "PROMPTS"} {
		if sections[key] == "" {
			t.Errorf("section %q should not be empty with ### delimiters", key)
		}
	}
}

func TestParseSections_TrailingTextOnHeader(t *testing.T) {
	input := `===ARCHITECTURE===
Dirs and files.

===PLAN===
The plan.

===PROMPTS (Stage-by-Stage)===
===STAGE 1: Core===
Build core.
`
	sections := parseSections(input, "ARCHITECTURE", "PLAN", "PROMPTS")
	if sections["PROMPTS"] == "" {
		t.Error("PROMPTS section should be found even with trailing '(Stage-by-Stage)' text")
	}
}

func TestParseSections_DoubleHashMarkdown(t *testing.T) {
	input := `## ARCHITECTURE

Overview.

## PLAN

Steps.

## PROMPTS

===STAGE 1: Init===
Initialize.
`
	sections := parseSections(input, "ARCHITECTURE", "PLAN", "PROMPTS")
	for _, key := range []string{"ARCHITECTURE", "PLAN", "PROMPTS"} {
		if sections[key] == "" {
			t.Errorf("section %q should not be empty with ## headers", key)
		}
	}
}

func TestParseSections_BoldMarkdown(t *testing.T) {
	input := `**ARCHITECTURE**
Layout.

**PLAN**
Steps.

**PROMPTS**
===STAGE 1: Setup===
Setup.
`
	sections := parseSections(input, "ARCHITECTURE", "PLAN", "PROMPTS")
	for _, key := range []string{"ARCHITECTURE", "PLAN", "PROMPTS"} {
		if sections[key] == "" {
			t.Errorf("section %q should not be empty with ** bold headers", key)
		}
	}
}

func TestExtractSectionName_NoFalsePositiveOnProse(t *testing.T) {
	keySet := map[string]bool{"ARCHITECTURE": true, "PLAN": true, "PROMPTS": true}

	// Prose lines mentioning a key should NOT be detected as section headers.
	proseLines := []string{
		"The architecture should follow clean patterns.",
		"Update the plan to include testing.",
		"Generate prompts for each stage.",
	}
	for _, line := range proseLines {
		if name := extractSectionName(line, keySet); name != "" {
			t.Errorf("prose line %q should not match section key, but got %q", line, name)
		}
	}
}

func TestExtractSectionName_MoscowWithTrailingText(t *testing.T) {
	keySet := map[string]bool{"VISION": true, "MOSCOW": true}

	cases := []struct {
		line string
		want string
	}{
		{"### MoSCoW Prioritization", "MOSCOW"},
		{"## MOSCOW Priorities", "MOSCOW"},
		{"### Vision", "VISION"},
		{"===MOSCOW===", "MOSCOW"},
		{"### MOSCOW (Feature List)", "MOSCOW"},
		{"**MOSCOW**", "MOSCOW"},
	}
	for _, tc := range cases {
		got := extractSectionName(tc.line, keySet)
		if got != tc.want {
			t.Errorf("extractSectionName(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestParseSections_VisionAndMoscow(t *testing.T) {
	input := `### Vision

Problem statement here.

### MoSCoW Prioritization

## Must Have
1. Feature A
`
	sections := parseSections(input, "VISION", "MOSCOW")
	if sections["VISION"] == "" {
		t.Error("VISION section should not be empty")
	}
	if sections["MOSCOW"] == "" {
		t.Error("MOSCOW section should not be empty when using '### MoSCoW Prioritization' header")
	}
}
