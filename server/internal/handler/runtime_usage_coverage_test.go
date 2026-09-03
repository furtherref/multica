package handler

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestListRuntimeUsageCoverageClassifiesCompletedRuns(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID := dbfx.Runtime(t, "coverage-runtime", testutil.Cols{
		"provider": "copilot",
	})
	agentID := dbfx.Agent(t, "coverage-agent", runtimeID)
	completedAt := time.Date(2026, 8, 27, 23, 30, 0, 0, time.UTC)

	addTask := func(status string) string {
		return dbfx.Task(t, agentID, testutil.Cols{
			"runtime_id":   runtimeID,
			"status":       status,
			"started_at":   completedAt.Add(-time.Minute),
			"completed_at": completedAt,
		})
	}
	addUsage := func(taskID string, input, output, cacheRead, cacheWrite int64) {
		dbfx.Insert(t, "task_usage", testutil.Cols{
			"task_id":            taskID,
			"provider":           "copilot",
			"model":              "gpt-5.6-terra",
			"input_tokens":       input,
			"output_tokens":      output,
			"cache_read_tokens":  cacheRead,
			"cache_write_tokens": cacheWrite,
		})
	}

	completeID := addTask("completed")
	addUsage(completeID, 500, 100, 4_000, 200)
	outputOnlyID := addTask("completed")
	addUsage(outputOnlyID, 0, 300, 0, 0)
	addTask("completed")
	failedID := addTask("failed")
	addUsage(failedID, 100, 50, 0, 0)
	cancelledID := addTask("cancelled")
	addUsage(cancelledID, 100, 50, 0, 0)

	rows, err := testHandler.listRuntimeUsageCoverage(
		context.Background(),
		parseUUID(runtimeID),
		"Asia/Shanghai",
		pgtype.Timestamptz{
			Time:  completedAt.Add(-24 * time.Hour),
			Valid: true,
		},
	)
	if err != nil {
		t.Fatalf("list runtime usage coverage: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("coverage rows = %#v, want one day", rows)
	}
	got := rows[0]
	if got.Date != "2026-08-28" {
		t.Fatalf("coverage date = %q, want Asia/Shanghai date 2026-08-28", got.Date)
	}
	if got.CompletedRuns != 3 || got.CompleteRuns != 1 ||
		got.OutputOnlyRuns != 1 || got.MissingRuns != 1 {
		t.Fatalf("coverage = %#v, want completed/complete/output-only/missing 3/1/1/1", got)
	}
}
