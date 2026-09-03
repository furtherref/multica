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

func readCopilotSessionUsageSnapshot(env []string, sessionID string) (copilotUsageSnapshot, bool, error) {
	if !isSafeCopilotSessionID(sessionID) {
		return nil, false, fmt.Errorf("unsafe Copilot session id")
	}
	home, err := copilotHomeDir(env)
	if err != nil {
		return nil, false, err
	}
	path := filepath.Join(home, ".copilot", "session-state", sessionID, "events.jsonl")
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open Copilot session events: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("stat Copilot session events: %w", err)
	}
	start := info.Size() - copilotSessionUsageTailBytes
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, false, fmt.Errorf("seek Copilot session events: %w", err)
	}
	tail, err := io.ReadAll(io.LimitReader(file, copilotSessionUsageTailBytes))
	if err != nil {
		return nil, false, fmt.Errorf("read Copilot session events: %w", err)
	}
	lines := bytes.Split(tail, []byte{'\n'})
	if start > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if !bytes.Contains(line, []byte("session.shutdown")) {
			continue
		}
		var evt copilotEvent
		if err := json.Unmarshal(line, &evt); err != nil || evt.Type != "session.shutdown" {
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
		return snapshot, true, nil
	}
	return nil, false, nil
}

func freshCopilotUsageSnapshot(snapshot copilotUsageSnapshot) map[string]TokenUsage {
	return copilotSnapshotToUsage(snapshot)
}

func diffCopilotUsageSnapshots(before, after copilotUsageSnapshot) (map[string]TokenUsage, error) {
	if before == nil {
		return nil, errors.New("Copilot resume usage baseline is unavailable")
	}
	for model := range before {
		if _, ok := after[model]; !ok {
			return nil, fmt.Errorf("Copilot usage counters dropped model %q", model)
		}
	}
	delta := make(copilotUsageSnapshot, len(after))
	for model, current := range after {
		previous := before[model]
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
	return copilotSnapshotToUsage(delta), nil
}

func copilotSnapshotToUsage(snapshot copilotUsageSnapshot) map[string]TokenUsage {
	usage := make(map[string]TokenUsage, len(snapshot))
	for model, raw := range snapshot {
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

func copilotHomeDir(env []string) (string, error) {
	keys := []string{"HOME"}
	if runtime.GOOS == "windows" {
		keys = []string{"USERPROFILE", "HOME"}
	}
	for _, key := range keys {
		if value := envValue(env, key); value != "" {
			return value, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("Copilot home directory is unavailable")
	}
	return home, nil
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
