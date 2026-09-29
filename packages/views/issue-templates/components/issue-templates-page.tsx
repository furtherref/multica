"use client";

import { useMemo, useState } from "react";
import { AlertCircle, FileText, Plus, Search } from "lucide-react";
import { toast } from "sonner";
import type { MemberWithUser } from "@multica/core/types";
import { useQuery } from "@tanstack/react-query";
import { getCoreRowModel, useReactTable } from "@tanstack/react-table";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  issueTemplateListOptions,
  useArchiveIssueTemplate,
  useReorderIssueTemplates,
  useSetIssueTemplateEnabled,
  useUnarchiveIssueTemplate,
} from "@multica/core/issue-templates";
import { memberListOptions } from "@multica/core/workspace/queries";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { DataTable } from "@multica/ui/components/ui/data-table";
import { Input } from "@multica/ui/components/ui/input";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Switch } from "@multica/ui/components/ui/switch";
import { useNavigation } from "../../navigation";
import { PageHeader } from "../../layout/page-header";
import { useT } from "../../i18n";
import { CreateIssueTemplateDialog } from "./create-issue-template-dialog";
import { type IssueTemplateRow, useIssueTemplateColumns } from "./issue-template-columns";

function PageHeaderBar({
  totalCount,
  onCreate,
}: {
  totalCount: number;
  onCreate: () => void;
}) {
  const { t } = useT("issue-templates");
  return (
    <PageHeader className="justify-between px-5">
      <div className="flex items-center gap-2">
        <FileText className="h-4 w-4 text-muted-foreground" />
        <h1 className="text-body font-medium">{t(($) => $.page.title)}</h1>
        {totalCount > 0 && (
          <span className="font-mono text-caption tabular-nums text-muted-foreground">
            {totalCount}
          </span>
        )}
        <p className="ml-2 hidden text-caption text-muted-foreground md:block">
          {t(($) => $.page.tagline)}
        </p>
      </div>
      <Button type="button" size="sm" onClick={onCreate}>
        <Plus className="h-3 w-3" />
        {t(($) => $.page.new_template)}
      </Button>
    </PageHeader>
  );
}

function CardToolbar({
  search,
  setSearch,
  showArchived,
  setShowArchived,
}: {
  search: string;
  setSearch: (v: string) => void;
  showArchived: boolean;
  setShowArchived: (v: boolean) => void;
}) {
  const { t } = useT("issue-templates");
  return (
    <div className="flex h-12 shrink-0 items-center gap-2 border-b px-4">
      <div className="relative">
        <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder={t(($) => $.page.search_placeholder)}
          className="h-8 w-72 pl-8 text-body"
        />
      </div>
      <div className="ml-auto">
        <label className="flex items-center gap-2 text-caption text-muted-foreground">
          <Switch checked={showArchived} onCheckedChange={setShowArchived} />
          {t(($) => $.page.show_archived)}
        </label>
      </div>
    </div>
  );
}

function EmptyState({ onCreate }: { onCreate: () => void }) {
  const { t } = useT("issue-templates");
  return (
    <div className="flex flex-1 flex-col items-center justify-center px-6 py-16 text-center">
      <div className="flex h-12 w-12 items-center justify-center rounded-full bg-muted">
        <FileText className="h-6 w-6 text-muted-foreground" />
      </div>
      <h2 className="mt-4 text-title-sm font-semibold">{t(($) => $.page.empty.title)}</h2>
      <p className="mt-1 max-w-md text-body text-muted-foreground">
        {t(($) => $.page.empty.description)}
      </p>
      <Button type="button" onClick={onCreate} size="sm" className="mt-5">
        <Plus className="h-3 w-3" />
        {t(($) => $.page.new_template)}
      </Button>
    </div>
  );
}

export function IssueTemplatesPage() {
  const { t } = useT("issue-templates");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const [search, setSearch] = useState("");
  const [showArchived, setShowArchived] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);

  const toggleEnabled = useSetIssueTemplateEnabled();
  const archiveTemplate = useArchiveIssueTemplate();
  const unarchiveTemplate = useUnarchiveIssueTemplate();
  const reorderTemplates = useReorderIssueTemplates();

  const {
    data: templates = [],
    isLoading,
    error: listError,
    refetch: refetchList,
  } = useQuery(issueTemplateListOptions(wsId, true));
  const { data: members = [] } = useQuery(memberListOptions(wsId));

  const membersById = useMemo(() => {
    const map = new Map<string, MemberWithUser>();
    for (const m of members) map.set(m.user_id, m);
    return map;
  }, [members]);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    const active = templates.filter((template) => (showArchived ? true : !template.archived));
    return active.filter((template) =>
      !q ||
      template.name.toLowerCase().includes(q) ||
      template.issue_title.toLowerCase().includes(q),
    );
  }, [templates, search, showArchived]);

  const visibleIds = useMemo(
    () => filtered.filter((t) => !t.archived).map((t) => t.id),
    [filtered],
  );

  const rows = useMemo<IssueTemplateRow[]>(
    () =>
      filtered.map((template) => {
        const idx = visibleIds.indexOf(template.id);
        return {
          template,
          creator: template.created_by
            ? membersById.get(template.created_by) ?? null
            : null,
          canMoveUp: !template.archived && idx > 0,
          canMoveDown: !template.archived && idx >= 0 && idx < visibleIds.length - 1,
        };
      }),
    [filtered, visibleIds, membersById],
  );

  const move = (id: string, dir: -1 | 1) => {
    const activeIds = filtered.filter((t) => !t.archived).map((t) => t.id);
    const from = activeIds.indexOf(id);
    const to = from + dir;
    if (from < 0 || to < 0 || to >= activeIds.length) return;
    const next = [...activeIds];
    const [item] = next.splice(from, 1);
    next.splice(to, 0, item!);
    reorderTemplates.mutate(next, {
      onError: (err) =>
        toast.error(err instanceof Error ? err.message : t(($) => $.toast.reorder_failed)),
    });
  };

  const columns = useIssueTemplateColumns({
    onToggleEnabled: (template) =>
      toggleEnabled.mutate(
        { id: template.id, enabled: !template.enabled },
        {
          onError: (err) =>
            toast.error(err instanceof Error ? err.message : t(($) => $.toast.toggle_failed)),
        },
      ),
    onArchive: (template) =>
      archiveTemplate.mutate(template.id, {
        onError: (err) =>
          toast.error(err instanceof Error ? err.message : t(($) => $.toast.archive_failed)),
      }),
    onUnarchive: (template) =>
      unarchiveTemplate.mutate(template.id, {
        onError: (err) =>
          toast.error(err instanceof Error ? err.message : t(($) => $.toast.unarchive_failed)),
      }),
    onMoveUp: (template) => move(template.id, -1),
    onMoveDown: (template) => move(template.id, 1),
  });

  const table = useReactTable({
    data: rows,
    columns,
    getCoreRowModel: getCoreRowModel(),
    enableColumnResizing: true,
  });

  if (isLoading) {
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        <PageHeaderBar totalCount={0} onCreate={() => setCreateOpen(true)} />
        <div className="flex min-h-0 flex-1 flex-col gap-4 p-6">
          <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-lg border">
            <div className="flex h-12 shrink-0 items-center border-b px-4">
              <Skeleton className="h-8 w-72 rounded-md" />
            </div>
            <div className="space-y-2 p-4">
              {Array.from({ length: 4 }).map((_, i) => (
                <Skeleton key={i} className="h-14 w-full rounded-md" />
              ))}
            </div>
          </div>
        </div>
      </div>
    );
  }

  if (listError) {
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        <PageHeaderBar totalCount={0} onCreate={() => setCreateOpen(true)} />
        <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 py-16 text-center">
          <AlertCircle className="h-8 w-8 text-destructive" />
          <div>
            <p className="text-body font-medium">
              {t(($) => $.page.list_error.title)}
            </p>
            <p className="mt-1 text-caption text-muted-foreground">
              {listError instanceof Error
                ? listError.message
                : t(($) => $.page.list_error.fallback)}
            </p>
          </div>
          <Button type="button" variant="outline" size="sm" onClick={() => refetchList()}>
            {t(($) => $.page.list_error.retry)}
          </Button>
        </div>
      </div>
    );
  }

  const totalCount = templates.length;
  const activeCount = templates.filter((t) => !t.archived).length;
  const showEmpty = activeCount === 0;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeaderBar totalCount={totalCount} onCreate={() => setCreateOpen(true)} />

      <div className="flex min-h-0 flex-1 flex-col gap-4 p-6">
        {showEmpty ? (
          <div className="flex flex-1 items-center justify-center">
            <EmptyState onCreate={() => setCreateOpen(true)} />
          </div>
        ) : (
          <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-lg border bg-background">
            <CardToolbar
              search={search}
              setSearch={setSearch}
              showArchived={showArchived}
              setShowArchived={setShowArchived}
            />
            {filtered.length === 0 ? (
              <div className="flex flex-1 flex-col items-center justify-center gap-2 px-4 py-16 text-center text-muted-foreground">
                <Search className="h-8 w-8 text-faint-foreground" />
                <p className="text-body">{t(($) => $.page.no_matches.title)}</p>
                <p className="max-w-xs text-caption">
                  {t(($) => $.page.no_matches.with_query, { query: search })}
                </p>
              </div>
            ) : (
              <DataTable
                table={table}
                onRowClick={(row) =>
                  navigation.push(paths.issueTemplateDetail(row.original.template.id))
                }
              />
            )}
          </div>
        )}
      </div>

      {createOpen && (
        <CreateIssueTemplateDialog
          onClose={() => setCreateOpen(false)}
          onCreated={(template) => {
            setCreateOpen(false);
            navigation.push(paths.issueTemplateDetail(template.id));
          }}
        />
      )}
    </div>
  );
}