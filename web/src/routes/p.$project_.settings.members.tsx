import { createFileRoute } from "@tanstack/react-router";
import { MembersSettingsPage } from "@/features/settings/MembersSettingsSection";

export const Route = createFileRoute("/p/$project_/settings/members")({
  component: ProjectSettingsMembersRoute,
});

function ProjectSettingsMembersRoute() {
  const { project } = Route.useParams();
  return <MembersSettingsPage projectSlug={project} />;
}
