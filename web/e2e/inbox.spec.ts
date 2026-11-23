import { expect, sendFixture, test } from "./helpers";

test("harness boots Mailcubby and ingests a fixture via sendmail", async ({ page }) => {
  await sendFixture("01_plain_text.eml", { user: "e2e-smoke" });
  await page.goto("/");
  await expect(page.getByText("Mailcubby").first()).toBeVisible();
});
