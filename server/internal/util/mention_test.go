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
			name:    "unmatched link opener does not cross paragraph boundary",
			content: "[\n\n]",
		},
		{
			name:    "unmatched image opener does not cross paragraph boundary",
			content: "![\n\n]\n\n[@Agent](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
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
		{
			name:    "visible agent label without optional at prefix",
			content: "[Agent](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "empty label is ignored",
			content: "[](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "whitespace label is ignored",
			content: "[  ](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "at prefix without a label is ignored",
			content: "[@](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "entity whitespace label is ignored",
			content: "[&#32;](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "HTML only label is ignored",
			content: "[<span></span>](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "script content removed by sanitization is not a visible label",
			content: "[<script>Agent</script>](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "script content cannot turn an at prefix into a visible label",
			content: "[<script>Agent</script>@](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "template content is not a visible label",
			content: "[<template>Agent</template>](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "template content cannot turn an at prefix into a visible label",
			content: "[<template>Agent</template>@](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "uppercase template with attributes is ignored",
			content: `[<TEMPLATE data-label="Agent"><span>Agent</span></TEMPLATE>](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)`,
		},
		{
			name:    "self closing template still hides following label text",
			content: "[<template/>Agent](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "visible label after nested templates still triggers",
			content: "[<template><template>hidden</template></template>Bob](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "visible label before template still triggers",
			content: "[Bob<template>hidden</template>](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "literal template in code label stays visible",
			content: "[`<template>Agent</template>`](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "escaped template label stays visible",
			content: "[&lt;template&gt;Agent&lt;/template&gt;](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "uppercase script with attributes is ignored",
			content: `[<SCRIPT type="text/plain">Agent</SCRIPT>](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)`,
		},
		{
			name:    "self closing script still hides following label text",
			content: "[<script/>Agent](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "nested script opener does not hide the real closing tag",
			content: "[<script>outer<script>inner</script>@](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "formatted script text is still hidden",
			content: "[<script>**Agent**</script>](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "text moved outside the mention by raw HTML does not validate its label",
			content: "[</a>Bob](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "visible span label still triggers",
			content: "[<span>Bob</span>](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "visible label after script still triggers",
			content: "[<script>hidden</script><span>Bob</span>](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "visible label before script still triggers",
			content: "[Bob<script>hidden</script>](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "style text unwrapped by sanitization stays visible",
			content: "[<style>Agent</style>](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "literal script in code label stays visible",
			content: "[`<script>Agent</script>`](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "escaped script label stays visible",
			content: "[&lt;script&gt;Agent&lt;/script&gt;](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "full reference link is ignored",
			content: "[@Agent][target]\n\n[target]: mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		},
		{
			name:    "collapsed reference link is ignored",
			content: "[@Agent][]\n\n[@Agent]: mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		},
		{
			name:    "shortcut reference link is ignored",
			content: "[@Agent]\n\n[@Agent]: mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		},
		{
			name:    "angle bracket destination is ignored",
			content: "[@Agent](<mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa>)",
		},
		{
			name:    "link with title is ignored",
			content: "[@Agent](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa \"title\")",
		},
		{
			name:    "destination whitespace is ignored",
			content: "[@Agent]( mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa )",
		},
		{
			name:    "entity encoded destination is ignored",
			content: "[@Agent](mention&#58;//agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "canonical text inside label cannot validate noncanonical outer link",
			content: "[@Agent `[Example](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)`](<mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa>)",
		},
		{
			name:    "mention in HTML comment label cannot validate outer link",
			content: "[A <!-- [X](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) -->](<mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa>)",
		},
		{
			name:    "mention in image destination cannot validate outer link",
			content: "[A ![x](\"[X](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)\")](<mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa>)",
		},
		{
			name:    "zero width entity label is ignored",
			content: "[&#x200B;](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "named zero width entity label is ignored",
			content: "[&ZeroWidthSpace;](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "invisible format label is ignored",
			content: "[\u200b\ufeff](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "invisible leading text cannot turn at prefix into a label",
			content: "[\u200b@](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) [&#x200B;@](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) [\u3164@](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "Hangul filler label is ignored",
			content: "[\u115f\u1160\u3164\uffa0](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "Hangul filler entities are ignored",
			content: "[&#x115F;&#x1160;&#x3164;&#xFFA0;](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "braille blank label is ignored",
			content: "[\u2800](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "braille blank entity is ignored",
			content: "[&#x2800;](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "visible Unicode label with invisible format still triggers",
			content: "[@\u200b助手](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "visible inline code label still triggers",
			content: "[`Agent`](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "escaped at prefix without label is ignored",
			content: `[@](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) [\@](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)`,
		},
		{
			name:    "mention inside image alt text is ignored",
			content: "![[@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)](https://example.com/image.png)",
		},
		{
			name:    "prose next to image alt mention still triggers",
			content: "![[@B](mention://agent/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb)](https://example.com/image.png) [@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "image is ignored",
			content: "![@Agent](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
		},
		{
			name:    "escaped link opener is ignored",
			content: `\[@Agent](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)`,
		},
		{
			name:    "autolink is ignored",
			content: "<mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa>",
		},
		{
			name:    "ignored reference does not hide later canonical mention",
			content: "[@Agent][target] then [@Agent](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)\n\n[target]: mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "mention inside fenced code block is ignored",
			content: "```\n[@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)\n```",
			want:    nil,
		},
		{
			name:    "mention inside tilde fenced code block is ignored",
			content: "~~~\n[@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)\n~~~",
			want:    nil,
		},
		{
			name:    "mention inside fenced block with info string is ignored",
			content: "```markdown\n[@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)\n```",
			want:    nil,
		},
		{
			name:    "mention inside inline code span is ignored",
			content: "use `[@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)` to mention",
			want:    nil,
		},
		{
			name:    "mention inside double-backtick span containing a backtick is ignored",
			content: "use ``[@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)` `` to mention",
			want:    nil,
		},
		{
			name:    "mention before a fenced block is still returned",
			content: "[@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)\n```\n[@B](mention://agent/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb)\n```",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "mention after a closing fence is still returned",
			content: "```\ncode\n```\n[@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "mention inside blockquote is still returned",
			content: "> [@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "unterminated fence suppresses everything after it",
			content: "```\n[@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)\nand more text",
			want:    nil,
		},
		{
			name:    "longer closing fence still closes and mention after it is returned",
			content: "```\ncode\n````\n[@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "four-space indented fence is not a closer so mention stays inert",
			content: "```\ncode\n    ```\n[@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)\n```",
			want:    nil,
		},
		{
			name:    "mention inside multiline code span is ignored",
			content: "use ``[@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)\nstill code`` to mention",
			want:    nil,
		},
		{
			name:    "fenced code inside a blockquote is ignored",
			content: "> ```\n> [@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)\n> ```",
			want:    nil,
		},
		{
			name:    "prose mention inside a blockquote is still returned next to quoted code",
			content: "> [@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)\n>\n> ```\n> [@B](mention://agent/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb)\n> ```",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "fenced diff inside details is ignored",
			content: "<details>\n<summary>Agent instructions diff</summary>\n\n```diff\n- [@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)\n+ [@B](mention://agent/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb)\n```\n\n</details>",
		},
		{
			name:    "details without blank line stays inert",
			content: "<details>\n<summary>Diff</summary>\n```diff\n+ [@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)\n```\n</details>",
		},
		{
			name:    "prose after details diff still triggers",
			content: "<details>\n<summary>Diff</summary>\n\n```diff\n+ [@B](mention://agent/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb)\n```\n\n</details>\n\n[@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "prose inside details with blank line still triggers",
			content: "<details>\n<summary>Request</summary>\n\n[@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)\n\n</details>",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "fenced diff inside list is ignored",
			content: "- Instructions:\n\n  ```diff\n  + [@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)\n  ```",
		},
		{
			name:    "indented code is ignored",
			content: "    [@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
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
