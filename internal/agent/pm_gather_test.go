package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

func TestParseSummarySection_Valid(t *testing.T) {
	output := `Let me summarize what we discussed.

===SUMMARY===
The user wants to build a REST API for task management with Go.
Key decisions: use PostgreSQL, JWT auth, Docker deployment.
Scope: greenfield project.
===END===`

	summary := parseSummarySection(output)
	if summary == "" {
		t.Fatal("expected to parse summary section")
	}
	if !strings.Contains(summary, "REST API") {
		t.Errorf("expected summary to contain 'REST API', got %q", summary)
	}
	if !strings.Contains(summary, "PostgreSQL") {
		t.Errorf("expected summary to contain 'PostgreSQL', got %q", summary)
	}
}

func TestParseSummarySection_Missing(t *testing.T) {
	output := "Here are some clarifying questions:\n1. What database?\n2. What auth method?"

	summary := parseSummarySection(output)
	if summary != "" {
		t.Errorf("expected empty summary, got %q", summary)
	}
}

func TestParseRequirementsSection_Valid(t *testing.T) {
	output := `Here are the detailed requirements.

===REQUIREMENTS===
# Requirements

## Overview
Build a task management REST API.

## Must Have
- CRUD operations for tasks
- JWT authentication

## Should Have
- Task filtering and search

## Constraints
- Use PostgreSQL
===END===`

	reqs := parseRequirementsSection(output)
	if reqs == "" {
		t.Fatal("expected to parse requirements section")
	}
	if !strings.Contains(reqs, "Must Have") {
		t.Errorf("expected requirements to contain 'Must Have', got %q", reqs)
	}
	if !strings.Contains(reqs, "CRUD operations") {
		t.Errorf("expected requirements to contain 'CRUD operations', got %q", reqs)
	}
}

func TestParseRequirementsSection_Missing(t *testing.T) {
	output := "Still gathering information..."

	reqs := parseRequirementsSection(output)
	if reqs != "" {
		t.Errorf("expected empty requirements, got %q", reqs)
	}
}

func TestPMAgent_GatherRequirements_UsesPromptLanguageForInitialMessage(t *testing.T) {
	b := bus.New()
	defer b.Close()

	sub := b.Subscribe()
	pm := NewPMAgent(
		b,
		&runner.MockRunner{Responses: []string{
			"Co chcesz zbudować albo zmienić?",
			"===SUMMARY===\nPodsumowanie\n===END===",
			"===REQUIREMENTS===\nWymagania\n===END===",
		}},
		artifacts.Workspace{},
		nil,
		"",
	)

	humanCh := make(chan string, 3)
	humanCh <- "Chcę dodać logowanie"
	humanCh <- "Logowanie ma być mailem i hasłem"
	humanCh <- "Tak, zgadza się"

	done := make(chan error, 1)
	go func() {
		_, _, err := pm.GatherRequirements(context.Background(), "", humanCh)
		done <- err
	}()

	var msg bus.Message
	for {
		msg = <-sub
		if msg.Type == bus.MsgConversation {
			break
		}
	}
	payload, ok := msg.Payload.(bus.ConversationPayload)
	if !ok {
		t.Fatalf("expected ConversationPayload, got %T", msg.Payload)
	}
	if !strings.Contains(payload.Content, "Co chcesz zbudować albo zmienić?") {
		t.Fatalf("expected initial PM message from model, got %q", payload.Content)
	}

	if err := <-done; err != nil {
		t.Fatalf("GatherRequirements returned error: %v", err)
	}
}

func TestPMAgent_GatherRequirements_RetriesAfterPromptLeak(t *testing.T) {
	b := bus.New()
	defer b.Close()

	sub := b.Subscribe()
	pm := NewPMAgent(
		b,
		&runner.MockRunner{Responses: []string{
			"1. **Discuss** — What would you like to build or change?",
			"Jak dokładnie ma zachowywać się aktualizacja w miejscu?",
			"===SUMMARY===\nGra powinna aktualizować się w miejscu.\n===END===",
			"===REQUIREMENTS===\nWymagania\n===END===",
		}},
		artifacts.Workspace{},
		nil,
		"",
	)

	humanCh := make(chan string, 3)
	humanCh <- "gra powinna się aktualizować w miejscu a nie w pionie"
	humanCh <- "Plansza ma odświeżać ten sam obszar"
	humanCh <- "Tak"

	done := make(chan error, 1)
	go func() {
		_, _, err := pm.GatherRequirements(context.Background(), "", humanCh)
		done <- err
	}()

	var got string
	for {
		msg := <-sub
		if msg.Type != bus.MsgConversation {
			continue
		}
		payload, ok := msg.Payload.(bus.ConversationPayload)
		if !ok {
			continue
		}
		got = payload.Content
		break
	}

	if strings.Contains(strings.ToLower(got), "discuss") {
		t.Fatalf("expected leaked prompt response to be retried, got %q", got)
	}
	if !strings.Contains(got, "Jak dokładnie ma zachowywać się aktualizacja w miejscu?") {
		t.Fatalf("expected retried PM question, got %q", got)
	}

	if err := <-done; err != nil {
		t.Fatalf("GatherRequirements returned error: %v", err)
	}
}
