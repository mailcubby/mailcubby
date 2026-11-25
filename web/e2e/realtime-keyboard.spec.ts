import { createProjectViaAPI, expect, sendFixture, test } from "./helpers";

test.describe("Phase 4b realtime, keyboard shortcuts, command palette, and focus", () => {
  test("incoming sendmail message appears live without reload and updates document title count", async ({
    page,
  }) => {
    const project = await createProjectViaAPI("Realtime Shop", "e2e-realtime");
    await page.goto(`/p/${project.slug}`);

    await expect(page.getByText("Live connected")).toBeVisible();
    await expect(page).toHaveTitle(/Realtime Shop/);

    await sendFixture("01_plain_text.eml", { user: project.slug });

    // Appears automatically via SSE without reloading.
    await expect(page.getByRole("option", { name: /Plain text greeting/ })).toBeVisible();
    await expect(page).toHaveTitle(/^\(1\) Realtime Shop/);

    await sendFixture("02_multipart_alternative.eml", { user: project.slug });
    await expect(page.getByRole("option", { name: /Order #1042 confirmed/ })).toBeVisible();
    await expect(page).toHaveTitle(/^\(2\) Realtime Shop/);
  });

  test("j/k navigation, 1-7 tab switching, e delete with undo, Cmd/Ctrl+K palette, and ? help", async ({
    page,
  }) => {
    const mainProject = await createProjectViaAPI("Keyboard Main", "e2e-kb-main");
    const otherProject = await createProjectViaAPI("Palette Target", "e2e-kb-target");

    // Send 3 messages; 04_multipart_related_cid is sent last so it sits at index 0 (newest).
    await sendFixture("01_plain_text.eml", { user: mainProject.slug });
    await sendFixture("02_multipart_alternative.eml", { user: mainProject.slug });
    await sendFixture("04_multipart_related_cid.eml", { user: mainProject.slug });

    await page.goto(`/p/${mainProject.slug}`);
    await expect(page.getByRole("option", { name: /Inline CID logo preview/ })).toBeVisible();
    await expect(page.getByRole("option", { name: /Order #1042 confirmed/ })).toBeVisible();

    // j selects and opens first message; j again moves to second; k moves back to first.
    await page.keyboard.press("j");
    await expect(page.getByRole("heading", { name: "Inline CID logo preview" })).toBeVisible();

    await page.keyboard.press("j");
    await expect(page.getByRole("heading", { name: "Order #1042 confirmed" })).toBeVisible();

    await page.keyboard.press("k");
    await expect(page.getByRole("heading", { name: "Inline CID logo preview" })).toBeVisible();

    // 1 to 7 switch message tabs.
    const tabExpectations: ReadonlyArray<[string, string]> = [
      ["2", "Text"],
      ["3", "HTML source"],
      ["4", "Headers"],
      ["5", "Raw"],
      ["6", "Attachments"],
      ["7", "Tech"],
      ["1", "Preview"],
    ];
    for (const [key, tabLabel] of tabExpectations) {
      await page.keyboard.press(key);
      await expect(page.getByRole("tab", { name: new RegExp(`^${tabLabel}`) })).toHaveAttribute(
        "aria-selected",
        "true",
      );
    }

    // e deletes optimistically and the Undo toast restores the row.
    await page.keyboard.press("e");
    await expect(page.getByRole("option", { name: /Inline CID logo preview/ })).toHaveCount(0);
    const undoButton = page.getByRole("button", { name: "Undo" });
    await expect(undoButton).toBeVisible();
    await undoButton.click();
    await expect(page.getByRole("option", { name: /Inline CID logo preview/ })).toBeVisible();

    // ? opens keyboard shortcuts help overlay; Escape closes it.
    await page.keyboard.press("?");
    const helpDialog = page.locator("dialog[open]");
    await expect(helpDialog).toBeVisible();
    await expect(helpDialog.getByRole("heading", { name: "Keyboard shortcuts" })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(helpDialog).toHaveCount(0);

    // Cmd/Ctrl+K opens command palette and navigates to another project.
    await page.keyboard.press("Control+k");
    const paletteInput = page.getByRole("combobox", {
      name: "Search projects, messages, and actions",
    });
    await expect(paletteInput).toBeVisible();
    await paletteInput.fill(otherProject.slug);
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(new RegExp(`/p/${otherProject.slug}$`));

    // g a navigates to /account/preferences where single-key shortcuts can be toggled.
    await page.keyboard.press("g");
    await page.keyboard.press("a");
    await expect(page).toHaveURL(/\/account\/preferences$/);
    await expect(page.getByRole("heading", { name: "Preferences" })).toBeVisible();
  });

  test("keyboard-only walkthrough reaches inbox controls with visible focus indicators", async ({
    page,
  }) => {
    const project = await createProjectViaAPI("Focus Walkthrough", "e2e-focus");
    await sendFixture("04_multipart_related_cid.eml", { user: project.slug });

    await page.goto(`/p/${project.slug}`);
    const row = page.getByRole("option", { name: /Inline CID logo preview/ });
    await expect(row).toBeVisible();
    await row.click();
    await expect(page.getByRole("heading", { name: "Inline CID logo preview" })).toBeVisible();

    // Start from the first interactive control in the header and Tab across all inbox regions.
    await page.locator("header a").first().focus();

    const visitedLabels: string[] = [];
    const seenRegions = new Set<string>();

    for (let step = 0; step < 45; step += 1) {
      await page.keyboard.press("Tab");
      const info = await page.evaluate(() => {
        const el = document.activeElement as HTMLElement | null;
        if (!el || el === document.body) {
          return null;
        }
        const style = window.getComputedStyle(el);
        const outlineVisible =
          style.outlineStyle !== "none" && Number.parseFloat(style.outlineWidth || "0") > 0;
        const shadowVisible = style.boxShadow !== "none" && style.boxShadow !== "";
        const label =
          el.getAttribute("aria-label") ||
          el.textContent?.trim().slice(0, 40) ||
          el.tagName.toLowerCase();
        const region = el.closest('article[aria-label="Message viewer"]')
          ? "viewer"
          : el.closest('section[aria-label="Message list"]')
            ? "list"
            : el.closest("aside")
              ? "sidebar"
              : el.closest("header")
                ? "header"
                : "other";
        return {
          tag: el.tagName.toLowerCase(),
          label,
          region,
          outlineStyle: style.outlineStyle,
          outlineWidth: style.outlineWidth,
          boxShadow: style.boxShadow,
          hasIndicator: outlineVisible || shadowVisible,
        };
      });

      if (!info) {
        continue;
      }

      // Skip iframes themselves (browser-managed frame focus), assert indicator on every app control.
      if (info.tag !== "iframe") {
        expect(
          info.hasIndicator,
          `Focused <${info.tag}> (${info.label}) lacks visible focus indicator (outlineStyle=${info.outlineStyle}, boxShadow=${info.boxShadow})`,
        ).toBe(true);
      }

      visitedLabels.push(info.label);
      seenRegions.add(info.region);
    }

    expect(seenRegions.has("header")).toBe(true);
    expect(seenRegions.has("sidebar")).toBe(true);
    expect(seenRegions.has("list")).toBe(true);
    expect(seenRegions.has("viewer")).toBe(true);

    // Shift+Tab moves focus backward and retains visible focus indicator.
    await page.keyboard.press("Shift+Tab");
    const backInfo = await page.evaluate(() => {
      const el = document.activeElement as HTMLElement | null;
      if (!el || el === document.body) {
        return null;
      }
      const style = window.getComputedStyle(el);
      return {
        outlineStyle: style.outlineStyle,
        boxShadow: style.boxShadow,
      };
    });
    expect(backInfo).not.toBeNull();
    expect(backInfo?.outlineStyle !== "none" || backInfo?.boxShadow !== "none").toBe(true);

    // Activate Keyboard shortcuts button via keyboard Enter, then dismiss with Escape.
    const shortcutsBtn = page.getByRole("button", { name: "Keyboard shortcuts" });
    await shortcutsBtn.focus();
    await page.keyboard.press("Enter");
    await expect(page.locator("dialog[open]")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.locator("dialog[open]")).toHaveCount(0);
    await expect(shortcutsBtn).toBeFocused();
  });
});
