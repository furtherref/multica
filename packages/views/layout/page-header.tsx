"use client";

import { cn } from "@multica/ui/lib/utils";
import { SidebarTrigger, useOptionalSidebar } from "@multica/ui/components/ui/sidebar";

function SidebarToggle() {
  const sidebar = useOptionalSidebar();

  if (!sidebar) {
    return null;
  }

  // On mobile: always show (hamburger). On desktop: only when sidebar is collapsed.
  return <SidebarTrigger className={cn("mr-2", sidebar.state === "expanded" && "md:hidden")} />;
}

interface PageHeaderProps {
  children: React.ReactNode;
  className?: string;
}

export function PageHeader({ children, className }: PageHeaderProps) {
  return (
    <div className={cn("flex h-12 shrink-0 items-center border-b px-4", className)}>
      <SidebarToggle />
      {children}
    </div>
  );
}
