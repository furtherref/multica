package util

import (
	"testing"
)

func TestParseMentions(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []Mention
	}{
		{
			name:    "simple agent mention",
			content: "[@Agent](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) please fix",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "agent name with square brackets",
			content: "[@David[TF]](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) please fix",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "agent name with nested brackets",
			content: "[@Bot[v2][beta]](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) help",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "multiple mentions with brackets",
			content: "[@A[1]](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) and [@B[2]](mention://agent/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb)",
			want: []Mention{
				{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"},
				{Type: "agent", ID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"},
			},
		},
		{
			name:    "issue mention without @",
			content: "[MUL-123](mention://issue/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) is related",
			want:    []Mention{{Type: "issue", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "member mention",
			content: "[@Bob](mention://member/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) look",
			want:    []Mention{{Type: "member", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "all mention",
			content: "[@All](mention://all/all) heads up",
			want:    []Mention{{Type: "all", ID: "all"}},
		},
		{
			name:    "deduplicate same mention",
			content: "[@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) and again [@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "no mentions",
			content: "just a plain comment",
			want:    nil,
		},
		{
			name:    "escaped brackets in label",
			content: `[@David\[TF\]](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) hi`,
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			// Regression: a bare bracketed phrase like "[this]" used to make the
			// scanner walk past whitespace and the next `[` and merge the two
			// into one fake match (label "this] out [@Bot"), with End pointing
			// past the real mention. In CanonicalizeMentions that span gets
			// replaced wholesale, silently deleting "[this] out " from the
			// persisted comment.
			name:    "plain bracketed text before mention on same line",
			content: "check [this] out [@Bot](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) please",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			// Empty markdown label `[](mention://all/all)` is invisible. The
			// previous regex required at least one label character (`.+?`);
			// the scanner rewrite let it through and routed it as @all.
			// Reject so an invisible link can't @-mention everyone.
			name:    "empty label is not a mention",
			content: "hi [](mention://all/all) there",
			want:    nil,
		},
		{
			// Regression: an unrelated bracketed phrase like "[draft" before a
			// real mention must not absorb the prefix into the match. Only
			// labels starting with `@` may anchor on the mention terminator
			// while still inside nested brackets — a non-@ outer `[` like
			// `[draft` indicates plain text, not a mention label.
			name:    "non-@ bracketed prefix before agent mention is parsed independently",
			content: "note [draft [@Bob](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) tail",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "bare @ label is not a mention",
			content: "[@](mention://all/all) hi",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMentions(tt.content)
			if len(got) != len(tt.want) {
				t.Fatalf("ParseMentions() returned %d mentions, want %d\ngot:  %+v\nwant: %+v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i].Type != tt.want[i].Type || got[i].ID != tt.want[i].ID {
					t.Errorf("mention[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestParseMentions_PopulatesLabel pins that the visible markdown label
// (the text inside the `[ ]`, without the optional leading @) is preserved
// on the returned Mention. Dispatch-time logging compares this against the
// resolved entity's canonical name to flag label/UUID mismatch.
func TestParseMentions_PopulatesLabel(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantLabel string
	}{
		{
			name:      "agent mention without @",
			content:   "[Bare](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			wantLabel: "Bare",
		},
		{
			name:      "agent mention with @",
			content:   "[@Alice](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			wantLabel: "Alice",
		},
		{
			name:      "ordinary markdown link before mention on same line is not part of label",
			content:   "See [docs](https://x) and [@Bot](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			wantLabel: "Bot",
		},
		{
			name:      "issue mention",
			content:   "[MUL-7](mention://issue/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			wantLabel: "MUL-7",
		},
		{
			name:      "all mention",
			content:   "[@all](mention://all/all)",
			wantLabel: "all",
		},
		{
			// Producer-side escape contract: a name containing a literal `[`
			// must be emitted as `\[` so the markdown round-trips. The label
			// captured here keeps the literal `\` from the escape — callers
			// that compare it against an in-memory name must escape that name
			// the same way (see util.EscapeMentionLabel).
			name:      "label with escaped left bracket round-trips",
			content:   `[@Alice \[QA](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)`,
			wantLabel: `Alice \[QA`,
		},
		{
			// Regression: an unrelated bracketed prefix like "[draft" must
			// NOT absorb itself into the label of the following real mention.
			// Only the label of `[@Bob](...)` should be captured.
			name:      "non-@ bracketed prefix before agent mention does not absorb prefix",
			content:   "note [draft [@Bob](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			wantLabel: "Bob",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMentions(tt.content)
			if len(got) != 1 {
				t.Fatalf("expected 1 mention, got %d: %+v", len(got), got)
			}
			if got[0].Label != tt.wantLabel {
				t.Errorf("Label = %q, want %q", got[0].Label, tt.wantLabel)
			}
		})
	}
}

func TestEscapeMentionLabel(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"plain", "plain"},
		{"David[TF]", `David\[TF\]`},
		{"Alice [QA", `Alice \[QA`},
		{"trailing ]", `trailing \]`},
		{"MUL-117[TF]", `MUL-117\[TF\]`},
		// Literal `\` in the name MUST be escaped first; otherwise the
		// scanner consumes the user's `\` as escaping the `\` we add for
		// `[`, then sees the `[` as raw structure and breaks the round-trip.
		{`foo\bar`, `foo\\bar`},
		{`pre\[post`, `pre\\\[post`},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := EscapeMentionLabel(tt.in); got != tt.want {
				t.Errorf("EscapeMentionLabel(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestUnescapeMentionLabel(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"plain", "plain"},
		{`David\[TF\]`, "David[TF]"},
		{`Alice \[QA`, "Alice [QA"},
		{`foo\\bar`, `foo\bar`},
		{`foo\\\[bar`, `foo\[bar`},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := UnescapeMentionLabel(tt.in); got != tt.want {
				t.Errorf("UnescapeMentionLabel(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestUnescapeMentionLabel_InverseOfEscape pins that escape→unescape is the
// identity function. Any name that goes through the producer-side escape
// and comes back through the consumer-side unescape MUST round-trip.
func TestUnescapeMentionLabel_InverseOfEscape(t *testing.T) {
	names := []string{
		"",
		"plain",
		"David[TF]",
		"Alice [QA",
		"trailing ]",
		`foo\bar`,
		`pre\[post`,
		`\start`,
		`mixed [a]\b`,
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			if got := UnescapeMentionLabel(EscapeMentionLabel(name)); got != name {
				t.Errorf("escape→unescape of %q yielded %q", name, got)
			}
		})
	}
}

// TestEscapeMentionLabel_RoundTrip is the contract test that ties the
// producer (EscapeMentionLabel) to the consumer (FindMentionMatches). Any
// name passing through escape MUST produce markdown that parses back as a
// single mention with the same ID.
func TestEscapeMentionLabel_RoundTrip(t *testing.T) {
	uuid := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	names := []string{
		"plain",
		"David[TF]",
		"Alice [QA",
		"trailing ]",
		`foo\bar`,
		`pre\[post`,
		`\start`,
		`mixed [a]\b`,
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			md := "[@" + EscapeMentionLabel(name) + "](mention://agent/" + uuid + ")"
			matches := FindMentionMatches(md)
			if len(matches) != 1 {
				t.Fatalf("FindMentionMatches(%q) returned %d matches, want 1", md, len(matches))
			}
			if matches[0].ID != uuid {
				t.Errorf("mention ID = %q, want %q", matches[0].ID, uuid)
			}
			if matches[0].Start != 0 || matches[0].End != len(md) {
				t.Errorf("match span = [%d,%d), want [0,%d)", matches[0].Start, matches[0].End, len(md))
			}
		})
	}
}

func TestHasMentionAll(t *testing.T) {
	tests := []struct {
		name     string
		mentions []Mention
		want     bool
	}{
		{"empty", nil, false},
		{"no all", []Mention{{Type: "agent", ID: "x"}}, false},
		{"has all", []Mention{{Type: "all", ID: "all"}}, true},
		{"mixed", []Mention{{Type: "agent", ID: "x"}, {Type: "all", ID: "all"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasMentionAll(tt.mentions); got != tt.want {
				t.Errorf("HasMentionAll() = %v, want %v", got, tt.want)
			}
		})
	}
}
