package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const copilotSessionUsageTailBytes int64 = 8 * 1024 * 1024

type copilotUsageSnapshot map[string]copilotUsageData

// copilotSessionUsageRead is what the session file says about a session's
// cumulative usage at the moment it was read.
type copilotSessionUsageRead struct {
	// Snapshot is the per-model cumulative usage from the latest complete
	// session.shutdown event. Nil when Found is false.
	Snapshot copilotUsageSnapshot
	// Found reports whether any session.shutdown was present in the tail.
	Found bool
	// ActivityAfterShutdown reports that model activity (assistant.* or
	// tool.* events) was appended after the latest shutdown. The session was
	// then used again without ever tearing down cleanly, so the snapshot no
	// longer describes the session's current counters and must not serve as
	// a resume baseline — diffing against it would fold that unrecorded
	// activity into the next run.
	ActivityAfterShutdown bool
}

func readCopilotSessionUsageSnapshot(env []string, sessionID string) (copilotSessionUsageRead, error) {
	var read copilotSessionUsageRead
	if !isSafeCopilotSessionID(sessionID) {
		return read, fmt.Errorf("unsafe Copilot session id")
	}
	configDir, err := copilotConfigDir(env)
	if err != nil {
		return read, err
	}
	path := filepath.Join(configDir, "session-state", sessionID, "events.jsonl")
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return read, nil
	}
	if err != nil {
		return read, fmt.Errorf("open Copilot session events: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return read, fmt.Errorf("stat Copilot session events: %w", err)
	}
	start := info.Size() - copilotSessionUsageTailBytes
	if start < 0 {
		start = 0
	}
	// Read one byte before the tail window: when it is a newline the window
	// begins on a complete line that must be kept; otherwise the first
	// (partial) line is dropped.
	readFrom := start
	if start > 0 {
		readFrom = start - 1
	}
	if _, err := file.Seek(readFrom, io.SeekStart); err != nil {
		return read, fmt.Errorf("seek Copilot session events: %w", err)
	}
	tail, err := io.ReadAll(io.LimitReader(file, copilotSessionUsageTailBytes+1))
	if err != nil {
		return read, fmt.Errorf("read Copilot session events: %w", err)
	}
	if start > 0 && len(tail) > 0 {
		if tail[0] == '\n' {
			tail = tail[1:]
		} else if nl := bytes.IndexByte(tail, '\n'); nl >= 0 {
			tail = tail[nl+1:]
		} else {
			tail = nil
		}
	}
	lines := bytes.Split(tail, []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var evt copilotEvent
		if err := json.Unmarshal(line, &evt); err != nil {
			// A torn trailing write; keep scanning for the last complete event.
			continue
		}
		if evt.Type != "session.shutdown" {
			if strings.HasPrefix(evt.Type, "assistant.") || strings.HasPrefix(evt.Type, "tool.") {
				read.ActivityAfterShutdown = true
			}
			continue
		}
		var shutdown copilotShutdownData
		if err := json.Unmarshal(evt.Data, &shutdown); err != nil || len(shutdown.ModelMetrics) == 0 {
			continue
		}
		snapshot := make(copilotUsageSnapshot, len(shutdown.ModelMetrics))
		for model, metric := range shutdown.ModelMetrics {
			snapshot[model] = metric.Usage
		}
		read.Snapshot = snapshot
		read.Found = true
		return read, nil
	}
	read.ActivityAfterShutdown = false
	return read, nil
}

// freshCopilotUsageSnapshot reports a whole session's counters as this run's
// usage. Only valid when the session was created by this run.
func freshCopilotUsageSnapshot(snapshot copilotUsageSnapshot, fallbackModel string) map[string]TokenUsage {
	return copilotSnapshotToUsage(snapshot, fallbackModel)
}

// diffCopilotUsageSnapshots reports the counters a resumed run added on top
// of the baseline captured before it started.
func diffCopilotUsageSnapshots(before, after copilotSessionUsageRead, fallbackModel string) (map[string]TokenUsage, error) {
	if !before.Found || before.Snapshot == nil {
		return nil, errors.New("Copilot resume usage baseline is unavailable")
	}
	if before.ActivityAfterShutdown {
		return nil, errors.New("Copilot resume usage baseline is stale: the session was used after its last shutdown")
	}
	for model := range before.Snapshot {
		if _, ok := after.Snapshot[model]; !ok {
			return nil, fmt.Errorf("Copilot usage counters dropped model %q", model)
		}
	}
	delta := make(copilotUsageSnapshot, len(after.Snapshot))
	for model, current := range after.Snapshot {
		previous := before.Snapshot[model]
		if current.InputTokens < previous.InputTokens ||
			current.OutputTokens < previous.OutputTokens ||
			current.CacheReadTokens < previous.CacheReadTokens ||
			current.CacheWriteTokens < previous.CacheWriteTokens {
			return nil, fmt.Errorf("Copilot usage counters regressed for model %q", model)
		}
		delta[model] = copilotUsageData{
			InputTokens:      current.InputTokens - previous.InputTokens,
			OutputTokens:     current.OutputTokens - previous.OutputTokens,
			CacheReadTokens:  current.CacheReadTokens - previous.CacheReadTokens,
			CacheWriteTokens: current.CacheWriteTokens - previous.CacheWriteTokens,
		}
	}
	return copilotSnapshotToUsage(delta, fallbackModel), nil
}

// copilotSnapshotToUsage mirrors the stream's session.shutdown handling: an
// empty modelMetrics key is attributed to fallbackModel so the row stays
// priceable instead of landing in the database under model "".
func copilotSnapshotToUsage(snapshot copilotUsageSnapshot, fallbackModel string) map[string]TokenUsage {
	usage := make(map[string]TokenUsage, len(snapshot))
	for model, raw := range snapshot {
		if model == "" {
			model = fallbackModel
		}
		addUsage(
			usage,
			model,
			raw.InputTokens,
			raw.OutputTokens,
			raw.CacheReadTokens,
			raw.CacheWriteTokens,
		)
	}
	return usage
}

// copilotConfigDir resolves the Copilot CLI state directory the same way the
// daemon's MCP config loader does: COPILOT_HOME wins, else <home>/.copilot.
func copilotConfigDir(env []string) (string, error) {
	if custom := strings.TrimSpace(envValue(env, "COPILOT_HOME")); custom != "" {
		return custom, nil
	}
	keys := []string{"HOME"}
	if runtime.GOOS == "windows" {
		keys = []string{"USERPROFILE", "HOME"}
	}
	for _, key := range keys {
		if value := envValue(env, key); value != "" {
			return filepath.Join(value, ".copilot"), nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("Copilot home directory is unavailable")
	}
	return filepath.Join(home, ".copilot"), nil
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimPrefix(env[i], prefix)
		}
	}
	return ""
}

func isSafeCopilotSessionID(sessionID string) bool {
	if sessionID == "" || sessionID == "." || sessionID == ".." {
		return false
	}
	for _, r := range sessionID {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}
