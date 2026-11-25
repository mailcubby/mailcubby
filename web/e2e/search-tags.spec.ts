import { createProjectViaAPI, expect, sendRawMail, test } from "./helpers";

test.describe("Phase 5 search and tags", () => {
  test("typing from:alice filters the list and shows a chip; adding a tag updates the sidebar count", async ({
    page,
  }) => {
    const project = await createProjectViaAPI("Search & Tags Shop", "e2e-search-tags");

    await sendRawMail(
      [
        "From: Alice <alice@shop.test>",
        "To: orders+urgent@shop.test",
        "Subject: Order 12 for João",
        "X-Tags: invoice, Staging",
        "Content-Type: text/plain; charset=utf-8",
        "",
        "Hello João, your order 12 is confirmed.",
      ].join("\r\n"),
      { user: project.slug, recipients: ["orders+urgent@shop.test"] },
    );

    await sendRawMail(
      [
        "From: Bob <bob@shop.test>",
        "To: team@shop.test",
        "Subject: Weekly status report",
        "Content-Type: text/plain; charset=utf-8",
        "",
        "Everything is green this week.",
      ].join("\r\n"),
      { user: project.slug, recipients: ["team@shop.test"] },
    );

    await page.goto(`/p/${project.slug}`);

    // Both messages and auto-tags from FR-086 appear.
    await expect(page.getByRole("option", { name: /Order 12 for João/ })).toBeVisible();
    await expect(page.getByRole("option", { name: /Weekly status report/ })).toBeVisible();
    await expect(page.getByTestId("sidebar-tag-invoice")).toBeVisible();
    await expect(page.getByTestId("sidebar-tag-count-invoice")).toHaveText("1");
    await expect(page.getByTestId("sidebar-tag-Staging")).toBeVisible();
    await expect(page.getByTestId("sidebar-tag-urgent")).toBeVisible();

    // Focus search via `/` shortcut and type `from:alice`.
    const searchInput = page.getByRole("combobox", { name: "Search messages" });
    await page.keyboard.press("/");
    await expect(searchInput).toBeFocused();
    await searchInput.fill("from:alice");

    // Chip `from:alice` is visible and list filters to Alice only.
    const aliceChip = page.getByTestId("search-chip").filter({ hasText: "from:alice" });
    await expect(aliceChip).toBeVisible();
    await expect(page.getByRole("option", { name: /Order 12 for João/ })).toBeVisible();
    await expect(page.getByRole("option", { name: /Weekly status report/ })).toHaveCount(0);

    // Removing the chip restores Bob's message.
    await page.getByRole("button", { name: "Remove filter from:alice" }).click();
    await expect(page.getByRole("option", { name: /Weekly status report/ })).toBeVisible();

    // Diacritic-insensitive search: `joao` finds `João`.
    await searchInput.fill("joao");
    await expect(page.getByRole("option", { name: /Order 12 for João/ })).toBeVisible();
    await expect(page.getByRole("option", { name: /Weekly status report/ })).toHaveCount(0);
    await page.getByRole("button", { name: "Clear search" }).click();
    await expect(page.getByRole("option", { name: /Weekly status report/ })).toBeVisible();

    // Open Bob's message and add a tag using the `t` shortcut in the message header.
    await page.getByRole("option", { name: /Weekly status report/ }).click();
    await expect(page.getByRole("heading", { name: "Weekly status report" })).toBeVisible();

    await page.keyboard.press("t");
    const tagInput = page.getByPlaceholder("Find or create tag...");
    await expect(tagInput).toBeVisible();
    await tagInput.fill("reviewed");
    await page.getByRole("button", { name: /Create “reviewed”/ }).click();

    // Sidebar immediately shows the new tag with count 1.
    await expect(page.getByTestId("sidebar-tag-reviewed")).toBeVisible();
    await expect(page.getByTestId("sidebar-tag-count-reviewed")).toHaveText("1");

    // Clicking the sidebar tag filters the list to Bob's message only.
    await page.getByTestId("sidebar-tag-reviewed").click();
    await expect(page.getByRole("option", { name: /Weekly status report/ })).toBeVisible();
    await expect(page.getByRole("option", { name: /Order 12 for João/ })).toHaveCount(0);
  });

  test("command palette exposes search operators and tags", async ({ page }) => {
    const project = await createProjectViaAPI("Palette Search Shop", "e2e-palette-search");

    await sendRawMail(
      [
        "From: Alice <alice@shop.test>",
        "To: bob@shop.test",
        "Subject: Tagged palette email",
        "X-Tags: billing",
        "",
        "Hello.",
      ].join("\r\n"),
      { user: project.slug },
    );

    await page.goto(`/p/${project.slug}`);
    await expect(page.getByTestId("sidebar-tag-billing")).toBeVisible();

    // Open command palette and check search operators + tag filter.
    await page.keyboard.press("ControlOrMeta+k");
    const paletteInput = page.getByRole("combobox", {
      name: "Search projects, messages, and actions",
    });
    await expect(paletteInput).toBeVisible();
    await paletteInput.fill("has:attachment");
    await expect(page.getByRole("option", { name: /has:attachment/ })).toBeVisible();

    await paletteInput.fill("billing");
    await page.getByRole("option", { name: /Filter by tag: billing/ }).click();
    await expect(page.getByTestId("search-chip").filter({ hasText: "tag:billing" })).toBeVisible();
  });
});
