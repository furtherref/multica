package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
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

	read, err := readCopilotSessionUsageSnapshot(
		[]string{"HOME=" + home},
		sessionID,
	)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if !read.Found {
		t.Fatal("expected a shutdown snapshot")
	}
	if read.ActivityAfterShutdown {
		t.Fatal("a session whose last event is a shutdown must not be marked active")
	}
	want := copilotUsageSnapshot{
		"claude-sonnet-5": {
			InputTokens:      30_000,
			OutputTokens:     900,
			CacheReadTokens:  24_000,
			CacheWriteTokens: 2_000,
		},
	}
	if !reflect.DeepEqual(read.Snapshot, want) {
		t.Fatalf("snapshot = %#v, want %#v", read.Snapshot, want)
	}
}

func TestCopilotSessionUsageHonorsCopilotHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	copilotHome := filepath.Join(t.TempDir(), "relocated-copilot")
	sessionID := "35059dc3-d928-4ffb-8616-b78938621d90"
	path := filepath.Join(copilotHome, "session-state", sessionID, "events.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir session state: %v", err)
	}
	if err := os.WriteFile(path, []byte(shutdownEvent("gpt-5.5", 1_000, 100, 800, 100)+"\n"), 0o600); err != nil {
		t.Fatalf("write session events: %v", err)
	}

	read, err := readCopilotSessionUsageSnapshot(
		[]string{"HOME=" + home, "COPILOT_HOME=" + copilotHome},
		sessionID,
	)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if !read.Found || read.Snapshot["gpt-5.5"].InputTokens != 1_000 {
		t.Fatalf("snapshot = %#v, found=%v; COPILOT_HOME should locate session-state", read.Snapshot, read.Found)
	}
}

func TestCopilotSessionUsageKeepsLineOnTailBoundary(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	sessionID := "35059dc3-d928-4ffb-8616-b78938621d91"
	shutdown := shutdownEvent("gpt-5.5", 1_000, 100, 800, 100) + "\n"
	fillerLine := `{"type":"assistant.message","data":{"content":"x"}}` + "\n"
	// Lay the shutdown line down so that it starts exactly at the tail
	// window boundary: everything from its first byte to EOF is exactly the
	// window size, and the byte before it is the prefix's newline.
	prefix := strings.Repeat(fillerLine, 3)
	suffix := make([]byte, 0, copilotSessionUsageTailBytes)
	remaining := int(copilotSessionUsageTailBytes) - len(shutdown)
	for remaining > 0 {
		chunk := fillerLine
		if len(chunk) > remaining {
			chunk = strings.Repeat("x", remaining-1) + "\n"
		}
		suffix = append(suffix, chunk...)
		remaining -= len(chunk)
	}
	writeCopilotSessionEvents(t, home, sessionID, prefix+shutdown+string(suffix))

	read, err := readCopilotSessionUsageSnapshot([]string{"HOME=" + home}, sessionID)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if !read.Found || read.Snapshot["gpt-5.5"].OutputTokens != 100 {
		t.Fatalf("snapshot = %#v, found=%v; a line starting on the window boundary must be kept", read.Snapshot, read.Found)
	}
}

func TestCopilotSessionUsageFlagsActivityAfterShutdown(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	sessionID := "35059dc3-d928-4ffb-8616-b78938621d92"
	writeCopilotSessionEvents(t, home, sessionID,
		shutdownEvent("gpt-5.5", 1_000, 100, 800, 100),
		`{"type":"session.start","data":{}}`,
		`{"type":"assistant.message","data":{"content":"turn from a run that was killed"}}`,
	)

	read, err := readCopilotSessionUsageSnapshot([]string{"HOME=" + home}, sessionID)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if !read.Found || !read.ActivityAfterShutdown {
		t.Fatalf("read = %#v; assistant activity after the last shutdown must mark the baseline stale", read)
	}
	if _, err := diffCopilotUsageSnapshots(read, copilotSessionUsageRead{
		Found:    true,
		Snapshot: copilotUsageSnapshot{"gpt-5.5": {InputTokens: 5_000, OutputTokens: 500}},
	}, "gpt-5.5"); err == nil {
		t.Fatal("expected a stale baseline to be rejected")
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

	usage := freshCopilotUsageSnapshot(snapshot, "fallback")
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

	usage, err := diffCopilotUsageSnapshots(
		copilotSessionUsageRead{Snapshot: before, Found: true},
		copilotSessionUsageRead{Snapshot: after, Found: true},
		"fallback",
	)
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

	read, err := readCopilotSessionUsageSnapshot([]string{"HOME=" + home}, sessionID)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if !read.Found || read.Snapshot["gpt-5.5"].OutputTokens != 100 {
		t.Fatalf("snapshot = %#v, found=%v", read.Snapshot, read.Found)
	}
	if read.ActivityAfterShutdown {
		t.Fatal("a torn trailing line is not model activity")
	}
}

func TestCopilotSessionUsageAttributesEmptyModelToFallback(t *testing.T) {
	t.Parallel()
	usage := freshCopilotUsageSnapshot(copilotUsageSnapshot{
		"": {InputTokens: 1_000, OutputTokens: 100},
	}, "claude-sonnet-5")
	if _, ok := usage[""]; ok {
		t.Fatal("empty model key must not be reported")
	}
	if got := usage["claude-sonnet-5"]; got.InputTokens != 1_000 || got.OutputTokens != 100 {
		t.Fatalf("fallback usage = %#v", got)
	}
}

func TestCopilotSessionUsageRejectsUnsafeSessionID(t *testing.T) {
	t.Parallel()
	_, err := readCopilotSessionUsageSnapshot([]string{"HOME=" + t.TempDir()}, "../escape")
	if err == nil {
		t.Fatal("expected unsafe session id to be rejected")
	}
}

func TestCopilotSessionUsageResumeRequiresBaseline(t *testing.T) {
	t.Parallel()
	_, err := diffCopilotUsageSnapshots(copilotSessionUsageRead{}, copilotSessionUsageRead{
		Found:    true,
		Snapshot: copilotUsageSnapshot{"gpt-5.5": {InputTokens: 100, OutputTokens: 10}},
	}, "gpt-5.5")
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

	_, err := diffCopilotUsageSnapshots(
		copilotSessionUsageRead{Snapshot: before, Found: true},
		copilotSessionUsageRead{Snapshot: after, Found: true},
		"gpt-5.5",
	)
	if err == nil {
		t.Fatal("expected regressed cumulative counters to be rejected")
	}
}

func TestCopilotResumeUsageLockSerializesSameSession(t *testing.T) {
	home := t.TempDir()
	env := []string{"HOME=" + home}
	sessionID := "35059dc3-d928-4ffb-8616-b78938621d90"

	releaseFirst, err := acquireCopilotResumeUsageLock(context.Background(), env, sessionID)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}

	secondAcquired := make(chan func(), 1)
	go func() {
		release, acquireErr := acquireCopilotResumeUsageLock(context.Background(), env, sessionID)
		if acquireErr == nil {
			secondAcquired <- release
		}
	}()

	select {
	case release := <-secondAcquired:
		release()
		releaseFirst()
		t.Fatal("second execution acquired the same session before the first released it")
	case <-time.After(100 * time.Millisecond):
	}

	releaseFirst()
	select {
	case release := <-secondAcquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("second execution did not acquire the session after release")
	}
}

func TestCopilotResumeUsageLockAllowsDifferentSessions(t *testing.T) {
	home := t.TempDir()
	env := []string{"HOME=" + home}

	releaseFirst, err := acquireCopilotResumeUsageLock(context.Background(), env, "35059dc3-d928-4ffb-8616-b78938621d91")
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	defer releaseFirst()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	releaseSecond, err := acquireCopilotResumeUsageLock(ctx, env, "35059dc3-d928-4ffb-8616-b78938621d92")
	if err != nil {
		t.Fatalf("different session was blocked: %v", err)
	}
	releaseSecond()
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

func waitForCopilotFixtureFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat fixture marker: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for fixture marker %q", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCopilotExecuteSerializesConcurrentResumesOfSameSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX-only")
	}
	home := t.TempDir()
	sessionID := "35059dc3-d928-4ffb-8616-b78938621d93"
	writeCopilotSessionEvents(t, home, sessionID, shutdownEvent("gpt-5.6-terra", 10_000, 500, 8_000, 1_000))

	fixtureDir := t.TempDir()
	firstClaim := filepath.Join(fixtureDir, "first-claim")
	firstStarted := filepath.Join(fixtureDir, "first-started")
	secondStarted := filepath.Join(fixtureDir, "second-started")
	releaseFirst := filepath.Join(fixtureDir, "release-first")
	sessionPath := filepath.Join(home, ".copilot", "session-state", sessionID, "events.jsonl")
	fakePath := filepath.Join(fixtureDir, "copilot")
	script := "#!/bin/sh\n" +
		"if mkdir \"" + firstClaim + "\" 2>/dev/null; then\n" +
		"  : > \"" + firstStarted + "\"\n" +
		"  while [ ! -f \"" + releaseFirst + "\" ]; do sleep 0.02; done\n" +
		"  printf '%s\\n' '" + shutdownEvent("gpt-5.6-terra", 25_000, 1_100, 20_000, 2_000) + "' >> \"" + sessionPath + "\"\n" +
		"else\n" +
		"  : > \"" + secondStarted + "\"\n" +
		"  printf '%s\\n' '" + shutdownEvent("gpt-5.6-terra", 40_000, 1_500, 32_000, 3_000) + "' >> \"" + sessionPath + "\"\n" +
		"fi\n" +
		"printf '%s\\n' '{\"type\":\"result\",\"sessionId\":\"" + sessionID + "\",\"exitCode\":0}'\n"
	writeTestExecutable(t, fakePath, []byte(script))

	backend, err := New("copilot", Config{
		ExecutablePath: fakePath,
		Env:            map[string]string{"HOME": home},
		Logger:         slog.Default(),
	})
	if err != nil {
		t.Fatalf("new Copilot backend: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer func() { _ = os.WriteFile(releaseFirst, nil, 0o600) }()

	first, err := backend.Execute(ctx, "first", ExecOptions{ResumeSessionID: sessionID})
	if err != nil {
		t.Fatalf("execute first resume: %v", err)
	}
	go func() { // The backend must always have a message consumer.
		for range first.Messages {
		}
	}()
	waitForCopilotFixtureFile(t, firstStarted)

	type executeResult struct {
		session *Session
		err     error
	}
	secondExecute := make(chan executeResult, 1)
	go func() {
		session, executeErr := backend.Execute(ctx, "second", ExecOptions{ResumeSessionID: sessionID})
		secondExecute <- executeResult{session: session, err: executeErr}
	}()

	select {
	case result := <-secondExecute:
		if result.session != nil {
			go func() {
				for range result.session.Messages {
				}
			}()
		}
		t.Fatalf("second same-session Execute returned before the first completed: %v", result.err)
	case <-time.After(150 * time.Millisecond):
	}
	if _, err := os.Stat(secondStarted); !os.IsNotExist(err) {
		t.Fatalf("second same-session process started early; stat error = %v", err)
	}

	if err := os.WriteFile(releaseFirst, nil, 0o600); err != nil {
		t.Fatalf("release first fixture: %v", err)
	}
	firstResult := <-first.Result
	if got := firstResult.Usage["gpt-5.6-terra"]; got.OutputTokens != 600 {
		t.Fatalf("first resume usage = %#v", got)
	}

	var second executeResult
	select {
	case second = <-secondExecute:
	case <-ctx.Done():
		t.Fatalf("second same-session Execute remained blocked: %v", ctx.Err())
	}
	if second.err != nil {
		t.Fatalf("execute second resume: %v", second.err)
	}
	go func() {
		for range second.session.Messages {
		}
	}()
	secondResult := <-second.session.Result
	if got := secondResult.Usage["gpt-5.6-terra"]; got.OutputTokens != 400 {
		t.Fatalf("second resume usage = %#v", got)
	}
}

func TestCopilotExecuteFreshSessionFileBeatsLegacyOutputOnly(t *testing.T) {
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
	}, ExecOptions{})

	got := result.Usage["claude-sonnet-5"]
	if got.InputTokens != 4_000 || got.OutputTokens != 900 {
		t.Fatalf("session-file usage = %#v, want full 4000/900 breakdown", got)
	}
}

func TestCopilotExecuteCompleteStdoutBeatsSessionFile(t *testing.T) {
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
	}, ExecOptions{})

	got := result.Usage["claude-sonnet-4.5"]
	if got.InputTokens != 1_500 || got.OutputTokens != 250 {
		t.Fatalf("stdout usage = %#v, want authoritative 1500/250", got)
	}
}

func TestCopilotExecuteResumeUsesSessionFileDelta(t *testing.T) {
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
	}, ExecOptions{ResumeSessionID: sessionID})

	got := result.Usage["gpt-5.6-terra"]
	if got.InputTokens != 2_000 || got.OutputTokens != 600 ||
		got.CacheReadTokens != 12_000 || got.CacheWriteTokens != 1_000 {
		t.Fatalf("resume delta = %#v", got)
	}
}

func TestCopilotExecuteResumeSkipsStaleBaseline(t *testing.T) {
	home := t.TempDir()
	sessionID := "35059dc3-d928-4ffb-8616-b78938621d88"
	writeCopilotSessionEvents(
		t,
		home,
		sessionID,
		shutdownEvent("gpt-5.6-terra", 10_000, 500, 8_000, 1_000),
		// A previous resumed run consumed tokens and was killed before its
		// shutdown was written; its usage was reported from stdout already.
		`{"type":"assistant.message","data":{"model":"gpt-5.6-terra","content":"killed turn"}}`,
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
	}, ExecOptions{ResumeSessionID: sessionID})

	if hasTokens(result.Usage) {
		t.Fatalf("usage = %#v; a stale baseline must not be diffed, the killed run would be billed twice", result.Usage)
	}
}

func TestCopilotExecuteResumeSkipsFileWhenSessionIDMismatches(t *testing.T) {
	home := t.TempDir()
	resumeID := "35059dc3-d928-4ffb-8616-b78938621d89"
	reportedID := "35059dc3-d928-4ffb-8616-b78938621d8a"
	writeCopilotSessionEvents(t, home, resumeID, shutdownEvent("gpt-5.6-terra", 10_000, 500, 8_000, 1_000))
	fakePath := writeCopilotSessionFixtureExecutable(
		t,
		home,
		reportedID,
		nil,
		[]string{shutdownEvent("gpt-5.6-terra", 25_000, 1_100, 20_000, 2_000)},
	)

	result := runCopilotExecuteWithConfig(t, Config{
		ExecutablePath: fakePath,
		Env:            map[string]string{"HOME": home},
		Logger:         slog.Default(),
	}, ExecOptions{ResumeSessionID: resumeID})

	if hasTokens(result.Usage) {
		t.Fatalf("usage = %#v; a resumed run reporting a different session id must not be billed as a fresh session", result.Usage)
	}
}
