package daemon

import (
	"reflect"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
)

func TestTaskUsageEntriesPreservesProviderCostWithoutTokens(t *testing.T) {
	t.Parallel()

	got := taskUsageEntries("copilot", map[string]agent.TokenUsage{
		"gpt-5.6-terra": {CostUSDTicks: 864_300_000_000},
	})
	want := []TaskUsageEntry{{
		Provider:     "copilot",
		Model:        "gpt-5.6-terra",
		CostUSDTicks: 864_300_000_000,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("taskUsageEntries() = %#v, want %#v", got, want)
	}
}

func TestTaskUsageEntriesDropsTrulyEmptyUsage(t *testing.T) {
	t.Parallel()

	got := taskUsageEntries("copilot", map[string]agent.TokenUsage{
		"gpt-5.6-terra": {},
	})
	if len(got) != 0 {
		t.Fatalf("taskUsageEntries() = %#v, want no entries", got)
	}
}
