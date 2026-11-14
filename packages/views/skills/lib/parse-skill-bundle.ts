import { unzipSync, type UnzipFileFilter } from "fflate";

export interface ParsedSkillBundle {
  name: string;
  description: string;
  content: string;
  files: { path: string; content: string }[];
  truncated: boolean;
  skippedBinaryCount: number;
}

export type ParseErrorCode =
  | "skill_md_not_found_zip"
  | "skill_md_not_found_folder"
  | "skill_md_too_large"
  | "skill_md_ambiguous_folder";

export class ParseError extends Error {
  code: ParseErrorCode;
  constructor(code: ParseErrorCode) {
    super(code);
    this.code = code;
  }
}

const MAX_FILE_SIZE = 1 << 20; // 1 MiB per file — mirrors backend maxImportFileSize
const MAX_TOTAL_SIZE = 8 << 20; // 8 MiB total — mirrors backend maxImportTotalSize
const MAX_FILE_COUNT = 128;
const MAX_ZIP_SIZE = 50 << 20; // 50 MiB compressed — guard against zip bombs

function parseFrontmatter(raw: string): {
  name: string;
  description: string;
  body: string;
} {
  if (!raw.startsWith("---")) return { name: "", description: "", body: raw };
  const end = raw.indexOf("---", 3);
  if (end < 0) return { name: "", description: "", body: raw };

  const fm = raw.slice(3, end);
  let name = "";
  let description = "";
  for (const line of fm.split("\n")) {
    const trimmed = line.trim();
    if (trimmed.startsWith("name:")) {
      name = trimmed.slice(5).trim().replace(/^["']|["']$/g, "");
    } else if (trimmed.startsWith("description:")) {
      description = trimmed.slice(12).trim().replace(/^["']|["']$/g, "");
    }
  }
  return { name, description, body: raw };
}

function isBinaryFile(path: string): boolean {
  const ext = path.split(".").pop()?.toLowerCase() ?? "";
  const binaryExts = new Set([
    "png", "jpg", "jpeg", "gif", "bmp", "ico", "webp", "svg", "avif",
    "mp3", "mp4", "wav", "ogg", "webm", "avi", "mov",
    "zip", "gz", "tar", "bz2", "7z", "rar", "xz",
    "pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx",
    "exe", "dll", "so", "dylib", "bin", "o", "a",
    "woff", "woff2", "ttf", "otf", "eot",
    "class", "pyc", "pyo", "wasm",
  ]);
  return binaryExts.has(ext);
}

function isSensitiveFile(name: string): boolean {
  const lower = name.toLowerCase();
  if (/^\.(env|npmrc|netrc)(\..*)?$/i.test(name)) return true;
  if (/^id_(rsa|dsa|ecdsa|ed25519|ed25519-sk|ecdsa-sk)$/i.test(name)) return true;
  if (/\.(pem|key|p12|pfx|jks|keystore)$/i.test(name)) return true;
  if (lower === "credentials.json" || lower === "service-account.json") return true;
  if (lower === ".htpasswd" || lower === ".pgpass" || lower === ".my.cnf") return true;
  return false;
}

function canonicalizePath(p: string): string | null {
  if (!p) return null;
  // eslint-disable-next-line no-control-regex
  if (/[\x00-\x1f]/.test(p)) return null;
  const segments = p.replace(/\\/g, "/").split("/").filter((s) => s !== "");
  const result: string[] = [];
  for (let i = 0; i < segments.length; i++) {
    const seg = segments[i]!;
    if (seg === ".." || seg === ".") return null;
    if (/^[A-Za-z]:$/.test(seg)) return null;
    // Reject dot-prefixed directories — blocks .git/, .ssh/
    if (seg.startsWith(".") && i < segments.length - 1) return null;
    // Reject known sensitive files at leaf level
    if (i === segments.length - 1 && isSensitiveFile(seg)) return null;
    result.push(seg);
  }
  const normalized = result.join("/");
  if (!normalized || normalized.startsWith("__MACOSX/") || normalized === "__MACOSX") return null;
  const basename = result[result.length - 1]!;
  if (basename.toLowerCase() === "skill.md") return null;
  return normalized;
}

function yamlScalar(value: string): string {
  if (!value) return '""';
  if (/[\n\r]/.test(value)) {
    value = value.replace(/\n/g, " ").replace(/\r/g, "");
  }
  // eslint-disable-next-line no-useless-escape
  if (/[:#\[\]{}&*!|>'"%@`,?]/.test(value) || value.startsWith("-") ||
      value === "true" || value === "false" ||
      value === "null" || value === "---" ||
      /^\d/.test(value)) {
    return `"${value.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
  }
  return value;
}

export function updateFrontmatter(
  content: string,
  name: string,
  description: string,
): string {
  const safeName = yamlScalar(name);
  const safeDesc = description ? yamlScalar(description) : "";

  if (!content.startsWith("---")) {
    const fm = ["---", `name: ${safeName}`];
    if (safeDesc) fm.push(`description: ${safeDesc}`);
    fm.push("---");
    return content ? fm.join("\n") + "\n" + content : fm.join("\n") + "\n";
  }
  const endIdx = content.indexOf("---", 3);
  if (endIdx < 0) return content;

  const fmBlock = content.slice(3, endIdx);
  const rest = content.slice(endIdx + 3);

  const lines: string[] = [];
  let hasName = false;
  let hasDesc = false;
  for (const line of fmBlock.split("\n")) {
    const trimmed = line.trim();
    if (trimmed.startsWith("name:")) {
      lines.push(`name: ${safeName}`);
      hasName = true;
    } else if (trimmed.startsWith("description:")) {
      if (safeDesc) {
        lines.push(`description: ${safeDesc}`);
      }
      hasDesc = true;
    } else {
      lines.push(line);
    }
  }
  if (!hasName) lines.splice(1, 0, `name: ${safeName}`);
  if (!hasDesc && safeDesc) lines.push(`description: ${safeDesc}`);

  return "---" + lines.join("\n") + "---" + rest;
}

export function buildExportContent(
  content: string,
  name: string,
  description: string,
): string {
  if (content.startsWith("---")) return content;
  const fm = [`---`, `name: ${name}`];
  if (description) fm.push(`description: ${description}`);
  fm.push(`---`);
  return content ? fm.join("\n") + "\n" + content : fm.join("\n") + "\n";
}

export function sanitizeExportName(name: string): string {
  let safe = name.replace(/[/\\]/g, "_");
  safe = safe.replace(/\.\./g, "");
  safe = safe.replace(/^_+$/, "");
  if (safe.startsWith(".")) safe = "_" + safe.slice(1);
  if (safe === "__MACOSX") safe = "_MACOSX";
  return safe || "skill";
}

export function sanitizeExportPath(p: string): string {
  // eslint-disable-next-line no-control-regex
  if (/[\x00-\x1f]/.test(p)) return "";
  if (/^[A-Za-z]:/.test(p)) return "";
  const segments = p.replace(/\\/g, "/").split("/").filter((s) => s !== "");
  if (segments.some((s) => s === ".." || s === ".")) return "";
  // Reject dot-prefixed directories (not leaf files)
  for (let i = 0; i < segments.length - 1; i++) {
    if (segments[i]!.startsWith(".")) return "";
  }
  const normalized = segments.join("/");
  if (!normalized || normalized.startsWith("__MACOSX/") || normalized === "__MACOSX") return "";
  const bn = segments[segments.length - 1]!;
  if (bn.toLowerCase() === "skill.md") return "";
  return normalized;
}

export function collectExportFiles(
  files: { path: string; content: string }[],
): { files: { path: string; content: string }[]; hasCollisions: boolean } {
  const seen = new Set<string>();
  const result: { path: string; content: string }[] = [];
  let hasCollisions = false;
  for (const f of files) {
    const safe = sanitizeExportPath(f.path);
    if (!safe) { hasCollisions = true; continue; }
    const collisionKey = safe.toLowerCase();
    if (seen.has(collisionKey)) {
      hasCollisions = true;
      continue;
    }
    seen.add(collisionKey);
    result.push({ path: safe, content: f.content });
  }
  return { files: result, hasCollisions };
}

const decoder = new TextDecoder("utf-8", { fatal: true });

function collectFilesForPrefix(
  entries: Record<string, Uint8Array>,
  skillMdPath: string,
  rootPrefix: string,
  allPrefixes: Set<string>,
): { files: { path: string; content: string }[]; truncated: boolean; skippedBinaryCount: number } {
  const files: { path: string; content: string }[] = [];
  const seenPaths = new Set<string>();
  let totalSize = 0;
  let truncated = false;
  let skippedBinaryCount = 0;

  for (const [rawPath, data] of Object.entries(entries)) {
    const normalized = rawPath.replace(/\\/g, "/");
    if (normalized.endsWith("/")) continue;
    if (normalized === skillMdPath) continue;

    if (!normalized.startsWith(rootPrefix)) continue;

    const rawRelative = normalized.slice(rootPrefix.length);

    // Skip files that belong to a nested/suppressed skill bundle
    const fullNorm = rootPrefix + rawRelative;
    if ([...allPrefixes].some((p) => p !== rootPrefix && fullNorm.startsWith(p))) continue;

    const relative = canonicalizePath(rawRelative);
    if (!relative) continue;
    if (isBinaryFile(relative)) { skippedBinaryCount++; continue; }
    if (data.byteLength > MAX_FILE_SIZE) { truncated = true; continue; }

    totalSize += data.byteLength;
    if (totalSize > MAX_TOTAL_SIZE) { truncated = true; break; }
    if (files.length >= MAX_FILE_COUNT) { truncated = true; break; }

    let text: string;
    try {
      text = decoder.decode(data);
    } catch {
      truncated = true;
      continue;
    }

    const collisionKey = relative.toLowerCase();
    if (seenPaths.has(collisionKey)) {
      truncated = true;
    } else {
      seenPaths.add(collisionKey);
      files.push({ path: relative, content: text });
    }
  }

  return { files, truncated, skippedBinaryCount };
}

export function parseZipBundles(buf: ArrayBuffer, fallbackName: string): ParsedSkillBundle[] {
  if (buf.byteLength > MAX_ZIP_SIZE) {
    throw new Error("Archive exceeds 50 MiB size limit");
  }
  let entryCount = 0;
  let totalUncompressed = 0;
  const rejectedFilterNames: string[] = [];
  const inflationFilter: UnzipFileFilter = (file) => {
    entryCount++;
    if (entryCount > MAX_FILE_COUNT * 4) { rejectedFilterNames.push(file.name); return false; }
    totalUncompressed += file.originalSize;
    if (totalUncompressed > MAX_TOTAL_SIZE * 4) { rejectedFilterNames.push(file.name); return false; }
    if (file.originalSize > MAX_FILE_SIZE * 2) { rejectedFilterNames.push(file.name); return false; }
    return true;
  };
  const entries = unzipSync(new Uint8Array(buf), { filter: inflationFilter });

  // Discover all SKILL.md paths, then keep only top-level roots:
  // if skill-a/SKILL.md and skill-a/templates/SKILL.md both exist,
  // only skill-a/ is a skill root — templates/SKILL.md is a supporting file.
  const candidates: { path: string; prefix: string; depth: number }[] = [];
  const rejectedPrefixes: string[] = [];

  for (const path of Object.keys(entries)) {
    const normalized = path.replace(/\\/g, "/");
    if (normalized.endsWith("/")) continue;
    const basename = normalized.split("/").pop()!;
    if (basename !== "SKILL.md") continue;
    if (normalized.startsWith("__MACOSX/")) continue;

    const parts = normalized.split("/");
    const parentSegs = parts.slice(0, -1);
    const hasInvalidSeg = parentSegs.some(
      (seg) => !seg || seg === ".." || seg === "." || seg.startsWith(".") ||
        // eslint-disable-next-line no-control-regex
        /[\x00-\x1f]/.test(seg) || /^[A-Za-z]:$/.test(seg),
    );
    const prefix = parentSegs.length > 0 ? parentSegs.join("/") + "/" : "";
    if (hasInvalidSeg) {
      rejectedPrefixes.push(prefix);
      continue;
    }

    candidates.push({ path: normalized, prefix, depth: parentSegs.length });
  }

  // Sort by depth so parents come first
  candidates.sort((a, b) => a.depth - b.depth || a.prefix.localeCompare(b.prefix));

  const skillMdPaths: { path: string; prefix: string }[] = [];
  const suppressedPrefixes: string[] = [];
  for (const c of candidates) {
    const isNested = skillMdPaths.some(
      (root) => c.prefix !== root.prefix && c.prefix.startsWith(root.prefix),
    );
    if (isNested) {
      suppressedPrefixes.push(c.prefix);
    } else {
      skillMdPaths.push(c);
    }
  }

  if (skillMdPaths.length === 0) {
    throw new ParseError("skill_md_not_found_zip");
  }

  const droppedSkillMdPrefixes: string[] = [];
  for (const n of rejectedFilterNames) {
    const norm = n.replace(/\\/g, "/");
    if (norm === "SKILL.md" || norm.endsWith("/SKILL.md")) {
      droppedSkillMdPrefixes.push(
        norm === "SKILL.md" ? "" : norm.slice(0, -"SKILL.md".length),
      );
    }
  }
  const hasDroppedSkillMd = droppedSkillMdPrefixes.some(
    (dp) => !skillMdPaths.some((root) => dp.startsWith(root.prefix) && dp !== root.prefix),
  );

  const allPrefixes = new Set([
    ...skillMdPaths.map((s) => s.prefix),
    ...suppressedPrefixes,
    ...rejectedPrefixes,
    ...droppedSkillMdPrefixes,
  ]);

  const bundles: ParsedSkillBundle[] = [];

  for (const { path: skillMdPath, prefix: rootPrefix } of skillMdPaths) {
    const skillMdBytes = entries[skillMdPath];
    if (!skillMdBytes) continue;
    if (skillMdBytes.byteLength > MAX_FILE_SIZE) {
      if (skillMdPaths.length === 1) throw new ParseError("skill_md_too_large");
      continue;
    }

    const skillMdContent = decoder.decode(skillMdBytes);
    const { name, description, body } = parseFrontmatter(skillMdContent);

    const folderName = rootPrefix
      ? rootPrefix.replace(/\/$/, "").split("/").pop()!
      : fallbackName;

    const collected = collectFilesForPrefix(entries, skillMdPath, rootPrefix, allPrefixes);
    const hasRelevantRejection = rejectedFilterNames.some((rn) => {
      const normalized = rn.replace(/\\/g, "/");
      if (!normalized.startsWith(rootPrefix)) return false;
      if (isBinaryFile(normalized)) return false;
      const isUnderOtherPrefix = [...allPrefixes].some(
        (p) => p !== rootPrefix && normalized.startsWith(p),
      );
      return !isUnderOtherPrefix;
    });
    bundles.push({
      name: name || folderName,
      description,
      content: body,
      files: collected.files,
      truncated: collected.truncated || hasRelevantRejection || hasDroppedSkillMd,
      skippedBinaryCount: collected.skippedBinaryCount,
    });
  }

  return bundles;
}

export async function parseZipBundle(file: File): Promise<ParsedSkillBundle> {
  const buf = await file.arrayBuffer();
  const bundles = parseZipBundles(buf, file.name.replace(/\.zip$/i, ""));
  if (bundles.length === 0) {
    throw new ParseError("skill_md_not_found_zip");
  }
  return bundles[0]!;
}

export async function parseFolderBundle(
  fileList: FileList,
): Promise<ParsedSkillBundle> {
  const filesArray = Array.from(fileList);

  const rejectedHiddenPrefixes: string[] = [];
  const skillMdCandidates = filesArray
    .filter((f) => {
      const parts = f.webkitRelativePath.split("/");
      if (parts[parts.length - 1] !== "SKILL.md") return false;
      if (parts.slice(0, -1).some((seg) => seg.startsWith("."))) {
        rejectedHiddenPrefixes.push(
          parts.slice(0, -1).join("/") + "/",
        );
        return false;
      }
      return true;
    })
    .sort(
      (a, b) =>
        a.webkitRelativePath.split("/").length -
        b.webkitRelativePath.split("/").length ||
        a.webkitRelativePath.localeCompare(b.webkitRelativePath),
    );
  const skillMdFile = skillMdCandidates[0];

  if (!skillMdFile) {
    throw new ParseError("skill_md_not_found_folder");
  }

  const skillMdParts = skillMdFile.webkitRelativePath.split("/");
  const rootPrefix =
    skillMdParts.length > 1
      ? skillMdParts.slice(0, -1).join("/") + "/"
      : "";

  // Check for independent (non-nested) candidates — reject as ambiguous
  const nestedPrefixes: string[] = [...rejectedHiddenPrefixes];
  for (const c of skillMdCandidates) {
    if (c === skillMdFile) continue;
    const cPrefix =
      c.webkitRelativePath.split("/").slice(0, -1).join("/") + "/";
    if (rootPrefix && cPrefix.startsWith(rootPrefix)) {
      nestedPrefixes.push(cPrefix);
    } else {
      throw new ParseError("skill_md_ambiguous_folder");
    }
  }

  if (skillMdFile.size > MAX_FILE_SIZE) {
    throw new ParseError("skill_md_too_large");
  }
  const skillMdText = decoder.decode(new Uint8Array(await skillMdFile.arrayBuffer()));
  const { name, description, body } = parseFrontmatter(skillMdText);

  const files: { path: string; content: string }[] = [];
  const seenPaths = new Set<string>();
  let totalSize = 0;
  let truncated = false;
  let skippedBinaryCount = 0;

  for (const f of filesArray) {
    if (f === skillMdFile) continue;

    const fullPath = f.webkitRelativePath;
    if (rootPrefix && !fullPath.startsWith(rootPrefix)) continue;
    if (nestedPrefixes.some((np) => fullPath.startsWith(np))) continue;
    const rawRelative = rootPrefix
      ? fullPath.slice(rootPrefix.length)
      : fullPath;

    const relative = canonicalizePath(rawRelative);
    if (!relative) continue;
    if (isBinaryFile(relative)) { skippedBinaryCount++; continue; }

    const collisionKey = relative.toLowerCase();
    if (seenPaths.has(collisionKey)) { truncated = true; continue; }

    if (f.size > MAX_FILE_SIZE) { truncated = true; continue; }

    totalSize += f.size;
    if (totalSize > MAX_TOTAL_SIZE) { truncated = true; break; }
    if (files.length >= MAX_FILE_COUNT) { truncated = true; break; }

    let content: string;
    try {
      content = decoder.decode(new Uint8Array(await f.arrayBuffer()));
    } catch {
      truncated = true;
      continue;
    }

    seenPaths.add(collisionKey);
    files.push({ path: relative, content });
  }

  const folderName = rootPrefix
    ? rootPrefix.replace(/\/$/, "").split("/").pop()!
    : filesArray[0]?.webkitRelativePath.split("/")[0] ?? "untitled";

  return {
    name: name || folderName,
    description,
    content: body,
    files,
    truncated,
    skippedBinaryCount,
  };
}
