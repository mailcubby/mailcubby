import { createFileRoute } from "@tanstack/react-router";
import { AdminStatsPage } from "@/features/admin/AdminStatsPage";

export const Route = createFileRoute("/admin/stats")({
  component: AdminStatsPage,
});
