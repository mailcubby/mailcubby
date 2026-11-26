// @vitest-environment node
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const SCRIPT = fileURLToPath(new URL("../../scripts/contrast.mjs", import.meta.url));

describe("design token contrast", () => {
  // Fails when a required token pair misses WCAG AA in light or dark, when the
  // two dark blocks in globals.css drift apart, when the dark chrome layer is
  // not lighter than the canvas, or when docs/ui-contrast.md is stale.
  it("passes AA and matches docs/ui-contrast.md", () => {
    let output = "";
    try {
      output = execFileSync(process.execPath, [SCRIPT, "--check"], {
        encoding: "utf8",
        stdio: "pipe",
      });
    } catch (err) {
      const stderr = (err as { stderr?: string }).stderr ?? String(err);
      expect.fail(stderr);
    }
    expect(output).toContain("all required pairs pass");
  });
});
