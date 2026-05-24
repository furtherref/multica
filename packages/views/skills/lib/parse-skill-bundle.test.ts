import { describe, expect, it } from "vitest";
import { zipSync, strToU8 } from "fflate";
import { parseZipBundles, parseFolderBundle, ParseError, sanitizeExportPath, sanitizeExportName, collectExportFiles, buildExportContent, updateFrontmatter } from "./parse-skill-bundle";

function makeZip(files: Record<string, string>): ArrayBuffer {
  const data: Record<string, Uint8Array> = {};
  for (const [path, content] of Object.entries(files)) {
    data[path] = strToU8(content);
  }
  return zipSync(data).buffer;
}

function makeZipRaw(files: Record<string, Uint8Array>): ArrayBuffer {
  return zipSync(files).buffer;
}

const FRONTMATTER = `---
name: my-skill
description: A test skill
---
# Content`;

describe("parseZipBundles", () => {
  it("throws when no SKILL.md is found", () => {
    const buf = makeZip({ "readme.md": "hello" });
    expect(() => parseZipBundles(buf, "fallback")).toThrow(ParseError);
  });

  it("parses a single skill in a folder", () => {
    const buf = makeZip({
      "my-skill/SKILL.md": FRONTMATTER,
      "my-skill/helpers.md": "helper content",
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles).toHaveLength(1);
    expect(bundles[0]!.name).toBe("my-skill");
    expect(bundles[0]!.files).toHaveLength(1);
    expect(bundles[0]!.files[0]!.path).toBe("helpers.md");
  });

  it("parses multiple skill folders", () => {
    const buf = makeZip({
      "skill-a/SKILL.md": "---\nname: skill-a\n---\n# A",
      "skill-b/SKILL.md": "---\nname: skill-b\n---\n# B",
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles).toHaveLength(2);
    const names = bundles.map((b) => b.name).sort();
    expect(names).toEqual(["skill-a", "skill-b"]);
  });

  it("ignores nested SKILL.md under an already-detected root", () => {
    const buf = makeZip({
      "skill-a/SKILL.md": "---\nname: skill-a\n---\n# Root",
      "skill-a/templates/SKILL.md": "---\nname: nested\n---\n# Nested",
      "skill-a/helpers.md": "helper",
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles).toHaveLength(1);
    expect(bundles[0]!.name).toBe("skill-a");
    expect(bundles[0]!.files.map((f) => f.path)).toEqual(["helpers.md"]);
  });

  it("treats root-level SKILL.md as parent of nested SKILL.md files", () => {
    const buf = makeZip({
      "SKILL.md": "---\nname: root-skill\n---\n# Root",
      "templates/SKILL.md": "---\nname: nested-template\n---\n# Nested",
      "helpers.md": "helper content",
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles).toHaveLength(1);
    expect(bundles[0]!.name).toBe("root-skill");
    expect(bundles[0]!.files.map((f) => f.path)).toEqual(["helpers.md"]);
  });

  it("excludes deep nested SKILL.md subtree from parent bundle", () => {
    const buf = makeZip({
      "skill-a/SKILL.md": "---\nname: skill-a\n---\n# A",
      "skill-a/helpers.md": "ok",
      "skill-a/templates/foo/SKILL.md": "---\nname: deep\n---\n# Deep",
      "skill-a/templates/foo/bar.md": "deep file",
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles).toHaveLength(1);
    expect(bundles[0]!.name).toBe("skill-a");
    const paths = bundles[0]!.files.map((f) => f.path);
    expect(paths).toContain("helpers.md");
    expect(paths).not.toContain("templates/foo/bar.md");
  });

  it("does not leak files from other skill folders into root bundle", () => {
    const buf = makeZip({
      "skill-a/SKILL.md": "---\nname: skill-a\n---\n# A",
      "skill-a/a-file.md": "a content",
      "skill-b/SKILL.md": "---\nname: skill-b\n---\n# B",
      "skill-b/b-file.md": "b content",
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles).toHaveLength(2);
    const a = bundles.find((b) => b.name === "skill-a")!;
    const b = bundles.find((b) => b.name === "skill-b")!;
    expect(a.files.map((f) => f.path)).toEqual(["a-file.md"]);
    expect(b.files.map((f) => f.path)).toEqual(["b-file.md"]);
  });

  it("allows dot-prefixed leaf files but rejects dot-prefixed directories", () => {
    const buf = makeZip({
      "my-skill/SKILL.md": FRONTMATTER,
      "my-skill/.gitignore": "node_modules",
      "my-skill/.editorconfig": "root = true",
      "my-skill/.git/config": "should be rejected",
      "my-skill/.ssh/id_rsa": "should be rejected",
      "my-skill/.env/secrets": "should be rejected",
      "my-skill/docs/v1..v2.md": "changelog",
    });
    const bundles = parseZipBundles(buf, "fallback");
    const paths = bundles[0]!.files.map((f) => f.path).sort();
    expect(paths).toContain(".gitignore");
    expect(paths).toContain(".editorconfig");
    expect(paths).toContain("docs/v1..v2.md");
    expect(paths).not.toContain(".git/config");
    expect(paths).not.toContain(".ssh/id_rsa");
    expect(paths).not.toContain(".env/secrets");
  });

  it("rejects known sensitive dotfiles like .env and .npmrc", () => {
    const buf = makeZip({
      "s/SKILL.md": FRONTMATTER,
      "s/.env": "SECRET=123",
      "s/.env.local": "LOCAL_SECRET=456",
      "s/.npmrc": "//registry:token",
      "s/.netrc": "machine login",
      "s/.gitignore": "node_modules",
    });
    const bundles = parseZipBundles(buf, "fallback");
    const paths = bundles[0]!.files.map((f) => f.path);
    expect(paths).not.toContain(".env");
    expect(paths).not.toContain(".env.local");
    expect(paths).not.toContain(".npmrc");
    expect(paths).not.toContain(".netrc");
    expect(paths).toContain(".gitignore");
  });

  it("rejects common sensitive files like id_rsa, *.pem, credentials.json", () => {
    const buf = makeZip({
      "s/SKILL.md": FRONTMATTER,
      "s/id_rsa": "-----BEGIN RSA PRIVATE KEY-----",
      "s/id_ed25519": "-----BEGIN OPENSSH PRIVATE KEY-----",
      "s/server.pem": "cert data",
      "s/private.key": "key data",
      "s/credentials.json": '{"token":"secret"}',
      "s/readme.md": "safe content",
    });
    const bundles = parseZipBundles(buf, "fallback");
    const paths = bundles[0]!.files.map((f) => f.path);
    expect(paths).not.toContain("id_rsa");
    expect(paths).not.toContain("id_ed25519");
    expect(paths).not.toContain("server.pem");
    expect(paths).not.toContain("private.key");
    expect(paths).not.toContain("credentials.json");
    expect(paths).toContain("readme.md");
  });

  it("rejects case-insensitive SKILL.md variants as support files", () => {
    const buf = makeZip({
      "my-skill/SKILL.md": FRONTMATTER,
      "my-skill/skill.md": "sneaky lowercase",
      "my-skill/Skill.md": "sneaky mixed case",
      "my-skill/sub/SKILL.MD": "sneaky uppercase ext",
      "my-skill/legit.md": "ok",
    });
    const bundles = parseZipBundles(buf, "fallback");
    const paths = bundles[0]!.files.map((f) => f.path);
    expect(paths).toEqual(["legit.md"]);
  });

  it("skips __MACOSX entries", () => {
    const buf = makeZip({
      "my-skill/SKILL.md": FRONTMATTER,
      "__MACOSX/my-skill/._SKILL.md": "mac metadata",
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles).toHaveLength(1);
    expect(bundles[0]!.files).toHaveLength(0);
  });

  it("extracts frontmatter name and description", () => {
    const buf = makeZip({
      "s/SKILL.md": '---\nname: "hello-world"\ndescription: does stuff\n---\nbody',
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles[0]!.name).toBe("hello-world");
    expect(bundles[0]!.description).toBe("does stuff");
  });

  it("uses folder name when frontmatter has no name", () => {
    const buf = makeZip({
      "cool-skill/SKILL.md": "no frontmatter here",
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles[0]!.name).toBe("cool-skill");
  });

  it("uses fallback name for root-level SKILL.md without frontmatter name", () => {
    const buf = makeZip({
      "SKILL.md": "no frontmatter here",
    });
    const bundles = parseZipBundles(buf, "my-archive");
    expect(bundles[0]!.name).toBe("my-archive");
  });

  it("rejects hidden SKILL.md roots in zip discovery", () => {
    const buf = makeZip({
      ".secret/SKILL.md": "---\nname: hidden\n---\n# Hidden",
      "legit/SKILL.md": "---\nname: legit\n---\n# Legit",
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles).toHaveLength(1);
    expect(bundles[0]!.name).toBe("legit");
  });

  it("throws when only hidden SKILL.md roots exist", () => {
    const buf = makeZip({
      ".hidden/SKILL.md": "---\nname: hidden\n---\n# H",
    });
    expect(() => parseZipBundles(buf, "fallback")).toThrow(ParseError);
  });

  it("handles same-depth SKILL.md candidates deterministically", () => {
    const buf = makeZip({
      "skill-b/SKILL.md": "---\nname: skill-b\n---\n# B",
      "skill-a/SKILL.md": "---\nname: skill-a\n---\n# A",
    });
    const bundles1 = parseZipBundles(buf, "fallback");
    const bundles2 = parseZipBundles(buf, "fallback");
    expect(bundles1.map((b) => b.name)).toEqual(bundles2.map((b) => b.name));
  });

  it("sets truncated flag when a file exceeds per-file size limit", () => {
    const bigContent = "x".repeat((1 << 20) + 1);
    const buf = makeZip({
      "s/SKILL.md": FRONTMATTER,
      "s/big.md": bigContent,
      "s/small.md": "ok",
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles[0]!.truncated).toBe(true);
    expect(bundles[0]!.files.map((f) => f.path)).toEqual(["small.md"]);
  });

  it("sets truncated when file count exceeds 128", () => {
    const files: Record<string, string> = { "s/SKILL.md": FRONTMATTER };
    for (let i = 0; i < 130; i++) {
      files[`s/f${i}.md`] = `file ${i}`;
    }
    const buf = makeZip(files);
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles[0]!.truncated).toBe(true);
    expect(bundles[0]!.files.length).toBeLessThanOrEqual(128);
  });

  it("sets truncated when total size exceeds 8 MiB", () => {
    const chunk = "x".repeat(1 << 20); // 1 MiB each
    const files: Record<string, string> = { "s/SKILL.md": FRONTMATTER };
    for (let i = 0; i < 10; i++) {
      files[`s/chunk${i}.md`] = chunk;
    }
    const buf = makeZip(files);
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles[0]!.truncated).toBe(true);
  });

  it("skips support files with invalid UTF-8 and sets truncated", () => {
    const invalidUtf8 = new Uint8Array([0x80, 0x81, 0xff, 0xfe]);
    const buf = makeZipRaw({
      "s/SKILL.md": strToU8(FRONTMATTER),
      "s/bad.md": invalidUtf8,
      "s/good.md": strToU8("valid"),
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles[0]!.truncated).toBe(true);
    expect(bundles[0]!.files.map((f) => f.path)).toEqual(["good.md"]);
  });

  it("throws when SKILL.md itself has invalid UTF-8", () => {
    const invalidUtf8 = new Uint8Array([0x80, 0x81, 0xff, 0xfe]);
    const buf = makeZipRaw({
      "s/SKILL.md": invalidUtf8,
    });
    expect(() => parseZipBundles(buf, "fallback")).toThrow();
  });

  it("reports skippedBinaryCount for binary files", () => {
    const buf = makeZipRaw({
      "s/SKILL.md": strToU8(FRONTMATTER),
      "s/image.png": new Uint8Array([0x89, 0x50, 0x4e, 0x47]),
      "s/doc.pdf": new Uint8Array([0x25, 0x50, 0x44, 0x46]),
      "s/readme.md": strToU8("hello"),
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles[0]!.skippedBinaryCount).toBe(2);
    expect(bundles[0]!.truncated).toBe(false);
    expect(bundles[0]!.files.map((f) => f.path)).toEqual(["readme.md"]);
  });

  it("marks bundles truncated when inflation filter drops a SKILL.md", () => {
    const bigSkillMd = "---\nname: big\n---\n" + "x".repeat((1 << 20) * 2 + 1);
    const buf = makeZip({
      "a/SKILL.md": FRONTMATTER,
      "a/helper.md": "ok",
      "b/SKILL.md": bigSkillMd,
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles.length).toBe(1);
    expect(bundles[0]!.name).toBe("my-skill");
    expect(bundles[0]!.truncated).toBe(true);
  });

  it("does not block import when oversized binary files trigger inflation filter", () => {
    const bigBinary = new Uint8Array((1 << 20) * 2 + 1); // > MAX_FILE_SIZE * 2
    const buf = makeZipRaw({
      "s/SKILL.md": strToU8(FRONTMATTER),
      "s/screenshot.png": bigBinary,
      "s/helper.md": strToU8("helper text"),
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles[0]!.truncated).toBe(false);
    expect(bundles[0]!.files.map((f) => f.path)).toEqual(["helper.md"]);
  });
});

describe("sanitizeExportName", () => {
  it("preserves normal names", () => {
    expect(sanitizeExportName("my-skill")).toBe("my-skill");
  });

  it("rewrites leading-dot names to prevent hidden folders", () => {
    expect(sanitizeExportName(".review")).not.toMatch(/^\./);
    expect(sanitizeExportName(".secret")).not.toMatch(/^\./);
  });

  it("rewrites __MACOSX to prevent archive root conflict", () => {
    expect(sanitizeExportName("__MACOSX")).not.toBe("__MACOSX");
  });

  it("replaces slashes and traversal in names", () => {
    const result = sanitizeExportName("../../payload");
    expect(result).not.toContain("/");
    expect(result).not.toContain("..");
  });

  it("returns fallback for empty names", () => {
    expect(sanitizeExportName("")).toBe("skill");
    expect(sanitizeExportName("..")).toBe("skill");
  });

  it("round-trips through zip import", () => {
    const name = sanitizeExportName(".review");
    const buf = makeZip({
      [`${name}/SKILL.md`]: `---\nname: ${name}\n---\n# Test`,
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles).toHaveLength(1);
    expect(bundles[0]!.name).toBe(name);
  });
});

describe("sanitizeExportPath", () => {
  it("preserves normal paths", () => {
    expect(sanitizeExportPath("helpers.md")).toBe("helpers.md");
    expect(sanitizeExportPath("templates/review.md")).toBe("templates/review.md");
  });

  it("preserves filenames containing double dots", () => {
    expect(sanitizeExportPath("docs/v1..v2.md")).toBe("docs/v1..v2.md");
    expect(sanitizeExportPath("changelog..old.md")).toBe("changelog..old.md");
  });

  it("rejects traversal segments", () => {
    expect(sanitizeExportPath("../secret.md")).toBe("");
    expect(sanitizeExportPath("a/../../x.md")).toBe("");
    expect(sanitizeExportPath("a/../b/c.md")).toBe("");
  });

  it("rejects dot segments", () => {
    expect(sanitizeExportPath("./notes.md")).toBe("");
  });

  it("normalizes double slashes", () => {
    expect(sanitizeExportPath("a//b.md")).toBe("a/b.md");
  });

  it("returns empty string for degenerate paths", () => {
    expect(sanitizeExportPath("..")).toBe("");
    expect(sanitizeExportPath("../..")).toBe("");
  });

  it("skips SKILL.md support files to protect the manifest", () => {
    const files = [
      { path: "SKILL.md", content: "should be skipped" },
      { path: "other.md", content: "ok" },
    ];
    const result = collectExportFiles(files);
    expect(result.files).toHaveLength(1);
    expect(result.files[0]!.path).toBe("other.md");
    expect(result.hasCollisions).toBe(true);
  });
});

describe("buildExportContent", () => {
  it("returns content as-is when it already has frontmatter", () => {
    const content = "---\nname: foo\n---\n# Body";
    expect(buildExportContent(content, "foo", "desc")).toBe(content);
  });

  it("injects frontmatter when content has none", () => {
    const result = buildExportContent("# Body", "my-skill", "A description");
    expect(result).toContain("---\nname: my-skill\ndescription: A description\n---");
    expect(result).toContain("# Body");
  });

  it("omits description line when description is empty", () => {
    const result = buildExportContent("# Body", "my-skill", "");
    expect(result).not.toContain("description:");
    expect(result).toContain("name: my-skill");
  });

  it("handles empty content", () => {
    const result = buildExportContent("", "my-skill", "desc");
    expect(result).toContain("name: my-skill");
  });
});

describe("updateFrontmatter", () => {
  it("replaces existing frontmatter name and description", () => {
    const content = "---\nname: old-name\ndescription: old desc\n---\n# Body";
    const result = updateFrontmatter(content, "new-name", "new desc");
    expect(result).toContain("name: new-name");
    expect(result).toContain("description: new desc");
    expect(result).toContain("# Body");
    expect(result).not.toContain("old-name");
  });

  it("injects frontmatter when content has none", () => {
    const result = updateFrontmatter("# Just body", "my-skill", "a desc");
    expect(result).toContain("---\nname: my-skill\ndescription: a desc\n---");
    expect(result).toContain("# Just body");
  });

  it("preserves other frontmatter fields", () => {
    const content = "---\nname: old\norigin: local\ndescription: old\ncustom: value\n---\nbody";
    const result = updateFrontmatter(content, "new", "new desc");
    expect(result).toContain("name: new");
    expect(result).toContain("description: new desc");
    expect(result).toContain("origin: local");
    expect(result).toContain("custom: value");
  });

  it("handles empty description", () => {
    const content = "---\nname: old\ndescription: old desc\n---\nbody";
    const result = updateFrontmatter(content, "new", "");
    expect(result).toContain("name: new");
    expect(result).not.toContain("description:");
  });

  it("quotes descriptions containing colons", () => {
    const result = updateFrontmatter("# Body", "s", "key: value pair");
    expect(result).toContain('description: "key: value pair"');
  });

  it("quotes YAML boolean-like values", () => {
    const result = updateFrontmatter("# Body", "true", "false");
    expect(result).toContain('name: "true"');
    expect(result).toContain('description: "false"');
  });

  it("flattens newlines in description", () => {
    const result = updateFrontmatter("# Body", "s", "line1\nline2");
    expect(result).not.toMatch(/description:.*\n.*line2/);
    expect(result).toContain("line1 line2");
  });
});

describe("path canonicalization", () => {
  it("normalizes double slashes in imported file paths", () => {
    const buf = makeZip({
      "s/SKILL.md": FRONTMATTER,
      "s/a//template.md": "content",
      "s/a/template.md": "other",
    });
    const bundles = parseZipBundles(buf, "fallback");
    const paths = bundles[0]!.files.map((f) => f.path);
    expect(paths).not.toContain("a//template.md");
    expect(paths.filter((p) => p === "a/template.md")).toHaveLength(1);
  });

  it("rejects Windows drive-letter paths", () => {
    const buf = makeZip({
      "s/SKILL.md": FRONTMATTER,
      "s/C:/payload.md": "evil",
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles[0]!.files).toHaveLength(0);
  });

  it("rejects drive-letter paths smuggled via empty segments", () => {
    const buf = makeZip({
      "s/SKILL.md": FRONTMATTER,
      "s//C:/payload.md": "evil",
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles[0]!.files).toHaveLength(0);
  });

  it("excludes hidden nested SKILL.md subtrees from parent bundle", () => {
    const buf = makeZip({
      "top/SKILL.md": FRONTMATTER,
      "top/.secret/SKILL.md": "---\nname: hidden\n---\n# H",
      "top/.secret/token.txt": "secret token",
      "top/legit.md": "ok",
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles).toHaveLength(1);
    const paths = bundles[0]!.files.map((f) => f.path);
    expect(paths).toContain("legit.md");
    expect(paths).not.toContain(".secret/token.txt");
  });

  it("rejects paths with control characters", () => {
    const buf = makeZip({
      "s/SKILL.md": FRONTMATTER,
      "s/file\x00name.md": "null byte",
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles[0]!.files).toHaveLength(0);
  });
});

describe("export collision handling", () => {
  it("reports collisions for SKILL.md support files", () => {
    const files = [
      { path: "SKILL.md", content: "sneaky" },
      { path: "other.md", content: "ok" },
    ];
    const result = collectExportFiles(files);
    expect(result.hasCollisions).toBe(true);
    expect(result.files).toHaveLength(1);
  });

  it("reports collisions for duplicate paths", () => {
    const files = [
      { path: "a//x.md", content: "first" },
      { path: "a/x.md", content: "second" },
    ];
    const result = collectExportFiles(files);
    expect(result.hasCollisions).toBe(true);
  });

  it("flags traversal paths as collisions", () => {
    const files = [
      { path: "a/../x.md", content: "bad" },
      { path: "ok.md", content: "good" },
    ];
    const result = collectExportFiles(files);
    expect(result.hasCollisions).toBe(true);
    expect(result.files).toHaveLength(1);
    expect(result.files[0]!.path).toBe("ok.md");
  });

  it("returns all files when no collisions", () => {
    const files = [
      { path: "a.md", content: "one" },
      { path: "b.md", content: "two" },
    ];
    const result = collectExportFiles(files);
    expect(result.files).toHaveLength(2);
    expect(result.hasCollisions).toBe(false);
  });

  it("detects case-insensitive path collisions", () => {
    const files = [
      { path: "README.md", content: "one" },
      { path: "readme.md", content: "two" },
    ];
    const result = collectExportFiles(files);
    expect(result.hasCollisions).toBe(true);
  });

  it("rejects control characters in export paths", () => {
    const files = [
      { path: "file\x01name.md", content: "bad" },
      { path: "ok.md", content: "good" },
    ];
    const result = collectExportFiles(files);
    expect(result.hasCollisions).toBe(true);
    expect(result.files).toHaveLength(1);
  });

  it("rejects Windows drive paths in export", () => {
    const files = [
      { path: "C:/payload.md", content: "bad" },
      { path: "ok.md", content: "good" },
    ];
    const result = collectExportFiles(files);
    expect(result.hasCollisions).toBe(true);
    expect(result.files).toHaveLength(1);
  });

  it("allows dot-prefixed leaf files but rejects dot-prefixed dirs in export", () => {
    const files = [
      { path: ".gitignore", content: "node_modules" },
      { path: ".claude/settings.json", content: "dot-dir path" },
      { path: "helpers.md", content: "ok" },
    ];
    const result = collectExportFiles(files);
    expect(result.files).toHaveLength(2);
    expect(result.files.map((f) => f.path).sort()).toEqual([".gitignore", "helpers.md"]);
    expect(result.hasCollisions).toBe(true);
  });
});

describe("root-plus-sibling SKILL.md", () => {
  it("treats root SKILL.md as the single skill, skips entire nested candidate subtree", () => {
    const buf = makeZip({
      "SKILL.md": "---\nname: root\n---\n# Root",
      "root-file.md": "root support",
      "skill-a/SKILL.md": "---\nname: skill-a\n---\n# A",
      "skill-a/helper.md": "a support",
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles).toHaveLength(1);
    expect(bundles[0]!.name).toBe("root");
    const filePaths = bundles[0]!.files.map((f) => f.path);
    expect(filePaths).toContain("root-file.md");
    expect(filePaths).not.toContain("skill-a/helper.md");
    expect(filePaths).not.toContain("skill-a/SKILL.md");
  });
});

describe("import path collision", () => {
  it("deduplicates colliding canonical paths and sets truncated", () => {
    const buf = makeZip({
      "s/SKILL.md": FRONTMATTER,
      "s/a//template.md": "first",
      "s/a/template.md": "second",
    });
    const bundles = parseZipBundles(buf, "fallback");
    const paths = bundles[0]!.files.map((f) => f.path);
    expect(paths.filter((p) => p === "a/template.md")).toHaveLength(1);
    expect(bundles[0]!.truncated).toBe(true);
  });

  it("deduplicates case-insensitive paths on import", () => {
    const buf = makeZip({
      "s/SKILL.md": FRONTMATTER,
      "s/README.md": "upper",
      "s/readme.md": "lower",
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles[0]!.files).toHaveLength(1);
    expect(bundles[0]!.truncated).toBe(true);
  });
});

describe("zip size guard", () => {
  it("rejects zip files exceeding compressed size limit", () => {
    const bigContent = "x".repeat(10 << 20);
    const buf = makeZip({ "s/SKILL.md": bigContent });
    expect(() => parseZipBundles(buf, "fallback")).toThrow();
  });
});

describe("parseZipBundles edge cases", () => {
  it("throws skill_md_too_large for single-skill zip with oversized SKILL.md", () => {
    const bigContent = "x".repeat((1 << 20) + 1);
    const buf = makeZip({ "s/SKILL.md": bigContent });
    expect(() => parseZipBundles(buf, "fallback")).toThrow(ParseError);
    try {
      parseZipBundles(buf, "fallback");
    } catch (e) {
      expect((e as ParseError).code).toBe("skill_md_too_large");
    }
  });

  it("accepts extensionless text files like README and justfile", () => {
    const buf = makeZip({
      "s/SKILL.md": FRONTMATTER,
      "s/README": "readme content",
      "s/justfile": "build: echo hi",
      "s/logo.png": "fake binary",
    });
    const bundles = parseZipBundles(buf, "fallback");
    const paths = bundles[0]!.files.map((f) => f.path).sort();
    expect(paths).toContain("README");
    expect(paths).toContain("justfile");
    expect(paths).not.toContain("logo.png");
  });
});

// --- Folder import tests ---

function makeFakeFileList(
  entries: Record<string, string>,
): FileList {
  const files = Object.entries(entries).map(([path, content]) => {
    const blob = new Blob([content], { type: "text/plain" });
    const file = new File([blob], path.split("/").pop()!, { type: "text/plain" });
    Object.defineProperty(file, "webkitRelativePath", { value: path });
    return file;
  });
  const list = Object.assign(files, {
    item: (i: number) => files[i] ?? null,
  });
  return list as unknown as FileList;
}

function makeFakeFileListRaw(
  entries: Record<string, Uint8Array>,
): FileList {
  const files = Object.entries(entries).map(([path, data]) => {
    const file = new File([data.buffer as ArrayBuffer], path.split("/").pop()!);
    Object.defineProperty(file, "webkitRelativePath", { value: path });
    return file;
  });
  const list = Object.assign(files, {
    item: (i: number) => files[i] ?? null,
  });
  return list as unknown as FileList;
}

describe("parseFolderBundle", () => {
  it("excludes files under hidden nested SKILL.md roots", async () => {
    const fileList = makeFakeFileList({
      "top/SKILL.md": FRONTMATTER,
      "top/.secret/SKILL.md": "---\nname: hidden\n---\n# H",
      "top/.secret/token.txt": "secret-value",
      "top/legit.md": "ok",
    });
    const bundle = await parseFolderBundle(fileList);
    const paths = bundle.files.map((f) => f.path);
    expect(paths).toContain("legit.md");
    expect(paths).not.toContain(".secret/token.txt");
  });

  it("skips support files with invalid UTF-8 and sets truncated", async () => {
    const fileList = makeFakeFileListRaw({
      "top/SKILL.md": strToU8(FRONTMATTER),
      "top/bad.md": new Uint8Array([0x80, 0x81, 0xff, 0xfe]),
      "top/good.md": strToU8("valid"),
    });
    const bundle = await parseFolderBundle(fileList);
    expect(bundle.truncated).toBe(true);
    expect(bundle.files.map((f) => f.path)).toEqual(["good.md"]);
  });

  it("throws when SKILL.md itself has invalid UTF-8", async () => {
    const fileList = makeFakeFileListRaw({
      "top/SKILL.md": new Uint8Array([0x80, 0x81, 0xff, 0xfe]),
    });
    await expect(parseFolderBundle(fileList)).rejects.toThrow();
  });

  it("uses leaf directory name as fallback, not full path", async () => {
    const fileList = makeFakeFileList({
      "parent/my-skill/SKILL.md": "---\n---\n# No name in frontmatter",
      "parent/my-skill/helper.md": "help",
    });
    const bundle = await parseFolderBundle(fileList);
    expect(bundle.name).toBe("my-skill");
  });

  it("treats nested SKILL.md as nested when rootPrefix is the top-level dir", async () => {
    const fileList = makeFakeFileList({
      "root/SKILL.md": FRONTMATTER,
      "root/templates/SKILL.md": "---\nname: nested\n---\n# T",
      "root/templates/example.md": "example",
    });
    const bundle = await parseFolderBundle(fileList);
    expect(bundle.name).toBe("my-skill");
  });

  it("does not throw ambiguous error for nested SKILL.md under root prefix", async () => {
    const fileList = makeFakeFileList({
      "myskill/SKILL.md": FRONTMATTER,
      "myskill/sub/SKILL.md": "---\nname: sub\n---\n# sub content",
      "myskill/sub/notes.md": "notes",
      "myskill/readme.md": "readme",
    });
    const bundle = await parseFolderBundle(fileList);
    expect(bundle.name).toBe("my-skill");
    expect(bundle.files.map((f) => f.path)).toContain("readme.md");
    expect(bundle.files.map((f) => f.path)).not.toContain("sub/notes.md");
  });
});

describe("parseZipBundles rootPrefix edge cases", () => {
  it("does not mark root bundle truncated due to rejected files under suppressed prefixes", () => {
    const bigContent = "x".repeat((1 << 20) * 2 + 1);
    const buf = makeZip({
      "SKILL.md": FRONTMATTER,
      "root-file.md": "ok",
      "nested/SKILL.md": bigContent,
    });
    const bundles = parseZipBundles(buf, "fallback");
    expect(bundles).toHaveLength(1);
    expect(bundles[0]!.name).toBe("my-skill");
    expect(bundles[0]!.truncated).toBe(false);
  });
});
