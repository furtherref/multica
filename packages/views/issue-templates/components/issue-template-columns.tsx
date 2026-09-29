"use client";

import {
  Archive,
  ArchiveRestore,
  ArrowDown,
  ArrowUp,
  ChevronRight,
  FileText,
  PauseCircle,
  Pencil,
  PlayCircle,
} from "lucide-react";
import type { ColumnDef } from "@tanstack/react-table";
import type { IssueTemplateSummary, MemberWithUser } from "@multica/core/types";
import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { readIssueTemplateOrigin } from "../lib/origin";
import { useT, useTimeAgo } from "../../i18n";

export interface IssueTemplateRow {
  template: IssueTemplateSummary;
  creator: MemberWithUser | null;
  canMoveUp: boolean;
  canMoveDown: boolean;
}

export interface TemplateRowActions {
  onToggleEnabled: (template: IssueTemplateSummary) => void;
  onArchive: (template: IssueTemplateSummary) => void;
  onUnarchive: (template: IssueTemplateSummary) => void;
  onMoveUp?: (template: IssueTemplateSummary) => void;
  onMoveDown?: (template: IssueTemplateSummary) => void;
}

const COL_WIDTHS = {
  name: 360,
  source: 220,
  status: 120,
  updated: 110,
  actions: 48,
  chevron: 24,
} as const;

export function useIssueTemplateColumns(actions: TemplateRowActions): ColumnDef<IssueTemplateRow>[] {
  const { t } = useT("issue-templates");
  const timeAgo = useTimeAgo();

  return [
    {
      id: "name",
      header: t(($) => $.table.name),
      size: COL_WIDTHS.name,
      meta: { grow: true },
      cell: ({ row }) => <IssueTemplateNameCell row={row.original} />,
    },
    {
      id: "source",
      header: t(($) => $.table.source),
      size: COL_WIDTHS.source,
      meta: { grow: true },
      cell: ({ row }) => (
        <SourceCell
          template={row.original.template}
          creator={row.original.creator}
        />
      ),
    },
    {
      id: "status",
      header: t(($) => $.table.status),
      size: COL_WIDTHS.status,
      cell: ({ row }) => <StatusCell template={row.original.template} />,
    },
    {
      id: "updated",
      header: t(($) => $.table.updated),
      size: COL_WIDTHS.updated,
      cell: ({ row }) => (
        <span className="whitespace-nowrap text-caption text-muted-foreground">
          {timeAgo(row.original.template.updated_at)}
        </span>
      ),
    },
    {
      id: "_actions",
      header: () => null,
      size: COL_WIDTHS.actions,
      enableResizing: false,
      cell: ({ row }) => <ActionsCell row={row.original} actions={actions} />,
    },
    {
      id: "_chevron",
      header: () => null,
      size: COL_WIDTHS.chevron,
      enableResizing: false,
      cell: () => (
        <ChevronRight className="h-4 w-4 shrink-0 text-faint-foreground transition-colors group-hover:text-muted-foreground" />
      ),
    },
  ];
}

function IssueTemplateNameCell({ row }: { row: IssueTemplateRow }) {
  const { t } = useT("issue-templates");
  const { template } = row;
  const summary = template.issue_title;

  return (
    <div className="min-w-0">
      <div className="flex min-w-0 items-center gap-2">
        <span className="block min-w-0 truncate font-medium">{template.name}</span>
        <span className="inline-flex shrink-0 items-center gap-0.5 font-mono text-caption text-muted-foreground">
          <FileText className="h-3 w-3" />
        </span>
      </div>
      <div
        className={`mt-0.5 max-w-xl truncate text-caption ${
          summary ? "text-muted-foreground" : "italic text-muted-foreground"
        }`}
      >
        {summary || t(($) => $.table.no_content)}
      </div>
    </div>
  );
}

function StatusCell({ template }: { template: IssueTemplateSummary }) {
  const { t } = useT("issue-templates");
  if (template.archived) {
    return (
      <Badge variant="outline" className="shrink-0 text-micro">
        {t(($) => $.table.status_archived)}
      </Badge>
    );
  }
  if (!template.enabled) {
    return (
      <Badge variant="secondary" className="shrink-0 text-micro">
        {t(($) => $.table.status_disabled)}
      </Badge>
    );
  }
  return (
    <Badge variant="ghost" className="shrink-0 text-micro">
      {t(($) => $.table.status_enabled)}
    </Badge>
  );
}

function SourceCell({
  template,
  creator,
}: {
  template: IssueTemplateSummary;
  creator: MemberWithUser | null;
}) {
  const { t } = useT("issue-templates");
  const origin = readIssueTemplateOrigin(template);
  const label =
    origin.type === "manual"
      ? t(($) => $.table.source_manual)
      : t(($) => $.table.source_manual);

  return (
    <div className="min-w-0">
      <div className="flex min-w-0 items-center gap-1.5 text-caption text-muted-foreground">
        <Pencil className="h-3 w-3 shrink-0" />
        <span className="block min-w-0 truncate">{label}</span>
      </div>
      {creator && (
        <div className="mt-1 flex min-w-0 items-center gap-1.5 text-caption text-muted-foreground">
          <ActorAvatar
            name={creator.name}
            initials={creator.name.slice(0, 2).toUpperCase()}
            avatarUrl={creator.avatar_url}
            size="xs"
          />
          <span className="truncate">
            {t(($) => $.table.by_creator, { name: creator.name })}
          </span>
        </div>
      )}
    </div>
  );
}

function ActionsCell({
  row,
  actions,
}: {
  row: IssueTemplateRow;
  actions: TemplateRowActions;
}) {
  const { t } = useT("issue-templates");
  const { template } = row;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={t(($) => $.table.actions.open, { name: template.name })}
          >
            <Pencil className="h-3.5 w-3.5" />
          </Button>
        }
      />
      <DropdownMenuContent align="end">
        {template.archived ? (
          <DropdownMenuItem onClick={() => actions.onUnarchive(template)}>
            <ArchiveRestore className="size-4" />
            {t(($) => $.table.actions.unarchive)}
          </DropdownMenuItem>
        ) : (
          <>
            {actions.onMoveUp && (
              <DropdownMenuItem disabled={!row.canMoveUp} onClick={() => actions.onMoveUp?.(template)}>
                <ArrowUp className="size-4" />
                {t(($) => $.table.actions.move_up)}
              </DropdownMenuItem>
            )}
            {actions.onMoveDown && (
              <DropdownMenuItem disabled={!row.canMoveDown} onClick={() => actions.onMoveDown?.(template)}>
                <ArrowDown className="size-4" />
                {t(($) => $.table.actions.move_down)}
              </DropdownMenuItem>
            )}
            <DropdownMenuSeparator />
            {template.enabled ? (
              <DropdownMenuItem onClick={() => actions.onToggleEnabled(template)}>
                <PauseCircle className="size-4" />
                {t(($) => $.table.actions.disable)}
              </DropdownMenuItem>
            ) : (
              <DropdownMenuItem onClick={() => actions.onToggleEnabled(template)}>
                <PlayCircle className="size-4" />
                {t(($) => $.table.actions.enable)}
              </DropdownMenuItem>
            )}
            <DropdownMenuItem variant="destructive" onClick={() => actions.onArchive(template)}>
              <Archive className="size-4" />
              {t(($) => $.table.actions.archive)}
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}