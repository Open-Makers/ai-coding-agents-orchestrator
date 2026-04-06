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
