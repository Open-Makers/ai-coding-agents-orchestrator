package bus

import (
	"fmt"
	"time"
)

type AgentRole string

const (
	RolePM         AgentRole = "pm"
	RolePlanner    AgentRole = "planner"
	RoleCoder      AgentRole = "coder"
	RoleTester     AgentRole = "tester"
	RoleReviewer   AgentRole = "reviewer"
	RoleUXReviewer AgentRole = "ux_reviewer"
	RoleSecurity   AgentRole = "security"
	RoleQA         AgentRole = "qa"
	RoleSystem     AgentRole = "system"
)

type MessageType string

const (
	MsgRequest      MessageType = "request"
	MsgResponse     MessageType = "response"
	MsgEvent        MessageType = "event"
	MsgHumanGate    MessageType = "human_gate"
	MsgUsage        MessageType = "usage"
	MsgConversation MessageType = "conversation" // PM↔human dialog turn
	MsgTaskSpec     MessageType = "task_spec"    // PM emits formalized task
)

type Message struct {
	ID        string      `json:"id"`
	From      AgentRole   `json:"from"`
	To        AgentRole   `json:"to"`
	Type      MessageType `json:"type"`
	Payload   any         `json:"payload"`
	Timestamp time.Time   `json:"timestamp"`
}

func NewMessage(from, to AgentRole, typ MessageType, payload any) Message {
	return Message{
		ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
		From:      from,
		To:        to,
		Type:      typ,
		Payload:   payload,
		Timestamp: time.Now(),
	}
}

// TokenPayload is the payload for streaming token events.
type TokenPayload struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// AgentUsage is the payload for MsgUsage events.
// It carries token counts for one agent's LLM completion(s).
// Runner and Model are not stored here — the TUI derives them from agentConfigs.
type AgentUsage struct {
	InputTokens  int  `json:"input_tokens"`
	OutputTokens int  `json:"output_tokens"`
	Estimated    bool `json:"estimated"` // true = heuristic count
}

// ConversationPayload carries a single turn of PM↔human dialog.
type ConversationPayload struct {
	From    string `json:"from"` // "pm" or "human"
	Content string `json:"content"`
}
