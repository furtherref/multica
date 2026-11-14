package util

import (
	"regexp"
	"strings"
)

// Mention represents a parsed @mention from markdown content.
type Mention struct {
	Type string // "member", "agent", "issue", or "all"
	ID   string // user_id, agent_id, issue_id, or "all"
	// Label is the visible text inside the markdown `[ ]` brackets, with
	// the optional leading `@` stripped. Routing always uses ID, but the
	// label is retained for dispatch-time observability — comparing it
	// against the resolved entity's canonical name surfaces label/UUID
	// mismatch (the failure mode that motivated mention.CanonicalizeMentions).
	Label string
}

// MentionRe matches [@Label](mention://type/id) or [Label](mention://issue/id) in markdown.
// The @ prefix is optional to support issue mentions which use [MUL-123](mention://issue/...).
// Uses .+? (non-greedy) instead of [^\]]* so labels containing square brackets
// (e.g. "David[TF]") are matched correctly. Use FindMentionMatches for
// scanning arbitrary Markdown content; this regex is kept for exact single
// mention parsing in CLI input normalization.
var MentionRe = regexp.MustCompile(`\[@?(.+?)\]\(mention://(member|agent|squad|issue|all)/([0-9a-fA-F-]+|all)\)`)

// MentionMatch represents one parsed mention with byte offsets into the
// original markdown content.
type MentionMatch struct {
	Start      int
	End        int
	LabelStart int
	LabelEnd   int
	Mention
}

// IsMentionAll returns true if the mention is an @all mention.
func (m Mention) IsMentionAll() bool {
	return m.Type == "all"
}

// FindMentionMatches extracts mention links from Markdown without crossing
// ordinary Markdown links. Regex alone is too blunt here: a line like
// `[docs](https://x) and [@Bot](mention://agent/...)` must not be treated as
// one giant mention whose label starts at `docs`.
func FindMentionMatches(content string) []MentionMatch {
	var result []MentionMatch
	for start := 0; start < len(content); start++ {
		if content[start] != '[' {
			continue
		}
		match, ok := scanMentionAt(content, start)
		if !ok {
			continue
		}
		result = append(result, match)
		start = match.End - 1
	}
	return result
}

func scanMentionAt(content string, start int) (MentionMatch, bool) {
	// depth tracks unescaped `[` opened AFTER the leading `[` at start, so
	// labels with balanced inner brackets like "David[TF]" or non-mention
	// links inside a label still parse. A `]` at depth > 0 is treated as
	// closing an inner `[` and the scan continues. A `]` at depth 0 that is
	// not the mention terminator means this `[` opened plain bracketed text
	// (e.g. "[note]") or a non-mention link, NOT a mention label — bail so
	// the outer loop can advance and look for the next candidate.
	//
	// Names that contain a raw, unpaired `[` or `]` must be escaped by the
	// producer (squad_briefing.formatMention, the frontend mention-extension,
	// canonicalize) so the resulting markdown round-trips through this
	// scanner. Trying to recognise unescaped unpaired brackets in the parser
	// is ambiguous with bracketed text preceding a real mention and leads to
	// data loss in CanonicalizeMentions — keep the parser strict.
	depth := 0
	for close := start + 1; close < len(content); close++ {
		switch content[close] {
		case '\n':
			return MentionMatch{}, false
		case '\\':
			if close+1 < len(content) {
				close++
			}
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
				continue
			}
			if close+1 >= len(content) || content[close+1] != '(' {
				return MentionMatch{}, false
			}
			if !strings.HasPrefix(content[close+2:], "mention://") {
				return MentionMatch{}, false
			}
			return parseMentionAt(content, start, close)
		}
	}
	return MentionMatch{}, false
}

func parseMentionAt(content string, start, close int) (MentionMatch, bool) {
	targetStart := close + len("](mention://")
	targetEnd := targetStart
	for targetEnd < len(content) && content[targetEnd] != ')' && content[targetEnd] != '\n' {
		targetEnd++
	}
	if targetEnd >= len(content) || content[targetEnd] != ')' {
		return MentionMatch{}, false
	}
	target := content[targetStart:targetEnd]
	mentionType, id, ok := strings.Cut(target, "/")
	if !ok || !isMentionType(mentionType) || !isMentionID(id) {
		return MentionMatch{}, false
	}

	labelStart := start + 1
	if labelStart < close && content[labelStart] == '@' {
		labelStart++
	}
	// Reject empty labels (`[](...)` or `[@](...)`). The previous regex
	// required `.+?`; without this guard an invisible markdown link would
	// route as e.g. `@all` and silently broadcast.
	if labelStart >= close {
		return MentionMatch{}, false
	}
	return MentionMatch{
		Start:      start,
		End:        targetEnd + 1,
		LabelStart: labelStart,
		LabelEnd:   close,
		Mention: Mention{
			Type:  mentionType,
			ID:    id,
			Label: content[labelStart:close],
		},
	}, true
}

func isMentionType(mentionType string) bool {
	switch mentionType {
	case "member", "agent", "squad", "issue", "all":
		return true
	default:
		return false
	}
}

func isMentionID(id string) bool {
	if id == "all" {
		return true
	}
	if id == "" {
		return false
	}
	for _, r := range id {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') || r == '-' {
			continue
		}
		return false
	}
	return true
}

// ParseMentions extracts deduplicated mentions from markdown content.
func ParseMentions(content string) []Mention {
	seen := make(map[string]bool)
	var result []Mention
	for _, m := range FindMentionMatches(content) {
		key := m.Type + ":" + m.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, m.Mention)
	}
	return result
}

// HasMentionAll returns true if any mention in the slice is an @all mention.
func HasMentionAll(mentions []Mention) bool {
	for _, m := range mentions {
		if m.IsMentionAll() {
			return true
		}
	}
	return false
}

// EscapeMentionLabel escapes `\`, `[` and `]` in a name so the resulting
// `[@<name>](mention://...)` markdown round-trips through FindMentionMatches
// and CanonicalizeMentions. Every backend producer of mention markdown
// (squad_briefing.formatMention, mention.CanonicalizeMentions on rewrite)
// must funnel labels through this before splicing into a markdown link.
//
// `\` is escaped FIRST because the subsequent `[`/`]` replacements introduce
// new backslashes. Without the leading step, a name like `foo\[bar` would
// emit `foo\\[bar` — the scanner would consume the first `\` as escaping the
// second `\`, then see `[` as raw structure and fail to round-trip. Order
// matters here.
func EscapeMentionLabel(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "[", "\\[")
	s = strings.ReplaceAll(s, "]", "\\]")
	return s
}

// UnescapeMentionLabel reverses EscapeMentionLabel: turns `\[`, `\]` and `\\`
// back into the literal `[`, `]`, `\`. Apply this when comparing a parsed
// MentionMatch.Label (which still carries the producer's escapes) against an
// in-memory canonical name, or when stripping mention markdown to plain text.
//
// Brackets are unescaped BEFORE the `\\` step — reversing the producer in
// reverse order. If `\\` ran first, it would consume the leading backslash
// of a legitimate `\[` pair, leaving a raw `[` in the output.
func UnescapeMentionLabel(s string) string {
	s = strings.ReplaceAll(s, "\\[", "[")
	s = strings.ReplaceAll(s, "\\]", "]")
	s = strings.ReplaceAll(s, "\\\\", "\\")
	return s
}
