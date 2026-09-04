package pricing

import (
	"testing"
	"time"
)

func TestEstimateCostTicksPricesUncostedTokensOfKnownModel(t *testing.T) {
	// claude-sonnet-5: input 2.00, output 10.00, cache read 0.20, cache write 2.50 per 1M.
	got := EstimateCostTicks("claude-sonnet-5", 0, 1_000_000, 100_000, 500_000, 200_000)
	// 2.00 + 1.00 + 0.10 + 0.50 = 3.60 USD.
	if want := USDToTicks(3.60); got != want {
		t.Fatalf("ticks = %d, want %d", got, want)
	}
}

func TestEstimateCostTicksKeepsProviderCostAndIgnoresUnpricedTokens(t *testing.T) {
	got := EstimateCostTicks("some-unknown-model", 1_500, 1_000_000, 1_000_000, 0, 0)
	if got != 1_500 {
		t.Fatalf("ticks = %d, want the provider cost 1500 only", got)
	}
}

func TestEstimateCostTicksAddsProviderCostToEstimate(t *testing.T) {
	got := EstimateCostTicks("claude-sonnet-5", USDToTicks(1), 1_000_000, 0, 0, 0)
	if want := USDToTicks(3); got != want {
		t.Fatalf("ticks = %d, want %d", got, want)
	}
}

func TestEstimateCostTicksForUsageUsesCopilotProviderAndDate(t *testing.T) {
	got := EstimateCostTicksForUsage(
		"copilot",
		"gpt-5.6-sol",
		time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		0,
		0,
		1_000_000,
		1_000_000,
		1_000_000,
		1_000_000,
	)
	if want := USDToTicks(2 + 10 + 0.2 + 2.5); got != want {
		t.Fatalf("ticks = %d, want %d", got, want)
	}
}

func TestUSDTicksRoundTrip(t *testing.T) {
	if USDToTicks(20) != 200_000_000_000 {
		t.Fatalf("USDToTicks(20) = %d", USDToTicks(20))
	}
	if TicksToUSD(200_000_000_000) != 20 {
		t.Fatalf("TicksToUSD = %v", TicksToUSD(200_000_000_000))
	}
	// Two-decimal amounts must survive float rounding.
	if TicksToUSD(USDToTicks(0.07)) != 0.07 {
		t.Fatalf("0.07 round trip = %v", TicksToUSD(USDToTicks(0.07)))
	}
}
