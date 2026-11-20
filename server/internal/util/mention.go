package util

import (
	"bytes"
	"html"
	"regexp"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	goldmarkutil "github.com/yuin/goldmark/util"
	nethtml "golang.org/x/net/html"
)

// Mention represents a parsed @mention from markdown content.
type Mention struct {
	Type string // "member", "agent", "squad", "issue", or "all"
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

var canonicalMentionRe = regexp.MustCompile("^" + MentionRe.String() + "$")

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
// Mentions inside fenced code blocks or inline code spans are ignored — they
// document the mention syntax rather than trigger an agent run. Mentions in
// blockquotes are still returned on purpose.
//
// The walk uses goldmark's CommonMark AST and only considers Link nodes, so
// code spans, fenced blocks (including nested-in-blockquote and CommonMark
// fence-length rules), and multiline code spans are excluded without a
// hand-rolled scanner.
// Each link must also use MentionRe's canonical inline source form and have
// visible label text. Reference links and other Markdown link forms are inert.
func ParseMentions(content string) []Mention {
	source := []byte(content)
	links := &mentionLinkParser{InlineParser: parser.NewLinkParser(), spans: make(map[*ast.Link]text.Segment)}
	inlineParsers := parser.DefaultInlineParsers()
	for i := range inlineParsers {
		if inlineParsers[i].Value == links.InlineParser {
			inlineParsers[i].Value = links
		}
	}
	markdown := parser.NewParser(
		parser.WithBlockParsers(parser.DefaultBlockParsers()...),
		parser.WithInlineParsers(inlineParsers...),
		parser.WithParagraphTransformers(parser.DefaultParagraphTransformers()...),
	)
	doc := markdown.Parse(text.NewReader(source))
	seen := make(map[string]bool)
	var result []Mention
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if _, ok := n.(*ast.Image); ok {
			return ast.WalkSkipChildren, nil
		}
		link, ok := n.(*ast.Link)
		if !ok {
			return ast.WalkContinue, nil
		}
		mention, ok := parseMentionLink(link, links.spans[link], source)
		if !ok {
			return ast.WalkContinue, nil
		}
		key := mention.Type + ":" + mention.ID
		if seen[key] {
			return ast.WalkContinue, nil
		}
		seen[key] = true
		result = append(result, mention)
		return ast.WalkContinue, nil
	})
	return result
}

// Goldmark links expose their start, but not their end. Record the span while
// its link parser owns the reader, before inline nodes lose that boundary.
type mentionLinkParser struct {
	parser.InlineParser
	spans map[*ast.Link]text.Segment
}

func (p *mentionLinkParser) Parse(parent ast.Node, reader text.Reader, context parser.Context) ast.Node {
	n := p.InlineParser.Parse(parent, reader, context)
	if link, ok := n.(*ast.Link); ok {
		_, end := reader.Position()
		p.spans[link] = text.NewSegment(link.Pos(), end.Start)
	}
	return n
}

func (p *mentionLinkParser) CloseBlock(parent ast.Node, reader text.Reader, context parser.Context) {
	// Preserve the link parser's cleanup of unmatched openers between blocks.
	p.InlineParser.(parser.CloseBlocker).CloseBlock(parent, reader, context)
}

func parseMentionLink(link *ast.Link, span text.Segment, source []byte) (Mention, bool) {
	if link.Reference != nil || len(link.Title) != 0 {
		return Mention{}, false
	}
	raw := span.Value(source)
	m := canonicalMentionRe.FindSubmatchIndex(raw)
	if m == nil {
		return Mention{}, false
	}
	mention := Mention{Type: string(raw[m[4]:m[5]]), ID: string(raw[m[6]:m[7]])}
	if string(link.Destination) != "mention://"+mention.Type+"/"+mention.ID {
		return Mention{}, false
	}
	// Fork: keep the producer's visible label for dispatch-time observability.
	mention.Label = string(raw[m[2]:m[3]])

	var label strings.Builder
	hasRawHTML := false
	_ = ast.Walk(link, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if _, ok := n.(*ast.Image); ok {
				return ast.WalkSkipChildren, nil
			}
			if _, ok := n.(*ast.RawHTML); ok {
				hasRawHTML = true
			}
			if node, ok := n.(*ast.Text); ok {
				value := node.Segment.Value(source)
				if !node.IsRaw() {
					value = goldmarkutil.UnescapePunctuations(value)
					label.WriteString(html.UnescapeString(string(value)))
				} else {
					label.Write(value)
				}
			}
		}
		return ast.WalkContinue, nil
	})
	labelText := label.String()
	if hasRawHTML {
		labelText = renderedMentionLabelText(link, source)
	}
	firstVisible := true
	for _, r := range labelText {
		if unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) || r == '\u2800' {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			if firstVisible && r == '@' {
				firstVisible = false
				continue
			}
			return mention, true
		}
	}
	return Mention{}, false
}

// Raw HTML changes which text survives the frontend's rehype-raw/sanitize
// pipeline. Parse the rendered fragment so script raw-text rules (including
// case, attributes, and self-closing syntax) match HTML rather than Markdown.
// This HTML is only inspected in memory; it is never served or executed.
func renderedMentionLabelText(link *ast.Link, source []byte) string {
	var rendered bytes.Buffer
	markdown := goldmark.New(goldmark.WithRendererOptions(goldmarkhtml.WithUnsafe()))
	if err := markdown.Renderer().Render(&rendered, source, link); err != nil {
		return ""
	}
	doc, err := nethtml.Parse(&rendered)
	if err != nil {
		return ""
	}
	var label strings.Builder
	var collect func(*nethtml.Node)
	collect = func(n *nethtml.Node) {
		// markdownSanitizeSchema strips scripts. parse5 stores template
		// contents in a separate fragment, so sanitization also drops them
		// when unwrapping the template. x/net/html keeps them as children.
		// Other disallowed tags, such as style, are unwrapped, keeping text.
		if n.Type == nethtml.ElementNode && (n.Data == "script" || n.Data == "template") {
			return
		}
		if n.Type == nethtml.TextNode {
			label.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	// Only the generated anchor is the mention label. Raw closing tags can
	// move text outside it; that text must not validate an empty mention.
	for node := range doc.Descendants() {
		if node.Type == nethtml.ElementNode && node.Data == "a" {
			collect(node)
			break
		}
	}
	return label.String()
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
