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
	for close := start + 1; close < len(content); close++ {
		switch content[close] {
		case '\n':
			return MentionMatch{}, false
		case '\\':
			if close+1 < len(content) {
				close++
			}
		case ']':
			if close+1 >= len(content) || content[close+1] != '(' {
				continue
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
