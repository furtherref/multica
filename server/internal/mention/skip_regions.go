// Package mention provides server-side canonicalization of mention links in
// markdown content, ensuring a mention's visible label always matches the
// entity its UUID resolves to.
package mention

import (
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5/pgtype"
)

// skipRegion represents a region of text that should not be modified.
type skipRegion struct {
	start, end int
}

// findSkipRegions identifies code regions in the content that should not be
// touched: fenced code blocks and inline code. CanonicalizeMentions uses this
// so mention-like text inside code spans is never rewritten.
func findSkipRegions(content string) []skipRegion {
	var regions []skipRegion

	// Fenced code blocks: ```...```
	fenceRe := regexp.MustCompile("(?m)^```[^`]*\n[\\s\\S]*?\n```")
	for _, loc := range fenceRe.FindAllStringIndex(content, -1) {
		regions = append(regions, skipRegion{loc[0], loc[1]})
	}

	// Inline code: `...` (but not inside fenced blocks — already handled).
	inlineRe := regexp.MustCompile("`[^`\n]+`")
	for _, loc := range inlineRe.FindAllStringIndex(content, -1) {
		regions = append(regions, skipRegion{loc[0], loc[1]})
	}

	return regions
}

// inSkipRegion checks if a position falls within any skip region.
func inSkipRegion(pos int, regions []skipRegion) bool {
	for _, r := range regions {
		if pos >= r.start && pos < r.end {
			return true
		}
	}
	return false
}

// uuidToString renders a pgtype.UUID as its canonical hyphenated string,
// returning "" for an invalid UUID.
func uuidToString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
