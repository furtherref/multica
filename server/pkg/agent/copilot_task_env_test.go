package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestCopilotTaskEnvironmentGuard(t *testing.T) {
	expected := map[string]string{"MULTICA_TASK_ID": "task", "MULTICA_TOKEN": "mat_fake_test_only"}
	if err := validateCopilotTaskEnvironment(expected, buildEnv(expected)); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"MULTICA_TASK_ID", "MULTICA_TOKEN"} {
		actual := map[string]string{"MULTICA_TASK_ID": "task", "MULTICA_TOKEN": "mat_fake_test_only"}
		delete(actual, key)
		err := validateCopilotTaskEnvironment(expected, buildEnv(actual))
		if err == nil || !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "mat_fake_test_only") {
			t.Fatalf("guard did not safely reject lost %s: %v", key, err)
		}
	}
	if err := validateCopilotTaskEnvironment(nil, nil); err != nil {
		t.Fatal("standalone call rejected")
	}
}

func TestCopilotRejectsMissingTaskCredentialBeforeSpawn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell")
	}
	home := t.TempDir()
	p := filepath.Join(home, "copilot")
	writeTestExecutable(t, p, []byte("#!/bin/sh\nprintf started > \"$HOME/started\"\n"))
	backend, err := New("copilot", Config{ExecutablePath: p, Logger: slog.Default(), Env: map[string]string{"HOME": home, "MULTICA_TASK_ID": "task"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = backend.Execute(context.Background(), "unused", ExecOptions{})
	if err == nil || !strings.Contains(err.Error(), "MULTICA_TOKEN") {
		t.Fatalf("missing credential not rejected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "started")); !os.IsNotExist(err) {
		t.Fatal("agent spawned without task credential")
	}
}

func TestCopilotTelemetryPreservesInjectedTaskEnvironment(t *testing.T) {
	for _, k := range []string{"COPILOT_OTEL_FILE_EXPORTER_PATH", "COPILOT_OTEL_EXPORTER_TYPE", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "COPILOT_OTEL_ENABLED"} {
		t.Setenv(k, "")
	}
	// The first pass must still strip ambient credentials, not task credentials.
	t.Setenv("MULTICA_UNTRUSTED_PARENT", "ambient")
	task := map[string]string{"MULTICA_TOKEN": "mat_fake_test_only", "MULTICA_TASK_ID": "task", "MULTICA_AGENT_ID": "agent", "MULTICA_WORKSPACE_ID": "workspace", "MULTICA_SERVER_URL": "https://task.invalid", "MULTICA_DAEMON_PORT": "19514", "MULTICA_TASK_CONFIG_ROOT": "/tmp/task-config", "PATH": "/task/bin", "GITLAB_TOKEN": "fake-gitlab-test-only"}
	before := buildEnv(task)
	original := append([]string(nil), before...)
	after, p, err := prepareCopilotOTelUsage(before)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)
	if p == "" {
		t.Fatal("telemetry was not enabled")
	}
	for k, v := range task {
		if envValue(after, k) != v {
			t.Errorf("task environment lost: %s", k)
		}
	}
	if envValue(after, "MULTICA_UNTRUSTED_PARENT") != "" {
		t.Fatal("ambient task context leaked")
	}
	if !reflect.DeepEqual(before, original) {
		t.Fatal("mutated caller environment")
	}
}

func TestCopilotChildReceivesTaskCredentialsWithTelemetry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell")
	}
	home := t.TempDir()
	p := filepath.Join(home, "copilot")
	writeTestExecutable(t, p, []byte(`#!/bin/sh
[ "$MULTICA_TOKEN" = mat_fake_test_only ] || exit 31
[ "$MULTICA_TASK_ID" = task ] || exit 32
[ "$MULTICA_AGENT_ID" = agent ] || exit 33
[ "$MULTICA_WORKSPACE_ID" = workspace ] || exit 34
[ "$MULTICA_DAEMON_PORT" = 19514 ] || exit 35
[ "$MULTICA_TASK_CONFIG_ROOT" = /tmp/task-config ] || exit 36
[ -n "$COPILOT_OTEL_FILE_EXPORTER_PATH" ] || exit 37
exit 0
`))
	result := runCopilotExecuteWithConfig(t, Config{ExecutablePath: p, Logger: slog.Default(), Env: map[string]string{"HOME": home, "MULTICA_TOKEN": "mat_fake_test_only", "MULTICA_TASK_ID": "task", "MULTICA_AGENT_ID": "agent", "MULTICA_WORKSPACE_ID": "workspace", "MULTICA_DAEMON_PORT": "19514", "MULTICA_TASK_CONFIG_ROOT": "/tmp/task-config"}}, ExecOptions{})
	if result.Status != "completed" {
		t.Fatalf("child task context unavailable: status=%s", result.Status)
	}
}
