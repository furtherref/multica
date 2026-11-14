"use client";

import { use } from "react";
import { IssueTemplateDetailPage } from "@multica/views/issue-templates";

export default function IssueTemplateDetailRoute({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return <IssueTemplateDetailPage templateId={id} />;
}
