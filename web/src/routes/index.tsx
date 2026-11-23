import { createFileRoute } from "@tanstack/react-router";
import { ProjectsHome } from "@/features/projects/ProjectsHome";

export const Route = createFileRoute("/")({
  component: ProjectsHome,
});
