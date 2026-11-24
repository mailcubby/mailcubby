import { createFileRoute } from "@tanstack/react-router";
import { GeneralSettingsPage } from "@/features/settings/GeneralSettingsSection";

export const Route = createFileRoute("/p/$project_/settings/general")({
  component: ProjectSettingsGeneralRoute,
});

function ProjectSettingsGeneralRoute() {
  const { project } = Route.useParams();
  return <GeneralSettingsPage projectSlug={project} />;
}
