import { createProjectViaAPI, expect, sendFixture, test, uniqueSlug } from "./helpers";

test.describe("Phase 4a local-mode inbox and message view (FR-072)", () => {
  test("send fixture, inspect tabs 1 to 7, and verify sandboxed preview iframe", async ({
    page,
  }) => {
    const projectSlug = uniqueSlug("e2e-tabs");
    await sendFixture("04_multipart_related_cid.eml", { user: projectSlug });

    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Projects" })).toBeVisible();

    const projectCard = page.getByRole("link", { name: new RegExp(projectSlug) });
    await expect(projectCard).toBeVisible();
    await projectCard.click();

    await expect(page).toHaveURL(new RegExp(`/p/${projectSlug}$`));

    const messageRow = page.getByRole("option", { name: /Inline CID logo preview/ });
    await expect(messageRow).toBeVisible();
    await messageRow.click();

    await expect(page).toHaveURL(new RegExp(`/p/${projectSlug}/m/`));
    await expect(page.getByRole("heading", { name: "Inline CID logo preview" })).toBeVisible();

    // Tab 1: Preview — verify strict sandbox attribute (SPEC 12.1).
    const previewTab = page.getByRole("tab", { name: "Preview" });
    await expect(previewTab).toHaveAttribute("aria-selected", "true");
    const iframe = page.locator('iframe[title="Email preview: Inline CID logo preview"]');
    await expect(iframe).toBeVisible();
    const sandboxAttr = await iframe.getAttribute("sandbox");
    expect(sandboxAttr).toBe("allow-popups allow-popups-to-escape-sandbox");
    expect(sandboxAttr).not.toContain("allow-scripts");
    expect(sandboxAttr).not.toContain("allow-same-origin");

    // Tab 2: Text
    await page.getByRole("tab", { name: "Text" }).click();
    await expect(page.getByRole("tabpanel")).toContainText("Generated from HTML");
    await expect(page.getByRole("tabpanel")).toContainText("Logo:");

    // Tab 3: HTML source
    await page.getByRole("tab", { name: "HTML source" }).click();
    await expect(page.getByRole("tabpanel")).toContainText("cid:pixel@example.test");

    // Tab 4: Headers
    await page.getByRole("tab", { name: "Headers" }).click();
    await expect(page.getByRole("tabpanel")).toContainText("<cid-04@example.test>");
    await expect(page.getByRole("tabpanel")).toContainText("Inline CID logo preview");
    await page.getByRole("button", { name: "Highlighted" }).click();
    await expect(page.getByRole("tabpanel")).toContainText("<cid-04@example.test>");

    // Tab 5: Raw
    await page.getByRole("tab", { name: "Raw" }).click();
    await expect(page.getByRole("tabpanel")).toContainText("rel-bound-04");

    // Tab 6: Attachments
    await page.getByRole("tab", { name: /Attachments/ }).click();
    await expect(page.getByRole("tabpanel")).toContainText("pixel.png");
    await expect(page.getByRole("tabpanel")).toContainText("image/png");

    // Tab 7: Tech
    await page.getByRole("tab", { name: "Tech" }).click();
    await expect(page.getByRole("tabpanel")).toContainText("SMTP Envelope");
    await expect(page.getByRole("tabpanel")).toContainText("brand@example.test");
    await expect(page.getByRole("tabpanel")).toContainText("<cid-04@example.test>");
  });

  test("send test email on an empty project persists across reload", async ({ page }) => {
    const project = await createProjectViaAPI("Empty Guide Project", uniqueSlug("e2e-empty-guide"));

    await page.goto(`/p/${project.slug}`);
    await expect(page.getByRole("heading", { name: "No messages yet" })).toBeVisible();
    // Audit M6: the title appears once; the list only says it is waiting.
    await expect(page.getByText("No messages yet")).toHaveCount(1);
    await expect(page.getByRole("heading", { name: "Waiting for mail" })).toBeVisible();
    await expect(page.getByText("MAIL_MAILER=smtp")).toBeVisible();

    await page.getByRole("button", { name: "Send test email" }).click();

    await expect(page).toHaveURL(new RegExp(`/p/${project.slug}/m/`));
    const deepLinkURL = page.url();
    await expect(page.locator("iframe")).toBeVisible();

    // Reload the deep link and verify it renders the message (no blank page).
    await page.reload();
    await expect(page).toHaveURL(deepLinkURL);
    await expect(page.locator("iframe")).toBeVisible();
    await expect(page.getByRole("tab", { name: "Preview" })).toBeVisible();
  });

  test("reloading a deep link /p/<slug>/m/<id> renders the message", async ({ page }) => {
    const projectSlug = uniqueSlug("e2e-deeplink");
    await sendFixture("05_attachments_utf8_rfc2231.eml", { user: projectSlug });

    await page.goto(`/p/${projectSlug}`);
    const row = page.getByRole("option", { name: /Documents attached/ });
    await expect(row).toBeVisible();
    await row.click();

    await expect(page).toHaveURL(new RegExp(`/p/${projectSlug}/m/`));
    const messageURL = page.url();

    await page.reload();
    await expect(page).toHaveURL(messageURL);
    await expect(
      page.getByRole("heading", { name: "Documents attached (PDF and ZIP)" }),
    ).toBeVisible();
    await expect(page.getByRole("tabpanel")).toContainText("invoice PDF");
  });

  test("bulk delete removes selected rows", async ({ page }) => {
    const projectSlug = uniqueSlug("e2e-bulk-delete");
    await sendFixture("01_plain_text.eml", { user: projectSlug });
    await sendFixture("02_multipart_alternative.eml", { user: projectSlug });
    await sendFixture("03_html_only.eml", { user: projectSlug });

    await page.goto(`/p/${projectSlug}`);

    const checkboxes = page.locator('[data-testid="message-select-checkbox"]');
    await expect(checkboxes).toHaveCount(3);

    await checkboxes.nth(0).click();
    await checkboxes.nth(1).click();

    const bulkToolbar = page.getByRole("toolbar", { name: "Bulk message actions" });
    await expect(bulkToolbar).toBeVisible();
    await expect(bulkToolbar).toContainText("2 selected");

    await bulkToolbar.getByRole("button", { name: "Delete" }).click();

    const confirmDialog = page.locator("dialog[open]");
    await expect(confirmDialog).toBeVisible();
    await confirmDialog.getByRole("button", { name: "Delete", exact: true }).click();

    await expect(page.locator('[data-testid="message-select-checkbox"]')).toHaveCount(1);
  });
});
