"use client";

import { useState } from "react";
import { ChevronRight, Server } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import type {
  AgentRuntime,
  MemberWithUser,
  RuntimeBudgetPeriod,
  RuntimeBudgetScope,
} from "@multica/core/types";
import { runtimeCostBudgetOptions } from "@multica/core/runtimes/queries";
import { memberListOptions } from "@multica/core/workspace/queries";
import { useCurrentMember } from "@multica/core/permissions/use-current-member";
import { canManageRuntimeBudget } from "@multica/core/permissions";
import { useWorkspaceId } from "@multica/core/hooks";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n";
import { formatUsd } from "../utils";
import { budgetPercent, countReachedUsers } from "../budget";
import { RuntimeBudgetDialog } from "./runtime-budget-dialog";

// Em dash placeholder for an unlimited period — no used/limit pair to show.
const DASH = "—";

function formatReset(iso: string, locale: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const sameDay = date.getTime() - Date.now() < 24 * 60 * 60 * 1000;
  return new Intl.DateTimeFormat(locale, sameDay
    ? { hour: "2-digit", minute: "2-digit" }
    : { weekday: "short", hour: "2-digit", minute: "2-digit" }).format(date);
}

function PeriodMeter({ period }: { period: RuntimeBudgetPeriod | null }) {
  const { t, i18n } = useT("runtimes");
  const locale = i18n.resolvedLanguage ?? i18n.language;
  if (!period) {
    return (
      <div className="flex flex-col gap-1.5">
        <div className="flex items-baseline justify-between gap-2">
          <span className="text-body font-medium tabular-nums text-muted-foreground">
            {t(($) => $.budget.used_of_limit, { used: DASH, limit: DASH })}
          </span>
          <span className="text-caption text-muted-foreground">{t(($) => $.budget.unlimited)}</span>
        </div>
        <div className="h-2 rounded-full bg-muted" />
      </div>
    );
  }
  const reached = period.reached === true;
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-baseline justify-between gap-2">
        <span className={cn("text-body font-medium tabular-nums", reached && "text-destructive")}>
          {formatUsd(period.used_usd)}{" "}
          <span className="font-normal text-muted-foreground">/ {formatUsd(period.limit_usd)}</span>
        </span>
        {reached ? (
          <span className="rounded-full bg-destructive/10 px-1.5 text-micro font-medium text-destructive">
            {t(($) => $.budget.limit_reached)}
          </span>
        ) : (
          <span className="text-caption text-muted-foreground">
            {t(($) => $.budget.resets_at, { when: formatReset(period.reset_at, locale) })}
          </span>
        )}
      </div>
      <div className="relative h-2 overflow-hidden rounded-full bg-muted">
        <div
          className={cn("h-full rounded-full", reached ? "bg-destructive" : "bg-chart-1")}
          style={{ width: `${budgetPercent(period)}%` }}
        />
      </div>
    </div>
  );
}

const ROW = "grid grid-cols-[220px_repeat(3,minmax(0,1fr))] items-center gap-6 px-4 py-3.5";

function ScopeRow({ scope, member, isRuntime }: { scope: RuntimeBudgetScope; member: MemberWithUser | null; isRuntime: boolean }) {
  const { t } = useT("runtimes");
  return (
    <div className={cn(ROW, "border-b")}>
      <div className="flex min-w-0 items-center gap-2">
        {isRuntime ? (
          <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md border bg-card">
            <Server className="h-3.5 w-3.5" />
          </span>
        ) : scope.user_id ? (
          <ActorAvatar actorType="member" actorId={scope.user_id} size="sm" />
        ) : null}
        <div className="flex min-w-0 flex-col">
          <span className="truncate text-body font-medium">
            {isRuntime ? t(($) => $.budget.runtime_total) : member?.name ?? t(($) => $.budget.former_member)}
          </span>
          <span className="text-micro text-muted-foreground">
            {isRuntime ? t(($) => $.budget.runtime_total_hint) : t(($) => $.budget.member_hint)}
          </span>
        </div>
      </div>
      <PeriodMeter period={scope.daily} />
      <PeriodMeter period={scope.weekly} />
      <PeriodMeter period={scope.monthly} />
    </div>
  );
}

export function BudgetSection({ runtime }: { runtime: AgentRuntime }) {
  const { t } = useT("runtimes");
  const wsId = useWorkspaceId();
  const {
    data: budget,
    isLoading: budgetLoading,
    isError: budgetFailed,
  } = useQuery(runtimeCostBudgetOptions(runtime.id));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { userId, role } = useCurrentMember(wsId);
  const [expanded, setExpanded] = useState(false);
  const [dialogOpen, setDialogOpen] = useState(false);

  // Both signals must agree: the local rule keeps the button off for anyone but
  // the runtime owner even if a drifted backend claims can_manage, and vice
  // versa.
  const canManage =
    canManageRuntimeBudget(runtime, { userId, role }).allowed && budget?.can_manage === true;
  const users = budget?.users ?? [];
  const hasAny = budget?.runtime != null || users.length > 0;
  const reachedCount = countReachedUsers(users);
  const memberById = new Map(members.map((m) => [m.user_id, m]));

  return (
    <div className="rounded-lg border bg-card">
      <div className="flex items-start justify-between gap-3 border-b p-4">
        <div className="min-w-0">
          <h3 className="text-title-sm font-semibold tracking-tight">{t(($) => $.budget.title)}</h3>
          <p className="text-caption text-muted-foreground">{t(($) => $.budget.description)}</p>
        </div>
        {canManage && (
          <Button type="button" variant="outline" size="sm" onClick={() => setDialogOpen(true)}>
            {hasAny ? t(($) => $.budget.edit_button) : t(($) => $.budget.set_button)}
          </Button>
        )}
      </div>

      {/* "No limits set" is a claim about the server's answer, so it may only
          render once the query resolved. While it is in flight or failed the
          card says so instead. */}
      {budgetLoading ? (
        <div className="flex flex-col gap-3 px-4 py-5">
          <Skeleton className="h-3 w-24" />
          <Skeleton className="h-10 rounded-md" />
        </div>
      ) : budgetFailed ? (
        <p className="px-4 py-7 text-center text-caption text-muted-foreground">
          {t(($) => $.budget.load_failed)}
        </p>
      ) : !hasAny ? (
        <div className="flex flex-col items-center gap-1 px-4 py-7">
          <p className="text-body font-medium">{t(($) => $.budget.empty_title)}</p>
          <p className="text-caption text-muted-foreground">{t(($) => $.budget.empty_body)}</p>
        </div>
      ) : (
        <>
          <div className={cn(ROW, "border-b py-2 text-micro uppercase tracking-wider text-muted-foreground")}>
            <div>{t(($) => $.budget.col_scope)}</div>
            <div>{t(($) => $.budget.col_daily)}</div>
            <div>{t(($) => $.budget.col_weekly)}</div>
            <div>{t(($) => $.budget.col_monthly)}</div>
          </div>
          {budget?.runtime && <ScopeRow scope={budget.runtime} member={null} isRuntime />}
          {expanded &&
            users.map((u) => (
              <ScopeRow key={u.user_id ?? "no-user"} scope={u} member={u.user_id ? memberById.get(u.user_id) ?? null : null} isRuntime={false} />
            ))}
          {users.length > 0 && (
            <button
              type="button"
              onClick={() => setExpanded((v) => !v)}
              className="flex w-full items-center gap-2 px-4 py-2.5 text-left text-body text-muted-foreground transition-colors hover:bg-muted/40 hover:text-foreground"
            >
              <ChevronRight className={cn("h-3.5 w-3.5 transition-transform", expanded && "rotate-90")} />
              <span>
                {expanded
                  ? t(($) => $.budget.hide_members)
                  : t(($) => $.budget.show_members, { count: users.length })}
              </span>
              {!expanded && reachedCount > 0 && (
                <span className="rounded-full bg-destructive/10 px-1.5 text-micro font-medium text-destructive">
                  {t(($) => $.budget.reached_badge, { count: reachedCount })}
                </span>
              )}
            </button>
          )}
        </>
      )}

      {canManage && budget && (
        <RuntimeBudgetDialog
          open={dialogOpen}
          onOpenChange={setDialogOpen}
          runtimeId={runtime.id}
          budget={budget}
          members={members}
        />
      )}
    </div>
  );
}
