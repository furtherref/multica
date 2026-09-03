package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func writeCopilotSessionEvents(t *testing.T, home, sessionID string, lines ...string) {
	t.Helper()
	path := filepath.Join(home, ".copilot", "session-state", sessionID, "events.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir session state: %v", err)
	}
	var body string
	for _, line := range lines {
		body += line
		if line == "" || line[len(line)-1] != '\n' {
			body += "\n"
		}
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write session events: %v", err)
	}
}

func shutdownEvent(model string, input, output, cacheRead, cacheWrite int64) string {
	return `{"type":"session.shutdown","data":{"modelMetrics":{"` + model + `":{"usage":{"inputTokens":` +
		itoa(input) + `,"outputTokens":` + itoa(output) + `,"cacheReadTokens":` +
		itoa(cacheRead) + `,"cacheWriteTokens":` + itoa(cacheWrite) + `}}}}}`
}

func itoa(n int64) string {
	return fmt.Sprintf("%d", n)
}

func TestCopilotSessionUsageReadsLatestShutdownFromSubprocessHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	sessionID := "35059dc3-d928-4ffb-8616-b78938621d85"
	writeCopilotSessionEvents(t, home, sessionID,
		shutdownEvent("claude-sonnet-5", 10_000, 500, 8_000, 1_000),
		`{"type":"assistant.message","data":{"content":"must not be parsed as usage"}}`,
		shutdownEvent("claude-sonnet-5", 30_000, 900, 24_000, 2_000),
	)

	snapshot, found, err := readCopilotSessionUsageSnapshot(
		[]string{"HOME=" + home},
		sessionID,
	)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if !found {
		t.Fatal("expected a shutdown snapshot")
	}
	want := copilotUsageSnapshot{
		"claude-sonnet-5": {
			InputTokens:      30_000,
			OutputTokens:     900,
			CacheReadTokens:  24_000,
			CacheWriteTokens: 2_000,
		},
	}
	if !reflect.DeepEqual(snapshot, want) {
		t.Fatalf("snapshot = %#v, want %#v", snapshot, want)
	}
}

func TestCopilotSessionUsageFreshSnapshotSeparatesCachedInput(t *testing.T) {
	t.Parallel()
	snapshot := copilotUsageSnapshot{
		"claude-sonnet-5": {
			InputTokens:      30_000,
			OutputTokens:     900,
			CacheReadTokens:  24_000,
			CacheWriteTokens: 2_000,
		},
	}

	usage := freshCopilotUsageSnapshot(snapshot)
	got := usage["claude-sonnet-5"]
	if got.InputTokens != 4_000 || got.OutputTokens != 900 {
		t.Fatalf("fresh usage input/output = %d/%d, want 4000/900", got.InputTokens, got.OutputTokens)
	}
	if got.CacheReadTokens != 24_000 || got.CacheWriteTokens != 2_000 {
		t.Fatalf("fresh cache = %d/%d, want 24000/2000", got.CacheReadTokens, got.CacheWriteTokens)
	}
}

func TestCopilotSessionUsageResumeDiffsEachModel(t *testing.T) {
	t.Parallel()
	before := copilotUsageSnapshot{
		"claude-sonnet-5": {
			InputTokens: 10_000, OutputTokens: 500, CacheReadTokens: 8_000, CacheWriteTokens: 1_000,
		},
	}
	after := copilotUsageSnapshot{
		"claude-sonnet-5": {
			InputTokens: 25_000, OutputTokens: 1_100, CacheReadTokens: 20_000, CacheWriteTokens: 2_000,
		},
		"gpt-5.6-terra": {
			InputTokens: 8_000, OutputTokens: 300, CacheReadTokens: 6_000, CacheWriteTokens: 1_000,
		},
	}

	usage, err := diffCopilotUsageSnapshots(before, after)
	if err != nil {
		t.Fatalf("diff snapshots: %v", err)
	}
	if got := usage["claude-sonnet-5"]; got != (TokenUsage{
		InputTokens:      2_000,
		OutputTokens:     600,
		CacheReadTokens:  12_000,
		CacheWriteTokens: 1_000,
	}) {
		t.Fatalf("claude delta = %#v", got)
	}
	if got := usage["gpt-5.6-terra"]; got != (TokenUsage{
		InputTokens:      1_000,
		OutputTokens:     300,
		CacheReadTokens:  6_000,
		CacheWriteTokens: 1_000,
	}) {
		t.Fatalf("new-model delta = %#v", got)
	}
}

func TestCopilotSessionUsageKeepsLastValidSnapshotBeforePartialLine(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	sessionID := "35059dc3-d928-4ffb-8616-b78938621d85"
	writeCopilotSessionEvents(t, home, sessionID,
		shutdownEvent("gpt-5.5", 1_000, 100, 800, 100),
		`{"type":"session.shutdown","data":{"modelMetrics":`,
	)

	snapshot, found, err := readCopilotSessionUsageSnapshot([]string{"HOME=" + home}, sessionID)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if !found || snapshot["gpt-5.5"].OutputTokens != 100 {
		t.Fatalf("snapshot = %#v, found=%v", snapshot, found)
	}
}

func TestCopilotSessionUsageRejectsUnsafeSessionID(t *testing.T) {
	t.Parallel()
	_, _, err := readCopilotSessionUsageSnapshot([]string{"HOME=" + t.TempDir()}, "../escape")
	if err == nil {
		t.Fatal("expected unsafe session id to be rejected")
	}
}

func TestCopilotSessionUsageResumeRequiresBaseline(t *testing.T) {
	t.Parallel()
	_, err := diffCopilotUsageSnapshots(nil, copilotUsageSnapshot{
		"gpt-5.5": {InputTokens: 100, OutputTokens: 10},
	})
	if err == nil {
		t.Fatal("expected missing baseline to be rejected")
	}
}

func TestCopilotSessionUsageRejectsCounterRegression(t *testing.T) {
	t.Parallel()
	before := copilotUsageSnapshot{
		"gpt-5.5": {InputTokens: 1_000, OutputTokens: 100},
	}
	after := copilotUsageSnapshot{
		"gpt-5.5": {InputTokens: 900, OutputTokens: 120},
	}

	_, err := diffCopilotUsageSnapshots(before, after)
	if err == nil {
		t.Fatal("expected regressed cumulative counters to be rejected")
	}
}

func runCopilotExecuteWithConfig(t *testing.T, cfg Config, opts ExecOptions) Result {
	t.Helper()
	backend, err := New("copilot", cfg)
	if err != nil {
		t.Fatalf("new Copilot backend: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := backend.Execute(ctx, "prompt", opts)
	if err != nil {
		t.Fatalf("execute Copilot: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()
	select {
	case result, ok := <-session.Result:
		if !ok {
			t.Fatal("result channel closed without a value")
		}
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for Copilot result")
	}
	return Result{}
}

func writeCopilotSessionFixtureExecutable(
	t *testing.T,
	home, sessionID string,
	stdoutEvents []string,
	sessionEvents []string,
) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX-only")
	}
	fakePath := filepath.Join(t.TempDir(), "copilot")
	sessionPath := filepath.Join(home, ".copilot", "session-state", sessionID, "events.jsonl")
	script := "#!/bin/sh\n" +
		"mkdir -p \"" + filepath.Dir(sessionPath) + "\"\n"
	for _, line := range sessionEvents {
		script += "printf '%s\\n' '" + line + "' >> \"" + sessionPath + "\"\n"
	}
	for _, line := range stdoutEvents {
		script += "printf '%s\\n' '" + line + "'\n"
	}
	script += "printf '%s\\n' '{\"type\":\"result\",\"sessionId\":\"" + sessionID + "\",\"exitCode\":0}'\n"
	writeTestExecutable(t, fakePath, []byte(script))
	return fakePath
}

func TestCopilotExecuteFreshSessionFileBeatsLegacyOutputOnly(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	sessionID := "35059dc3-d928-4ffb-8616-b78938621d85"
	fakePath := writeCopilotSessionFixtureExecutable(
		t,
		home,
		sessionID,
		[]string{
			`{"type":"assistant.message","data":{"model":"claude-sonnet-5","content":"done","outputTokens":5}}`,
		},
		[]string{shutdownEvent("claude-sonnet-5", 30_000, 900, 24_000, 2_000)},
	)

	result := runCopilotExecuteWithConfig(t, Config{
		ExecutablePath: fakePath,
		Env:            map[string]string{"HOME": home},
		Logger:         slog.Default(),
	}, ExecOptions{Timeout: 5 * time.Second})

	got := result.Usage["claude-sonnet-5"]
	if got.InputTokens != 4_000 || got.OutputTokens != 900 {
		t.Fatalf("session-file usage = %#v, want full 4000/900 breakdown", got)
	}
}

func TestCopilotExecuteCompleteStdoutBeatsSessionFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	sessionID := "35059dc3-d928-4ffb-8616-b78938621d86"
	fakePath := writeCopilotSessionFixtureExecutable(
		t,
		home,
		sessionID,
		[]string{fixtureAssistantUsage},
		[]string{shutdownEvent("claude-sonnet-4.5", 30_000, 900, 24_000, 2_000)},
	)

	result := runCopilotExecuteWithConfig(t, Config{
		ExecutablePath: fakePath,
		Env:            map[string]string{"HOME": home},
		Logger:         slog.Default(),
	}, ExecOptions{Timeout: 5 * time.Second})

	got := result.Usage["claude-sonnet-4.5"]
	if got.InputTokens != 1_500 || got.OutputTokens != 250 {
		t.Fatalf("stdout usage = %#v, want authoritative 1500/250", got)
	}
}

func TestCopilotExecuteResumeUsesSessionFileDelta(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	sessionID := "35059dc3-d928-4ffb-8616-b78938621d87"
	writeCopilotSessionEvents(
		t,
		home,
		sessionID,
		shutdownEvent("gpt-5.6-terra", 10_000, 500, 8_000, 1_000),
	)
	fakePath := writeCopilotSessionFixtureExecutable(
		t,
		home,
		sessionID,
		nil,
		[]string{shutdownEvent("gpt-5.6-terra", 25_000, 1_100, 20_000, 2_000)},
	)

	result := runCopilotExecuteWithConfig(t, Config{
		ExecutablePath: fakePath,
		Env:            map[string]string{"HOME": home},
		Logger:         slog.Default(),
	}, ExecOptions{Timeout: 5 * time.Second, ResumeSessionID: sessionID})

	got := result.Usage["gpt-5.6-terra"]
	if got.InputTokens != 2_000 || got.OutputTokens != 600 ||
		got.CacheReadTokens != 12_000 || got.CacheWriteTokens != 1_000 {
		t.Fatalf("resume delta = %#v", got)
	}
}
