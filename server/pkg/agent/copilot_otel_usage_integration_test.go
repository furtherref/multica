//go:build agentintegration

package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Explicit opt-in only: this test uses an authenticated CLI and consumes quota.
// Set MULTICA_COPILOT_SMOKE_EXECUTABLE to an absolute CLI/wrapper path and
// MULTICA_RUN_REAL_AGENT_SMOKE=1. It observes exported usage while the real
// process is alive, then cancels through the production backend and checks the
// result. The prompt only asks for sleep; it must not inspect workspace files.
func TestCopilotRealTelemetrySurvivesCancellation(t *testing.T) {
	if os.Getenv("MULTICA_RUN_REAL_AGENT_SMOKE") != "1" {
		t.Skip("set MULTICA_RUN_REAL_AGENT_SMOKE=1 to access a real Copilot account")
	}
	executable := os.Getenv("MULTICA_COPILOT_SMOKE_EXECUTABLE")
	if !filepath.IsAbs(executable) {
		t.Fatal("MULTICA_COPILOT_SMOKE_EXECUTABLE must be an absolute path")
	}
	if reason := copilotOTelSkipReason(os.Environ()); reason != "" {
		t.Fatalf("cannot observe a private exporter: %s", reason)
	}
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	t.Setenv("TMP", root)
	t.Setenv("TEMP", root)
	backend, err := New("copilot", Config{ExecutablePath: executable, Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session, err := backend.Execute(ctx, "Run the shell command sleep 25, then reply OK. Do not read or modify any files.", ExecOptions{Model: "gpt-5.6-luna", Cwd: root, Timeout: 60 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range session.Messages {
		}
	}()
	// Ensure an assertion failure still waits for process ownership cleanup.
	t.Cleanup(func() {
		cancel()
		for range session.Result {
		}
	})
	deadline := time.NewTimer(50 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(20 * time.Millisecond)
	defer poll.Stop()
	var before map[string]TokenUsage
observe:
	for {
		select {
		case <-deadline.C:
			t.Fatal("no completed request exported while CLI was running")
		case result := <-session.Result:
			t.Fatalf("CLI exited before observation: status=%s", result.Status)
		case <-poll.C:
			paths, err := filepath.Glob(filepath.Join(root, "multica-copilot-usage-*.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range paths {
				before, err = readCopilotOTelUsage(p)
				if err == nil && hasTokens(before) {
					break observe
				}
			}
		}
	}
	select {
	case <-session.Result:
		t.Fatal("CLI finished before cancellation")
	default:
	}
	t.Logf("observed live request usage for %d models before cancellation", len(before))
	cancel()
	select {
	case result := <-session.Result:
		if result.Status != "aborted" {
			t.Fatalf("status=%s, want aborted", result.Status)
		}
		for model, u := range before {
			got := result.Usage[model]
			if got.InputTokens < u.InputTokens || got.OutputTokens < u.OutputTokens || got.CacheReadTokens < u.CacheReadTokens || got.CacheWriteTokens < u.CacheWriteTokens {
				t.Fatalf("lost live usage for %s: before=%+v after=%+v", model, u, got)
			}
		}
	case <-time.After(15 * time.Second):
		t.Fatal("cancellation did not finish within its bound")
	}
}
