import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Navigate } from "@tanstack/react-router";
import { projectQueryOptions } from "@/lib/queries";

export const Route = createFileRoute("/p/$project_/settings/")({
  component: ProjectSettingsIndexRoute,
});

function ProjectSettingsIndexRoute() {
  const { project } = Route.useParams();
  const { data } = useQuery(projectQueryOptions(project));

  if (data && data.role === "member") {
    return <Navigate to="/p/$project/settings/smtp" params={{ project }} replace />;
  }
  return <Navigate to="/p/$project/settings/general" params={{ project }} replace />;
}
