//go:build unix

package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// Full local chain: real Copilot adapter + controlled subprocess -> cancellation
// poll -> executeAndDrain -> HTTP usage write -> cancel acknowledgement. No
// installed CLI or authenticated account is used by this default test.
func TestCancelledCopilotProcessAccountingReachesServer(t *testing.T) {
	home := t.TempDir()
	ready := filepath.Join(home, "ready")
	executable := filepath.Join(home, "copilot")
	script := `#!/bin/sh
flush() {
printf '%s\n' '{"type":"span","traceId":"trace","spanId":"span","attributes":{"gen_ai.operation.name":"chat","gen_ai.response.model":"gpt-5.5","gen_ai.usage.input_tokens":1000,"gen_ai.usage.output_tokens":20,"gen_ai.usage.cache_read.input_tokens":800,"github.copilot.nano_aiu":10000}}' >> "$COPILOT_OTEL_FILE_EXPORTER_PATH"
exit 0
}
trap flush TERM
printf ready > "$HOME/ready"
while :; do sleep 1; done
`
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var writes, acks, completes atomic.Int32
	var usage []TaskUsageEntry
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/status"):
			w.Header().Set("Content-Type", "application/json")
			if _, err := os.Stat(ready); err == nil {
				_, _ = w.Write([]byte(`{"status":"cancelled"}`))
			} else {
				_, _ = w.Write([]byte(`{"status":"running"}`))
			}
		case strings.HasSuffix(r.URL.Path, "/usage"):
			var body struct {
				Usage []TaskUsageEntry `json:"usage"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			usage = body.Usage
			writes.Add(1)
		case strings.HasSuffix(r.URL.Path, "/cancel-ack"):
			if writes.Load() != 1 {
				t.Error("cancel acknowledged before accounting was written")
			}
			acks.Add(1)
		case strings.HasSuffix(r.URL.Path, "/complete"):
			completes.Add(1)
		}
	}))
	defer srv.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	backend, err := agent.New("copilot", agent.Config{ExecutablePath: executable, Logger: logger, Env: map[string]string{"HOME": home}})
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{client: NewClient(srv.URL), logger: logger, workspaces: make(map[string]*workspaceState), runtimeIndex: map[string]Runtime{"rt": {ID: "rt", Provider: "copilot"}}, cancelPollInterval: 10 * time.Millisecond}
	d.runner = taskRunnerFunc(func(ctx context.Context, task Task, provider string, _ int, logger *slog.Logger) (TaskResult, error) {
		result, _, err := d.executeAndDrain(ctx, backend, "unused", agent.ExecOptions{Cwd: home, Model: "gpt-5.5"}, logger, task.ID, "", new(atomic.Int32))
		return TaskResult{Status: result.Status, Usage: taskUsageEntries(provider, result.Usage)}, err
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d.handleTask(ctx, Task{ID: "task", RuntimeID: "rt", IssueID: "issue", Agent: &AgentData{Name: "test"}}, 0)
	if writes.Load() != 1 || acks.Load() != 1 || completes.Load() != 0 {
		t.Fatalf("writes=%d acks=%d completes=%d", writes.Load(), acks.Load(), completes.Load())
	}
	if len(usage) != 1 || usage[0].InputTokens != 200 || usage[0].OutputTokens != 20 || usage[0].CacheReadTokens != 800 || usage[0].CostUSDTicks != 1000 {
		t.Fatalf("subprocess accounting not preserved: %+v", usage)
	}
}
