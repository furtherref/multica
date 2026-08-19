"use client";

import { useMemo, useState } from "react";
import { ArrowLeft, Ban, MoreHorizontal, RotateCcw, Search } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogCancel,
  AlertDialogAction,
} from "@multica/ui/components/ui/alert-dialog";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from "@multica/ui/components/ui/dropdown-menu";
import { cn } from "@multica/ui/lib/utils";
import { useAuthStore } from "@multica/core/auth";
import { adminKeys, adminUsersOptions } from "@multica/core/admin/queries";
import type { AdminUser } from "@multica/core/admin/schema";
import { api } from "@multica/core/api";
import { paths } from "@multica/core/paths";
import { useNavigation } from "../navigation";
import { useT } from "../i18n";
import { SettingsCard } from "../settings/components/settings-layout";

function displayName(user: AdminUser): string {
  return user.name || user.email;
}

function AdminUserRow({
  user,
  isSelf,
  busy,
  onSuspend,
  onRestore,
}: {
  user: AdminUser;
  isSelf: boolean;
  busy: boolean;
  onSuspend: () => void;
  onRestore: () => void;
}) {
  const { t } = useT("admin");
  const isSuspended = user.account_status === "suspended";
  // Server-driven enum: an installed client talking to a newer backend may
  // see an account_status value it doesn't recognize (schema falls back to
  // "unknown" — see packages/core/admin/schema.ts). Never guess a menu
  // action against a state we can't interpret: show a neutral badge and
  // hide the ⋯ menu entirely rather than defaulting to "active" behavior.
  const isUnknownStatus = user.account_status === "unknown";
  const name = displayName(user);

  return (
    <div
      className="flex items-center gap-3 px-4 py-3"
      data-testid={`admin-user-row-${user.id}`}
    >
      <ActorAvatar
        name={name}
        initials={name.charAt(0).toUpperCase()}
        avatarUrl={user.avatar_url}
        size="lg"
        className={cn(isSuspended && "opacity-50")}
      />
      <div className="min-w-0 flex-1">
        <div
          className={cn(
            "truncate text-body font-medium",
            isSuspended && "text-muted-foreground",
          )}
        >
          {name}
        </div>
        <div className="truncate text-caption text-muted-foreground">{user.email}</div>
      </div>
      {isSuspended && (
        <Badge variant="destructive">
          <Ban className="h-3 w-3" />
          {t(($) => $.users.suspended_badge)}
        </Badge>
      )}
      {isUnknownStatus && (
        <Badge variant="outline">{t(($) => $.users.unknown_status_badge)}</Badge>
      )}
      {isSelf ? (
        <Badge variant="outline">{t(($) => $.users.self_badge)}</Badge>
      ) : isUnknownStatus ? null : (
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <Button
                variant="ghost"
                size="icon-sm"
                disabled={busy}
                aria-label={t(($) => $.users.actions_menu_aria)}
              >
                <MoreHorizontal className="h-4 w-4 text-muted-foreground" />
              </Button>
            }
          />
          <DropdownMenuContent align="end" className="w-auto">
            {isSuspended ? (
              <DropdownMenuItem onClick={onRestore}>
                <RotateCcw className="h-3.5 w-3.5" />
                {t(($) => $.users.restore_action)}
              </DropdownMenuItem>
            ) : (
              <DropdownMenuItem variant="destructive" onClick={onSuspend}>
                <Ban className="h-3.5 w-3.5" />
                {t(($) => $.users.suspend_action)}
              </DropdownMenuItem>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  );
}

export function AdminUsersPage() {
  const { t } = useT("admin");
  const navigation = useNavigation();
  const currentUser = useAuthStore((s) => s.user);
  const qc = useQueryClient();
  const { data: users = [] } = useQuery(adminUsersOptions());
  const [search, setSearch] = useState("");
  const [actionUserId, setActionUserId] = useState<string | null>(null);
  const [confirmAction, setConfirmAction] = useState<{
    title: string;
    description: string;
    variant?: "destructive";
    onConfirm: () => Promise<void>;
  } | null>(null);

  const filteredUsers = useMemo(() => {
    const query = search.trim().toLowerCase();
    if (!query) return users;
    return users.filter(
      (u) =>
        u.name.toLowerCase().includes(query) || u.email.toLowerCase().includes(query),
    );
  }, [users, search]);

  const applyStatus = async (user: AdminUser, status: "active" | "suspended") => {
    setActionUserId(user.id);
    try {
      await api.setUserAccountStatus(user.id, status);
      qc.invalidateQueries({ queryKey: adminKeys.users() });
      toast.success(
        status === "suspended"
          ? t(($) => $.users.toast_suspended)
          : t(($) => $.users.toast_restored),
      );
    } catch (e) {
      toast.error(
        e instanceof Error
          ? e.message
          : status === "suspended"
            ? t(($) => $.users.toast_suspend_failed)
            : t(($) => $.users.toast_restore_failed),
      );
    } finally {
      setActionUserId(null);
    }
  };

  const handleSuspend = (user: AdminUser) => {
    const name = displayName(user);
    setConfirmAction({
      title: t(($) => $.users.suspend_title, { name }),
      description: t(($) => $.users.suspend_description, { name }),
      variant: "destructive",
      onConfirm: () => applyStatus(user, "suspended"),
    });
  };

  const handleRestore = (user: AdminUser) => {
    const name = displayName(user);
    setConfirmAction({
      title: t(($) => $.users.restore_title, { name }),
      description: t(($) => $.users.restore_description, { name }),
      onConfirm: () => applyStatus(user, "active"),
    });
  };

  return (
    <div className="mx-auto w-full max-w-3xl space-y-8 px-6 py-8">
      <Button
        variant="ghost"
        size="sm"
        className="-ml-2 text-muted-foreground"
        onClick={() => navigation.push(paths.root())}
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        {t(($) => $.back_to_workspace)}
      </Button>

      <header>
        <h1 className="text-title-lg font-semibold tracking-tight">
          {t(($) => $.page.title)}
        </h1>
        <p className="mt-1 max-w-2xl text-body leading-6 text-muted-foreground">
          {t(($) => $.page.description)}
        </p>
      </header>

      <section className="space-y-3">
        <div className="flex min-w-0 items-end justify-between gap-4 px-0.5">
          <h2 className="text-body font-semibold">
            {t(($) => $.users.section_title, { count: users.length })}
          </h2>
          <div className="relative w-64 shrink-0">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder={t(($) => $.users.search_placeholder)}
              className="pl-8"
              aria-label={t(($) => $.users.search_placeholder)}
            />
          </div>
        </div>

        {filteredUsers.length > 0 ? (
          <SettingsCard>
            {filteredUsers.map((user) => (
              <div key={user.id}>
                <AdminUserRow
                  user={user}
                  isSelf={user.id === currentUser?.id}
                  busy={actionUserId === user.id}
                  onSuspend={() => handleSuspend(user)}
                  onRestore={() => handleRestore(user)}
                />
              </div>
            ))}
          </SettingsCard>
        ) : (
          <p className="text-body text-muted-foreground">{t(($) => $.users.empty)}</p>
        )}
      </section>

      <AlertDialog
        open={!!confirmAction}
        onOpenChange={(open) => {
          if (!open) setConfirmAction(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{confirmAction?.title}</AlertDialogTitle>
            <AlertDialogDescription>{confirmAction?.description}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.users.confirm_cancel)}</AlertDialogCancel>
            <AlertDialogAction
              variant={confirmAction?.variant === "destructive" ? "destructive" : "default"}
              onClick={async () => {
                await confirmAction?.onConfirm();
                setConfirmAction(null);
              }}
            >
              {t(($) => $.users.confirm_action)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
