package daemon

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
)

func TestMergeUsageMixedPricing(t *testing.T) {
	priced := agent.TokenUsage{InputTokens: 100, CostUSDTicks: agent.CostUSDTicksPerUSD}
	unpriced := agent.TokenUsage{InputTokens: 1_000_000}
	for _, tc := range []struct {
		name string
		a, b agent.TokenUsage
		want agent.TokenUsage
	}{
		{"priced then unpriced", priced, unpriced, agent.TokenUsage{InputTokens: 1_000_100}},
		{"unpriced then priced", unpriced, priced, agent.TokenUsage{InputTokens: 1_000_100}},
		{"both priced", priced, priced, agent.TokenUsage{InputTokens: 200, CostUSDTicks: 2 * agent.CostUSDTicksPerUSD}},
		{"both unpriced", unpriced, unpriced, agent.TokenUsage{InputTokens: 2_000_000}},
		{"empty retry", priced, agent.TokenUsage{}, priced},
		{"empty first attempt", agent.TokenUsage{}, priced, priced},
		{"output only", priced, agent.TokenUsage{OutputTokens: 10}, agent.TokenUsage{InputTokens: 100, OutputTokens: 10}},
		{"cache only", priced, agent.TokenUsage{CacheReadTokens: 10, CacheWriteTokens: 20}, agent.TokenUsage{InputTokens: 100, CacheReadTokens: 10, CacheWriteTokens: 20}},
		{"cost only with priced tokens", agent.TokenUsage{CostUSDTicks: 100}, priced, agent.TokenUsage{InputTokens: 100, CostUSDTicks: agent.CostUSDTicksPerUSD + 100}},
		{"cost only with empty retry", agent.TokenUsage{CostUSDTicks: 100}, agent.TokenUsage{}, agent.TokenUsage{CostUSDTicks: 100}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := map[string]agent.TokenUsage{"gpt-5.5": tc.a}
			b := map[string]agent.TokenUsage{"gpt-5.5": tc.b}
			got := mergeUsage(a, b)["gpt-5.5"]
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			if a["gpt-5.5"] != tc.a || b["gpt-5.5"] != tc.b {
				t.Fatal("mutated input usage")
			}
		})
	}
}

func TestCopilotFreshRetryDoesNotHideUnpricedTokens(t *testing.T) {
	first := agent.Result{Status: "failed", Error: "copilot exited with error: exit status 7", Usage: map[string]agent.TokenUsage{"gpt-5.5": {InputTokens: 100, CostUSDTicks: agent.CostUSDTicksPerUSD}}}
	if !shouldRetryWithFreshSession(first, "prior-session", 0, "copilot") {
		t.Fatal("expected fresh-session retry")
	}
	retry := agent.Result{Status: "completed", SessionID: "fresh", Usage: map[string]agent.TokenUsage{"gpt-5.5": {InputTokens: 1_000_000}}}
	result, _ := reconcileFreshRetryResult(first, first.Usage, 0, retry, 0, nil)
	entries := taskUsageEntries("copilot", result.Usage)
	if len(entries) != 1 || entries[0].InputTokens != 1_000_100 || entries[0].CostUSDTicks != 0 {
		t.Fatalf("partial quote still masks retry tokens: %+v", entries)
	}
}

func TestMergeUsagePricesStayModelScoped(t *testing.T) {
	got := mergeUsage(map[string]agent.TokenUsage{"priced": {InputTokens: 100, CostUSDTicks: 100}}, map[string]agent.TokenUsage{"unpriced": {InputTokens: 200}})
	if got["priced"].CostUSDTicks != 100 || got["unpriced"].CostUSDTicks != 0 {
		t.Fatalf("cross-model repricing: %+v", got)
	}
}
