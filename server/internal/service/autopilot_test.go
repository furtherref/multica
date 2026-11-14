package service

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestAutopilotErrorType(t *testing.T) {
	cases := map[string]string{
		"unknown execution_mode: nope": "configuration",
		"issue blocked":                "issue_terminal",
		"issue cancelled":              "issue_terminal",
		"enqueue task: no runtime":     "dispatch_error",
		"task failed":                  "task_error",
		"unexpected":                   "autopilot_error",
	}

	for reason, want := range cases {
		if got := autopilotErrorType(reason); got != want {
			t.Fatalf("autopilotErrorType(%q) = %q, want %q", reason, got, want)
		}
	}
}

func TestTaskFailureReasonForAutopilotRun(t *testing.T) {
	cases := []struct {
		name string
		task db.AgentTaskQueue
		want string
	}{
		{
			name: "prefers raw error text",
			task: db.AgentTaskQueue{
				Error:         pgtype.Text{String: "tests failed", Valid: true},
				FailureReason: pgtype.Text{String: "agent_error", Valid: true},
			},
			want: "tests failed",
		},
		{
			name: "falls back to classified reason when error is blank",
			task: db.AgentTaskQueue{
				Error:         pgtype.Text{String: "   ", Valid: true},
				FailureReason: pgtype.Text{String: "codex_semantic_inactivity", Valid: true},
			},
			want: "codex_semantic_inactivity",
		},
		{
			name: "generic default when nothing is set",
			task: db.AgentTaskQueue{},
			want: "task failed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := taskFailureReasonForAutopilotRun(tc.task); got != tc.want {
				t.Fatalf("taskFailureReasonForAutopilotRun() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildIssueDescription_NoTriggerPayload(t *testing.T) {
	s := &AutopilotService{}
	ap := db.Autopilot{Description: pgtype.Text{String: "do the thing", Valid: true}}
	run := db.AutopilotRun{Source: "schedule", TriggeredAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}}

	got := s.buildIssueDescription(ap, run, "UTC")
	if !strings.HasPrefix(got.String, "do the thing") {
		t.Fatalf("description should preserve user description: %q", got.String)
	}
	if !strings.Contains(got.String, "Autopilot run triggered at") {
		t.Fatalf("description should include schedule note: %q", got.String)
	}
	if strings.Contains(got.String, "Webhook event") {
		t.Fatalf("description must not mention webhook for non-webhook source: %q", got.String)
	}
}

func TestBuildIssueDescription_UsesTriggerTimezone(t *testing.T) {
	s := &AutopilotService{}
	ap := db.Autopilot{Description: pgtype.Text{String: "daily sync", Valid: true}}
	run := db.AutopilotRun{
		Source:      "schedule",
		TriggeredAt: pgtype.Timestamptz{Time: time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC), Valid: true},
	}

	got := s.buildIssueDescription(ap, run, "Asia/Tokyo")
	if !strings.Contains(got.String, "Autopilot run triggered at 2026-05-26 09:00 Asia/Tokyo") {
		t.Fatalf("description should use trigger timezone: %q", got.String)
	}
	if strings.Contains(got.String, "2026-05-26 00:00 UTC") {
		t.Fatalf("description must not render the trigger time in UTC when trigger timezone is known: %q", got.String)
	}
}

// An invalid IANA timezone string must fall back to UTC instead of leaving the
// timestamp half-formatted in the issue body.
func TestBuildIssueDescription_InvalidTriggerTimezoneFallsBackToUTC(t *testing.T) {
	s := &AutopilotService{}
	ap := db.Autopilot{Description: pgtype.Text{String: "do the thing", Valid: true}}
	run := db.AutopilotRun{
		Source:      "schedule",
		TriggeredAt: pgtype.Timestamptz{Time: time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC), Valid: true},
	}

	got := s.buildIssueDescription(ap, run, "Foo/Bar")
	if !strings.Contains(got.String, "Autopilot run triggered at 2026-05-26 00:00 UTC") {
		t.Fatalf("invalid trigger timezone should fall back to UTC: %q", got.String)
	}
}

func TestInterpolateTemplate_InvalidTriggerTimezoneFallsBackToUTC(t *testing.T) {
	s := &AutopilotService{}
	ap := db.Autopilot{
		Title:              "fallback",
		IssueTitleTemplate: pgtype.Text{String: "report {{date}}", Valid: true},
	}
	run := db.AutopilotRun{
		TriggeredAt: pgtype.Timestamptz{Time: time.Date(2026, 5, 26, 23, 30, 0, 0, time.UTC), Valid: true},
	}

	got := s.interpolateTemplate(ap, run, "Foo/Bar")
	if want := "report 2026-05-26"; got != want {
		t.Fatalf("interpolateTemplate = %q, want %q", got, want)
	}
}

func TestBuildIssueDescription_WithWebhookPayload(t *testing.T) {
	s := &AutopilotService{}
	ap := db.Autopilot{Description: pgtype.Text{String: "watch PRs", Valid: true}}
	payload := []byte(`{"event":"github.pull_request.opened","eventPayload":{"number":7},"request":{"receivedAt":"2026-05-09T00:00:00Z","contentType":"application/json"}}`)
	run := db.AutopilotRun{Source: "webhook", TriggerPayload: payload, TriggeredAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}}

	got := s.buildIssueDescription(ap, run, "UTC")
	if !strings.HasPrefix(got.String, "watch PRs") {
		t.Fatalf("user description not preserved: %q", got.String)
	}
	if !strings.Contains(got.String, "Webhook event: github.pull_request.opened") {
		t.Fatalf("description should include webhook event line: %q", got.String)
	}
	if !strings.Contains(got.String, "\"number\": 7") && !strings.Contains(got.String, "\"number\":7") {
		t.Fatalf("description should include payload json: %q", got.String)
	}
	// Italic schedule line must come before the webhook block.
	idxItalic := strings.Index(got.String, "*Autopilot run triggered")
	idxWebhook := strings.Index(got.String, "Webhook event")
	if idxItalic < 0 || idxWebhook < 0 || idxItalic > idxWebhook {
		t.Fatalf("italic line should appear before webhook block: %q", got.String)
	}
}

func TestBuildIssueDescription_WebhookSourceMissingEnvelope(t *testing.T) {
	// Defensive: if a future caller stuffs a non-envelope JSON object into
	// trigger_payload, we should still emit a webhook block with sensible
	// defaults rather than skipping the section entirely.
	s := &AutopilotService{}
	ap := db.Autopilot{Description: pgtype.Text{String: "thing", Valid: true}}
	payload := []byte(`{"raw":"missing envelope"}`)
	run := db.AutopilotRun{Source: "webhook", TriggerPayload: payload, TriggeredAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}}

	got := s.buildIssueDescription(ap, run, "UTC")
	if !strings.Contains(got.String, "Webhook event:") {
		t.Fatalf("should still emit webhook block: %q", got.String)
	}
}

func TestBuildIssueDescription_NonWebhookSourceWithPayloadIgnored(t *testing.T) {
	// Manual / schedule with a payload should not get a webhook block.
	s := &AutopilotService{}
	ap := db.Autopilot{Description: pgtype.Text{String: "thing", Valid: true}}
	run := db.AutopilotRun{Source: "manual", TriggerPayload: []byte(`{"event":"x.y"}`), TriggeredAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}}

	got := s.buildIssueDescription(ap, run, "UTC")
	if strings.Contains(got.String, "Webhook event") {
		t.Fatalf("non-webhook source should not include webhook block: %q", got.String)
	}
}

// TestInterpolateTemplate covers the three behaviours that real autopilot
// runs depend on: {{date}} substitution, falling back to Title when the
// template is unset/empty, and leaving any non-{{date}} text alone (the
// handler is the layer that prevents unknown tokens from being stored in
// the first place — service-layer interpolation stays substitute-or-leave).
func TestInterpolateTemplate(t *testing.T) {
	s := &AutopilotService{}
	run := db.AutopilotRun{TriggeredAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}}
	today := run.TriggeredAt.Time.UTC().Format("2006-01-02")

	cases := []struct {
		name   string
		ap     db.Autopilot
		expect string
	}{
		{
			name:   "date placeholder substituted",
			ap:     db.Autopilot{Title: "fallback", IssueTitleTemplate: pgtype.Text{String: "probe — {{date}}", Valid: true}},
			expect: "probe — " + today,
		},
		{
			name:   "date placeholder with whitespace substituted",
			ap:     db.Autopilot{Title: "fallback", IssueTitleTemplate: pgtype.Text{String: "probe — {{ date }}", Valid: true}},
			expect: "probe — " + today,
		},
		{
			name:   "empty template falls back to autopilot title",
			ap:     db.Autopilot{Title: "fallback title", IssueTitleTemplate: pgtype.Text{Valid: false}},
			expect: "fallback title",
		},
		{
			name:   "template without placeholder is returned verbatim",
			ap:     db.Autopilot{Title: "fallback", IssueTitleTemplate: pgtype.Text{String: "static title", Valid: true}},
			expect: "static title",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.interpolateTemplate(tc.ap, run, "UTC"); got != tc.expect {
				t.Fatalf("interpolateTemplate = %q, want %q", got, tc.expect)
			}
		})
	}
}

func TestInterpolateTemplate_UsesTriggerTimezoneForDate(t *testing.T) {
	s := &AutopilotService{}
	ap := db.Autopilot{
		Title:              "fallback",
		IssueTitleTemplate: pgtype.Text{String: "Tokyo report {{date}}", Valid: true},
	}
	run := db.AutopilotRun{
		TriggeredAt: pgtype.Timestamptz{Time: time.Date(2026, 5, 26, 23, 30, 0, 0, time.UTC), Valid: true},
	}

	got := s.interpolateTemplate(ap, run, "Asia/Tokyo")
	if want := "Tokyo report 2026-05-27"; got != want {
		t.Fatalf("interpolateTemplate = %q, want %q", got, want)
	}
}

// TestValidateIssueTitleTemplate locks down what create/update accept.
// Reject path: anything inside {{...}} that is not in the supported set.
// Accept path: empty, plain text, and the canonical {{date}} placeholder
// in both compact and whitespace-padded forms.
func TestValidateIssueTitleTemplate(t *testing.T) {
	t.Run("accepts empty template", func(t *testing.T) {
		if err := ValidateIssueTitleTemplate(""); err != nil {
			t.Fatalf("empty template must be valid: %v", err)
		}
	})
	t.Run("accepts plain text", func(t *testing.T) {
		if err := ValidateIssueTitleTemplate("daily report"); err != nil {
			t.Fatalf("plain text must be valid: %v", err)
		}
	})
	t.Run("accepts {{date}}", func(t *testing.T) {
		if err := ValidateIssueTitleTemplate("probe — {{date}}"); err != nil {
			t.Fatalf("{{date}} must be valid: %v", err)
		}
	})
	t.Run("accepts {{ date }} with whitespace", func(t *testing.T) {
		if err := ValidateIssueTitleTemplate("probe — {{ date }}"); err != nil {
			t.Fatalf("{{ date }} must be valid: %v", err)
		}
	})

	rejections := []struct {
		name string
		tmpl string
		// nameInError is the offending variable name that must appear in the
		// returned error so CLI users see which token was rejected.
		nameInError string
	}{
		{"go template style", "probe — {{.TriggeredAt}}", ".TriggeredAt"},
		{"mustache style unknown variable", "probe — {{trigger_id}}", "trigger_id"},
		{"datetime not yet supported", "probe — {{datetime}}", "datetime"},
		{"empty placeholder", "probe — {{}}", ""},
		{"mixed valid + invalid still fails", "probe — {{date}} {{trigger_source}}", "trigger_source"},
	}
	for _, tc := range rejections {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateIssueTitleTemplate(tc.tmpl)
			if err == nil {
				t.Fatalf("expected rejection for %q", tc.tmpl)
			}
			if !strings.Contains(err.Error(), "unknown template variable") {
				t.Fatalf("error should mention unknown template variable: %v", err)
			}
			if tc.nameInError != "" && !strings.Contains(err.Error(), tc.nameInError) {
				t.Fatalf("error should name the offending token %q: %v", tc.nameInError, err)
			}
		})
	}
}

// TestAutopilotLocalDayWindow pins the "same day" boundary to the trigger's
// timezone rather than UTC: a 20:00 Asia/Shanghai firing is still the same
// local day as an earlier 08:00 one, even though UTC has already rolled over.
func TestAutopilotLocalDayWindow(t *testing.T) {
	cases := []struct {
		name      string
		triggered time.Time
		// planned is the run's canonical occurrence; zero means "not a
		// scheduled occurrence" and exercises the wall-clock fallback.
		planned   time.Time
		timezone  string
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			name:      "shanghai evening stays on the local day",
			triggered: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC), // 20:00 +08
			timezone:  "Asia/Shanghai",
			wantStart: time.Date(2026, 11, 4, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60)),
			wantEnd:   time.Date(2026, 11, 5, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60)),
		},
		{
			name:      "utc afternoon is the same utc day",
			triggered: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC),
			timezone:  "UTC",
			wantStart: time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "utc late evening already belongs to the next shanghai day",
			triggered: time.Date(2026, 11, 4, 17, 0, 0, 0, time.UTC), // 01:00 +08 next day
			timezone:  "Asia/Shanghai",
			wantStart: time.Date(2026, 11, 5, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60)),
			wantEnd:   time.Date(2026, 11, 6, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60)),
		},
		{
			name:      "unknown timezone falls back to utc",
			triggered: time.Date(2026, 11, 4, 22, 0, 0, 0, time.UTC),
			timezone:  "Not/AZone",
			wantStart: time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC),
		},
		{
			// A scheduled slot that fires late (or is replayed after an
			// outage) belongs to the day of the SLOT, not the day of the
			// dispatch. planned_at wins over the wall clock.
			name:      "planned occurrence wins over dispatch time",
			triggered: time.Date(2026, 11, 4, 9, 30, 0, 0, time.UTC),
			planned:   time.Date(2026, 11, 3, 23, 0, 0, 0, time.UTC), // 07:00 +08 on the 4th
			timezone:  "Asia/Shanghai",
			wantStart: time.Date(2026, 11, 4, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60)),
			wantEnd:   time.Date(2026, 11, 5, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60)),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := db.AutopilotRun{TriggeredAt: pgtype.Timestamptz{Time: tc.triggered, Valid: true}}
			if !tc.planned.IsZero() {
				run.PlannedAt = pgtype.Timestamptz{Time: tc.planned, Valid: true}
			}
			start, end := autopilotLocalDayWindow(run, tc.timezone)
			if !start.Equal(tc.wantStart) {
				t.Fatalf("day start = %s, want %s", start, tc.wantStart)
			}
			if !end.Equal(tc.wantEnd) {
				t.Fatalf("day end = %s, want %s", end, tc.wantEnd)
			}
			// The window must always be exactly one local day, so a DST
			// transition day neither leaks nor double-counts an hour.
			if got, want := end.Sub(start), 24*time.Hour; got != want {
				t.Fatalf("window length = %s, want %s", got, want)
			}
		})
	}
}

// TestValidateIssueDedupPolicy locks the accepted set and, importantly, that an
// omitted policy normalizes to the historic "off" behaviour so no existing
// autopilot silently changes dedup semantics.
func TestValidateIssueDedupPolicy(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	cases := []struct {
		name   string
		raw    *string
		want   string
		wantOK bool
	}{
		{name: "omitted is off", raw: nil, want: "off", wantOK: true},
		{name: "empty string is off", raw: strPtr(""), want: "off", wantOK: true},
		{name: "explicit off", raw: strPtr("off"), want: "off", wantOK: true},
		{name: "per_day", raw: strPtr("per_day"), want: "per_day", wantOK: true},
		{name: "whitespace tolerated", raw: strPtr("  per_day  "), want: "per_day", wantOK: true},
		{name: "typo rejected", raw: strPtr("perday"), want: "off", wantOK: false},
		{name: "wrong case rejected", raw: strPtr("PER_DAY"), want: "off", wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ValidateIssueDedupPolicy(tc.raw)
			if ok != tc.wantOK {
				t.Fatalf("ValidateIssueDedupPolicy(%v) ok = %v, want %v", tc.raw, ok, tc.wantOK)
			}
			if string(got) != tc.want {
				t.Fatalf("ValidateIssueDedupPolicy(%v) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
