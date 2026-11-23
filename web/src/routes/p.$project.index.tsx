import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/p/$project/")({
  component: ProjectInboxEmptyRoute,
});

function ProjectInboxEmptyRoute() {
  return null;
}
