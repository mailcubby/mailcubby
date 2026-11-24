import { createFileRoute } from "@tanstack/react-router";
import { AuditSettingsSection } from "@/features/settings/AuditSettingsSection";

export const Route = createFileRoute("/p/$project_/settings/audit")({
  component: ProjectSettingsAuditRoute,
});

function ProjectSettingsAuditRoute() {
  const { project } = Route.useParams();
  return <AuditSettingsSection projectSlug={project} />;
}
