// Package logging configures structured JSON logging for the daemon.
package logging

import (
	"log"
	"log/slog"
	"os"
	"strings"
)

// stdlibWriter adapts the stdlib log package to slog so existing log.Printf
// calls are emitted as structured JSON entries when JSON logging is enabled.
type stdlibWriter struct{}

// Write forwards a stdlib log line to slog as an Info message.
func (stdlibWriter) Write(p []byte) (int, error) {
	slog.Info(strings.TrimSpace(string(p)))
	return len(p), nil
}

// Setup configures logging. When format is "json", the slog default handler
// writes structured JSON to os.Stderr and the stdlib log package is redirected
// through slog. Any other format leaves logging untouched.
func Setup(format string) {
	if format != "json" {
		return
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	log.SetFlags(0)
	log.SetOutput(stdlibWriter{})
}
