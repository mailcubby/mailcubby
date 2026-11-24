import { createFileRoute } from "@tanstack/react-router";
import { RoutingSettingsSection } from "@/features/settings/RoutingSettingsSection";

export const Route = createFileRoute("/p/$project_/settings/routing")({
  component: ProjectSettingsRoutingRoute,
});

function ProjectSettingsRoutingRoute() {
  const { project } = Route.useParams();
  return <RoutingSettingsSection projectSlug={project} />;
}
