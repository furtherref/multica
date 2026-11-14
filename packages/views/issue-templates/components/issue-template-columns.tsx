"use client";

import { ChevronRight, FileText, Pencil } from "lucide-react";
import type { ColumnDef } from "@tanstack/react-table";
import type { IssueTemplateSummary, MemberWithUser } from "@multica/core/types";
import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import { readIssueTemplateOrigin } from "../lib/origin";
import { useT, useTimeAgo } from "../../i18n";

export interface IssueTemplateRow {
  template: IssueTemplateSummary;
  creator: MemberWithUser | null;
}

const COL_WIDTHS = {
  name: 360,
  source: 220,
  updated: 110,
  chevron: 48,
} as const;

export function useIssueTemplateColumns(): ColumnDef<IssueTemplateRow>[] {
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
