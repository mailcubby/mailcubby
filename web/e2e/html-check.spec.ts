import { type ChildProcess, spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import AxeBuilder from "@axe-core/playwright";
import type { Page } from "@playwright/test";
import {
  chooseTheme,
  createProjectViaAPI,
  expect,
  getE2EConfig,
  sendFixture,
  sendRawMail,
  test,
  uniqueSlug,
} from "./helpers";

const DEV_APP_KEY = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
const ADMIN_EMAIL = "admin@htmlcheck.test";
const ADMIN_PASSWORD = "admin-super-secret-123";

function getFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.unref();
    server.on("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const addr = server.address();
      if (!addr || typeof addr === "string") {
        server.close(() => reject(new Error("failed to allocate port")));
        return;
      }
      const { port } = addr;
      server.close((err) => {
        if (err) {
          reject(err);
          return;
        }
        resolve(port);
      });
    });
  });
}

async function waitForReady(url: string, child: ChildProcess, logs: string[]): Promise<void> {
  const deadline = Date.now() + 15_000;
  let lastError = "server did not respond";

  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw new Error(`server-mode mailcubby exited early (${child.exitCode}):\n${logs.join("")}`);
    }
    try {
      const res = await fetch(`${url}/readyz`);
      if (res.ok) {
        return;
      }
      lastError = `HTTP ${res.status}`;
    } catch (err) {
      lastError = err instanceof Error ? err.message : String(err);
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }

  throw new Error(`server-mode mailcubby did not become ready (${lastError}):\n${logs.join("")}`);
}

async function startBootstrappedServer(): Promise<{
  httpURL: string;
  stop: () => Promise<void>;
}> {
  const { binaryPath, repoRoot } = getE2EConfig();
  const httpPort = await getFreePort();
  const smtpPort = await getFreePort();
  const dataDir = mkdtempSync(path.join(tmpdir(), "mailcubby-e2e-16c-"));
  const httpURL = `http://127.0.0.1:${httpPort}`;
  const logs: string[] = [];

  const child = spawn(binaryPath, ["serve"], {
    cwd: repoRoot,
    env: {
      ...process.env,
      MAILCUBBY_MODE: "server",
      MAILCUBBY_DATA_DIR: dataDir,
      MAILCUBBY_BASE_URL: httpURL,
      MAILCUBBY_APP_KEY: DEV_APP_KEY,
      MAILCUBBY_ADMIN_EMAIL: ADMIN_EMAIL,
      MAILCUBBY_ADMIN_PASSWORD: ADMIN_PASSWORD,
      MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH: "true",
      MAILCUBBY_HTTP_ADDR: `127.0.0.1:${httpPort}`,
      MAILCUBBY_SMTP_ADDR: `127.0.0.1:${smtpPort}`,
      MAILCUBBY_SMTP_PUBLIC_HOST: "127.0.0.1",
      MAILCUBBY_SMTP_PUBLIC_PORT: String(smtpPort),
      MAILCUBBY_LOG_LEVEL: "warn",
    },
    stdio: ["ignore", "pipe", "pipe"],
  });

  child.stdout?.on("data", (chunk: Buffer) => logs.push(chunk.toString()));
  child.stderr?.on("data", (chunk: Buffer) => logs.push(chunk.toString()));

  try {
    await waitForReady(httpURL, child, logs);
    return {
      httpURL,
      stop: async () => {
        if (child.exitCode === null) {
          await new Promise<void>((resolve) => {
            const timer = setTimeout(() => {
              child.kill("SIGKILL");
              resolve();
            }, 5_000);
            child.once("exit", () => {
              clearTimeout(timer);
              resolve();
            });
            child.kill("SIGTERM");
          });
        }
        rmSync(dataDir, { recursive: true, force: true });
      },
    };
  } catch (err) {
    child.kill("SIGKILL");
    rmSync(dataDir, { recursive: true, force: true });
    throw err;
  }
}

async function assertAccessible(page: Page, screenName: string): Promise<void> {
  await page.evaluate(() =>
    Promise.allSettled(
      document
        .getAnimations()
        .filter((a) => a instanceof CSSTransition)
        .map((a) => a.finished),
    ),
  );
  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .exclude('iframe[title^="Email preview:"]')
    .analyze();
  const blocking = results.violations.filter(
    (v) => v.impact === "serious" || v.impact === "critical",
  );
  expect(
    blocking.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`),
    `WCAG 2.1 AA violations on "${screenName}"`,
  ).toEqual([]);
}

const MODERN_HTML_EMAIL = [
  "From: Designer <design@example.test>",
  "To: dev@example.test",
  "Subject: Modern CSS layout newsletter",
  "MIME-Version: 1.0",
  'Content-Type: text/html; charset="utf-8"',
  "",
  "<!doctype html>",
  "<html><head><style>",
  ".hero { display: flex; gap: 12px; border-radius: 8px; }",
  "@media (prefers-color-scheme: dark) { .hero { display: grid; } }",
  "</style></head>",
  '<body><table role="presentation"><tr><td class="hero"><p>Hello world</p></td></tr></table></body>',
  "</html>",
].join("\r\n");

test.describe("Phase 16c: HTML compatibility check UI (FR-079)", () => {
  test("opens HTML check via shortcut 9, filters clients, expands disclosure, hides on text-only, and passes axe in light and dark", async ({
    page,
  }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    const slug = uniqueSlug("html-check-local");
    await createProjectViaAPI("HTML Check Local", slug);

    // Send a plain-text message and a modern HTML message.
    await sendFixture("01_plain_text.eml", { user: slug });
    await sendRawMail(MODERN_HTML_EMAIL, { user: slug });

    await page.goto(`/p/${slug}`);

    // Open the modern HTML email.
    await page.getByRole("option", { name: /Modern CSS layout newsletter/ }).click();
    await expect(page.getByRole("heading", { name: "Modern CSS layout newsletter" })).toBeVisible();

    const checkTab = page.getByRole("tab", { name: "HTML check" });
    await expect(checkTab).toBeVisible();

    // Keyboard shortcut `9` switches to the HTML check tab and updates URL `?tab=check`.
    await page.keyboard.press("9");
    await expect(checkTab).toHaveAttribute("aria-selected", "true");
    await expect(page).toHaveURL(/tab=check/);

    const panel = page.getByRole("tabpanel");
    await expect(panel.getByRole("heading", { name: "HTML & CSS Compatibility" })).toBeVisible();
    await expect(panel.getByText(/^Supported \d+([.,]\d+)?%$/)).toBeVisible();
    await expect(panel.getByText(/^Partial \d+([.,]\d+)?%$/)).toBeVisible();
    await expect(panel.getByText(/^Unsupported \d+([.,]\d+)?%$/)).toBeVisible();

    // "Only show issues" switch is enabled by default.
    const onlyIssues = panel.getByRole("switch", { name: "Only show issues" });
    await expect(onlyIssues).toBeChecked();

    // Expand a detected CSS issue row and check its disclosure details + caniemail.com link.
    const firstIssueBtn = panel.locator("ul li > button[aria-expanded]").first();
    await expect(firstIssueBtn).toHaveAttribute("aria-expanded", "false");
    await firstIssueBtn.click();
    await expect(firstIssueBtn).toHaveAttribute("aria-expanded", "true");

    const caniemailLink = panel.getByRole("link", { name: "View on caniemail.com" });
    await expect(caniemailLink).toBeVisible();
    expect(await caniemailLink.getAttribute("href")).toMatch(/^https:\/\/www\.caniemail\.com\//);
    expect(await caniemailLink.getAttribute("rel")).toBe("noopener noreferrer");

    // Client family filter pills filter the client support cards inside the expanded row.
    const filterGroup = panel.getByRole("group", { name: "Filter by email client family" });
    await filterGroup.getByRole("button", { name: "Outlook" }).click();
    await expect(filterGroup.getByRole("button", { name: "Outlook" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await expect(panel.getByText("Outlook Classic (Windows)")).toBeVisible();
    await expect(panel.getByText("Outlook.com / New Outlook")).toBeVisible();
    await expect(panel.getByText("Gmail (Desktop Webmail)")).toHaveCount(0);

    // Switch back to All and verify WCAG 2.1 AA in both Light and Dark themes (with disclosure open).
    await filterGroup.getByRole("button", { name: "All" }).click();
    await assertAccessible(page, "HTML check tab with expanded feature (Light)");

    await chooseTheme(page, "Dark");
    await assertAccessible(page, "HTML check tab with expanded feature (Dark)");

    // Restore Light theme.
    await chooseTheme(page, "Light");

    // Toggle "Only show issues" off to view all detected features, then back on.
    await onlyIssues.click();
    await expect(onlyIssues).not.toBeChecked();
    await expect(panel.getByRole("heading", { name: "Detected features" })).toBeVisible();
    await onlyIssues.click();
    await expect(onlyIssues).toBeChecked();

    // Filtering to Apple Mail (which supports every modern CSS feature in this email) shows the structured EmptyState.
    await filterGroup.getByRole("button", { name: "Apple Mail" }).click();
    await expect(
      panel.getByRole("heading", { name: "All detected features are supported in Apple Mail" }),
    ).toBeVisible();

    // Move focus off form controls and verify keyboard shortcuts help dialog lists `9` for HTML check.
    await checkTab.focus();
    await page.keyboard.press("?");
    const helpDialog = page.locator("dialog[open]");
    await expect(helpDialog).toBeVisible();
    await expect(helpDialog.getByText("Switch to HTML check tab")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(helpDialog).toHaveCount(0);

    // Switching to a plain-text message hides the HTML check tab, falls back cleanly, and fires zero /html-check calls.
    const plainTextHtmlCheckRequests: string[] = [];
    page.on("request", (req) => {
      if (new URL(req.url()).pathname.endsWith("/html-check")) {
        plainTextHtmlCheckRequests.push(req.url());
      }
    });
    await page.getByRole("option", { name: /Plain text greeting/ }).click();
    await expect(page.getByRole("heading", { name: "Plain text greeting" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "HTML check" })).toHaveCount(0);
    await expect(page.getByRole("tab", { name: "Text" })).toHaveAttribute("aria-selected", "true");
    expect(plainTextHtmlCheckRequests).toEqual([]);
  });

  test("HTML check tab is hidden for viewers when viewer_hide_technical=true and visible when false", async ({
    browser,
  }) => {
    const srv = await startBootstrappedServer();
    const adminContext = await browser.newContext();
    const adminPage = await adminContext.newPage();
    const slug = uniqueSlug("html-check-viewer");

    try {
      await adminPage.goto(`${srv.httpURL}/login`);
      await adminPage.getByLabel("Email").fill(ADMIN_EMAIL);
      await adminPage.getByLabel("Password", { exact: true }).fill(ADMIN_PASSWORD);
      await adminPage.getByRole("button", { name: "Sign in" }).click();
      await expect(adminPage).toHaveURL(`${srv.httpURL}/`);

      const meRes = await adminPage.request.get(`${srv.httpURL}/api/v1/auth/me`);
      const me = (await meRes.json()) as { csrf_token: string };
      const headers = { "X-CSRF-Token": me.csrf_token, Origin: srv.httpURL };

      const created = await adminPage.request.post(`${srv.httpURL}/api/v1/projects`, {
        headers,
        data: { name: "HTML Check Viewer", slug },
      });
      expect(created.status()).toBe(201);

      const sent = await adminPage.request.post(
        `${srv.httpURL}/api/v1/projects/${slug}/send-test`,
        { headers },
      );
      expect(sent.status()).toBe(201);
      const sentMsg = (await sent.json()) as { id: string };

      const patched = await adminPage.request.patch(`${srv.httpURL}/api/v1/projects/${slug}`, {
        headers,
        data: { settings: { viewer_hide_technical: true } },
      });
      expect(patched.status()).toBe(200);

      const invited = await adminPage.request.post(
        `${srv.httpURL}/api/v1/projects/${slug}/invites`,
        { headers, data: { email: "viewer@htmlcheck.test", role: "viewer" } },
      );
      expect(invited.status()).toBe(201);
      const { url: inviteUrl } = (await invited.json()) as { url: string };

      // Owner can see HTML check tab, and "Preview as viewer" hides it.
      await adminPage.goto(`${srv.httpURL}/p/${slug}/m/${sentMsg.id}`);
      await expect(adminPage.getByRole("tab", { name: "HTML check" })).toBeVisible();
      await adminPage.getByRole("button", { name: "Preview as viewer" }).click();
      await expect(adminPage.getByRole("tab", { name: "HTML check" })).toHaveCount(0);

      // Real viewer with viewer_hide_technical=true does not see HTML check tab.
      const viewerContext = await browser.newContext();
      const viewerPage = await viewerContext.newPage();
      try {
        await viewerPage.goto(inviteUrl);
        await viewerPage.getByLabel("Full name").fill("Check Viewer");
        await viewerPage.getByLabel("Password", { exact: true }).fill("viewer-secret-pass-1");
        await viewerPage.getByRole("button", { name: "Create account & join" }).click();
        await expect(viewerPage).toHaveURL(`${srv.httpURL}/p/${slug}`);

        const viewerHtmlCheckRequests: string[] = [];
        const trackViewerRequests = (req: { url: () => string }) => {
          if (new URL(req.url()).pathname.endsWith("/html-check")) {
            viewerHtmlCheckRequests.push(req.url());
          }
        };
        viewerPage.on("request", trackViewerRequests);

        await viewerPage.goto(`${srv.httpURL}/p/${slug}/m/${sentMsg.id}?tab=check`);
        await expect(viewerPage.getByRole("tab", { name: "Preview" })).toHaveAttribute(
          "aria-selected",
          "true",
        );
        await expect(viewerPage.getByRole("tab", { name: "HTML check" })).toHaveCount(0);

        // Pressing 9 does nothing for the restricted viewer and triggers zero /html-check requests.
        await viewerPage.keyboard.press("9");
        await expect(viewerPage.getByRole("tab", { name: "Preview" })).toHaveAttribute(
          "aria-selected",
          "true",
        );
        expect(viewerHtmlCheckRequests).toEqual([]);
        viewerPage.off("request", trackViewerRequests);

        // Turning off viewer_hide_technical exposes the HTML check tab to the viewer.
        const unhide = await adminPage.request.patch(`${srv.httpURL}/api/v1/projects/${slug}`, {
          headers,
          data: { settings: { viewer_hide_technical: false } },
        });
        expect(unhide.status()).toBe(200);

        await viewerPage.reload();
        const viewerCheckTab = viewerPage.getByRole("tab", { name: "HTML check" });
        await expect(viewerCheckTab).toBeVisible();
        await viewerCheckTab.click();
        await expect(
          viewerPage
            .getByRole("tabpanel")
            .getByRole("heading", { name: "HTML & CSS Compatibility" }),
        ).toBeVisible();
      } finally {
        await viewerContext.close();
      }
    } finally {
      await adminContext.close();
      await srv.stop();
    }
  });
});
