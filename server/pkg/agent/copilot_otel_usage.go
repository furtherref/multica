package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
)

// Each Execute gets a fresh file. Copilot exports completed request spans while
// it runs, so a killed process can still report earlier calls without shutdown.
// Never reuse the session file: resume history and concurrent sessions would
// otherwise be indistinguishable from this invocation's requests.
// This is an exit-time fallback, not a durable daemon restart journal: requests
// that have not been exported yet and a killed daemon cannot be recovered here.
func prepareCopilotOTelUsage(env []string) ([]string, string, error) {
	if copilotOTelSkipReason(env) != "" {
		return env, "", nil
	}
	f, err := os.CreateTemp("", "multica-copilot-usage-*.jsonl")
	if err != nil {
		return env, "", err
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return env, "", err
	}
	return mergeEnv(env, map[string]string{
		"COPILOT_OTEL_ENABLED":                               "true",
		"COPILOT_OTEL_EXPORTER_TYPE":                         "file",
		"COPILOT_OTEL_FILE_EXPORTER_PATH":                    path,
		"OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT": "false",
	}), path, nil
}

// A configured skip is expected, not an exporter failure. Only include the
// setting name in diagnostics: endpoint values may contain credentials.
func copilotOTelSkipReason(env []string) string {
	// Respect an explicitly configured exporter rather than diverting telemetry
	// away from an operator's collector. Existing stdout/session recovery remains.
	for _, key := range []string{"COPILOT_OTEL_FILE_EXPORTER_PATH", "COPILOT_OTEL_EXPORTER_TYPE", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"} {
		if envValue(env, key) != "" {
			return "existing telemetry configuration: " + key
		}
	}
	if envValue(env, "COPILOT_OTEL_ENABLED") == "false" {
		return "Copilot telemetry explicitly disabled"
	}
	return ""
}

// Only request spans are additive. invoke_agent and metrics contain overlapping
// totals. nano_aiu is 1e-9 AI credits and 1 credit is $0.01: ten nano_aiu equal
// one of our 1e-10 USD ticks. github.copilot.cost is a premium request multiplier
// in CLI 1.0.82, not a dollar amount, and must never be used as currency.
func readCopilotOTelUsage(path string) (map[string]TokenUsage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	usage := map[string]TokenUsage{}
	seen := map[string]bool{}
	unpriced := map[string]bool{}
	nanoByModel := map[string]int64{}
	inconsistentCacheSpans := 0
	// Bounded scan, not a tail: dropping the beginning would lose early calls.
	const maxBytes = 128 * 1024 * 1024
	limited := &io.LimitedReader{R: f, N: maxBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		var span struct {
			Type       string                     `json:"type"`
			TraceID    string                     `json:"traceId"`
			SpanID     string                     `json:"spanId"`
			Attributes map[string]json.RawMessage `json:"attributes"`
		}
		// A process may die halfway through its last line. Keep completed spans.
		if json.Unmarshal(scanner.Bytes(), &span) != nil || span.Type != "span" {
			continue
		}
		str := func(key string) string { var s string; _ = json.Unmarshal(span.Attributes[key], &s); return s }
		if str("gen_ai.operation.name") != "chat" || span.TraceID == "" || span.SpanID == "" {
			continue
		}
		model := str("gen_ai.response.model")
		if model == "" {
			model = str("gen_ai.request.model")
		}
		if model == "" {
			continue
		}
		num := func(keys ...string) (int64, bool) {
			for _, key := range keys {
				if raw, ok := span.Attributes[key]; ok {
					var n float64
					if json.Unmarshal(raw, &n) != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1<<53 || math.Trunc(n) != n {
						return 0, false
					}
					return int64(n), true
				}
			}
			return 0, false
		}
		input, hasInput := num("gen_ai.usage.input_tokens")
		output, hasOutput := num("gen_ai.usage.output_tokens")
		if !hasInput || !hasOutput {
			continue
		}
		read, _ := num("gen_ai.usage.cache_read.input_tokens", "gen_ai.usage.cache_read_input_tokens")
		write, _ := num("gen_ai.usage.cache_write.input_tokens", "gen_ai.usage.cache_creation.input_tokens", "gen_ai.usage.cache_creation_input_tokens")
		key := span.TraceID + ":" + span.SpanID
		if seen[key] {
			continue
		}
		seen[key] = true
		if read+write > input {
			// Preserve the independently measured output, caches and provider
			// amount. addUsage clamps only the uncached input remainder to zero.
			inconsistentCacheSpans++
		}
		addUsage(usage, model, input, output, read, write)
		nano, hasCost := num("github.copilot.nano_aiu")
		if !hasCost || nano > math.MaxInt64-nanoByModel[model] {
			unpriced[model] = true
		} else {
			nanoByModel[model] += nano
		}
	}
	for model, u := range usage {
		// A per-model row cannot represent a partially provider-priced token split.
		// Estimate all its tokens if even one request has no authoritative amount.
		if !unpriced[model] {
			n := nanoByModel[model]
			u.CostUSDTicks = n / 10
			if n%10 >= 5 {
				u.CostUSDTicks++
			}
			usage[model] = u
		}
	}
	if err := scanner.Err(); err != nil {
		return usage, fmt.Errorf("read Copilot request telemetry: %w", err)
	}
	if limited.N == 0 {
		return usage, fmt.Errorf("Copilot request telemetry exceeded %d bytes", maxBytes)
	}
	if inconsistentCacheSpans > 0 {
		return usage, fmt.Errorf("Copilot request telemetry: %d spans have cache tokens exceeding input; uncached input clamped to zero", inconsistentCacheSpans)
	}
	return usage, nil
}
