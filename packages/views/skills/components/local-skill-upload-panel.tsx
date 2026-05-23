"use client";

import { useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { AlertCircle, CheckCircle2, FileArchive, FolderUp, Loader2, Upload } from "lucide-react";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import type { ImportLocalSkillResult, Skill } from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  skillDetailOptions,
  workspaceKeys,
} from "@multica/core/workspace/queries";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useScrollFade } from "@multica/ui/hooks/use-scroll-fade";
import { useT } from "../../i18n";
import {
  buildLocalSkillCandidates,
  candidateToImportRequest,
  filesFromDataTransfer,
  MAX_LOCAL_SKILL_IMPORT_BATCH,
  readZipFile,
  type LocalSkillCandidate,
  type LocalSkillInputFile,
} from "../utils/local-skill-upload";

interface ImportSummary {
  created: { skill: Skill; source_label: string }[];
  skipped: ImportLocalSkillResult[];
  failed: ImportLocalSkillResult[];
}

interface ResultReasonLabels {
  already_exists: string;
  missing_skill_md: string;
  invalid_file_path: string;
  hidden_file: string;
  metadata_file: string;
  absolute_path: string;
  path_traversal: string;
  file_too_large: string;
  binary_file: string;
  too_many_files: string;
  bundle_too_large: string;
  imported: string;
}

export function LocalSkillUploadPanel({
  onImported,
}: {
  onImported?: (skill: Skill) => void;
}) {
  const { t } = useT("skills");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const folderInputRef = useRef<HTMLInputElement>(null);
  const zipInputRef = useRef<HTMLInputElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const fadeStyle = useScrollFade(scrollRef);

  const [candidates, setCandidates] = useState<LocalSkillCandidate[]>([]);
  const [error, setError] = useState("");
  const [importing, setImporting] = useState(false);
  const [doneCount, setDoneCount] = useState(0);
  const [summary, setSummary] = useState<ImportSummary | null>(null);

  const selectedCandidates = useMemo(
    () => candidates.filter((candidate) => candidate.valid && candidate.selected && candidate.name.trim()),
    [candidates],
  );
  const validCandidates = candidates.filter((candidate) => candidate.valid);
  const invalidCandidate = candidates.find((candidate) => !candidate.valid);
  const overBatchLimit = validCandidates.length > MAX_LOCAL_SKILL_IMPORT_BATCH;

  const setCandidate = (id: string, patch: Partial<LocalSkillCandidate>) => {
    setCandidates((prev) =>
      prev.map((candidate) =>
        candidate.id === id ? { ...candidate, ...patch } : candidate,
      ),
    );
  };

  const handleFiles = async (files: File[] | LocalSkillInputFile[], label: string) => {
    setError("");
    setSummary(null);
    try {
      const next = await buildLocalSkillCandidates(files, label);
      setCandidates(limitSelectedCandidates(next));
    } catch (err) {
      setCandidates([]);
      setError(err instanceof Error ? err.message : t(($) => $.upload_import.errors.unreadable_zip));
    }
  };

  const handleFolderChange = async (files: FileList | null) => {
    if (!files || files.length === 0) {
      setError(t(($) => $.upload_import.errors.empty_selection));
      return;
    }
    const list = Array.from(files);
    await handleFiles(list, rootLabel(list[0]?.webkitRelativePath || list[0]?.name || "upload"));
  };

  const handleZipChange = async (files: FileList | null) => {
    const file = files?.[0];
    if (!file) {
      setError(t(($) => $.upload_import.errors.empty_selection));
      return;
    }
    await handleZipFile(file);
  };

  const handleZipFile = async (file: File) => {
    try {
      const entries = await readZipFile(file);
      await handleFiles(entries, file.name);
    } catch {
      setCandidates([]);
      setError(t(($) => $.upload_import.errors.unreadable_zip));
    }
  };

  const handleDrop = async (event: React.DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    const droppedFiles = Array.from(event.dataTransfer.files);
    const droppedZip = droppedFiles.length === 1 ? droppedFiles[0] : null;
    if (droppedZip && isZipFile(droppedZip)) {
      await handleZipFile(droppedZip);
      return;
    }
    if (event.dataTransfer.items.length > 0) {
      const files = await filesFromDataTransfer(event.dataTransfer.items);
      await handleFiles(files, t(($) => $.upload_import.drop_label));
      return;
    }
    await handleFolderChange(event.dataTransfer.files);
  };

  const handleImport = async () => {
    if (selectedCandidates.length === 0) return;
    setImporting(true);
    setError("");
    setSummary(null);
    setDoneCount(0);
    try {
      const payload = {
        skills: selectedCandidates.map(candidateToImportRequest),
      };
      const result = await api.importLocalSkills(payload);
      for (const item of result.created) {
        qc.setQueryData(skillDetailOptions(wsId, item.skill.id).queryKey, item.skill);
      }
      await Promise.all([
        qc.invalidateQueries({ queryKey: workspaceKeys.skills(wsId) }),
        qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) }),
      ]);
      setDoneCount(result.created.length);
      setSummary(result);
      if (result.created.length > 0 && result.skipped.length === 0 && result.failed.length === 0) {
        toast.success(t(($) => $.upload_import.toast_imported));
        onImported?.(result.created[0]!.skill);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : t(($) => $.upload_import.errors.import_failed));
    } finally {
      setImporting(false);
    }
  };

  const readyLabel =
    selectedCandidates.length === 1
      ? t(($) => $.upload_import.ready_single, { name: selectedCandidates[0]?.name ?? "" })
      : t(($) => $.upload_import.ready_multiple, { count: selectedCandidates.length });

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="shrink-0 border-b px-5 py-3">
        <div
          onDragOver={(event) => event.preventDefault()}
          onDrop={handleDrop}
          className="flex flex-col items-center justify-center rounded-lg border border-dashed bg-muted/20 px-4 py-5 text-center"
        >
          <Upload className="h-5 w-5 text-muted-foreground" />
          <p className="mt-2 text-sm font-medium">{t(($) => $.upload_import.drop_title)}</p>
          <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.upload_import.hint)}</p>
          <div className="mt-3 flex flex-wrap justify-center gap-2">
            <Button type="button" size="sm" variant="outline" onClick={() => folderInputRef.current?.click()}>
              <FolderUp className="h-3.5 w-3.5" />
              {t(($) => $.upload_import.choose_folder)}
            </Button>
            <Button type="button" size="sm" variant="outline" onClick={() => zipInputRef.current?.click()}>
              <FileArchive className="h-3.5 w-3.5" />
              {t(($) => $.upload_import.choose_zip)}
            </Button>
          </div>
          <input
            ref={folderInputRef}
            data-testid="local-skill-folder-input"
            type="file"
            multiple
            className="hidden"
            onChange={(event) => {
              handleFolderChange(event.currentTarget.files);
              event.currentTarget.value = "";
            }}
            {...{ webkitdirectory: "" }}
          />
          <input
            ref={zipInputRef}
            data-testid="local-skill-zip-input"
            type="file"
            accept=".zip,application/zip"
            className="hidden"
            onChange={(event) => {
              handleZipChange(event.currentTarget.files);
              event.currentTarget.value = "";
            }}
          />
        </div>
      </div>

      <div
        ref={scrollRef}
        style={fadeStyle}
        className="flex-1 min-h-0 overflow-y-auto px-5 py-3"
        aria-disabled={importing || undefined}
      >
        {error && <AlertMessage tone="destructive">{error}</AlertMessage>}
        {overBatchLimit && (
          <AlertMessage tone="destructive">
            {t(($) => $.upload_import.errors.too_many_skills, { count: MAX_LOCAL_SKILL_IMPORT_BATCH })}
          </AlertMessage>
        )}
        {invalidCandidate?.reason === "missing_skill_md" && (
          <AlertMessage tone="destructive">
            {t(($) => $.upload_import.errors.no_skill_md)}
          </AlertMessage>
        )}
        {invalidCandidate?.reason === "unreadable_skill_md" && (
          <AlertMessage tone="destructive">
            {t(($) => $.upload_import.errors.unreadable_skill_md)}
          </AlertMessage>
        )}

        {validCandidates.length === 1 && (
          <SingleSkillPreview
            candidate={validCandidates[0]!}
            onChange={(patch) => setCandidate(validCandidates[0]!.id, patch)}
          />
        )}
        {validCandidates.length > 1 && (
          <MultiSkillPreview
            candidates={candidates}
            selectedCount={selectedCandidates.length}
            onChange={setCandidate}
          />
        )}

        {summary && <ResultSummary summary={summary} />}
      </div>

      <div className="flex shrink-0 items-center gap-3 border-t bg-muted/30 px-5 py-3">
        <div className="min-w-0 flex-1 text-xs text-muted-foreground">
          {selectedCandidates.length > 0
            ? readyLabel
            : t(($) => $.upload_import.select_skill)}
        </div>
        <Button
          type="button"
          size="sm"
          onClick={handleImport}
          disabled={selectedCandidates.length === 0 || importing}
        >
          {importing ? (
            <>
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
              {t(($) => $.upload_import.importing, { done: doneCount, total: selectedCandidates.length })}
            </>
          ) : (
            t(($) => $.create.url.import)
          )}
        </Button>
      </div>
    </div>
  );
}

function SingleSkillPreview({
  candidate,
  onChange,
}: {
  candidate: LocalSkillCandidate;
  onChange: (patch: Partial<LocalSkillCandidate>) => void;
}) {
  const { t } = useT("skills");
  return (
    <div className="space-y-3">
      <div className="rounded-lg border bg-card p-3">
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1">
            <Label className="text-xs text-muted-foreground">{t(($) => $.upload_import.name_label)}</Label>
            <Input value={candidate.name} onChange={(event) => onChange({ name: event.target.value })} />
          </div>
          <div className="space-y-1">
            <Label className="text-xs text-muted-foreground">{t(($) => $.upload_import.source_label)}</Label>
            <Input value={candidate.root || candidate.label} readOnly className="font-mono text-xs" />
          </div>
        </div>
        <div className="mt-3 space-y-1">
          <Label className="text-xs text-muted-foreground">{t(($) => $.upload_import.description_label)}</Label>
          <Textarea
            value={candidate.description}
            onChange={(event) => onChange({ description: event.target.value })}
            rows={2}
            className="resize-none text-sm"
          />
        </div>
        <PreviewMeta candidate={candidate} />
      </div>
    </div>
  );
}

function MultiSkillPreview({
  candidates,
  selectedCount,
  onChange,
}: {
  candidates: LocalSkillCandidate[];
  selectedCount: number;
  onChange: (id: string, patch: Partial<LocalSkillCandidate>) => void;
}) {
  const { t } = useT("skills");
  return (
    <div className="space-y-2">
      {candidates.map((candidate) => (
        <div
          key={candidate.id}
          className={`rounded-lg border p-3 ${candidate.valid ? "bg-card" : "bg-muted/30 opacity-70"}`}
        >
          <div className="flex items-start gap-3">
            <Checkbox
              checked={candidate.selected}
              disabled={!candidate.valid || (!candidate.selected && selectedCount >= MAX_LOCAL_SKILL_IMPORT_BATCH)}
              onCheckedChange={(checked) => onChange(candidate.id, { selected: checked === true })}
              className="mt-1"
            />
            <div className="min-w-0 flex-1 space-y-2">
              <div className="grid gap-2 sm:grid-cols-2">
                <Input
                  value={candidate.name}
                  disabled={!candidate.valid}
                  onChange={(event) => onChange(candidate.id, { name: event.target.value })}
                  aria-label={t(($) => $.upload_import.name_label)}
                />
                <Input
                  value={candidate.description}
                  disabled={!candidate.valid}
                  onChange={(event) => onChange(candidate.id, { description: event.target.value })}
                  aria-label={t(($) => $.upload_import.description_label)}
                />
              </div>
              <PreviewMeta candidate={candidate} />
            </div>
          </div>
        </div>
      ))}
    </div>
  );
}

function PreviewMeta({ candidate }: { candidate: LocalSkillCandidate }) {
  const { t } = useT("skills");
  const reasonLabels = useResultReasonLabels();
  return (
    <div className="mt-3 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
      <Badge variant="outline">{t(($) => $.upload_import.primary_file_badge)}</Badge>
      <Badge variant="secondary">
        {t(($) => $.upload_import.files_count, { count: candidate.fileCount })}
      </Badge>
      {candidate.skipped.length > 0 && (
        <Badge variant="outline">
          {t(($) => $.upload_import.skipped_count, { count: candidate.skipped.length })}
        </Badge>
      )}
      {candidate.skipped.length > 0 && (
        <div className="basis-full space-y-1 pt-1">
          {candidate.skipped.map((item) => (
            <div key={`${item.path}-${item.reason}`} className="truncate">
              {item.path}: {resultReasonLabel(item.reason, reasonLabels)}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function ResultSummary({ summary }: { summary: ImportSummary }) {
  const reasonLabels = useResultReasonLabels();
  return (
    <div className="mt-3 space-y-2">
      {summary.created.map((item) => (
        <ResultLine key={`created-${item.skill.id}`} name={item.skill.name} reason={reasonLabels.imported} ok />
      ))}
      {summary.skipped.map((item) => (
        <ResultLine key={`skipped-${item.name}`} name={item.name} reason={resultReasonLabel(item.reason, reasonLabels)} />
      ))}
      {summary.failed.map((item) => (
        <ResultLine key={`failed-${item.name}`} name={item.name} reason={resultReasonLabel(item.reason, reasonLabels)} />
      ))}
    </div>
  );
}

function ResultLine({ name, reason, ok = false }: { name: string; reason: string; ok?: boolean }) {
  return (
    <div className="flex items-center gap-2 rounded-md border px-3 py-2 text-xs">
      {ok ? (
        <CheckCircle2 className="h-3.5 w-3.5 text-success" />
      ) : (
        <AlertCircle className="h-3.5 w-3.5 text-warning" />
      )}
      <span>{name} {reason}</span>
    </div>
  );
}

function AlertMessage({ children }: { children: React.ReactNode; tone: "destructive" }) {
  return (
    <div
      role="alert"
      className="mb-3 flex items-start gap-2 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive"
    >
      <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      {children}
    </div>
  );
}

function rootLabel(path: string): string {
  return path.replace(/\\/g, "/").split("/").filter(Boolean)[0] || "upload";
}

function limitSelectedCandidates(candidates: LocalSkillCandidate[]): LocalSkillCandidate[] {
  let selectedValidCount = 0;
  return candidates.map((candidate) => {
    if (!candidate.valid) return candidate;
    selectedValidCount += 1;
    if (selectedValidCount <= MAX_LOCAL_SKILL_IMPORT_BATCH) return candidate;
    return { ...candidate, selected: false };
  });
}

function isZipFile(file: File): boolean {
  return file.name.toLowerCase().endsWith(".zip") || file.type === "application/zip";
}

function useResultReasonLabels(): ResultReasonLabels {
  const { t } = useT("skills");
  return {
    already_exists: t(($) => $.upload_import.result_reasons.already_exists),
    missing_skill_md: t(($) => $.upload_import.result_reasons.missing_skill_md),
    invalid_file_path: t(($) => $.upload_import.result_reasons.invalid_file_path),
    hidden_file: t(($) => $.upload_import.result_reasons.hidden_file),
    metadata_file: t(($) => $.upload_import.result_reasons.metadata_file),
    absolute_path: t(($) => $.upload_import.result_reasons.absolute_path),
    path_traversal: t(($) => $.upload_import.result_reasons.path_traversal),
    file_too_large: t(($) => $.upload_import.result_reasons.file_too_large),
    binary_file: t(($) => $.upload_import.result_reasons.binary_file),
    too_many_files: t(($) => $.upload_import.result_reasons.too_many_files),
    bundle_too_large: t(($) => $.upload_import.result_reasons.bundle_too_large),
    imported: t(($) => $.upload_import.result_reasons.imported),
  };
}

function resultReasonLabel(reason: string, labels: ResultReasonLabels): string {
  return reason in labels ? labels[reason as keyof ResultReasonLabels] : reason.replaceAll("_", " ");
}
