package agent

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCopilotExecuteKeepsRequestUsageWhenProcessFailsWithoutShutdown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell")
	}
	p := filepath.Join(t.TempDir(), "copilot")
	body := `#!/bin/sh
printf '%s\n' '{"type":"span","traceId":"t","spanId":"call","attributes":{"gen_ai.operation.name":"chat","gen_ai.response.model":"gpt-5.5","gen_ai.usage.input_tokens":1000,"gen_ai.usage.output_tokens":20,"gen_ai.usage.cache_read.input_tokens":800}}' >> "$COPILOT_OTEL_FILE_EXPORTER_PATH"
exit 7
`
	writeTestExecutable(t, p, []byte(body))
	result := runCopilotExecuteWithConfig(t, Config{ExecutablePath: p, Logger: slog.Default(), Env: map[string]string{"HOME": t.TempDir()}}, ExecOptions{})
	if result.Usage["gpt-5.5"] != (TokenUsage{InputTokens: 200, OutputTokens: 20, CacheReadTokens: 800}) {
		t.Fatalf("lost interrupted usage: %+v", result)
	}
}

func TestCopilotExecuteRequestUsageSurvivesCancellation(t *testing.T) {
	testCopilotCancellation(t, `#!/bin/sh
printf '%s\n' '{"type":"span","traceId":"t","spanId":"call","attributes":{"gen_ai.operation.name":"chat","gen_ai.response.model":"gpt-5.5","gen_ai.usage.input_tokens":1000,"gen_ai.usage.output_tokens":20}}' >> "$COPILOT_OTEL_FILE_EXPORTER_PATH"
printf '%s' "$COPILOT_OTEL_FILE_EXPORTER_PATH" > "$HOME/ready"
exec sleep 30
`, TokenUsage{InputTokens: 1000, OutputTokens: 20})
}

func TestCopilotCancellationAllowsTelemetryFlush(t *testing.T) {
	testCopilotCancellation(t, `#!/bin/sh
flush() {
printf '%s\n' '{"type":"span","traceId":"t","spanId":"call","attributes":{"gen_ai.operation.name":"chat","gen_ai.response.model":"gpt-5.5","gen_ai.usage.input_tokens":1000,"gen_ai.usage.output_tokens":20}}' >> "$COPILOT_OTEL_FILE_EXPORTER_PATH"
exit 0
}
trap flush TERM
printf '%s' "$COPILOT_OTEL_FILE_EXPORTER_PATH" > "$HOME/ready"
while :; do sleep 1; done
`, TokenUsage{InputTokens: 1000, OutputTokens: 20})
}

func TestCopilotCancellationEscalatesWhenTermIgnored(t *testing.T) {
	testCopilotCancellation(t, `#!/bin/sh
trap '' TERM
printf '%s' "$COPILOT_OTEL_FILE_EXPORTER_PATH" > "$HOME/ready"
exec sleep 30
`, TokenUsage{})
}

func testCopilotCancellation(t *testing.T, script string, want TokenUsage) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell")
	}
	home := t.TempDir()
	p := filepath.Join(home, "copilot")
	writeTestExecutable(t, p, []byte(script))
	backend, err := New("copilot", Config{ExecutablePath: p, Logger: slog.Default(), Env: map[string]string{"HOME": home}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session, err := backend.Execute(ctx, "prompt", ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range session.Messages {
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	var telemetryPath []byte
	for time.Now().Before(deadline) {
		telemetryPath, _ = os.ReadFile(filepath.Join(home, "ready"))
		if len(telemetryPath) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(telemetryPath) == 0 {
		t.Fatal("request was not exported")
	}
	cancel()
	select {
	case result := <-session.Result:
		if result.Status != "aborted" || result.Usage["gpt-5.5"] != want {
			t.Fatalf("cancelled result = %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation hung")
	}
	// Worker cleanup completes when the channel is closed.
	for range session.Result {
	}
	if _, err := os.Stat(string(telemetryPath)); !os.IsNotExist(err) {
		// File cleanup may follow channel close by a few instructions.
		deadline = time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if _, err = os.Stat(string(telemetryPath)); os.IsNotExist(err) {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("telemetry file not removed: %v", err)
	}
}

func TestCopilotExecuteRequestUsageSourcePrecedence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell")
	}
	for _, resumed := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh snapshot", true: "stale resumed snapshot"}[resumed], func(t *testing.T) {
			home := t.TempDir()
			id := "35059dc3-d928-4ffb-8616-b78938621d88"
			writeCopilotSessionEvents(t, home, id, shutdownEvent("gpt-5.5", 10000, 100, 0, 0), `{"type":"assistant.message","data":{"content":"previous interrupted call"}}`)
			p := filepath.Join(home, "copilot")
			body := `#!/bin/sh
printf '%s\n' '{"type":"span","traceId":"t","spanId":"call","attributes":{"gen_ai.operation.name":"chat","gen_ai.response.model":"gpt-5.5","gen_ai.usage.input_tokens":1000,"gen_ai.usage.output_tokens":20}}' >> "$COPILOT_OTEL_FILE_EXPORTER_PATH"
printf '%s\n' '{"type":"session.start","data":{"sessionId":"` + id + `"}}'
printf '%s\n' '` + shutdownEvent("gpt-5.5", 12000, 140, 0, 0) + `' >> "$HOME/.copilot/session-state/` + id + `/events.jsonl"
`
			writeTestExecutable(t, p, []byte(body))
			opts := ExecOptions{}
			want := TokenUsage{InputTokens: 12000, OutputTokens: 140}
			if resumed {
				opts.ResumeSessionID = id
				want = TokenUsage{InputTokens: 1000, OutputTokens: 20}
			}
			result := runCopilotExecuteWithConfig(t, Config{ExecutablePath: p, Logger: slog.Default(), Env: map[string]string{"HOME": home}}, opts)
			if result.Usage["gpt-5.5"] != want {
				t.Fatalf("got %+v, want %+v", result.Usage, want)
			}
		})
	}
}

func TestCopilotOTelUsageEnvironmentIsolation(t *testing.T) {
	env := []string{"HOME=/example", "OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=true"}
	firstEnv, first, err := prepareCopilotOTelUsage(env)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(first)
	_, second, err := prepareCopilotOTelUsage(env)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(second)
	if first == second || envValue(firstEnv, "OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT") != "false" {
		t.Fatal("telemetry not isolated")
	}
	info, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("unsafe mode: %v", info.Mode())
	}
	for _, setting := range []string{"COPILOT_OTEL_ENABLED=false", "COPILOT_OTEL_EXPORTER_TYPE=otlp", "OTEL_EXPORTER_OTLP_ENDPOINT=http://collector"} {
		original := append(append([]string{}, env...), setting)
		got, path, err := prepareCopilotOTelUsage(original)
		if err != nil || path != "" || !reflect.DeepEqual(original, got) {
			t.Fatalf("overrode operator configuration: %s", setting)
		}
	}
}

func TestCopilotOTelUsagePreservesInconsistentCacheSpan(t *testing.T) {
	p := filepath.Join(t.TempDir(), "otel.jsonl")
	body := `{"type":"span","traceId":"t","spanId":"a","attributes":{"gen_ai.operation.name":"chat","gen_ai.request.model":"gpt-5.5","gen_ai.usage.input_tokens":100,"gen_ai.usage.output_tokens":20,"gen_ai.usage.cache_read.input_tokens":200,"gen_ai.usage.cache_write.input_tokens":50,"github.copilot.nano_aiu":1000}}
`
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readCopilotOTelUsage(p)
	if err == nil || !strings.Contains(err.Error(), "cache") {
		t.Fatalf("missing diagnostic: %v", err)
	}
	if got["gpt-5.5"] != (TokenUsage{OutputTokens: 20, CacheReadTokens: 200, CacheWriteTokens: 50, CostUSDTicks: 100}) {
		t.Fatalf("lost inconsistent request: %+v", got)
	}
}

func TestCopilotOTelDiagnosticsDistinguishSkipFromFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell")
	}
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "configured skip", true: "file creation failure"}[fail], func(t *testing.T) {
			home := t.TempDir()
			p := filepath.Join(home, "copilot")
			writeTestExecutable(t, p, []byte("#!/bin/sh\nexit 0\n"))
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			env := map[string]string{"HOME": home}
			if fail {
				t.Setenv("TMPDIR", filepath.Join(home, "missing"))
			} else {
				env["OTEL_EXPORTER_OTLP_ENDPOINT"] = "http://private-collector"
			}
			runCopilotExecuteWithConfig(t, Config{ExecutablePath: p, Logger: logger, Env: env}, ExecOptions{})
			got := logs.String()
			want := `"level":"DEBUG","msg":"Copilot request telemetry skipped"`
			if fail {
				want = `"level":"WARN","msg":"Copilot request telemetry unavailable"`
			}
			if !strings.Contains(got, want) {
				t.Fatalf("missing telemetry diagnostic %s: %s", want, got)
			}
			if !fail && (strings.Contains(got, "Copilot request telemetry unavailable") || strings.Contains(got, "private-collector")) {
				t.Fatalf("skip produced failure or leaked endpoint: %s", got)
			}
		})
	}
}

func TestCopilotOTelUsageReadsCompletedCallsWithoutShutdown(t *testing.T) {
	p := filepath.Join(t.TempDir(), "otel.jsonl")
	body := `{"type":"span","traceId":"t","spanId":"one","attributes":{"gen_ai.operation.name":"chat","gen_ai.response.model":"gpt-5.6-luna","gen_ai.usage.input_tokens":20355,"gen_ai.usage.output_tokens":5,"gen_ai.usage.cache_write.input_tokens":20352,"github.copilot.cost":1,"github.copilot.nano_aiu":509460000}}
{"type":"span","traceId":"t","spanId":"one","attributes":{"gen_ai.operation.name":"chat","gen_ai.response.model":"gpt-5.6-luna","gen_ai.usage.input_tokens":20355,"gen_ai.usage.output_tokens":5,"gen_ai.usage.cache_write.input_tokens":20352,"github.copilot.nano_aiu":509460000}}
{"type":"span","traceId":"t","spanId":"root","attributes":{"gen_ai.operation.name":"invoke_agent","gen_ai.request.model":"gpt-5.6-luna","gen_ai.usage.input_tokens":20355}}
{"type":"metric"}
{"type":"span","unfinished":`
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readCopilotOTelUsage(p)
	if err != nil {
		t.Fatal(err)
	}
	want := TokenUsage{InputTokens: 3, OutputTokens: 5, CacheWriteTokens: 20352, CostUSDTicks: 50946000}
	if got["gpt-5.6-luna"] != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCopilotOTelUsageDoesNotTreatPremiumMultiplierAsDollars(t *testing.T) {
	p := filepath.Join(t.TempDir(), "otel.jsonl")
	body := `{"type":"span","traceId":"t","spanId":"a","attributes":{"gen_ai.operation.name":"chat","gen_ai.request.model":"gpt-5.5","gen_ai.usage.input_tokens":1000,"gen_ai.usage.cache_read.input_tokens":800,"gen_ai.usage.output_tokens":20,"github.copilot.cost":7.5}}` + "\n"
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readCopilotOTelUsage(p)
	if err != nil {
		t.Fatal(err)
	}
	if got["gpt-5.5"] != (TokenUsage{InputTokens: 200, CacheReadTokens: 800, OutputTokens: 20}) {
		t.Fatalf("usage = %+v", got)
	}
}

func TestCopilotOTelUsageMixedPricesDoNotHideUnpricedCalls(t *testing.T) {
	p := filepath.Join(t.TempDir(), "otel.jsonl")
	body := `{"type":"span","traceId":"t","spanId":"a","attributes":{"gen_ai.operation.name":"chat","gen_ai.request.model":"gpt-5.5","gen_ai.usage.input_tokens":1000,"gen_ai.usage.output_tokens":20,"github.copilot.nano_aiu":1000}}
{"type":"span","traceId":"t","spanId":"b","attributes":{"gen_ai.operation.name":"chat","gen_ai.request.model":"gpt-5.5","gen_ai.usage.input_tokens":2000,"gen_ai.usage.output_tokens":30}}
{"type":"span","traceId":"t","spanId":"c","attributes":{"gen_ai.operation.name":"chat","gen_ai.request.model":"other","gen_ai.usage.input_tokens":100,"gen_ai.usage.output_tokens":1,"github.copilot.nano_aiu":1000}}
`
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readCopilotOTelUsage(p)
	if err != nil {
		t.Fatal(err)
	}
	if got["gpt-5.5"] != (TokenUsage{InputTokens: 3000, OutputTokens: 50}) || got["other"] != (TokenUsage{InputTokens: 100, OutputTokens: 1, CostUSDTicks: 100}) {
		t.Fatalf("mixed price usage = %+v", got)
	}
}
