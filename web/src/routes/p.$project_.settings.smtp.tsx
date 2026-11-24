import { createFileRoute } from "@tanstack/react-router";
import { SmtpSettingsSection } from "@/features/settings/SmtpSettingsSection";

export const Route = createFileRoute("/p/$project_/settings/smtp")({
  component: ProjectSettingsSmtpRoute,
});

function ProjectSettingsSmtpRoute() {
  const { project } = Route.useParams();
  return <SmtpSettingsSection projectSlug={project} />;
}
