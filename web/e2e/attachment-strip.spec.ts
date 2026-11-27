import AxeBuilder from "@axe-core/playwright";
import type { Page } from "@playwright/test";
import { createProjectViaAPI, expect, sendFixture, test, uniqueSlug } from "./helpers";

async function openMessage(page: Page, slug: string, subject: RegExp): Promise<void> {
  await page.goto(`/p/${slug}`);
  const row = page.getByRole("option", { name: subject }).first();
  await expect(row).toBeVisible();
  await row.click();
  await expect(page).toHaveURL(new RegExp(`/p/${slug}/m/`));
}

test.describe("Phase 14c: attachment strip in the message view (FR-072)", () => {
  test("PDF and PNG attachments show two download cards, PNG with a thumbnail", async ({
    page,
  }) => {
    const slug = uniqueSlug("strip-pdf-png");
    await createProjectViaAPI("Strip PDF PNG", slug);
    await sendFixture("16_attachments_pdf_png.eml", { user: slug });
    await openMessage(page, slug, /Monthly report with chart/);

    await expect(page.getByRole("tab", { name: /Preview/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    const strip = page.getByRole("region", { name: "Attachment downloads" });
    await expect(strip).toBeVisible();
    await expect(strip.getByRole("link")).toHaveCount(2);

    const png = strip.getByRole("link", { name: /^Download chart\.png / });
    const pdfPreview = strip.getByRole("button", { name: /^Preview monthly-report\.pdf / });
    const pdfDownload = strip.getByRole("link", { name: "Download monthly-report.pdf" });
    await expect(png).toBeVisible();
    await expect(pdfPreview).toBeVisible();
    await expect(pdfDownload).toBeVisible();

    // PNG thumbnail is loaded from the ?inline=1 endpoint (allowed by the app CSP img-src 'self').
    const thumb = png.locator("img");
    await expect(thumb).toHaveAttribute("src", /\/attachments\/\d+\?inline=1$/);
    await expect
      .poll(() => thumb.evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth))
      .toBeGreaterThan(0);

    // PDF card shows an icon (no thumbnail) and splits preview + download cleanly (no nested interactive elements).
    await expect(pdfPreview.locator("img")).toHaveCount(0);
    await expect(pdfPreview.locator("svg")).toHaveCount(2);
    await expect(page.locator("embed, object")).toHaveCount(0);

    // Clicking the download actions downloads each file with the right name.
    const [pdfDownloadEvent] = await Promise.all([
      page.waitForEvent("download"),
      pdfDownload.click(),
    ]);
    expect(pdfDownloadEvent.suggestedFilename()).toBe("monthly-report.pdf");
    const [pngDownload] = await Promise.all([page.waitForEvent("download"), png.click()]);
    expect(pngDownload.suggestedFilename()).toBe("chart.png");

    // The strip also accompanies the Text tab, but not the technical tabs.
    await page.getByRole("tab", { name: "Text" }).click();
    await expect(strip).toBeVisible();
    await page.getByRole("tab", { name: "Headers" }).click();
    await expect(strip).toHaveCount(0);
    await page.getByRole("tab", { name: /Attachments/ }).click();
    await expect(strip).toHaveCount(0);
    await page.getByRole("tab", { name: "Preview" }).click();
    await expect(strip).toBeVisible();

    // axe: no serious or critical violations on the message view with the strip.
    const results = await new AxeBuilder({ page })
      .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
      // Sandboxed preview iframe contains untrusted third-party email HTML, not app chrome.
      .exclude('iframe[title^="Email preview:"]')
      .analyze();
    const blocking = results.violations.filter(
      (v) => v.impact === "serious" || v.impact === "critical",
    );
    expect(
      blocking.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`),
    ).toEqual([]);
  });

  test("a message without attachments shows no strip", async ({ page }) => {
    const slug = uniqueSlug("strip-none");
    await createProjectViaAPI("Strip None", slug);
    await sendFixture("02_multipart_alternative.eml", { user: slug });
    await openMessage(page, slug, /Order #1042 confirmed/);

    await expect(page.locator('iframe[title^="Email preview:"]')).toBeVisible();
    await expect(page.getByRole("region", { name: "Attachment downloads" })).toHaveCount(0);
    await page.getByRole("tab", { name: "Text" }).click();
    await expect(page.getByRole("tab", { name: "Text" })).toHaveAttribute("aria-selected", "true");
    await expect(page.getByRole("region", { name: "Attachment downloads" })).toHaveCount(0);
  });

  test("an inline cid: image is listed in the Attachments tab but not in the strip", async ({
    page,
  }) => {
    const slug = uniqueSlug("strip-cid");
    await createProjectViaAPI("Strip CID", slug);
    await sendFixture("17_related_cid_unreferenced_inline.eml", { user: slug });
    await openMessage(page, slug, /Newsletter with inline logo and terms/);

    // The preview renders the cid: logo (referenced as cid:Logo%40Brand.Example.test).
    const preview = page.frameLocator('iframe[title^="Email preview:"]');
    await expect(preview.getByRole("img", { name: "Brand logo" })).toHaveAttribute(
      "src",
      /^data:image\/png;base64,/,
    );

    const strip = page.getByRole("region", { name: "Attachment downloads" });
    await expect(strip).toBeVisible();
    await expect(strip.getByRole("link")).toHaveCount(2);
    await expect(strip.getByRole("link", { name: /^Download terms\.txt / })).toBeVisible();
    // An inline part that the HTML does not reference stays in the strip.
    await expect(strip.getByRole("link", { name: /^Download badge\.png / })).toBeVisible();
    await expect(strip.getByRole("link", { name: /logo\.png/ })).toHaveCount(0);

    await page.getByRole("tab", { name: /Attachments/ }).click();
    const panel = page.getByRole("tabpanel");
    await expect(panel.getByText("logo.png", { exact: true })).toBeVisible();
    await expect(panel.getByText("cid:logo@brand.example.test")).toBeVisible();
    await expect(panel.getByText("shown in preview")).toHaveCount(1);
    await expect(panel.getByText("terms.txt", { exact: true })).toBeVisible();
    await expect(panel.getByText("badge.png", { exact: true })).toBeVisible();
  });
});
