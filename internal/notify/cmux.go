// Package notify sends best-effort desktop/terminal notifications to external
// tools. Currently it integrates with cmux (manaflow-ai/cmux), a macOS terminal
// app that surfaces notifications for AI coding agents, mirroring how Claude
// Code's hooks forward alerts into cmux.
package notify

import (
	"context"
	"net"
	"runtime"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/executil"
)

// cmuxSocket is the Unix socket cmux listens on. Its presence is used to detect
// whether cmux is running before attempting to notify.
const cmuxSocket = "/tmp/cmux.sock"

// CMuxAvailable reports whether a cmux instance is reachable. cmux is macOS-only,
// so this is always false on other platforms.
func CMuxAvailable() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	conn, err := net.DialTimeout("unix", cmuxSocket, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// SendCMux fires a cmux notification via the `cmux notify` CLI. It is
// best-effort and silently no-ops when cmux is not running or the CLI is
// missing. subtitle may be empty.
func SendCMux(title, subtitle, body string) {
	if !CMuxAvailable() {
		return
	}
	args := []string{"notify", "--title", title, "--body", body}
	if subtitle != "" {
		args = append(args, "--subtitle", subtitle)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = executil.CommandContext(ctx, "cmux", args...).Run()
}
