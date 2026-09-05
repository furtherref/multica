package agent

import (
	"fmt"
	"strings"
)

// Fail before launching a paid agent when setup corrupts daemon task context.
// Standalone callers without a task ID are unchanged. Never log token values.
func validateCopilotTaskEnvironment(expected map[string]string, actual []string) error {
	if expected["MULTICA_TASK_ID"] == "" {
		return nil
	}
	if !strings.HasPrefix(expected["MULTICA_TOKEN"], "mat_") {
		return fmt.Errorf("Copilot task environment requires a task-scoped MULTICA_TOKEN")
	}
	for key, value := range expected {
		if strings.HasPrefix(key, "MULTICA_") && envValue(actual, key) != value {
			return fmt.Errorf("Copilot task environment changed during setup: %s", key)
		}
	}
	return nil
}
