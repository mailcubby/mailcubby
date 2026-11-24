import { describe, expect, it } from "vitest";
import { canUserCreateProjects, getEffectiveRole } from "@/lib/viewerPreview";

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
});
