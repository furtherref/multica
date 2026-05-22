"use client";

import { useMemo, useState } from "react";
import {
  Check,
  ChevronDown,
  ChevronUp,
  Copy,
  SquareSplitHorizontal,
  SquareSplitVertical,
} from "lucide-react";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
import { useT } from "../../i18n";

type DiffViewMode = "unified" | "split";

interface DiffViewerProps {
  output?: string;
  oldText?: string;
  newText?: string;
  filePath?: string;
  defaultMode?: DiffViewMode;
}

interface DiffLine {
  type: "add" | "del" | "context" | "hunk" | "file";
  text: string;
}

interface SplitRow {
  type: "add" | "del" | "context" | "pair" | "hunk" | "file";
  left: string;
  right: string;
}

const splitCellBaseClass = "w-1/2 whitespace-pre-wrap break-all px-1 py-0.5";

function parseUnifiedDiff(text: string): DiffLine[] {
  const lines: DiffLine[] = [];
  for (const line of text.split("\n")) {
    if (line.startsWith("--- ") || line.startsWith("+++ ")) {
      lines.push({ type: "file", text: line });
      continue;
    }
    if (line.startsWith("@@ ")) {
      lines.push({ type: "hunk", text: line });
      continue;
    }
    if (line.startsWith("+") && !line.startsWith("+++ ")) {
      lines.push({ type: "add", text: line });
      continue;
    }
    if (line.startsWith("-") && !line.startsWith("--- ")) {
      lines.push({ type: "del", text: line });
      continue;
    }
    lines.push({ type: "context", text: line });
  }
  return lines;
}

function splitTextForDiff(text: string): string[] {
  if (text.length === 0) return [];

  const lines = text.split("\n");
  if (lines[lines.length - 1] === "") {
    lines.pop();
  }
  return lines;
}

function buildDiffFromOldNew(oldText: string, newText: string, filePath?: string): string {
  const oldLines = splitTextForDiff(oldText);
  const newLines = splitTextForDiff(newText);
  const oldStart = oldLines.length > 0 ? 1 : 0;
  const newStart = newLines.length > 0 ? 1 : 0;
  const path = filePath ?? "file";
  const lines: string[] = [
    `--- ${path}`,
    `+++ ${path}`,
    `@@ -${oldStart},${oldLines.length} +${newStart},${newLines.length} @@`,
    ...oldLines.map((line) => `-${line}`),
    ...newLines.map((line) => `+${line}`),
  ];
  return lines.join("\n");
}

function stripDiffPrefix(line: string, type: DiffLine["type"]): string {
  if (type === "add" || type === "del") {
    return line.slice(1);
  }
  if (type === "context" && line.startsWith(" ")) {
    return line.slice(1);
  }
  return line;
}

function buildSplitRows(lines: DiffLine[]): SplitRow[] {
  const rows: SplitRow[] = [];
  let i = 0;
  while (i < lines.length) {
    const current = lines[i]!;

    if (current.type === "file" || current.type === "hunk") {
      rows.push({
        type: current.type,
        left: current.text,
        right: current.text,
      });
      i += 1;
      continue;
    }

    if (current.type === "del") {
      const deletions: DiffLine[] = [];
      const additions: DiffLine[] = [];
      while (lines[i]?.type === "del") {
        deletions.push(lines[i]!);
        i += 1;
      }
      while (lines[i]?.type === "add") {
        additions.push(lines[i]!);
        i += 1;
      }

      const rowCount = Math.max(deletions.length, additions.length);
      for (let rowIndex = 0; rowIndex < rowCount; rowIndex++) {
        const deletion = deletions[rowIndex];
        const addition = additions[rowIndex];
        if (deletion && addition) {
          rows.push({
            type: "pair",
            left: stripDiffPrefix(deletion.text, "del"),
            right: stripDiffPrefix(addition.text, "add"),
          });
        } else if (deletion) {
          rows.push({
            type: "del",
            left: stripDiffPrefix(deletion.text, "del"),
            right: "",
          });
        } else if (addition) {
          rows.push({
            type: "add",
            left: "",
            right: stripDiffPrefix(addition.text, "add"),
          });
        }
      }
      continue;
    }

    if (current.type === "add") {
      const additions: DiffLine[] = [];
      while (lines[i]?.type === "add") {
        additions.push(lines[i]!);
        i += 1;
      }
      for (const addition of additions) {
        rows.push({
          type: "add",
          left: "",
          right: stripDiffPrefix(addition.text, "add"),
        });
      }
      continue;
    }

    rows.push({
      type: "context",
      left: stripDiffPrefix(current.text, "context"),
      right: stripDiffPrefix(current.text, "context"),
    });
    i += 1;
  }
  return rows;
}

export function DiffViewer({
  output,
  oldText,
  newText,
  filePath,
  defaultMode = "unified",
}: DiffViewerProps) {
  const { t } = useT("agents");
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  const [copyFailed, setCopyFailed] = useState(false);
  const [mode, setMode] = useState<DiffViewMode>(defaultMode);
  const nextMode: DiffViewMode = mode === "unified" ? "split" : "unified";

  const diffText = useMemo(() => {
    if (oldText != null || newText != null) {
      return buildDiffFromOldNew(oldText ?? "", newText ?? "", filePath);
    }
    if (output && output.length > 0) return output;
    return "";
  }, [output, oldText, newText, filePath]);

  const lines = useMemo(() => parseUnifiedDiff(diffText), [diffText]);
  const hasDiffStructure = lines.some(
    (line) =>
      line.type === "add" ||
      line.type === "del" ||
      line.type === "file" ||
      line.type === "hunk",
  );
  const toggleDiffLabel =
    nextMode === "split"
      ? t(($) => $.transcript.switch_to_diff_split)
      : t(($) => $.transcript.switch_to_diff_unified);
  const isLong = lines.length > 100;
  const displayLines = expanded || !isLong ? lines : lines.slice(0, 100);
  const splitRows = useMemo(() => buildSplitRows(displayLines), [displayLines]);
  const truncated = !expanded && isLong;
  let copyLabel = t(($) => $.transcript.copy_diff);
  if (copyFailed) {
    copyLabel = t(($) => $.transcript.copy_failed);
  } else if (copied) {
    copyLabel = t(($) => $.transcript.copied);
  }

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(diffText);
      setCopyFailed(false);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      setCopied(false);
      setCopyFailed(true);
      setTimeout(() => setCopyFailed(false), 2000);
    }
  };

  return (
    <div className="overflow-hidden rounded">
      <div className="flex items-center justify-between border-b bg-muted/60 px-3 py-1.5">
        <span className="text-[10px] text-muted-foreground">
          {hasDiffStructure ? t(($) => $.transcript.file_changes) : t(($) => $.transcript.file_content)}
        </span>
        <div className="flex items-center gap-1.5">
          <Tooltip>
            <TooltipTrigger
              render={<button type="button" />}
              aria-label={toggleDiffLabel}
              className="flex size-6 items-center justify-center rounded-full border border-border bg-background text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
              onClick={() => setMode(nextMode)}
            >
              {nextMode === "split" ? (
                <SquareSplitVertical className="size-3.5" />
              ) : (
                <SquareSplitHorizontal className="size-3.5" />
              )}
            </TooltipTrigger>
            <TooltipContent>{toggleDiffLabel}</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger
              render={<button type="button" />}
              aria-label={copyLabel}
              className={
                copyFailed
                  ? "text-destructive transition-colors hover:text-destructive"
                  : "text-muted-foreground transition-colors hover:text-foreground"
              }
              onClick={handleCopy}
            >
              {copied ? <Check className="size-3" /> : <Copy className="size-3" />}
            </TooltipTrigger>
            <TooltipContent>{copyLabel}</TooltipContent>
          </Tooltip>
        </div>
      </div>

      {!hasDiffStructure ? (
        <div className="p-3 text-[11px] text-muted-foreground">
          {t(($) => $.transcript.no_visual_diff)}
        </div>
      ) : (
        <div className="max-h-96 overflow-auto p-3 text-[11px] font-mono leading-relaxed">
          {mode === "unified" ? (
            <div className="whitespace-pre-wrap break-all">
              {displayLines.map((line, i) => (
                <div
                  key={i}
                  className={
                    line.type === "add"
                      ? "bg-success/10 text-success"
                      : line.type === "del"
                        ? "bg-destructive/10 text-destructive"
                        : line.type === "hunk" || line.type === "file"
                          ? "text-info"
                          : "text-muted-foreground"
                  }
                >
                  {line.text}
                </div>
              ))}
            </div>
          ) : (
            <table className="w-full table-fixed border-collapse">
              <tbody>
                {splitRows.map((row, i) => {
                  if (row.type === "file" || row.type === "hunk") {
                    return (
                      <tr key={i}>
                        <td colSpan={2} className="whitespace-pre-wrap break-all px-1 py-0.5 text-info">
                          {row.left}
                        </td>
                      </tr>
                    );
                  }

                  return (
                    <tr key={i}>
                      <td
                        className={
                          row.type === "del" || row.type === "pair"
                            ? `${splitCellBaseClass} bg-destructive/10 text-destructive`
                            : `${splitCellBaseClass} text-muted-foreground`
                        }
                      >
                        {row.left}
                      </td>
                      <td
                        className={
                          row.type === "add" || row.type === "pair"
                            ? `${splitCellBaseClass} bg-success/10 text-success`
                            : `${splitCellBaseClass} text-muted-foreground`
                        }
                      >
                        {row.right}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}

          {truncated && (
            <button
              type="button"
              className="mt-1 flex items-center gap-1 text-[10px] text-primary hover:underline"
              onClick={() => setExpanded(true)}
            >
              <ChevronDown className="size-3" />
              {t(($) => $.transcript.show_all_lines, { count: lines.length })}
            </button>
          )}
          {expanded && isLong && (
            <button
              type="button"
              className="mt-1 flex items-center gap-1 text-[10px] text-primary hover:underline"
              onClick={() => setExpanded(false)}
            >
              <ChevronUp className="size-3" />
              {t(($) => $.transcript.collapse)}
            </button>
          )}
        </div>
      )}
    </div>
  );
}
