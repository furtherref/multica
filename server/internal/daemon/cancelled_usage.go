package daemon

import (
	"log/slog"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// Covers the CLI's graceful stop, forced-kill backstop and telemetry read.
// This is independent of the cancelled execution context. It is not a durable
// recovery journal: a killed daemon or an unresponsive backend can still lose
// unreported usage, which must be visible in the daemon log.
const cancelledAgentResultGrace = 15 * time.Second

func (d *Daemon) waitForCancelledAgentResult(results <-chan agent.Result, logger *slog.Logger) agent.Result {
	grace := d.cancelledResultWait
	if grace <= 0 {
		grace = cancelledAgentResultGrace
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case result, ok := <-results:
		if ok {
			return result
		}
		logger.Warn("agent accounting unavailable after cancellation", "reason", "result channel closed")
	case <-timer.C:
		// A result already delivered at the deadline wins over the timer.
		select {
		case result, ok := <-results:
			if ok {
				return result
			}
		default:
		}
		logger.Warn("agent accounting unavailable after cancellation", "reason", "result wait timed out", "wait", grace)
	}
	return agent.Result{}
}
