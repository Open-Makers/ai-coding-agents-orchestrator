package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

func TestParseTaskSpec_Valid(t *testing.T) {
	output := `Let me formalize this as a task.

===TASKSPEC===
TITLE: Add user authentication
SCOPE: feature
DESCRIPTION:
Implement JWT-based authentication for the REST API.
Add login and register endpoints.
ACCEPTANCE_CRITERIA:
- Users can register with email and password
- Users can login and receive a JWT token
- Protected endpoints require a valid token
CONSTRAINTS:
- Use bcrypt for password hashing
FILES_TO_MODIFY:
- internal/auth/handler.go
- internal/auth/middleware.go
===END===`

	spec, ok := parseTaskSpec(output)
	if !ok {
		t.Fatal("expected to parse task spec")
	}

	if spec.Title != "Add user authentication" {
		t.Errorf("title = %q, want %q", spec.Title, "Add user authentication")
	}
	if spec.Scope != "feature" {
		t.Errorf("scope = %q, want %q", spec.Scope, "feature")
	}
	if len(spec.AcceptanceCriteria) != 3 {
		t.Errorf("acceptance criteria = %d, want 3", len(spec.AcceptanceCriteria))
	}
	if len(spec.Constraints) != 1 {
		t.Errorf("constraints = %d, want 1", len(spec.Constraints))
	}
	if len(spec.FilesToModify) != 2 {
		t.Errorf("files_to_modify = %d, want 2", len(spec.FilesToModify))
	}
}

func TestParseTaskSpec_NoSpec(t *testing.T) {
	output := "Let me ask some clarifying questions:\n1. What framework are you using?\n2. Do you need OAuth?"

	_, ok := parseTaskSpec(output)
	if ok {
		t.Fatal("expected no task spec to be parsed")
	}
}

func TestParseExecutionPlan_Valid(t *testing.T) {
	output := `===EXECUTION_PLAN===
NEEDS_ARCHITECTURE: false
NEEDS_DETAILED_PLAN: false
CODER_INSTRUCTIONS:
Implement JWT authentication in the existing auth package.
Modify internal/auth/handler.go to add Login and Register handlers.
Create internal/auth/middleware.go for JWT validation middleware.
===END===`

	plan := parseExecutionPlan(output)

	if plan.NeedsArchitecture {
		t.Error("expected NeedsArchitecture=false")
	}
	if plan.NeedsDetailedPlan {
		t.Error("expected NeedsDetailedPlan=false")
	}
	if plan.CoderInstructions == "" {
		t.Error("expected non-empty coder instructions")
	}
}

func TestParseExecutionPlan_Greenfield(t *testing.T) {
	output := `===EXECUTION_PLAN===
NEEDS_ARCHITECTURE: true
NEEDS_DETAILED_PLAN: true
CODER_INSTRUCTIONS:
Build a task management REST API in Go with PostgreSQL.
===END===`

	plan := parseExecutionPlan(output)

	if !plan.NeedsArchitecture {
		t.Error("expected NeedsArchitecture=true")
	}
	if !plan.NeedsDetailedPlan {
		t.Error("expected NeedsDetailedPlan=true")
	}
}

func TestNegotiateTask_ForcesTaskSpecAfterAffirmativeLoop(t *testing.T) {
	b := bus.New()
	defer b.Close()

	pm := NewPMAgent(
		b,
		&runner.MockRunner{Responses: []string{
			"Możesz podać bardziej szczegółowe informacje o tym, co dokładnie chcesz zmienić w projekcie?",
			"Czy możesz podać bardziej szczegółowe informacje na temat zmiany?",
			`===TASKSPEC===
TITLE: Aktualizacja planszy tic-tac-toe w miejscu
SCOPE: bugfix
DESCRIPTION:
Zmień rendering gry tic-tac-toe w konsoli tak, aby plansza była odświeżana w tym samym miejscu zamiast dopisywania kolejnych plansz pod poprzednimi.
Wykorzystaj istniejącą logikę CLI i renderowania gry.
ACCEPTANCE_CRITERIA:
- Po wykonaniu ruchu widoczna jest jedna aktualna plansza
- Nowy ruch nie powoduje dopisania kolejnej planszy pod poprzednią
- Rozgrywka pozostaje poprawna logicznie
CONSTRAINTS:
- Oprzyj zmianę na obecnej implementacji gry konsolowej
FILES_TO_MODIFY:
- internal/cli/cli.go
- internal/game/game.go
===END===`,
		}},
		artifacts.Workspace{},
		nil,
		"",
	)

	humanCh := make(chan string, 2)
	humanCh <- "ma się aktualizować w tym samym miejscu zamiast nowa plansza z nowym ruchem pod starą"
	humanCh <- "tak"

	spec, err := pm.NegotiateTask(context.Background(), "gra powinna się aktualizować w miejscu a nie w pionie", "", humanCh)
	if err != nil {
		t.Fatalf("NegotiateTask returned error: %v", err)
	}

	if spec.Scope != "bugfix" {
		t.Fatalf("expected bugfix scope, got %q", spec.Scope)
	}
	if spec.Title == "" {
		t.Fatal("expected non-empty title")
	}
	if len(spec.FilesToModify) == 0 {
		t.Fatal("expected files to modify inferred from forced TaskSpec")
	}
}

func TestPlanTask_PassesBrownfieldFlag(t *testing.T) {
	mock := &runner.MockRunner{Responses: []string{
		`===EXECUTION_PLAN===
NEEDS_ARCHITECTURE: false
NEEDS_DETAILED_PLAN: false
CODER_INSTRUCTIONS:
Modify internal/cli/cli.go to refresh the board in place.
===END===`,
	}}

	pm := NewPMAgent(bus.New(), mock, artifacts.Workspace{}, nil, "")

	spec := TaskSpec{
		Title:       "Refresh board in place",
		Scope:       "bugfix",
		Description: "Re-render the tic-tac-toe board in place.",
	}

	if _, err := pm.PlanTask(context.Background(), spec, "## Repository Context\n", true); err != nil {
		t.Fatalf("PlanTask error: %v", err)
	}

	if len(mock.Requests) != 1 {
		t.Fatalf("expected 1 mock request, got %d", len(mock.Requests))
	}
	userContent := mock.Requests[0].Messages[0].Content
	if !strings.Contains(userContent, "Brownfield: true") {
		t.Errorf("expected user content to include 'Brownfield: true', got:\n%s", userContent)
	}
}
