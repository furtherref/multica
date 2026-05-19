import { describe, it, expect } from "vitest";
import { BaseMentionExtension } from "./mention-extension";

const tokenizer = BaseMentionExtension.config.markdownTokenizer!;

// The tiptap MarkdownTokenizer/renderMarkdown types have broad signatures
// (multi-arg overloads). Our extension always provides single-argument
// implementations, so cast for test convenience.
const startFn = tokenizer.start as (src: string) => number;
const tokenizeFn = tokenizer.tokenize as (
  src: string,
) => { type: string; raw: string; attributes: Record<string, string> } | undefined;
const renderMarkdown = BaseMentionExtension.config.renderMarkdown as (
  node: { attrs: Record<string, string> },
) => string;

function tokenize(src: string) {
  const start = startFn(src);
  if (start === -1) return undefined;
  return tokenizeFn(src.slice(start));
}

describe("mention tokenizer", () => {
  it("parses a plain mention", () => {
    const token = tokenize("[@Alice](mention://member/aaa-bbb)");
    expect(token).toBeDefined();
    expect(token!.attributes.label).toBe("Alice");
    expect(token!.attributes.type).toBe("member");
    expect(token!.attributes.id).toBe("aaa-bbb");
  });

  it("parses a mention with escaped brackets (round-trip from renderMarkdown)", () => {
    // renderMarkdown escapes brackets: David[TF] → David\[TF\]
    const md = renderMarkdown({
      attrs: { id: "aaa-bbb", label: "David[TF]", type: "agent" },
    });
    expect(md).toBe("[@David\\[TF\\]](mention://agent/aaa-bbb)");

    const token = tokenize(md);
    expect(token).toBeDefined();
    expect(token!.attributes.label).toBe("David[TF]");
    expect(token!.attributes.type).toBe("agent");
  });

  it("does not match an ordinary Markdown link before a mention", () => {
    const src =
      "Check [docs](https://example.com) - [@User](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)";

    // start() must NOT land on the [docs] link at index 6
    const start = startFn(src);
    expect(start).toBeGreaterThan(6);

    // tokenize from the correct start position
    const token = tokenizeFn(src.slice(start));
    expect(token).toBeDefined();
    expect(token!.attributes.label).toBe("User");
    expect(token!.attributes.id).toBe("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa");
  });

  it("handles multiple ordinary links before a mention", () => {
    const src =
      "See [a](https://a.com) and [b](https://b.com) - [@Bot](mention://agent/abc-123)";
    const start = startFn(src);
    const token = tokenizeFn(src.slice(start));
    expect(token).toBeDefined();
    expect(token!.attributes.label).toBe("Bot");
  });

  it("round-trips an agent label with nested brackets", () => {
    const md = renderMarkdown({
      attrs: { id: "x-y-z", label: "Bot[v2][beta]", type: "agent" },
    });
    const token = tokenize(md);
    expect(token).toBeDefined();
    expect(token!.attributes.label).toBe("Bot[v2][beta]");
  });

  it("parses issue mentions without @ prefix", () => {
    const token = tokenize("[MUL-123](mention://issue/aaa-bbb)");
    expect(token).toBeDefined();
    expect(token!.attributes.label).toBe("MUL-123");
    expect(token!.attributes.type).toBe("issue");
  });

  it("round-trips a label containing a literal backslash", () => {
    // renderMarkdown must escape the literal backslash so the scanner
    // doesn't eat the next char; tokenizer unescapes after brackets.
    const md = renderMarkdown({
      attrs: { id: "ops-1", label: "Ops\\Bot", type: "agent" },
    });
    expect(md).toBe("[@Ops\\\\Bot](mention://agent/ops-1)");

    const token = tokenize(md);
    expect(token).toBeDefined();
    expect(token!.attributes.label).toBe("Ops\\Bot");
  });

  it("rejects an empty mention label", () => {
    // `[](mention://all/all)` is invisible markdown — the regex's `+`
    // already requires one char, so the start search returns -1.
    expect(startFn("[](mention://all/all)")).toBe(-1);
  });

  it("rejects a bare-@ mention label", () => {
    // `[@](mention://all/all)` matches via regex backtracking (`@?` → empty,
    // label captures `@`). Tokenize must reject it so a re-render doesn't
    // emit `[@@](...)` and slip past the backend's non-empty guard.
    const token = tokenize("[@](mention://all/all)");
    expect(token).toBeUndefined();
  });

  it("round-trips a label with a backslash adjacent to a bracket", () => {
    // `\[` in the name means literal `\` then literal `[` — order of
    // escapes matters (backslash first) so the producer emits
    // `\\\[`, which the consumer unescapes back to `\[`.
    const md = renderMarkdown({
      attrs: { id: "x", label: "foo\\[bar", type: "agent" },
    });
    expect(md).toBe("[@foo\\\\\\[bar](mention://agent/x)");

    const token = tokenize(md);
    expect(token).toBeDefined();
    expect(token!.attributes.label).toBe("foo\\[bar");
  });
});
