import type { AuthUser, MetaResponse, ProjectRole } from "@/lib/api";

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
