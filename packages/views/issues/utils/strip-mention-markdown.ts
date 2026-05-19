/**
 * Strip mention markdown syntax to plain text.
 *
 * Handles:
 * - Simple mentions: `[@Name](mention://agent/id)` → `@Name`
 * - Escaped brackets in names: `[@David\[TF\]](mention://agent/id)` → `@David[TF]`
 * - Escaped backslash in names: `[@Ops\\Bot](mention://agent/id)` → `@Ops\Bot`
 * - Issue mentions (no @): `[MUL-123](mention://issue/id)` → `MUL-123`
 * - Does NOT touch regular markdown links: `[docs](https://...)` stays unchanged
 * - Does NOT touch backslash-escaped mentions: `\[@Name](mention://...)` stays unchanged
 *
 * The regex + unescape mirrors the tokenizer in mention-extension.ts and the
 * Go-side util.UnescapeMentionLabel. Brackets are unescaped BEFORE `\\` so
 * a legitimate `\[` pair isn't broken by collapsing the leading `\`.
 */
export function stripMentionMarkdown(text: string): string {
  return text.replace(
    /(?<![\\])\[(@?)((?:\\.|[^\]])+)\]\(mention:\/\/\w+\/[^)]+\)/g,
    (_, prefix: string, rawLabel: string) => {
      const label = rawLabel
        .replace(/\\\[/g, "[")
        .replace(/\\\]/g, "]")
        .replace(/\\\\/g, "\\");
      return `${prefix}${label}`;
    },
  );
}
