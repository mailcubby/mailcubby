import { createFileRoute } from "@tanstack/react-router";
import { ApiSettingsSection } from "@/features/settings/ApiSettingsSection";

export const Route = createFileRoute("/p/$project_/settings/api")({
  component: ProjectSettingsApiRoute,
});

function ProjectSettingsApiRoute() {
  const { project } = Route.useParams();
  return <ApiSettingsSection projectSlug={project} />;
}
