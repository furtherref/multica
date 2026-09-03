"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Plus, Server, X } from "lucide-react";
import type {
  MemberWithUser,
  RuntimeBudgetPeriodKey,
  RuntimeCostBudget,
  RuntimeCostBudgetInput,
} from "@multica/core/types";
import { useUpdateRuntimeCostBudget } from "@multica/core/runtimes/mutations";
import { useWorkspaceId } from "@multica/core/hooks";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import {
  Command,
  CommandEmpty,
  CommandInput,
  CommandItem,
  CommandList,
} from "@multica/ui/components/ui/command";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n";
import { budgetToInput, parseBudgetField, scopeIsEmpty } from "../budget";

const PERIODS: RuntimeBudgetPeriodKey[] = ["daily", "weekly", "monthly"];

type Draft = Record<RuntimeBudgetPeriodKey, string>;
type UserDraft = Draft & { user_id: string };

function toDraft(limits: { daily_usd: number | null; weekly_usd: number | null; monthly_usd: number | null }): Draft {
  const s = (v: number | null) => (v === null ? "" : String(v));
  return { daily: s(limits.daily_usd), weekly: s(limits.weekly_usd), monthly: s(limits.monthly_usd) };
}

const EMPTY_LIMITS = { daily_usd: null, weekly_usd: null, monthly_usd: null };

export function RuntimeBudgetDialog({
  open,
  onOpenChange,
  runtimeId,
  budget,
  members,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  runtimeId: string;
  budget: RuntimeCostBudget;
  members: MemberWithUser[];
}) {
  const { t } = useT("runtimes");
  const wsId = useWorkspaceId();
  const update = useUpdateRuntimeCostBudget(wsId);
  const seed = useMemo(() => budgetToInput(budget), [budget]);
  const [runtimeDraft, setRuntimeDraft] = useState<Draft>(() => toDraft(seed.runtime ?? EMPTY_LIMITS));
  const [userDrafts, setUserDrafts] = useState<UserDraft[]>(() =>
    seed.users.map((u) => ({ user_id: u.user_id ?? "", ...toDraft(u) })),
  );
  const [pickerOpen, setPickerOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Re-seed on the closed -> open transition only, so a stale draft never
  // overwrites a budget another admin saved meanwhile. It must NOT re-seed on
  // `seed` identity alone: `seed` derives from the budget query, whose
  // `used_usd` moves on every refetch (30s staleTime, window refocus), and
  // re-seeding then would wipe whatever the admin has typed.
  const wasOpen = useRef(open);
  useEffect(() => {
    const justOpened = open && !wasOpen.current;
    wasOpen.current = open;
    if (!justOpened) return;
    setRuntimeDraft(toDraft(seed.runtime ?? EMPTY_LIMITS));
    setUserDrafts(seed.users.map((u) => ({ user_id: u.user_id ?? "", ...toDraft(u) })));
    setError(null);
    setPickerOpen(false);
  }, [open, seed]);

  const memberById = new Map(members.map((m) => [m.user_id, m]));
  const available = members.filter((m) => !userDrafts.some((d) => d.user_id === m.user_id));

  const handleSave = async () => {
    const parseScope = (d: Draft) => {
      const daily = parseBudgetField(d.daily);
      const weekly = parseBudgetField(d.weekly);
      const monthly = parseBudgetField(d.monthly);
      if (daily === undefined || weekly === undefined || monthly === undefined) return undefined;
      return { daily_usd: daily, weekly_usd: weekly, monthly_usd: monthly };
    };
    const runtime = parseScope(runtimeDraft);
    if (!runtime) {
      setError(t(($) => $.budget.dialog.invalid_amount));
      return;
    }
    const users: RuntimeCostBudgetInput["users"] = [];
    for (const d of userDrafts) {
      const scope = parseScope(d);
      if (!scope) {
        setError(t(($) => $.budget.dialog.invalid_amount));
        return;
      }
      if (scopeIsEmpty(scope)) continue;
      users.push({ user_id: d.user_id, ...scope });
    }
    setError(null);
    try {
      await update.mutateAsync({ runtimeId, input: { runtime: scopeIsEmpty(runtime) ? null : runtime, users } });
      onOpenChange(false);
    } catch {
      setError(t(($) => $.budget.dialog.save_failed));
    }
  };

  // The accessible name is read aloud, so both halves must be localized —
  // never the raw `daily` / `weekly` / `monthly` union key.
  const periodLabel: Record<RuntimeBudgetPeriodKey, string> = {
    daily: t(($) => $.budget.col_daily),
    weekly: t(($) => $.budget.col_weekly),
    monthly: t(($) => $.budget.col_monthly),
  };

  const renderInputs = (label: string, draft: Draft, onChange: (p: RuntimeBudgetPeriodKey, v: string) => void) =>
    PERIODS.map((p) => (
      <Input
        key={p}
        inputMode="decimal"
        aria-label={`${label} ${periodLabel[p]}`}
        placeholder={t(($) => $.budget.dialog.no_limit_placeholder)}
        value={draft[p]}
        onChange={(e) => onChange(p, e.target.value)}
      />
    ));

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t(($) => $.budget.dialog.title)}</DialogTitle>
          <DialogDescription>{t(($) => $.budget.dialog.description)}</DialogDescription>
        </DialogHeader>

        <div className="space-y-2">
          <div className="grid grid-cols-[minmax(0,1.3fr)_repeat(3,minmax(0,1fr))_1.75rem] gap-2 px-1 text-micro uppercase tracking-wider text-muted-foreground">
            <div>{t(($) => $.budget.col_scope)}</div>
            <div>{t(($) => $.budget.col_daily)}</div>
            <div>{t(($) => $.budget.col_weekly)}</div>
            <div>{t(($) => $.budget.col_monthly)}</div>
            <div />
          </div>

          <div className="grid grid-cols-[minmax(0,1.3fr)_repeat(3,minmax(0,1fr))_1.75rem] items-center gap-2 border-t px-1 py-2">
            <div className="flex min-w-0 items-center gap-2">
              <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md border bg-card">
                <Server className="h-3.5 w-3.5" />
              </span>
              <div className="flex min-w-0 flex-col">
                <span className="text-body font-medium">{t(($) => $.budget.runtime_total)}</span>
                <span className="text-micro text-muted-foreground">{t(($) => $.budget.dialog.runtime_hint)}</span>
              </div>
            </div>
            {renderInputs(t(($) => $.budget.runtime_total), runtimeDraft, (p, v) => setRuntimeDraft((d) => ({ ...d, [p]: v })))}
            <div />
          </div>

          {userDrafts.map((d, i) => {
            const member = memberById.get(d.user_id);
            const name = member?.name ?? t(($) => $.budget.former_member);
            return (
              <div key={d.user_id} className="grid grid-cols-[minmax(0,1.3fr)_repeat(3,minmax(0,1fr))_1.75rem] items-center gap-2 border-t px-1 py-2">
                <div className="flex min-w-0 items-center gap-2">
                  <ActorAvatar actorType="member" actorId={d.user_id} size="sm" />
                  <div className="flex min-w-0 flex-col">
                    <span className="truncate text-body font-medium">{name}</span>
                    <span className="text-micro text-muted-foreground">{t(($) => $.budget.member_hint)}</span>
                  </div>
                </div>
                {renderInputs(name, d, (p, v) =>
                  setUserDrafts((all) => all.map((row, j) => (j === i ? { ...row, [p]: v } : row))),
                )}
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="size-7"
                  aria-label={t(($) => $.budget.dialog.remove_aria)}
                  onClick={() => setUserDrafts((all) => all.filter((_, j) => j !== i))}
                >
                  <X className="h-3.5 w-3.5" />
                </Button>
              </div>
            );
          })}

          <div className="flex items-center justify-between gap-2 border-t px-1 pt-2">
            {/* Popover + Command rather than a hand-rolled listbox: it gives
                keyboard navigation, `aria-expanded` on the trigger, outside
                click dismissal, and an Escape that closes only the popover
                instead of the surrounding dialog. */}
            <Popover open={pickerOpen} onOpenChange={setPickerOpen}>
              <PopoverTrigger
                render={
                  <Button type="button" variant="outline" size="sm" disabled={available.length === 0}>
                    <Plus className="h-3.5 w-3.5" />
                    {t(($) => $.budget.dialog.add_member)}
                  </Button>
                }
              />
              <PopoverContent align="start" className="w-64 p-0">
                <Command>
                  <CommandInput placeholder={t(($) => $.budget.dialog.search_member)} />
                  <CommandList className="max-h-56">
                    <CommandEmpty>{t(($) => $.budget.dialog.no_member_results)}</CommandEmpty>
                    {available.map((m) => (
                      <CommandItem
                        key={m.user_id}
                        value={m.name}
                        className="flex items-center gap-2"
                        onSelect={() => {
                          setUserDrafts((all) => [...all, { user_id: m.user_id, daily: "", weekly: "", monthly: "" }]);
                          setPickerOpen(false);
                        }}
                      >
                        <ActorAvatar actorType="member" actorId={m.user_id} size="sm" />
                        <span className="truncate">{m.name}</span>
                      </CommandItem>
                    ))}
                  </CommandList>
                </Command>
              </PopoverContent>
            </Popover>
            <span className="text-micro text-muted-foreground">{t(($) => $.budget.dialog.clear_hint)}</span>
          </div>

          {error && <p className="text-caption text-destructive">{error}</p>}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t(($) => $.budget.dialog.cancel)}</Button>
          <Button onClick={handleSave} disabled={update.isPending}>{t(($) => $.budget.dialog.save)}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
