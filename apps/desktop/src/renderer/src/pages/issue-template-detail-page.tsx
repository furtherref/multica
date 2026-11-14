import { useParams } from "react-router-dom";
import { IssueTemplateDetailPage as SharedIssueTemplateDetailPage } from "@multica/views/issue-templates";

export function IssueTemplateDetailPage() {
  const { id } = useParams<{ id: string }>();
  if (!id) return null;
  return <SharedIssueTemplateDetailPage templateId={id} />;
}
