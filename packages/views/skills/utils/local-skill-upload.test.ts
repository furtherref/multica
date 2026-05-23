import { describe, expect, it } from "vitest";
import JSZip from "jszip";
import {
  buildLocalSkillCandidates,
  normalizeUploadPath,
  parseSkillFrontmatter,
  readZipFile,
} from "./local-skill-upload";

describe("local skill upload parser", () => {
  it("parses yaml frontmatter defaults", () => {
    expect(parseSkillFrontmatter("---\nname: Code Review\ndescription: Reviews PRs\n---\nBody")).toEqual({
      name: "Code Review",
      description: "Reviews PRs",
    });
  });

  it("groups a selected root folder that contains SKILL.md", async () => {
    const candidates = await buildLocalSkillCandidates(
      [
        file("review-helper/SKILL.md", "---\nname: Review Helper\n---\nBody"),
        file("review-helper/templates/check.md", "check"),
      ],
      "review-helper",
    );

    expect(candidates).toHaveLength(1);
    expect(candidates[0]).toMatchObject({
      root: "review-helper",
      label: "review-helper",
      valid: true,
      name: "Review Helper",
      fileCount: 2,
    });
    expect(candidates[0]?.files).toEqual([{ path: "templates/check.md", content: "check" }]);
  });

  it("keeps supporting files for root-level skill bundles", async () => {
    const candidates = await buildLocalSkillCandidates(
      [
        file("SKILL.md", "---\nname: Root Skill\n---\nBody"),
        file("templates/check.md", "check"),
      ],
      "root-skill.zip",
    );

    expect(candidates).toHaveLength(1);
    expect(candidates[0]).toMatchObject({
      root: "",
      label: "root-skill.zip",
      valid: true,
      name: "Root Skill",
      fileCount: 2,
    });
    expect(candidates[0]?.files).toEqual([{ path: "templates/check.md", content: "check" }]);
  });

  it("uses the source label for unnamed root-level skill bundles", async () => {
    const candidates = await buildLocalSkillCandidates(
      [file("SKILL.md", "# Review Helper")],
      "review-helper.zip",
    );

    expect(candidates[0]).toMatchObject({
      root: "",
      label: "review-helper.zip",
      valid: true,
      name: "review-helper.zip",
    });
  });

  it("counts SKILL.md in the displayed file count", async () => {
    const candidates = await buildLocalSkillCandidates(
      [file("review-helper/SKILL.md", "# Review Helper")],
      "review-helper",
    );

    expect(candidates[0]).toMatchObject({
      valid: true,
      fileCount: 1,
    });
    expect(candidates[0]?.files).toEqual([]);
  });

  it("groups multiple child skill roots", async () => {
    const candidates = await buildLocalSkillCandidates(
      [
        file("team/review/SKILL.md", "# Review"),
        file("team/docs/SKILL.md", "# Docs"),
        file("team/notes.txt", "ignored"),
      ],
      "team",
    );

    expect(candidates.map((c) => c.root)).toEqual(["team/docs", "team/review"]);
    expect(candidates.map((c) => c.label)).toEqual(["team/docs", "team/review"]);
  });

  it("keeps zip source labels before skill roots", async () => {
    const candidates = await buildLocalSkillCandidates(
      [file("team/review/SKILL.md", "# Review")],
      "team.zip",
    );

    expect(candidates[0]?.label).toBe("team.zip/team/review");
  });

  it("keeps nested SKILL.md files inside an already detected parent skill", async () => {
    const candidates = await buildLocalSkillCandidates(
      [
        file("team/top/SKILL.md", "---\nname: Top\n---\n# Top"),
        file("team/top/templates/SKILL.md", "# Template"),
        file("team/release/reporter/SKILL.md", "---\nname: Release Reporter\n---\n# Reporter"),
      ],
      "team",
    );

    expect(candidates.map((c) => c.root)).toEqual(["team/release/reporter", "team/top"]);
    expect(candidates.find((c) => c.root === "team/top")?.files).toEqual([
      { path: "templates/SKILL.md", content: "# Template" },
    ]);
  });

  it("skips hidden, binary, oversized, and traversal files", async () => {
    const candidates = await buildLocalSkillCandidates(
      [
        file("review/SKILL.md", "# Review"),
        file("review/.DS_Store", "ignored"),
        file("review/../secret.md", "bad"),
        file("review/binary.dat", "hello\u0000world"),
        file("review/large.txt", "x".repeat(1024 * 1024 + 1)),
      ],
      "review",
    );

    expect(candidates[0]?.files).toEqual([]);
    expect(candidates[0]?.skipped.map((s) => s.reason)).toEqual([
      "hidden_file",
      "path_traversal",
      "binary_file",
      "file_too_large",
    ]);
  });

  it("skips files with invalid UTF-8 bytes", async () => {
    const candidates = await buildLocalSkillCandidates(
      [
        file("review/SKILL.md", "# Review"),
        bytesFile("review/binary.dat", [0xff, 0xfe, 0xfd]),
      ],
      "review",
    );

    expect(candidates[0]?.files).toEqual([]);
    expect(candidates[0]?.skipped).toEqual([
      { path: "review/binary.dat", reason: "binary_file" },
    ]);
  });

  it("surfaces an oversized primary SKILL.md selection", async () => {
    const candidates = await buildLocalSkillCandidates(
      [file("review/SKILL.md", "x".repeat(1024 * 1024 + 1))],
      "review",
    );

    expect(candidates).toHaveLength(1);
    expect(candidates[0]).toMatchObject({
      valid: false,
      reason: "unreadable_skill_md",
      fileCount: 0,
      files: [],
      skipped: [{ path: "review/SKILL.md", reason: "file_too_large" }],
    });
  });

  it("surfaces an unreadable primary SKILL.md selection", async () => {
    const candidates = await buildLocalSkillCandidates(
      [bytesFile("review/SKILL.md", [0xff, 0xfe, 0xfd])],
      "review",
    );

    expect(candidates).toHaveLength(1);
    expect(candidates[0]).toMatchObject({
      valid: false,
      reason: "unreadable_skill_md",
      fileCount: 0,
      files: [],
      skipped: [{ path: "review/SKILL.md", reason: "binary_file" }],
    });
  });

  it("counts aggregate bundle size in UTF-8 bytes", async () => {
    const candidates = await buildLocalSkillCandidates(
      [
        file("review/SKILL.md", "# Review"),
        ...Array.from({ length: 9 }, (_, index) =>
          file(`review/templates/${index}.md`, "你".repeat(340_000)),
        ),
      ],
      "review",
    );

    expect(candidates[0]?.files).toHaveLength(8);
    expect(candidates[0]?.skipped).toEqual([
      { path: "review/templates/8.md", reason: "bundle_too_large" },
    ]);
  });

  it("does not read folder files past the per-skill file cap", async () => {
    const counter = { reads: 0 };
    const candidates = await buildLocalSkillCandidates(
      [
        countedFile("review/SKILL.md", "# Review", counter),
        ...Array.from({ length: 130 }, (_, index) =>
          countedFile(`review/templates/${index}.md`, `template-${index}`, counter),
        ),
      ],
      "review",
    );

    expect(counter.reads).toBe(129);
    expect(candidates[0]?.files).toHaveLength(128);
    expect(candidates[0]?.skipped).toEqual([
      { path: "review/templates/128.md", reason: "too_many_files" },
      { path: "review/templates/129.md", reason: "too_many_files" },
    ]);
  });

  it("does not read folder files past the per-skill byte budget", async () => {
    const counter = { reads: 0 };
    const candidates = await buildLocalSkillCandidates(
      [
        countedFile("review/SKILL.md", "# Review", counter),
        ...Array.from({ length: 10 }, (_, index) =>
          countedFile(`review/templates/${index}.md`, "你".repeat(340_000), counter),
        ),
      ],
      "review",
    );

    expect(counter.reads).toBeLessThan(11);
    expect(candidates[0]?.files.length).toBeLessThanOrEqual(8);
    const skippedBundleLarge = candidates[0]?.skipped.filter(
      (s) => s.reason === "bundle_too_large",
    );
    expect(skippedBundleLarge?.length).toBeGreaterThan(0);
  });

  it("reports unreadable primary SKILL.md with a specific reason", async () => {
    const candidates = await buildLocalSkillCandidates(
      [bytesFile("review/SKILL.md", [0xff, 0xfe, 0xfd])],
      "review",
    );

    expect(candidates).toHaveLength(1);
    expect(candidates[0]).toMatchObject({
      valid: false,
      reason: "unreadable_skill_md",
    });
  });

  it("reports oversized primary SKILL.md with a specific reason", async () => {
    const candidates = await buildLocalSkillCandidates(
      [file("review/SKILL.md", "x".repeat(1024 * 1024 + 1))],
      "review",
    );

    expect(candidates).toHaveLength(1);
    expect(candidates[0]).toMatchObject({
      valid: false,
      reason: "unreadable_skill_md",
    });
  });

  it("reports oversized SKILL.md from zip with a specific reason", async () => {
    const zip = new JSZip();
    zip.file("SKILL.md", "x".repeat(1024 * 1024 + 1));
    const blob = await zip.generateAsync({ type: "blob", compression: "DEFLATE" });

    const entries = await readZipFile(new File([blob], "skill.zip"));
    const candidates = await buildLocalSkillCandidates(entries, "skill.zip");

    expect(candidates).toHaveLength(1);
    expect(candidates[0]).toMatchObject({
      valid: false,
      reason: "unreadable_skill_md",
    });
  });

  it("rejects absolute and traversal paths during normalization", () => {
    expect(normalizeUploadPath("/tmp/SKILL.md")).toEqual({ ok: false, reason: "absolute_path" });
    expect(normalizeUploadPath("skill/../../secret.md")).toEqual({
      ok: false,
      reason: "path_traversal",
    });
  });

  it("rejects zip entries whose original name contains traversal", async () => {
    const zip = new JSZip();
    zip.file("skill/../SKILL.md", "# Traversal");
    const blob = await zip.generateAsync({ type: "blob" });

    await expect(readZipFile(new File([blob], "skill.zip"))).rejects.toThrow("path_traversal");
  });

  it("marks oversized zip entries without inflating them", async () => {
    const zip = new JSZip();
    zip.file("SKILL.md", "# Review");
    zip.file("templates/large.md", "x".repeat(1024 * 1024 + 1));
    const blob = await zip.generateAsync({ type: "blob", compression: "DEFLATE" });

    const entries = await readZipFile(new File([blob], "skill.zip"));
    const candidates = await buildLocalSkillCandidates(entries, "skill.zip");

    expect(entries.find((entry) => entry.path === "templates/large.md")?.file).toBeUndefined();
    expect(candidates).toHaveLength(1);
    expect(candidates[0]).toMatchObject({
      root: "",
      valid: true,
      fileCount: 1,
    });
    expect(candidates[0]?.files).toEqual([]);
    expect(candidates[0]?.skipped).toEqual([
      { path: "templates/large.md", reason: "file_too_large" },
    ]);
  });

  it("surfaces an oversized primary SKILL.md from a zip", async () => {
    const zip = new JSZip();
    zip.file("SKILL.md", "x".repeat(1024 * 1024 + 1));
    const blob = await zip.generateAsync({ type: "blob", compression: "DEFLATE" });

    const entries = await readZipFile(new File([blob], "skill.zip"));
    const candidates = await buildLocalSkillCandidates(entries, "skill.zip");

    expect(entries).toEqual([{ path: "SKILL.md", skippedReason: "file_too_large" }]);
    expect(candidates).toHaveLength(1);
    expect(candidates[0]).toMatchObject({
      root: "",
      label: "skill.zip",
      valid: false,
      reason: "unreadable_skill_md",
      fileCount: 0,
      files: [],
      skipped: [{ path: "SKILL.md", reason: "file_too_large" }],
    });
  });

  it("keeps oversized zip SKILL.md markers alongside valid skills", async () => {
    const zip = new JSZip();
    zip.file("review/SKILL.md", "# Review");
    zip.file("broken/SKILL.md", "x".repeat(1024 * 1024 + 1));
    const blob = await zip.generateAsync({ type: "blob", compression: "DEFLATE" });

    const entries = await readZipFile(new File([blob], "skills.zip"));
    const candidates = await buildLocalSkillCandidates(entries, "skills.zip");

    expect(entries.find((entry) => entry.path === "broken/SKILL.md")).toEqual({
      path: "broken/SKILL.md",
      skippedReason: "file_too_large",
    });
    expect(candidates.map((candidate) => candidate.root)).toEqual(["broken", "review"]);
    expect(candidates.find((candidate) => candidate.root === "broken")).toMatchObject({
      valid: false,
      skipped: [{ path: "broken/SKILL.md", reason: "file_too_large" }],
    });
    expect(candidates.find((candidate) => candidate.root === "review")).toMatchObject({
      valid: true,
      fileCount: 1,
    });
  });

  it("allows zip imports with multiple skills under the per-skill file cap", async () => {
    const zip = new JSZip();
    zip.file("review/SKILL.md", "# Review");
    zip.file("docs/SKILL.md", "# Docs");
    for (let index = 0; index < 70; index += 1) {
      zip.file(`review/templates/${index}.md`, "review");
      zip.file(`docs/templates/${index}.md`, "docs");
    }
    const blob = await zip.generateAsync({ type: "blob" });
    const buffer = await blob.arrayBuffer();

    await expect(readZipFile(new File([buffer], "skills.zip"))).resolves.toHaveLength(142);
  });

  it("marks excess zip entries without rejecting the skill", async () => {
    const zip = new JSZip();
    zip.file("SKILL.md", "# Review");
    for (let index = 0; index < 130; index += 1) {
      zip.file(`templates/${index}.md`, "template");
    }
    const blob = await zip.generateAsync({ type: "blob" });

    const entries = await readZipFile(new File([blob], "skill.zip"));
    const candidates = await buildLocalSkillCandidates(entries, "skill.zip");

    expect(candidates).toHaveLength(1);
    expect(candidates[0]).toMatchObject({
      root: "",
      valid: true,
      fileCount: 129,
    });
    expect(candidates[0]?.files).toHaveLength(128);
    expect(candidates[0]?.skipped).toEqual([
      { path: "templates/128.md", reason: "too_many_files" },
      { path: "templates/129.md", reason: "too_many_files" },
    ]);
  });

  it("does not let skipped zip entries consume the supporting file budget", async () => {
    const zip = new JSZip();
    zip.file("SKILL.md", "# Review");
    for (let index = 0; index < 128; index += 1) {
      zip.file(`.hidden/${index}.md`, "ignored");
    }
    zip.file("templates/real.md", "real content");
    const blob = await zip.generateAsync({ type: "blob" });

    const entries = await readZipFile(new File([blob], "skill.zip"));
    const candidates = await buildLocalSkillCandidates(entries, "skill.zip");

    expect(candidates).toHaveLength(1);
    expect(candidates[0]?.files).toEqual([{ path: "templates/real.md", content: "real content" }]);
    expect(candidates[0]?.skipped).toHaveLength(128);
    expect(candidates[0]?.skipped.every((item) => item.reason === "hidden_file")).toBe(true);
  });

  it("does not let binary zip files consume the supporting file budget", async () => {
    const zip = new JSZip();
    zip.file("SKILL.md", "# Review");
    for (let index = 0; index < 128; index += 1) {
      zip.file(`templates/${index}.bin`, new Uint8Array([0xff, 0xfe, 0xfd]));
    }
    zip.file("templates/real.md", "real content");
    const blob = await zip.generateAsync({ type: "blob" });

    const entries = await readZipFile(new File([blob], "skill.zip"));
    const candidates = await buildLocalSkillCandidates(entries, "skill.zip");

    expect(candidates).toHaveLength(1);
    expect(candidates[0]?.files).toEqual([{ path: "templates/real.md", content: "real content" }]);
    const binarySkipped = candidates[0]?.skipped.filter((s) => s.reason === "binary_file");
    expect(binarySkipped).toHaveLength(128);
  });

  it("enforces per-root byte budget before inflating zip entries", async () => {
    const zip = new JSZip();
    zip.file("SKILL.md", "# Review");
    for (let index = 0; index < 10; index += 1) {
      zip.file(`templates/${index}.md`, "x".repeat(1_000_000));
    }
    const blob = await zip.generateAsync({ type: "blob", compression: "DEFLATE" });

    const entries = await readZipFile(new File([blob], "skill.zip"));

    const inflatedCount = entries.filter((e) => e.file).length;
    const skippedBundleLarge = entries.filter((e) => e.skippedReason === "bundle_too_large");
    expect(inflatedCount).toBeLessThan(12);
    expect(skippedBundleLarge.length).toBeGreaterThan(0);
  });

  it("skips hidden zip entries without inflating them", async () => {
    const zip = new JSZip();
    zip.file("SKILL.md", "# Review");
    zip.file(".hidden/secret.md", "x".repeat(500_000));
    zip.file("templates/real.md", "real content");
    const blob = await zip.generateAsync({ type: "blob" });

    const entries = await readZipFile(new File([blob], "skill.zip"));

    const hiddenEntry = entries.find((e) => e.path === ".hidden/secret.md");
    expect(hiddenEntry?.file).toBeUndefined();
    expect(hiddenEntry?.skippedReason).toBe("hidden_file");
    const realEntry = entries.find((e) => e.path === "templates/real.md");
    expect(realEntry?.file).toBeDefined();
  });

  it("ignores zip entries outside detected skill roots", async () => {
    const zip = new JSZip();
    zip.file("skill/SKILL.md", "# Review");
    zip.file("skill/templates/real.md", "real content");
    zip.file("unrelated/large.txt", "x".repeat(1024 * 1024));
    const blob = await zip.generateAsync({ type: "blob" });

    const entries = await readZipFile(new File([blob], "skill.zip"));

    expect(entries.map((entry) => entry.path).sort()).toEqual([
      "skill/SKILL.md",
      "skill/templates/real.md",
    ]);
  });
});

function file(path: string, content: string): File & { webkitRelativePath: string } {
  const value = new File([content], path.split("/").at(-1) ?? "file.txt", { type: "text/plain" });
  Object.defineProperty(value, "webkitRelativePath", { value: path });
  return value as File & { webkitRelativePath: string };
}

function bytesFile(path: string, bytes: number[]): File & { webkitRelativePath: string } {
  const buffer = new ArrayBuffer(bytes.length);
  new Uint8Array(buffer).set(bytes);
  const value = new File([buffer], path.split("/").at(-1) ?? "file.bin", {
    type: "application/octet-stream",
  });
  Object.defineProperty(value, "webkitRelativePath", { value: path });
  return value as File & { webkitRelativePath: string };
}

function countedFile(
  path: string,
  content: string,
  counter: { reads: number },
): File & { webkitRelativePath: string } {
  const value = file(path, content);
  const originalArrayBuffer = value.arrayBuffer.bind(value);
  Object.defineProperty(value, "arrayBuffer", {
    value: async () => {
      counter.reads += 1;
      return originalArrayBuffer();
    },
  });
  return value;
}
