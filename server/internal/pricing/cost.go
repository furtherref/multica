package pricing

import (
	"math"
	"time"
)

// USDToTicks converts a dollar amount to 1e-10 USD ticks, rounding to the
// nearest tick so two-decimal inputs are exact.
func USDToTicks(usd float64) int64 {
	return int64(math.Round(usd * CostUSDTicksPerUSD))
}

// TicksToUSD is the inverse of USDToTicks.
func TicksToUSD(ticks int64) float64 {
	return float64(ticks) / CostUSDTicksPerUSD
}

// EstimateCostTicks is the single server-side cost formula for budgets:
// the provider-reported cost (authoritative where present) plus the rate-table
// estimate for the tokens the provider did not price. A model without a rate
// row contributes only its provider cost; its uncosted tokens count as zero,
// which is why budget spend is a lower bound.
func EstimateCostTicks(model string, costTicks, uncostedInput, uncostedOutput, uncostedCacheRead, uncostedCacheWrite int64) int64 {
	return EstimateCostTicksForUsage("", model, time.Time{}, 0, costTicks, uncostedInput, uncostedOutput, uncostedCacheRead, uncostedCacheWrite)
}

// EstimateCostTicksForUsage is EstimateCostTicks with the billing provider,
// occurrence time, and optional per-request input size needed by effective-
// dated and long-context provider prices.
func EstimateCostTicksForUsage(provider, model string, occurredAt time.Time, requestInputTokens, costTicks, uncostedInput, uncostedOutput, uncostedCacheRead, uncostedCacheWrite int64) int64 {
	price, ok := PriceForUsage(provider, model, occurredAt, requestInputTokens)
	if !ok {
		return costTicks
	}
	usd := TokenCostUSD(uncostedInput, price.InputPerM) +
		TokenCostUSD(uncostedOutput, price.OutputPerM) +
		TokenCostUSD(uncostedCacheRead, price.CacheReadPerM) +
		TokenCostUSD(uncostedCacheWrite, price.CacheWritePerM)
	return costTicks + USDToTicks(usd)
}
