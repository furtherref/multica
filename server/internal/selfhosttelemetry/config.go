package selfhosttelemetry

import (
	"log/slog"
	"strings"
)

// Config is read once during API server startup. Endpoint selection is
// deliberately absent: self-host telemetry can only be sent to Multica's
// first-party collector compiled into the client.
type Config struct {
	Enabled bool
	// DoNotTrack records that DO_NOT_TRACK vetoed telemetry, so the startup
	// log names the switch that keeps it off.
	DoNotTrack bool
}

// ConfigFromEnv makes self-host telemetry opt-in; this fork keeps it off
// unless the operator sets optIn (MULTICA_TELEMETRY_ENABLED), where upstream
// defaults it on. The industry-standard DO_NOT_TRACK opt-out still wins when
// both are set. Each switch is on only for 1 or true (case-insensitive, after
// trimming); every other value leaves it off.
func ConfigFromEnv(optIn, doNotTrack string) Config {
	if switchOn(doNotTrack) {
		return Config{DoNotTrack: true}
	}
	return Config{Enabled: switchOn(optIn)}
}

func switchOn(raw string) bool {
	raw = strings.TrimSpace(raw)
	return raw == "1" || strings.EqualFold(raw, "true")
}

// LogStartupStatus makes the telemetry state observable without logging the
// raw environment values or any telemetry data.
func LogStartupStatus(logger *slog.Logger, config Config) {
	switch {
	case config.Enabled:
		logger.Info("self-host telemetry enabled")
	case config.DoNotTrack:
		logger.Info("self-host telemetry disabled via DO_NOT_TRACK")
	default:
		logger.Info("self-host telemetry disabled by default; set MULTICA_TELEMETRY_ENABLED=true to opt in")
	}
}
