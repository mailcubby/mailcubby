import type { AuthUser, MetaResponse, ProjectRole, ProjectSummary } from "@/lib/api";

/**
 * Resolves the UI role for a project, downgrading an owner to "viewer" when
 * "Preview as viewer" mode is active (UI 3.8).
 */
export function getEffectiveRole(actualRole: ProjectRole, previewAsViewer: boolean): ProjectRole {
  if (previewAsViewer && actualRole === "owner") {
    return "viewer";
  }
  return actualRole;
}

/**
 * Returns true if the current user can create new projects (SPEC FR-031, UI 3.3).
 */
export function canUserCreateProjects(
  mode: MetaResponse["mode"] | undefined,
  user: Pick<AuthUser, "is_admin" | "can_create_projects"> | undefined,
): boolean {
  if (mode !== "server") {
    return true;
  }
  return Boolean(user?.is_admin || user?.can_create_projects);
}

/**
 * Returns the slug of the only project of a non-admin server-mode user, or
 * null. Such a user skips the projects home and opens that inbox (audit V1,
 * SPEC FR-035), and the sidebar has no "Back to all projects" (audit V4).
 * Returns null while the user or the project list is not loaded.
 */
export function getSingleProjectSlug(
  mode: MetaResponse["mode"] | undefined,
  user: Pick<AuthUser, "is_admin"> | undefined,
  projects: ReadonlyArray<Pick<ProjectSummary, "slug">> | undefined,
): string | null {
  if (mode !== "server" || !user || user.is_admin || projects?.length !== 1) {
    return null;
  }
  return projects[0]?.slug ?? null;
}
