package main

import (
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
)

func TestVisibleInAgentPane(t *testing.T) {
	tests := []struct {
		name string
		role bus.AgentRole
		msg  bus.Message
		want bool
	}{
		{
			name: "shows tokens from same role",
			role: bus.RolePlanner,
			msg:  bus.NewMessage(bus.RolePlanner, "", bus.MsgEvent, bus.TokenPayload{Text: "x"}),
			want: true,
		},
		{
			name: "shows targeted system message",
			role: bus.RoleTester,
			msg:  bus.NewMessage(bus.RoleSystem, bus.RoleTester, bus.MsgEvent, "starting tester"),
			want: true,
		},
		{
			name: "hides broadcast system message",
			role: bus.RoleCoder,
			msg:  bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, "planner"),
			want: false,
		},
		{
			name: "hides other agent output",
			role: bus.RoleReviewer,
			msg:  bus.NewMessage(bus.RoleCoder, "", bus.MsgEvent, bus.TokenPayload{Text: "patch"}),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := visibleInAgentPane(tt.role, tt.msg); got != tt.want {
				t.Fatalf("visibleInAgentPane(%q, %#v) = %t, want %t", tt.role, tt.msg, got, tt.want)
			}
		})
	}
}
