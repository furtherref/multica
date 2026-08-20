"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useAuthStore } from "@multica/core/auth";
import { paths } from "@multica/core/paths";
import { AdminUsersPage } from "@multica/views/admin";

export default function AdminRoutePage() {
  const router = useRouter();
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);

  // Unauthenticated users have nowhere meaningful to land here — kick them
  // through login and bring them back, mirroring apps/web/app/(auth)/invitations/page.tsx.
  useEffect(() => {
    if (!isLoading && !user) {
      router.replace(
        `${paths.login()}?next=${encodeURIComponent(paths.admin())}`,
      );
    }
  }, [isLoading, user, router]);

  // Non-admins have no reason to be here — send them home. This is a UX
  // nicety only; the server independently 403s every /api/admin/* call for
  // non-admins, so this redirect is not the security boundary.
  useEffect(() => {
    if (!isLoading && user && user.is_system_admin !== true) {
      router.replace(paths.root());
    }
  }, [isLoading, user, router]);

  if (isLoading || !user || user.is_system_admin !== true) return null;

  // The page renders a full-height sidebar + scrolling content split, so it
  // needs a definite viewport height from the route wrapper.
  return (
    <div className="h-dvh overflow-hidden">
      <AdminUsersPage />
    </div>
  );
}
