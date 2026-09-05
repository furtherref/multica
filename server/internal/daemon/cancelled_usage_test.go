package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

func TestCancelledAccountingWaitIsBounded(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "silent backend", true: "closed result channel"}[closed], func(t *testing.T) {
			d := newTestDaemon(t)
			d.cancelledResultWait = 20 * time.Millisecond
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			results := make(chan agent.Result)
			if closed {
				close(results)
			}
			start := time.Now()
			got := d.waitForCancelledAgentResult(results, logger)
			if time.Since(start) > time.Second {
				t.Fatal("accounting wait exceeded bound")
			}
			if len(got.Usage) > 0 || !strings.Contains(logs.String(), "accounting unavailable") {
				t.Fatalf("missing diagnostic: result=%+v logs=%s", got, logs.String())
			}
			if !closed && time.Since(start) < d.cancelledResultWait {
				t.Fatal("did not honor accounting grace")
			}
		})
	}
}

func TestCancelledAccountingDeadlineAndWatchdogRetainUsage(t *testing.T) {
	for _, watchdog := range []bool{false, true} {
		t.Run(map[bool]string{false: "parent deadline", true: "idle watchdog"}[watchdog], func(t *testing.T) {
			d := newTestDaemon(t)
			d.cancelledResultWait = time.Second
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			wantStatus := "timeout"
			if watchdog {
				cancel()
				ctx, cancel = context.WithCancel(context.Background())
				d.cfg.AgentIdleWatchdog = 10 * time.Millisecond
				wantStatus = "idle_watchdog"
			}
			defer cancel()
			want := map[string]agent.TokenUsage{"model": {CostUSDTicks: 1234}}
			got, _, err := d.executeAndDrain(ctx, cancelledUsageBackend{delay: 50 * time.Millisecond, result: agent.Result{Status: "aborted", Usage: want}}, "unused", agent.ExecOptions{}, slog.Default(), "task", "", new(atomic.Int32))
			if err != nil || got.Status != wantStatus || !reflect.DeepEqual(got.Usage, want) {
				t.Fatalf("status/usage lost: %+v, err=%v", got, err)
			}
		})
	}
}

func TestCancelledAccountingClosedResultDoesNotSucceed(t *testing.T) {
	d := newTestDaemon(t)
	msgs := make(chan agent.Message)
	close(msgs)
	results := make(chan agent.Result)
	close(results)
	got, _, err := d.executeAndDrain(context.Background(), sessionBackend{&agent.Session{Messages: msgs, Result: results}}, "unused", agent.ExecOptions{}, slog.Default(), "task", "", new(atomic.Int32))
	if err != nil || got.Status != "failed" {
		t.Fatalf("closed backend became successful: %+v, %v", got, err)
	}
}

// Close the transcript on cancellation, but deliver accounting later, exactly
// as a CLI that still needs to flush/read its session telemetry does.
type cancelledUsageBackend struct {
	result agent.Result
	delay  time.Duration
}

func (b cancelledUsageBackend) Execute(ctx context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
	msgs := make(chan agent.Message)
	results := make(chan agent.Result, 1)
	go func() {
		<-ctx.Done()
		close(msgs)
		time.Sleep(b.delay)
		results <- b.result
		close(results)
	}()
	return &agent.Session{Messages: msgs, Result: results}, nil
}

func TestCancelledExecutionRetainsDelayedUsage(t *testing.T) {
	d := newTestDaemon(t)
	d.cancelledResultWait = time.Second
	want := map[string]agent.TokenUsage{"gpt-5.5": {InputTokens: 100, OutputTokens: 20, CostUSDTicks: 1234}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := cancelledUsageBackend{delay: 50 * time.Millisecond, result: agent.Result{Status: "completed", SessionID: "session", Usage: want}}
	got, _, err := d.executeAndDrain(ctx, backend, "unused", agent.ExecOptions{}, slog.Default(), "task", "", new(atomic.Int32))
	if err != nil || got.Status != "cancelled" || got.SessionID != "session" || !reflect.DeepEqual(got.Usage, want) {
		t.Fatalf("cancelled result lost accounting or became successful: %+v, err=%v", got, err)
	}
}

func TestCancelledExecutionWinsWhenResultAlreadyReady(t *testing.T) {
	d := newTestDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 50; i++ {
		msgs := make(chan agent.Message)
		close(msgs)
		results := make(chan agent.Result, 1)
		results <- agent.Result{Status: "completed", Usage: map[string]agent.TokenUsage{"model": {OutputTokens: 10}}}
		close(results)
		got, _, err := d.executeAndDrain(ctx, sessionBackend{&agent.Session{Messages: msgs, Result: results}}, "unused", agent.ExecOptions{}, slog.Default(), "task", "", new(atomic.Int32))
		if err != nil || got.Status != "cancelled" || got.Usage["model"].OutputTokens != 10 {
			t.Fatalf("ready result/cancel race: %+v, err=%v", got, err)
		}
	}
}

func TestResultStatusSurvivesCancellationDuringTranscriptFlush(t *testing.T) {
	for _, status := range []string{"completed", "failed"} {
		t.Run(status, func(t *testing.T) {
			flushStarted := make(chan struct{})
			releaseFlush := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseFlush) }) }
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/messages") {
					close(flushStarted)
					<-releaseFlush
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			defer release()
			d := newTestDaemon(t)
			d.client = NewClient(srv.URL)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			msgs := make(chan agent.Message)
			results := make(chan agent.Result)
			want := agent.Result{Status: status, Output: "finished output", Error: "original diagnostic", SessionID: "session", Usage: map[string]agent.TokenUsage{"model": {OutputTokens: 10}}}
			done := make(chan agent.Result, 1)
			go func() {
				got, _, err := d.executeAndDrain(ctx, sessionBackend{&agent.Session{Messages: msgs, Result: results}}, "unused", agent.ExecOptions{}, slog.Default(), "task", "", new(atomic.Int32))
				if err != nil {
					t.Error(err)
				}
				done <- got
			}()
			msgs <- agent.Message{Type: agent.MessageText, Content: "tail"}
			close(msgs)
			// Unbuffered send proves the normal-result branch was selected.
			results <- want
			<-flushStarted
			cancel()
			release()
			select {
			case got := <-done:
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("flush-only cancellation changed result: got=%+v want=%+v", got, want)
				}
			case <-time.After(time.Second):
				t.Fatal("transcript flush did not finish")
			}
		})
	}
}

func TestCancelledTaskUsageReachesHTTPAfterBackendCleanup(t *testing.T) {
	for _, parentCancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "server poll cancellation", true: "daemon parent cancellation"}[parentCancel], func(t *testing.T) {
			var usageCalls atomic.Int32
			var completeCalls atomic.Int32
			var ackBeforeUsage atomic.Bool
			var received []TaskUsageEntry
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/usage"):
					var body struct {
						Usage []TaskUsageEntry `json:"usage"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					received = body.Usage
					usageCalls.Add(1)
				case strings.HasSuffix(r.URL.Path, "/complete"):
					completeCalls.Add(1)
				case strings.HasSuffix(r.URL.Path, "/cancel-ack"):
					if usageCalls.Load() == 0 {
						ackBeforeUsage.Store(true)
					}
				case strings.HasSuffix(r.URL.Path, "/status"):
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"status":"cancelled"}`))
				}
			}))
			defer srv.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			d := newTestDaemon(t)
			d.cancelledResultWait = time.Second
			d.client = NewClient(srv.URL)
			d.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
			d.workspaces = make(map[string]*workspaceState)
			d.runtimeIndex = map[string]Runtime{"rt": {ID: "rt", Provider: "copilot"}}
			want := map[string]agent.TokenUsage{"gpt-5.5": {InputTokens: 100, OutputTokens: 20}, "cost-only": {CostUSDTicks: 1234}}
			d.runner = taskRunnerFunc(func(runCtx context.Context, task Task, provider string, _ int, logger *slog.Logger) (TaskResult, error) {
				if parentCancel {
					cancel()
				}
				result, _, err := d.executeAndDrain(runCtx, cancelledUsageBackend{delay: 50 * time.Millisecond, result: agent.Result{Status: "completed", Usage: want}}, "unused", agent.ExecOptions{}, logger, task.ID, "", new(atomic.Int32))
				return TaskResult{Status: result.Status, Usage: taskUsageEntries(provider, result.Usage)}, err
			})
			d.handleTask(ctx, Task{ID: "task", RuntimeID: "rt", IssueID: "issue", Agent: &AgentData{Name: "test"}}, 0)
			if usageCalls.Load() != 1 || completeCalls.Load() != 0 || ackBeforeUsage.Load() {
				t.Fatalf("usage=%d completed=%d ackBeforeUsage=%v", usageCalls.Load(), completeCalls.Load(), ackBeforeUsage.Load())
			}
			got := map[string]agent.TokenUsage{}
			for _, u := range received {
				got[u.Model] = agent.TokenUsage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CostUSDTicks: u.CostUSDTicks}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("HTTP accounting=%+v want=%+v", got, want)
			}
		})
	}
}
