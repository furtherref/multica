"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import {
  AlertCircle,
  ArrowLeft,
  CheckCircle2,
  ChevronRight,
  Download,
  FileArchive,
  FolderOpen,
  HardDrive,
  Loader2,
  Pencil,
  Plus,
  SkipForward,
  Upload,
  X as XIcon,
} from "lucide-react";
import { toast } from "sonner";
import { useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import type { Skill } from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { isImeComposing } from "@multica/core/utils";
import {
  skillDetailOptions,
  workspaceKeys,
} from "@multica/core/workspace/queries";
import {
  Dialog,
  DialogContent,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Progress } from "@multica/ui/components/ui/progress";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useScrollFade } from "@multica/ui/hooks/use-scroll-fade";
import { cn } from "@multica/ui/lib/utils";
import { openExternal } from "../../platform";
import { RuntimeLocalSkillImportPanel } from "./runtime-local-skill-import-panel";
import { useT } from "../../i18n";
import { isNameConflictError } from "../lib/utils";
import {
  parseZipBundles,
  parseFolderBundle,
  ParseError,
  updateFrontmatter,
  type ParsedSkillBundle,
} from "../lib/parse-skill-bundle";

type Method = "chooser" | "manual" | "url" | "runtime" | "upload";

function seedAfterCreate(
  qc: ReturnType<typeof useQueryClient>,
  wsId: string,
  skill: Skill,
) {
  qc.setQueryData(skillDetailOptions(wsId, skill.id).queryKey, skill);
  qc.invalidateQueries({ queryKey: workspaceKeys.skills(wsId) });
  qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
}

// ---------------------------------------------------------------------------
// Chooser — initial method picker (3 cards)
// ---------------------------------------------------------------------------

function MethodChooser({ onChoose }: { onChoose: (m: Method) => void }) {
  const { t } = useT("skills");
  const methods: {
    key: Method;
    icon: typeof Plus;
    titleKey: "manual" | "url" | "runtime" | "upload";
  }[] = [
    { key: "manual", icon: Plus, titleKey: "manual" },
    { key: "url", icon: Download, titleKey: "url" },
    { key: "upload", icon: Upload, titleKey: "upload" },
    { key: "runtime", icon: HardDrive, titleKey: "runtime" },
  ];
  return (
    <div className="grid gap-2 p-5">
      {methods.map(({ key, icon: Icon, titleKey }) => (
        <button
          key={key}
          type="button"
          onClick={() => onChoose(key)}
          className="group flex items-start gap-3 rounded-lg border bg-card p-4 text-left transition-colors hover:border-primary/40 hover:bg-accent/40"
        >
          <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground group-hover:text-foreground">
            <Icon className="h-4 w-4" />
          </div>
          <div className="min-w-0 flex-1">
            <div className="text-sm font-medium">
              {t(($) => $.create.method_card[`${titleKey}_title`])}
            </div>
            <div className="mt-0.5 text-xs text-muted-foreground">
              {t(($) => $.create.method_card[`${titleKey}_desc`])}
            </div>
          </div>
          <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground/40 transition-colors group-hover:text-muted-foreground" />
        </button>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Manual form
// ---------------------------------------------------------------------------

function ManualForm({
  onCreated,
  onCancel,
}: {
  onCreated: (skill: Skill) => void;
  onCancel: () => void;
}) {
  const { t } = useT("skills");
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const scrollRef = useRef<HTMLDivElement>(null);
  const fadeStyle = useScrollFade(scrollRef);

  const submit = async () => {
    const trimmed = name.trim();
    if (!trimmed) return;
    setLoading(true);
    setError("");
    try {
      const skill = await api.createSkill({
        name: trimmed,
        description: description.trim(),
      });
      seedAfterCreate(qc, wsId, skill);
      toast.success(t(($) => $.create.manual.toast_created));
      onCreated(skill);
    } catch (err) {
      setError(err instanceof Error ? err.message : t(($) => $.create.manual.fallback_error));
      setLoading(false);
    }
  };

  return (
    <>
      <div
        ref={scrollRef}
        style={fadeStyle}
        className="flex-1 min-h-0 space-y-4 overflow-y-auto px-5 py-4"
      >
        <div className="space-y-1.5">
          <Label
            htmlFor="create-skill-name"
            className="text-xs text-muted-foreground"
          >
            {t(($) => $.create.manual.name_label)}
          </Label>
          <Input
            id="create-skill-name"
            autoFocus
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              setError("");
            }}
            placeholder={t(($) => $.create.manual.name_placeholder)}
            onKeyDown={(e) => {
              if (isImeComposing(e)) return;
              if (e.key === "Enter") submit();
            }}
          />
          <p className="text-xs text-muted-foreground">
            {t(($) => $.create.manual.name_hint)}
          </p>
        </div>

        <div className="space-y-1.5">
          <Label
            htmlFor="create-skill-desc"
            className="text-xs text-muted-foreground"
          >
            <Pencil className="h-3 w-3" />
            {t(($) => $.create.manual.description_label)}
          </Label>
          <Textarea
            id="create-skill-desc"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder={t(($) => $.create.manual.description_placeholder)}
            rows={3}
            className="resize-none"
          />
        </div>

        {error && (
          <div
            role="alert"
            className="flex items-start gap-2 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive"
          >
            <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
            <span>
              {error}
              {isNameConflictError(error) && (
                <>{t(($) => $.create.manual.name_conflict_hint)}</>
              )}
            </span>
          </div>
        )}
      </div>

      <div className="flex shrink-0 items-center justify-end gap-2 border-t bg-muted/30 px-5 py-3">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={onCancel}
          disabled={loading}
        >
          {t(($) => $.create.manual.cancel)}
        </Button>
        <Button
          type="button"
          size="sm"
          onClick={submit}
          disabled={!name.trim() || loading}
        >
          {loading ? (
            <>
              <Loader2 className="h-3 w-3 animate-spin" />
              {t(($) => $.create.manual.submitting)}
            </>
          ) : (
            t(($) => $.create.manual.submit)
          )}
        </Button>
      </div>
    </>
  );
}

// ---------------------------------------------------------------------------
// URL import form
// ---------------------------------------------------------------------------

type DetectedSource = "clawhub" | "skills.sh" | "github" | null;

function detectUrlSource(url: string): DetectedSource {
  const u = url.trim().toLowerCase();
  if (u.includes("clawhub.ai")) return "clawhub";
  if (u.includes("skills.sh")) return "skills.sh";
  if (u.includes("github.com")) return "github";
  return null;
}

function SourceCard({
  label,
  exampleHost,
  browseUrl,
  active,
}: {
  label: string;
  exampleHost: string;
  browseUrl: string;
  active: boolean;
}) {
  return (
    <div
      className={`rounded-md border px-3 py-2.5 transition-colors ${
        active ? "border-primary bg-primary/5" : ""
      }`}
    >
      <div className="text-xs font-medium">{label}</div>
      <button
        type="button"
        onClick={() => openExternal(browseUrl)}
        className="mt-0.5 block max-w-full truncate text-left font-mono text-xs text-brand underline decoration-brand/40 underline-offset-2 hover:decoration-brand"
      >
        {exampleHost}
      </button>
    </div>
  );
}

function UrlForm({
  onCreated,
  onCancel,
}: {
  onCreated: (skill: Skill) => void;
  onCancel: () => void;
}) {
  const { t } = useT("skills");
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const [url, setUrl] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const source = detectUrlSource(url);
  const scrollRef = useRef<HTMLDivElement>(null);
  const fadeStyle = useScrollFade(scrollRef);

  const submit = async () => {
    const trimmed = url.trim();
    if (!trimmed) return;
    setLoading(true);
    setError("");
    try {
      const skill = await api.importSkill({ url: trimmed });
      seedAfterCreate(qc, wsId, skill);
      toast.success(t(($) => $.create.url.toast_imported));
      onCreated(skill);
    } catch (err) {
      setError(err instanceof Error ? err.message : t(($) => $.create.url.fallback_error));
      setLoading(false);
    }
  };

  const submittingLabel = (() => {
    if (!loading) return t(($) => $.create.url.import);
    if (source === "clawhub") return t(($) => $.create.url.importing_clawhub);
    if (source === "skills.sh") return t(($) => $.create.url.importing_skills_sh);
    if (source === "github") return t(($) => $.create.url.importing_github);
    return t(($) => $.create.url.importing);
  })();

  return (
    <>
      <div
        ref={scrollRef}
        style={fadeStyle}
        className="flex-1 min-h-0 space-y-4 overflow-y-auto px-5 py-4"
      >
        <div className="space-y-1.5">
          <Label htmlFor="import-url" className="text-xs text-muted-foreground">
            {t(($) => $.create.url.url_label)}
          </Label>
          <Input
            id="import-url"
            autoFocus
            value={url}
            onChange={(e) => {
              setUrl(e.target.value);
              setError("");
            }}
            placeholder="https://clawhub.ai/owner/skill"
            className="font-mono text-sm"
            onKeyDown={(e) => {
              if (e.key === "Enter") submit();
            }}
          />
        </div>

        <div>
          <p className="mb-2 text-xs text-muted-foreground">
            {t(($) => $.create.url.supported_sources)}
          </p>
          <div className="grid grid-cols-3 gap-2">
            <SourceCard
              label="ClawHub"
              exampleHost="clawhub.ai/owner/skill"
              browseUrl="https://clawhub.ai"
              active={source === "clawhub"}
            />
            <SourceCard
              label="Skills.sh"
              exampleHost="skills.sh/owner/repo/skill"
              browseUrl="https://skills.sh"
              active={source === "skills.sh"}
            />
            <SourceCard
              label="GitHub"
              exampleHost="github.com/owner/repo"
              browseUrl="https://github.com"
              active={source === "github"}
            />
          </div>
        </div>

        {error && (
          <div
            role="alert"
            className="flex items-start gap-2 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive"
          >
            <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
            <span>
              {error}
              {isNameConflictError(error) && (
                <>{t(($) => $.create.url.name_conflict_hint)}</>
              )}
            </span>
          </div>
        )}
      </div>

      <div className="flex shrink-0 items-center justify-end gap-2 border-t bg-muted/30 px-5 py-3">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={onCancel}
          disabled={loading}
        >
          {t(($) => $.create.url.cancel)}
        </Button>
        <Button
          type="button"
          size="sm"
          onClick={submit}
          disabled={!url.trim() || loading}
        >
          {loading ? (
            <>
              <Loader2 className="h-3 w-3 animate-spin" />
              {submittingLabel}
            </>
          ) : (
            <>
              <Download className="h-3 w-3" />
              {submittingLabel}
            </>
          )}
        </Button>
      </div>
    </>
  );
}

// ---------------------------------------------------------------------------
// Upload form — supports single skill and bulk (multi-skill zip) import
// ---------------------------------------------------------------------------

type UploadBulkResult = {
  name: string;
  status: "success" | "skipped" | "failed";
  error?: string;
  skill?: Skill;
};

type UploadBulkState = {
  phase: "idle" | "importing" | "done" | "cancelled";
  total: number;
  completed: number;
  results: UploadBulkResult[];
};

const INITIAL_UPLOAD_BULK: UploadBulkState = {
  phase: "idle",
  total: 0,
  completed: 0,
  results: [],
};

function UploadBulkSummary({ results }: { results: UploadBulkResult[] }) {
  const { t } = useT("skills");
  const succeeded = results.filter((r) => r.status === "success");
  const skipped = results.filter((r) => r.status === "skipped");
  const failed = results.filter((r) => r.status === "failed");

  return (
    <div className="space-y-4 py-2">
      <div className="grid grid-cols-3 gap-2 text-center">
        <div className="rounded-md bg-green-50 px-3 py-2 dark:bg-green-950/30">
          <div className="text-lg font-semibold text-green-700 dark:text-green-400">
            {succeeded.length}
          </div>
          <div className="text-xs text-muted-foreground">
            {t(($) => $.create.upload.bulk_summary_imported)}
          </div>
        </div>
        <div className="rounded-md bg-yellow-50 px-3 py-2 dark:bg-yellow-950/30">
          <div className="text-lg font-semibold text-yellow-700 dark:text-yellow-400">
            {skipped.length}
          </div>
          <div className="text-xs text-muted-foreground">
            {t(($) => $.create.upload.bulk_summary_skipped)}
          </div>
        </div>
        <div className="rounded-md bg-red-50 px-3 py-2 dark:bg-red-950/30">
          <div className="text-lg font-semibold text-red-700 dark:text-red-400">
            {failed.length}
          </div>
          <div className="text-xs text-muted-foreground">
            {t(($) => $.create.upload.bulk_summary_failed)}
          </div>
        </div>
      </div>

      <div className="max-h-64 space-y-1 overflow-y-auto rounded-md border p-2">
        {results.map((r, i) => (
          <div
            key={i}
            className="flex items-center gap-2 rounded px-2 py-1.5 text-xs"
          >
            {r.status === "success" && (
              <CheckCircle2 className="h-3.5 w-3.5 shrink-0 text-green-600" />
            )}
            {r.status === "skipped" && (
              <SkipForward className="h-3.5 w-3.5 shrink-0 text-yellow-600" />
            )}
            {r.status === "failed" && (
              <AlertCircle className="h-3.5 w-3.5 shrink-0 text-destructive" />
            )}
            <span className="min-w-0 flex-1 truncate">{r.name}</span>
            {r.error && (
              <span className="max-w-[200px] shrink-0 truncate text-muted-foreground">
                {r.error}
              </span>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

function UploadForm({
  onCreated,
  onCancel,
  onBulkDone,
  onWideChange,
}: {
  onCreated: (skill: Skill) => void;
  onCancel: () => void;
  onBulkDone?: () => void;
  onWideChange?: (wide: boolean) => void;
}) {
  const { t } = useT("skills");
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  // Single-skill state
  const [bundle, setBundle] = useState<ParsedSkillBundle | null>(null);
  // Multi-skill state
  const [bundles, setBundles] = useState<ParsedSkillBundle[]>([]);
  const [selectedIndices, setSelectedIndices] = useState<Set<number>>(new Set());
  const [bulkState, setBulkState] = useState<UploadBulkState>(INITIAL_UPLOAD_BULK);
  const cancelRef = useRef(false);

  useEffect(() => {
    return () => {
      cancelRef.current = true;
    };
  }, []);

  const [parsing, setParsing] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [dragOver, setDragOver] = useState(false);
  const zipInputRef = useRef<HTMLInputElement>(null);
  const folderInputRef = useRef<HTMLInputElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const fadeStyle = useScrollFade(scrollRef);

  const isBulk = bundles.length > 1;

  const localizeParseError = useCallback(
    (err: unknown): string => {
      if (err instanceof ParseError) {
        const key = `error_${err.code}` as const;
        return t(($) => $.create.upload[key]);
      }
      return t(($) => $.create.upload.fallback_error);
    },
    [t],
  );

  const handleZipFile = useCallback(
    async (file: File) => {
      setParsing(true);
      setError("");
      setBundle(null);
      setBundles([]);
      setSelectedIndices(new Set());
      setBulkState(INITIAL_UPLOAD_BULK);
      try {
        if (file.size > 50 << 20) {
          throw new Error("Archive exceeds 50 MiB size limit");
        }
        const buf = await file.arrayBuffer();
        const parsed = parseZipBundles(buf, file.name.replace(/\.zip$/i, ""));
        if (parsed.length === 0) {
          throw new ParseError("skill_md_not_found_zip");
        }
        if (parsed.length === 1) {
          setBundle(parsed[0]!);
          onWideChange?.(false);
        } else {
          setBundles(parsed);
          setSelectedIndices(new Set(parsed.map((_, i) => i)));
          onWideChange?.(true);
        }
      } catch (err) {
        setError(localizeParseError(err));
      } finally {
        setParsing(false);
      }
    },
    [onWideChange, localizeParseError],
  );

  const handleFolderFiles = useCallback(
    async (files: FileList) => {
      setParsing(true);
      setError("");
      setBundle(null);
      setBundles([]);
      setSelectedIndices(new Set());
      setBulkState(INITIAL_UPLOAD_BULK);
      try {
        const result = await parseFolderBundle(files);
        setBundle(result);
        onWideChange?.(false);
      } catch (err) {
        setError(localizeParseError(err));
      } finally {
        setParsing(false);
      }
    },
    [onWideChange, localizeParseError],
  );

  const handleZipSelect = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const file = e.target.files?.[0];
      if (!file) return;
      handleZipFile(file);
      e.target.value = "";
    },
    [handleZipFile],
  );

  const handleFolderSelect = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const files = e.target.files;
      if (!files?.length) return;
      handleFolderFiles(files);
      e.target.value = "";
    },
    [handleFolderFiles],
  );

  const handleDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault();
      setDragOver(false);
      const items = e.dataTransfer.items;
      if (!items?.length) return;

      const firstItem = items[0] as DataTransferItem | undefined;
      if (!firstItem || firstItem.kind !== "file") return;
      const file = firstItem.getAsFile();
      if (!file) return;

      if (/\.zip$/i.test(file.name)) {
        handleZipFile(file);
      } else {
        setError(t(($) => $.create.upload.error_drop_zip_only));
      }
    },
    [handleZipFile, t],
  );

  const resetAll = useCallback(() => {
    setBundle(null);
    setBundles([]);
    setSelectedIndices(new Set());
    setBulkState(INITIAL_UPLOAD_BULK);
    setError("");
    onWideChange?.(false);
  }, [onWideChange]);

  // --- Single-skill submit ---
  const submitSingle = async () => {
    if (!bundle) return;
    setLoading(true);
    setError("");
    try {
      const content = updateFrontmatter(
        bundle.content,
        bundle.name,
        bundle.description,
      );
      const skill = await api.createSkill({
        name: bundle.name,
        description: bundle.description,
        content,
        config: { origin: { type: "upload" } },
        files: bundle.files,
      });
      seedAfterCreate(qc, wsId, skill);
      toast.success(t(($) => $.create.upload.toast_created));
      onCreated(skill);
    } catch (err) {
      const msg = err instanceof Error ? err.message : "";
      setError(
        isNameConflictError(msg)
          ? t(($) => $.create.upload.error_name_conflict)
          : msg || t(($) => $.create.upload.fallback_error),
      );
      setLoading(false);
    }
  };

  // --- Bulk import ---
  const handleBulkImport = async () => {
    const toImport = bundles.filter((_, i) => selectedIndices.has(i));
    if (toImport.length === 0) return;

    cancelRef.current = false;
    setBulkState({ phase: "importing", total: toImport.length, completed: 0, results: [] });

    const results: UploadBulkResult[] = [];

    for (const b of toImport) {
      if (cancelRef.current) break;
      try {
        const skill = await api.createSkill({
          name: b.name,
          description: b.description,
          content: b.content,
          config: { origin: { type: "upload" } },
          files: b.files,
        });
        results.push({ name: b.name, status: "success", skill });
      } catch (err) {
        const msg = err instanceof Error ? err.message : "";
        results.push({
          name: b.name,
          status: isNameConflictError(msg) ? "skipped" : "failed",
          error: msg || t(($) => $.create.upload.fallback_error),
        });
      }
      setBulkState((prev) => ({
        ...prev,
        completed: prev.completed + 1,
        results: [...results],
      }));
    }

    qc.invalidateQueries({ queryKey: workspaceKeys.skills(wsId) });
    qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
    for (const r of results) {
      if (r.status === "success" && r.skill) {
        qc.setQueryData(skillDetailOptions(wsId, r.skill.id).queryKey, r.skill);
      }
    }

    setBulkState((prev) => ({
      ...prev,
      phase: cancelRef.current ? "cancelled" : "done",
    }));
  };

  // --- Selection helpers ---
  const toggleIndex = (idx: number) => {
    setSelectedIndices((prev) => {
      const next = new Set(prev);
      if (next.has(idx)) next.delete(idx);
      else next.add(idx);
      return next;
    });
  };

  const toggleAll = () => {
    if (selectedIndices.size === bundles.length) {
      setSelectedIndices(new Set());
    } else {
      setSelectedIndices(new Set(bundles.map((_, i) => i)));
    }
  };

  const allSelected = bundles.length > 0 && selectedIndices.size === bundles.length;

  // --- File picker (shared between single & bulk) ---
  const filePickerContent = (
    <>
      <input
        ref={zipInputRef}
        type="file"
        accept=".zip"
        className="hidden"
        onChange={handleZipSelect}
      />
      <input
        ref={folderInputRef}
        type="file"
        // @ts-expect-error -- webkitdirectory is not in the TS DOM types
        webkitdirectory=""
        className="hidden"
        onChange={handleFolderSelect}
      />
      <div
        onDragOver={(e) => {
          e.preventDefault();
          setDragOver(true);
        }}
        onDragLeave={() => setDragOver(false)}
        onDrop={handleDrop}
        className={cn(
          "flex flex-col items-center justify-center gap-3 rounded-lg border-2 border-dashed px-6 py-8 text-center transition-colors",
          dragOver
            ? "border-primary bg-primary/5"
            : "border-muted-foreground/25 hover:border-muted-foreground/40",
        )}
      >
        <FileArchive className="h-8 w-8 text-muted-foreground/50" />
        <p className="text-sm text-muted-foreground">
          {dragOver
            ? t(($) => $.create.upload.drop_zone_active)
            : t(($) => $.create.upload.drop_zone)}
        </p>
        <p className="text-xs text-muted-foreground/60">
          {t(($) => $.create.upload.or)}
        </p>
        <div className="flex gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => zipInputRef.current?.click()}
          >
            <FileArchive className="h-3 w-3" />
            {t(($) => $.create.upload.browse_zip)}
          </Button>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => folderInputRef.current?.click()}
          >
            <FolderOpen className="h-3 w-3" />
            {t(($) => $.create.upload.browse_folder)}
          </Button>
        </div>
      </div>
    </>
  );

  // --- Render: bulk progress / summary ---
  if (bulkState.phase === "importing") {
    const pct =
      bulkState.total > 0
        ? Math.round((bulkState.completed / bulkState.total) * 100)
        : 0;
    return (
      <>
        <div className="flex-1 min-h-0 space-y-4 overflow-y-auto px-5 py-4">
          <div className="space-y-4 py-4">
            <div className="text-center">
              <Loader2 className="mx-auto h-6 w-6 animate-spin text-primary" />
              <p className="mt-3 text-sm font-medium">
                {t(($) => $.create.upload.bulk_progress, {
                  completed: bulkState.completed,
                  total: bulkState.total,
                })}
              </p>
            </div>
            <Progress value={pct} />
            <div className="max-h-48 space-y-1 overflow-y-auto">
              {bulkState.results.map((r, i) => (
                <div
                  key={i}
                  className="flex items-center gap-2 rounded px-2 py-1 text-xs"
                >
                  {r.status === "success" && (
                    <CheckCircle2 className="h-3.5 w-3.5 shrink-0 text-green-600" />
                  )}
                  {r.status === "skipped" && (
                    <SkipForward className="h-3.5 w-3.5 shrink-0 text-yellow-600" />
                  )}
                  {r.status === "failed" && (
                    <AlertCircle className="h-3.5 w-3.5 shrink-0 text-destructive" />
                  )}
                  <span className="truncate">{r.name}</span>
                </div>
              ))}
            </div>
          </div>
        </div>
        <div className="flex shrink-0 items-center justify-end gap-2 border-t bg-muted/30 px-5 py-3">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => {
              cancelRef.current = true;
            }}
          >
            {t(($) => $.create.upload.bulk_cancel)}
          </Button>
        </div>
      </>
    );
  }

  if (bulkState.phase === "done" || bulkState.phase === "cancelled") {
    return (
      <>
        <div className="flex-1 min-h-0 overflow-y-auto px-5 py-4">
          <p className="mb-2 text-xs text-muted-foreground">
            {bulkState.phase === "done"
              ? t(($) => $.create.upload.bulk_complete_hint)
              : t(($) => $.create.upload.bulk_cancelled_hint)}
          </p>
          <UploadBulkSummary results={bulkState.results} />
        </div>
        <div className="flex shrink-0 items-center justify-end gap-2 border-t bg-muted/30 px-5 py-3">
          <Button
            type="button"
            size="sm"
            onClick={onBulkDone ?? onCancel}
          >
            {t(($) => $.create.upload.bulk_done)}
          </Button>
        </div>
      </>
    );
  }

  // --- Render: main content (file picker / single edit / bulk list) ---
  return (
    <>
      <div
        ref={scrollRef}
        style={fadeStyle}
        className="flex-1 min-h-0 space-y-4 overflow-y-auto px-5 py-4"
      >
        {/* File picker — shown when nothing is loaded yet */}
        {!bundle && !isBulk && !parsing && filePickerContent}

        {parsing && (
          <div className="flex flex-col items-center justify-center gap-2 py-8 text-muted-foreground">
            <Loader2 className="h-5 w-5 animate-spin" />
            <p className="text-sm">{t(($) => $.create.upload.parsing)}</p>
          </div>
        )}

        {/* Single skill editing */}
        {bundle && !parsing && (
          <div className="space-y-3">
            <div className="space-y-1.5">
              <Label className="text-xs text-muted-foreground">
                {t(($) => $.create.upload.parsed_name)}
              </Label>
              <Input
                value={bundle.name}
                onChange={(e) =>
                  setBundle({ ...bundle, name: e.target.value })
                }
              />
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs text-muted-foreground">
                <Pencil className="h-3 w-3" />
                {t(($) => $.create.upload.parsed_description)}
              </Label>
              <Textarea
                value={bundle.description}
                onChange={(e) =>
                  setBundle({ ...bundle, description: e.target.value })
                }
                rows={2}
                className="resize-none"
                placeholder={t(($) => $.create.upload.no_description)}
              />
            </div>
            {bundle.files.length > 0 && (
              <p className="text-xs text-muted-foreground">
                {t(($) => $.create.upload.parsed_files, {
                  count: bundle.files.length,
                })}
              </p>
            )}
            {bundle.truncated && (
              <p className="text-xs text-destructive">
                {t(($) => $.create.upload.truncated_warning)}
              </p>
            )}
            {bundle.skippedBinaryCount > 0 && (
              <p className="text-xs text-muted-foreground">
                {t(($) => $.create.upload.binary_skipped_warning, {
                  count: bundle.skippedBinaryCount,
                })}
              </p>
            )}
            <button
              type="button"
              onClick={resetAll}
              className="text-xs text-muted-foreground underline underline-offset-2 hover:text-foreground"
            >
              {t(($) => $.create.upload.change_file)}
            </button>
          </div>
        )}

        {/* Bulk skill list with checkboxes */}
        {isBulk && !parsing && (
          <div className="space-y-3">
            <div className="flex items-center gap-2">
              <Checkbox
                checked={allSelected}
                onCheckedChange={toggleAll}
              />
              <button
                type="button"
                onClick={toggleAll}
                className="text-xs font-medium text-muted-foreground hover:text-foreground"
              >
                {t(($) => $.create.upload.select_all, {
                  count: bundles.length,
                })}
              </button>
            </div>
            <div className="space-y-2">
              {bundles.map((b, idx) => {
                const checked = selectedIndices.has(idx);
                return (
                  <div
                    key={idx}
                    role="button"
                    tabIndex={0}
                    onClick={() => toggleIndex(idx)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        toggleIndex(idx);
                      }
                    }}
                    className={cn(
                      "flex items-start gap-3 rounded-lg border px-4 py-3 text-left transition-colors cursor-pointer",
                      checked
                        ? "border-primary bg-primary/5"
                        : "hover:bg-accent/40",
                    )}
                  >
                    <Checkbox
                      checked={checked}
                      tabIndex={-1}
                      className="pointer-events-none mt-0.5"
                    />
                    <div className="min-w-0 flex-1">
                      <div className="text-sm font-medium truncate">
                        {b.name}
                      </div>
                      {b.description && (
                        <p className="mt-0.5 text-xs text-muted-foreground line-clamp-2">
                          {b.description}
                        </p>
                      )}
                      {b.files.length > 0 && (
                        <p className="mt-0.5 text-xs text-muted-foreground/60">
                          {t(($) => $.create.upload.parsed_files, {
                            count: b.files.length,
                          })}
                        </p>
                      )}
                      {b.truncated && (
                        <p className="mt-0.5 text-xs text-destructive">
                          {t(($) => $.create.upload.truncated_warning)}
                        </p>
                      )}
                      {b.skippedBinaryCount > 0 && (
                        <p className="mt-0.5 text-xs text-muted-foreground">
                          {t(($) => $.create.upload.binary_skipped_warning, {
                            count: b.skippedBinaryCount,
                          })}
                        </p>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
            <button
              type="button"
              onClick={resetAll}
              className="text-xs text-muted-foreground underline underline-offset-2 hover:text-foreground"
            >
              {t(($) => $.create.upload.change_file)}
            </button>
          </div>
        )}

        {error && (
          <div
            role="alert"
            className="flex items-start gap-2 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive"
          >
            <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
            <span>{error}</span>
          </div>
        )}
      </div>

      {/* Footer */}
      <div className="flex shrink-0 items-center justify-end gap-2 border-t bg-muted/30 px-5 py-3">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => {
            resetAll();
            onCancel();
          }}
          disabled={loading}
        >
          {t(($) => $.create.upload.cancel)}
        </Button>

        {isBulk ? (
          <Button
            type="button"
            size="sm"
            onClick={handleBulkImport}
            disabled={
              selectedIndices.size === 0 ||
              bundles.some((b, i) => selectedIndices.has(i) && b.truncated)
            }
          >
            <Upload className="h-3 w-3" />
            {t(($) => $.create.upload.bulk_import_button, {
              count: selectedIndices.size,
            })}
          </Button>
        ) : (
          <Button
            type="button"
            size="sm"
            onClick={submitSingle}
            disabled={!bundle?.name.trim() || bundle?.truncated || loading}
          >
            {loading ? (
              <>
                <Loader2 className="h-3 w-3 animate-spin" />
                {t(($) => $.create.upload.submitting)}
              </>
            ) : (
              <>
                <Upload className="h-3 w-3" />
                {t(($) => $.create.upload.submit)}
              </>
            )}
          </Button>
        )}
      </div>
    </>
  );
}

// ---------------------------------------------------------------------------
// Root dialog
// ---------------------------------------------------------------------------

export function CreateSkillDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated?: (skill: Skill) => void;
}) {
  const { t } = useT("skills");
  const [method, setMethod] = useState<Method>("chooser");
  const [uploadWide, setUploadWide] = useState(false);

  const handleCreated = (skill: Skill) => {
    onCreated?.(skill);
    onClose();
  };

  const wide = method === "runtime" || (method === "upload" && uploadWide);

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent
        showCloseButton={false}
        className={cn(
          "flex flex-col gap-0 overflow-hidden p-0",
          "!transition-all !duration-300 !ease-out",
          wide
            ? "!h-[min(600px,85vh)] !max-w-2xl !w-full"
            : "!h-auto !max-h-[85vh] !max-w-md !w-full",
        )}
      >
        {/* Header */}
        <div className="flex shrink-0 items-start justify-between gap-3 border-b px-5 pt-4 pb-3">
          <div className="flex items-center gap-2 min-w-0">
            {method !== "chooser" && (
              <Tooltip>
                <TooltipTrigger
                  render={
                    <button
                      type="button"
                      onClick={() => {
                        setMethod("chooser");
                        setUploadWide(false);
                      }}
                      className="-ml-1 rounded-sm p-1 text-muted-foreground opacity-70 transition-opacity hover:bg-accent/60 hover:opacity-100"
                      aria-label={t(($) => $.create.back_aria)}
                    >
                      <ArrowLeft className="h-3.5 w-3.5" />
                    </button>
                  }
                />
                <TooltipContent side="bottom">{t(($) => $.create.back)}</TooltipContent>
              </Tooltip>
            )}
            <div className="min-w-0">
              <DialogTitle className="truncate text-base font-medium">
                {t(($) => $.create.method[method].title)}
              </DialogTitle>
              <p className="mt-0.5 text-xs text-muted-foreground">
                {t(($) => $.create.method[method].desc)}
              </p>
            </div>
          </div>
          <Tooltip>
            <TooltipTrigger
              render={
                <button
                  type="button"
                  onClick={onClose}
                  className="rounded-sm p-1 text-muted-foreground opacity-70 transition-opacity hover:bg-accent/60 hover:opacity-100"
                  aria-label={t(($) => $.create.close_aria)}
                >
                  <XIcon className="h-3.5 w-3.5" />
                </button>
              }
            />
            <TooltipContent side="bottom">{t(($) => $.create.close)}</TooltipContent>
          </Tooltip>
        </div>

        {/* Method body — each form owns its scroll middle + footer */}
        {method === "chooser" && <MethodChooser onChoose={setMethod} />}
        {method === "manual" && (
          <ManualForm
            onCreated={handleCreated}
            onCancel={() => setMethod("chooser")}
          />
        )}
        {method === "url" && (
          <UrlForm
            onCreated={handleCreated}
            onCancel={() => setMethod("chooser")}
          />
        )}
        {method === "upload" && (
          <UploadForm
            onCreated={handleCreated}
            onCancel={() => setMethod("chooser")}
            onBulkDone={onClose}
            onWideChange={setUploadWide}
          />
        )}
        {method === "runtime" && (
          <RuntimeLocalSkillImportPanel
            onImported={handleCreated}
            onBulkDone={onClose}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
