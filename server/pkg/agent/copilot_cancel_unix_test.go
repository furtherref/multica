//go:build unix

package agent

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

func TestCopilotCancellationReapsTermIgnoringDescendant(t *testing.T) {
	home := t.TempDir()
	pidFile := filepath.Join(home, "pids")
	fake := filepath.Join(home, "copilot")
	// The leader exits on TERM, but the detached-stdio child ignores it.
	// Reuse the existing process-tree fixture; its stdout is immaterial here.
	writeTestExecutable(t, fake, []byte(claudeMixedSignalFakeScript()))
	backend, err := New("copilot", Config{ExecutablePath: fake, Logger: slog.Default(), Env: map[string]string{"HOME": home, "CLAUDE_PID_FILE": pidFile}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session, err := backend.Execute(ctx, "unused", ExecOptions{Cwd: home})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range session.Messages {
		}
	}()
	pids := waitForPids(t, pidFile)
	cancel()
	select {
	case result := <-session.Result:
		if result.Status != "aborted" {
			t.Fatalf("status=%s", result.Status)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancellation hung")
	}
	for _, pid := range pids {
		waitProcessGone(t, pid)
	}
}
