import { createFileRoute } from "@tanstack/react-router";
import { RetentionSettingsSection } from "@/features/settings/RetentionSettingsSection";

export const Route = createFileRoute("/p/$project_/settings/retention")({
  component: ProjectSettingsRetentionRoute,
});

function ProjectSettingsRetentionRoute() {
  const { project } = Route.useParams();
  return <RetentionSettingsSection projectSlug={project} />;
}
