import { describe, expect, it } from "vitest";
import { canUserCreateProjects, getEffectiveRole, getSingleProjectSlug } from "@/lib/viewerPreview";

describe("viewerPreview helpers", () => {
  it("downgrades owner to viewer only when previewAsViewer is enabled", () => {
    expect(getEffectiveRole("owner", false)).toBe("owner");
    expect(getEffectiveRole("owner", true)).toBe("viewer");
    expect(getEffectiveRole("member", true)).toBe("member");
    expect(getEffectiveRole("member", false)).toBe("member");
    expect(getEffectiveRole("viewer", true)).toBe("viewer");
    expect(getEffectiveRole("viewer", false)).toBe("viewer");
  });

  it("checks project creation permission by mode and user flags", () => {
    expect(canUserCreateProjects("local", undefined)).toBe(true);
    expect(canUserCreateProjects("local", { is_admin: false, can_create_projects: false })).toBe(
      true,
    );
    expect(canUserCreateProjects("server", undefined)).toBe(false);
    expect(canUserCreateProjects("server", { is_admin: false, can_create_projects: false })).toBe(
      false,
    );
    expect(canUserCreateProjects("server", { is_admin: false, can_create_projects: true })).toBe(
      true,
    );
    expect(canUserCreateProjects("server", { is_admin: true, can_create_projects: false })).toBe(
      true,
    );
  });

  // FR-035, audit V1 and V4: a user with exactly one project and neither admin
  // rights nor project creation skips the projects home.
  it("returns the only project slug for a server-mode user without admin or create rights", () => {
    const one = [{ slug: "client-portal" }];
    const two = [{ slug: "client-portal" }, { slug: "internal-ops" }];
    const member = { is_admin: false, can_create_projects: false };
    expect(getSingleProjectSlug("server", member, one)).toBe("client-portal");
    expect(
      getSingleProjectSlug("server", { is_admin: true, can_create_projects: false }, one),
    ).toBeNull();
    // Phase 15e: users who can create projects keep the home with "New project".
    expect(
      getSingleProjectSlug("server", { is_admin: false, can_create_projects: true }, one),
    ).toBeNull();
    expect(getSingleProjectSlug("server", member, two)).toBeNull();
    expect(getSingleProjectSlug("server", member, [])).toBeNull();
    expect(getSingleProjectSlug("local", member, one)).toBeNull();
    expect(getSingleProjectSlug(undefined, member, one)).toBeNull();
    expect(getSingleProjectSlug("server", undefined, one)).toBeNull();
    expect(getSingleProjectSlug("server", member, undefined)).toBeNull();
  });
});
