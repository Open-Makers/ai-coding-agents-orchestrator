package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

const (
	envLogLevel  = "ORCH_LOG_LEVEL"
	envLogFormat = "ORCH_LOG_FORMAT"

	// LogFileName is the default log file name inside the workspace.
	LogFileName = "orchestrator.log"
)

var (
	logMu   sync.Mutex
	logFile *os.File
)

// Setup initializes the global slog logger with a discard handler.
// Call SetupFile once the workspace directory is known to start writing to disk.
func Setup() {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// SetupFile opens (or creates) the given file path for append-mode logging
// and configures the global slog logger to write there.
// The caller should defer logging.Close() to flush the file.
func SetupFile(path string) error {
	logMu.Lock()
	defer logMu.Unlock()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}

	if logFile != nil {
		_ = logFile.Close()
	}
	logFile = f

	level := parseLevel(os.Getenv(envLogLevel))
	handler := newHandler(f, level)
	slog.SetDefault(slog.New(handler))
	return nil
}

// Close flushes and closes the log file. Safe to call multiple times.
func Close() {
	logMu.Lock()
	defer logMu.Unlock()

	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
	}
}

// ForComponent returns a logger with a pre-set "component" attribute.
func ForComponent(component string) *slog.Logger {
	return slog.Default().With(slog.String("component", component))
}

func parseLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func newHandler(w io.Writer, level slog.Level) slog.Handler {
	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: level == slog.LevelDebug,
	}
	format := strings.ToLower(strings.TrimSpace(os.Getenv(envLogFormat)))
	if format == "json" {
		return slog.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}
