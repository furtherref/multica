import JSZip from "jszip";
import type { ImportLocalSkillRequest } from "@multica/core/types";

export const MAX_SKILL_FILE_BYTES = 1024 * 1024;
export const MAX_SKILL_FILES = 128;
export const MAX_SKILL_ACCEPTED_BYTES = 8 * 1024 * 1024;
export const MAX_LOCAL_SKILL_IMPORT_BATCH = 16;

export interface LocalSkillInputFile {
  path: string;
  file?: File;
  skippedReason?: SkippedLocalSkillFileReason;
}

export type SkippedLocalSkillFileReason =
  | "hidden_file"
  | "metadata_file"
  | "absolute_path"
  | "path_traversal"
  | "file_too_large"
  | "binary_file"
  | "too_many_files"
  | "bundle_too_large";

export interface SkippedLocalSkillFile {
  path: string;
  reason: SkippedLocalSkillFileReason;
}

export interface LocalSkillCandidate {
  id: string;
  root: string;
  label: string;
  valid: boolean;
  reason?: "missing_skill_md" | "unreadable_skill_md" | "all_files_skipped";
  name: string;
  description: string;
  content: string;
  fileCount: number;
  files: { path: string; content: string }[];
  skipped: SkippedLocalSkillFile[];
  selected: boolean;
}

interface InvalidSkillGroup {
  root: string;
  skipped: SkippedLocalSkillFile[];
}

type NormalizedUploadPath =
  | { ok: true; path: string }
  | { ok: false; reason: "absolute_path" | "path_traversal" };

interface ReadUploadFile {
  path: string;
  content: string;
}

interface SkillGroup {
  root: string;
  skillFile: ReadUploadFile;
  files: ReadUploadFile[];
  skipped: SkippedLocalSkillFile[];
}

const metadataFiles = new Set(["thumbs.db", "desktop.ini"]);
const textEncoder = new TextEncoder();
const utf8Decoder = new TextDecoder("utf-8", { fatal: true });

export function parseSkillFrontmatter(content: string): { name?: string; description?: string } {
  const match = content.match(/^---\r?\n([\s\S]*?)\r?\n---/);
  if (!match) return {};

  const result: { name?: string; description?: string } = {};
  const frontmatter = match[1] ?? "";
  for (const line of frontmatter.split(/\r?\n/)) {
    const field = line.match(/^(name|description):\s*(.+?)\s*$/);
    if (!field) continue;
    const key = field[1] as "name" | "description";
    result[key] = (field[2] ?? "").replace(/^["']|["']$/g, "").trim();
  }
  return result;
}

export function normalizeUploadPath(path: string): NormalizedUploadPath {
  const normalized = path.replace(/\\/g, "/").replace(/^\.\/+/, "");
  if (normalized.startsWith("/") || /^[a-zA-Z]:\//.test(normalized)) {
    return { ok: false, reason: "absolute_path" };
  }
  const parts = normalized.split("/").filter(Boolean);
  if (parts.includes("..")) {
    return { ok: false, reason: "path_traversal" };
  }
  return { ok: true, path: parts.join("/") };
}

export async function buildLocalSkillCandidates(
  files: File[] | LocalSkillInputFile[],
  sourceLabel: string,
): Promise<LocalSkillCandidate[]> {
  const skipped: SkippedLocalSkillFile[] = [];
  const normalizedInputs: LocalSkillInputFile[] = [];

  for (const item of files) {
    const input = toInputFile(item);
    const normalized = normalizeUploadPath(input.path);
    if (!normalized.ok) {
      skipped.push({ path: input.path, reason: normalized.reason });
      continue;
    }
    if (isHiddenPath(normalized.path)) {
      skipped.push({ path: normalized.path, reason: "hidden_file" });
      continue;
    }
    if (isMetadataFile(normalized.path)) {
      skipped.push({ path: normalized.path, reason: "metadata_file" });
      continue;
    }
    normalizedInputs.push({ ...input, path: normalized.path });
  }

  const skillRoots = normalizedInputs
    .filter((input) =>
      basename(input.path) === "SKILL.md" &&
      (input.skippedReason !== undefined ||
        (!!input.file && input.file.size <= MAX_SKILL_FILE_BYTES)),
    )
    .map((input) => dirname(input.path))
    .sort()
    .filter((root, index, roots) => !hasAncestorSkillRoot(root, roots.slice(0, index)));

  const readFiles: ReadUploadFile[] = [];
  const supportingFileCountsByRoot = new Map<string, number>();
  const bytesByRoot = new Map<string, number>();

  for (const input of normalizedInputs) {
    if (input.skippedReason) {
      skipped.push({ path: input.path, reason: input.skippedReason });
      continue;
    }
    if (!input.file) continue;
    const root = nearestSkillRoot(input.path, skillRoots);
    if (root === null) {
      if (basename(input.path) === "SKILL.md" && input.file.size > MAX_SKILL_FILE_BYTES) {
        skipped.push({ path: input.path, reason: "file_too_large" });
      }
      continue;
    }
    if (input.file.size > MAX_SKILL_FILE_BYTES) {
      skipped.push({ path: input.path, reason: "file_too_large" });
      continue;
    }

    const isSupportingFile = input.path !== joinPath(root, "SKILL.md");
    if (isSupportingFile && (supportingFileCountsByRoot.get(root) ?? 0) >= MAX_SKILL_FILES) {
      skipped.push({ path: input.path, reason: "too_many_files" });
      continue;
    }
    if (isSupportingFile && (bytesByRoot.get(root) ?? 0) >= MAX_SKILL_ACCEPTED_BYTES) {
      skipped.push({ path: input.path, reason: "bundle_too_large" });
      continue;
    }

    const content = await readUtf8Text(input.file);
    if (content === null) {
      skipped.push({ path: input.path, reason: "binary_file" });
      continue;
    }
    if (content.includes("\u0000")) {
      skipped.push({ path: input.path, reason: "binary_file" });
      continue;
    }
    if (isSupportingFile) {
      supportingFileCountsByRoot.set(root, (supportingFileCountsByRoot.get(root) ?? 0) + 1);
    }
    const fileBytes = utf8ByteLength(content);
    bytesByRoot.set(root, (bytesByRoot.get(root) ?? 0) + fileBytes);
    readFiles.push({ path: input.path, content: content.replaceAll("\u0000", "") });
  }

  if (skillRoots.length === 0) {
    const hasSkippedSkillMd = skipped.some(
      (s) => basename(s.path) === "SKILL.md" && (s.reason === "binary_file" || s.reason === "file_too_large"),
    );
    return [
      {
        id: "missing-skill-md",
        root: sourceLabel,
        label: sourceLabel,
        valid: false,
        reason: hasSkippedSkillMd ? "unreadable_skill_md" : "missing_skill_md",
        name: folderName(sourceLabel),
        description: "",
        content: "",
        fileCount: 0,
        files: [],
        skipped,
        selected: false,
      },
    ];
  }

  const groups = new Map<string, SkillGroup>();
  const invalidGroups = new Map<string, InvalidSkillGroup>();
  for (const root of skillRoots) {
    const skillFile = readFiles.find((file) => file.path === joinPath(root, "SKILL.md"));
    if (skillFile) {
      groups.set(root, { root, skillFile, files: [], skipped: [] });
    } else {
      invalidGroups.set(root, { root, skipped: [] });
    }
  }

  for (const file of readFiles) {
    const root = nearestSkillRoot(file.path, skillRoots);
    if (root === null) continue;
    if (file.path === joinPath(root, "SKILL.md")) continue;
    groups.get(root)?.files.push({
      path: relativePath(root, file.path),
      content: file.content,
    });
  }

  for (const item of skipped) {
    const root = nearestSkillRoot(item.path, skillRoots);
    if (root !== null) {
      groups.get(root)?.skipped.push(item);
      invalidGroups.get(root)?.skipped.push(item);
    }
  }

  return [
    ...Array.from(groups.values()).map((group) => buildCandidate(group, sourceLabel)),
    ...Array.from(invalidGroups.values()).map((group) => buildInvalidCandidate(group, sourceLabel)),
  ].sort((a, b) => a.root.localeCompare(b.root));
}

export function candidateToImportRequest(candidate: LocalSkillCandidate): ImportLocalSkillRequest {
  return {
    name: candidate.name,
    description: candidate.description,
    content: candidate.content,
    files: candidate.files,
    source: { type: "uploaded_bundle", label: candidate.label },
  };
}

export async function readZipFile(file: File): Promise<LocalSkillInputFile[]> {
  const zip = await JSZip.loadAsync(await file.arrayBuffer());
  const entries: LocalSkillInputFile[] = [];
  const fileEntries = Object.values(zip.files).filter((entry) => !entry.dir);
  const normalizedEntries: {
    entry?: JSZip.JSZipObject;
    path: string;
    skippedReason?: SkippedLocalSkillFileReason;
  }[] = [];

  for (const entry of fileEntries) {
    const unsafeName = (entry as typeof entry & { unsafeOriginalName?: string }).unsafeOriginalName ?? entry.name;
    const original = normalizeUploadPath(unsafeName);
    if (!original.ok) {
      throw new Error(original.reason);
    }
    const normalized = normalizeUploadPath(entry.name);
    if (!normalized.ok) {
      throw new Error(normalized.reason);
    }
    if (zipEntryUncompressedSize(entry) > MAX_SKILL_FILE_BYTES) {
      normalizedEntries.push({
        path: normalized.path,
        skippedReason: "file_too_large",
      });
      continue;
    }
    normalizedEntries.push({ entry, path: normalized.path });
  }

  const skillRoots = normalizedEntries
    .filter((item) => basename(item.path) === "SKILL.md" && (item.entry || item.skippedReason))
    .map((item) => dirname(item.path))
    .sort()
    .filter((root, index, roots) => !hasAncestorSkillRoot(root, roots.slice(0, index)));

  const bytesByRoot = new Map<string, number>();
  const fileCountsByRoot = new Map<string, number>();
  for (const { entry, path, skippedReason } of normalizedEntries) {
    const root = nearestSkillRoot(path, skillRoots);
    if (root === null) continue;
    if (skippedReason) {
      entries.push({ path, skippedReason });
      continue;
    }
    if (!entry) continue;
    if (isHiddenPath(path) || isMetadataFile(path)) {
      entries.push({
        path,
        skippedReason: isHiddenPath(path) ? "hidden_file" : "metadata_file",
      });
      continue;
    }
    const isSupportingFile = path !== joinPath(root, "SKILL.md");
    if (isSupportingFile && (bytesByRoot.get(root) ?? 0) >= MAX_SKILL_ACCEPTED_BYTES) {
      entries.push({ path, skippedReason: "bundle_too_large" });
      continue;
    }
    if (isSupportingFile && (fileCountsByRoot.get(root) ?? 0) >= MAX_SKILL_FILES * 2) {
      entries.push({ path, skippedReason: "too_many_files" });
      continue;
    }
    const blob = await entry.async("blob");
    bytesByRoot.set(root, (bytesByRoot.get(root) ?? 0) + zipEntryUncompressedSize(entry));
    if (isSupportingFile) {
      fileCountsByRoot.set(root, (fileCountsByRoot.get(root) ?? 0) + 1);
    }
    entries.push({
      path,
      file: new File([blob], basename(path)),
    });
  }
  return entries;
}

function zipEntryUncompressedSize(entry: JSZip.JSZipObject): number {
  const withData = entry as JSZip.JSZipObject & {
    _data?: { uncompressedSize?: number };
  };
  return withData._data?.uncompressedSize ?? 0;
}

export async function filesFromDataTransfer(items: DataTransferItemList): Promise<LocalSkillInputFile[]> {
  const files: LocalSkillInputFile[] = [];
  for (const item of Array.from(items)) {
    const entry = getDroppedEntry(item);
    if (entry) {
      files.push(...(await readEntry(entry, "")));
      continue;
    }
    const file = item.getAsFile();
    if (file) files.push({ path: file.name, file });
  }
  return files;
}

function buildCandidate(group: SkillGroup, sourceLabel: string): LocalSkillCandidate {
  const defaults = parseSkillFrontmatter(group.skillFile.content);
  const acceptedFiles = group.files.slice(0, MAX_SKILL_FILES);
  const skipped = [...group.skipped];

  if (group.files.length > MAX_SKILL_FILES) {
    for (const file of group.files.slice(MAX_SKILL_FILES)) {
      skipped.push({ path: joinPath(group.root, file.path), reason: "too_many_files" });
    }
  }

  let total = utf8ByteLength(group.skillFile.content);
  const sizeLimitedFiles: { path: string; content: string }[] = [];
  for (const file of acceptedFiles) {
    const fileBytes = utf8ByteLength(file.content);
    if (total + fileBytes > MAX_SKILL_ACCEPTED_BYTES) {
      skipped.push({ path: joinPath(group.root, file.path), reason: "bundle_too_large" });
      continue;
    }
    total += fileBytes;
    sizeLimitedFiles.push(file);
  }

  const name = defaults.name || folderName(group.root || sourceLabel);
  return {
    id: group.root || sourceLabel,
    root: group.root,
    label: sourceLabelForGroup(group.root, sourceLabel),
    valid: true,
    name,
    description: defaults.description || "",
    content: group.skillFile.content,
    fileCount: 1 + sizeLimitedFiles.length,
    files: sizeLimitedFiles,
    skipped,
    selected: true,
  };
}

function buildInvalidCandidate(group: InvalidSkillGroup, sourceLabel: string): LocalSkillCandidate {
  const skillMdPath = joinPath(group.root, "SKILL.md");
  const skillMdSkipped = group.skipped.some(
    (s) => s.path === skillMdPath && (s.reason === "binary_file" || s.reason === "file_too_large"),
  );
  return {
    id: group.root || sourceLabel,
    root: group.root,
    label: sourceLabelForGroup(group.root, sourceLabel),
    valid: false,
    reason: skillMdSkipped ? "unreadable_skill_md" : "missing_skill_md",
    name: folderName(group.root || sourceLabel),
    description: "",
    content: "",
    fileCount: 0,
    files: [],
    skipped: group.skipped,
    selected: false,
  };
}

function sourceLabelForGroup(root: string, sourceLabel: string): string {
  if (!root) return sourceLabel;
  if (!sourceLabel) return root;
  if (root === sourceLabel || root.startsWith(`${sourceLabel}/`)) return root;
  return `${sourceLabel}/${root}`;
}

function utf8ByteLength(value: string): number {
  return textEncoder.encode(value).byteLength;
}

async function readUtf8Text(file: File): Promise<string | null> {
  try {
    return utf8Decoder.decode(await file.arrayBuffer());
  } catch {
    return null;
  }
}

function toInputFile(item: File | LocalSkillInputFile): LocalSkillInputFile {
  if ("path" in item && ("file" in item || "skippedReason" in item)) return item;
  const file = item as File & { webkitRelativePath?: string };
  return { path: file.webkitRelativePath || file.name, file };
}

function isHiddenPath(path: string): boolean {
  return path.split("/").some((part) => part.startsWith("."));
}

function isMetadataFile(path: string): boolean {
  return metadataFiles.has(basename(path).toLowerCase());
}

function basename(path: string): string {
  return path.split("/").filter(Boolean).at(-1) || "";
}

function dirname(path: string): string {
  const parts = path.split("/").filter(Boolean);
  return parts.slice(0, -1).join("/");
}

function folderName(path: string): string {
  return basename(path) || "Untitled Skill";
}

function joinPath(root: string, path: string): string {
  return root ? `${root}/${path}` : path;
}

function relativePath(root: string, path: string): string {
  if (!root) return path;
  return path.startsWith(`${root}/`) ? path.slice(root.length + 1) : path;
}

function nearestSkillRoot(path: string, roots: string[]): string | null {
  const sorted = [...roots].sort((a, b) => b.length - a.length);
  return sorted.find((root) => path === root || path.startsWith(`${root}/`) || (!root && path)) ?? null;
}

function hasAncestorSkillRoot(root: string, previousRoots: string[]): boolean {
  return previousRoots.some((candidate) => candidate === "" || root.startsWith(`${candidate}/`));
}

type DroppedEntry = FileSystemFileEntry | FileSystemDirectoryEntry;

function getDroppedEntry(item: DataTransferItem): DroppedEntry | null {
  const withEntry = item as DataTransferItem & {
    webkitGetAsEntry?: () => FileSystemEntry | null;
  };
  const entry = withEntry.webkitGetAsEntry?.() ?? null;
  if (!entry) return null;
  return entry as DroppedEntry;
}

async function readEntry(entry: DroppedEntry, prefix: string): Promise<LocalSkillInputFile[]> {
  if (entry.isFile) {
    const file = await fileFromEntry(entry as FileSystemFileEntry);
    return [{ path: joinPath(prefix, file.name), file }];
  }

  const directory = entry as FileSystemDirectoryEntry;
  const children = await readAllDirectoryEntries(directory);
  const childPrefix = joinPath(prefix, directory.name);
  const nested = await Promise.all(children.map((child) => readEntry(child, childPrefix)));
  return nested.flat();
}

function fileFromEntry(entry: FileSystemFileEntry): Promise<File> {
  return new Promise((resolve, reject) => entry.file(resolve, reject));
}

async function readAllDirectoryEntries(entry: FileSystemDirectoryEntry): Promise<DroppedEntry[]> {
  const reader = entry.createReader();
  const entries: DroppedEntry[] = [];
  while (true) {
    const batch = await new Promise<DroppedEntry[]>((resolve, reject) => {
      reader.readEntries((items) => resolve(items as DroppedEntry[]), reject);
    });
    if (batch.length === 0) return entries;
    entries.push(...batch);
  }
}
