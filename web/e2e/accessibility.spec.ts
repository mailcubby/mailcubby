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
  test,
  uniqueSlug,
} from "./helpers";

const DEV_APP_KEY = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";

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

async function waitForReadyAndToken(
  url: string,
  child: ChildProcess,
  logs: string[],
): Promise<string> {
  const deadline = Date.now() + 15_000;
  let lastError = "server did not respond";

  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw new Error(`server-mode mailcubby exited early (${child.exitCode}):\n${logs.join("")}`);
    }

    const joined = logs.join("");
    const match = joined.match(/setup token: ([A-Za-z0-9_-]+)/);
    try {
      const res = await fetch(`${url}/readyz`);
      if (res.ok && match?.[1]) {
        return match[1];
      }
      lastError = res.ok ? "waiting for setup token in logs" : `HTTP ${res.status}`;
    } catch (err) {
      lastError = err instanceof Error ? err.message : String(err);
    }

    await new Promise((resolve) => setTimeout(resolve, 100));
  }

  throw new Error(`server-mode mailcubby did not become ready (${lastError}):\n${logs.join("")}`);
}

async function startFreshServerInstance(): Promise<{
  httpURL: string;
  setupToken: string;
  stop: () => Promise<void>;
}> {
  const { binaryPath, repoRoot } = getE2EConfig();
  const httpPort = await getFreePort();
  const smtpPort = await getFreePort();
  const dataDir = mkdtempSync(path.join(tmpdir(), "mailcubby-e2e-a11y-"));
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
      MAILCUBBY_INBOX_DOMAIN: "inbox.a11y.test",
      MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH: "true",
      MAILCUBBY_HTTP_ADDR: `127.0.0.1:${httpPort}`,
      MAILCUBBY_SMTP_ADDR: `127.0.0.1:${smtpPort}`,
      MAILCUBBY_SMTP_PUBLIC_HOST: "mail.a11y.test",
      MAILCUBBY_SMTP_PUBLIC_PORT: String(smtpPort),
      MAILCUBBY_LOG_LEVEL: "warn",
    },
    stdio: ["ignore", "pipe", "pipe"],
  });

  child.stdout?.on("data", (chunk: Buffer) => logs.push(chunk.toString()));
  child.stderr?.on("data", (chunk: Buffer) => logs.push(chunk.toString()));

  try {
    const setupToken = await waitForReadyAndToken(httpURL, child, logs);
    return {
      httpURL,
      setupToken,
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

/**
 * Runs axe-core WCAG 2.1 AA checks on the current page and asserts zero
 * serious or critical violations (SPEC NFR-010, docs/UI.md section 5).
 */
async function assertAccessible(page: Page, screenName: string): Promise<void> {
  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    // Sandboxed preview iframe contains untrusted third-party email HTML, not app chrome.
    .exclude('iframe[title^="Email preview:"]')
    .analyze();

  const blocking = results.violations.filter(
    (v) => v.impact === "serious" || v.impact === "critical",
  );

  if (blocking.length > 0) {
    const details = blocking
      .map(
        (v) =>
          `[${v.impact}] ${v.id}: ${v.help}\n` +
          v.nodes
            .slice(0, 5)
            .map((n) => `  - ${n.target.join(" ")} :: ${n.failureSummary ?? ""}`)
            .join("\n"),
      )
      .join("\n\n");
    expect(blocking, `WCAG 2.1 AA violations on "${screenName}":\n${details}`).toEqual([]);
  }
}

test.describe("Phase 13: WCAG 2.1 AA accessibility audit (NFR-010)", () => {
  test("local mode screens have zero serious or critical axe violations in light and dark themes", async ({
    page,
  }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    const slug = uniqueSlug("a11y-local");
    await createProjectViaAPI("A11y Local", slug);

    // Projects home (light theme).
    await page.goto("/");
    await expect(page.getByRole("link", { name: new RegExp(slug) })).toBeVisible();
    await chooseTheme(page, "Light");
    await assertAccessible(page, "local / (light)");

    // Header menu open (theme radios inside role=menu).
    await page.getByRole("button", { name: "Preferences menu" }).click();
    await expect(page.getByRole("menuitemradio", { name: "Light" })).toHaveAttribute(
      "aria-checked",
      "true",
    );
    await assertAccessible(page, "local header menu");
    await page.keyboard.press("Escape");

    // Projects home (dark theme).
    await chooseTheme(page, "Dark");
    await assertAccessible(page, "local / (dark)");
    await chooseTheme(page, "Light");

    // Empty project inbox (setup guide).
    await page.goto(`/p/${slug}`);
    await expect(page.getByRole("heading", { name: "No messages yet" })).toBeVisible();
    await assertAccessible(page, "local empty inbox");
    await chooseTheme(page, "Dark");
    await assertAccessible(page, "local empty inbox (dark)");
    await chooseTheme(page, "Light");

    // Send a rich fixture and inspect inbox + message view tabs + dialogs.
    await sendFixture("04_multipart_related_cid.eml", { user: slug });
    const row = page.getByRole("option", { name: /Inline CID logo preview/i }).first();
    await expect(row).toBeVisible();
    await row.click();
    await expect(page.locator('iframe[title^="Email preview:"]')).toBeVisible();
    await assertAccessible(page, "local inbox with message preview");

    // Dark theme on inbox with message open.
    await chooseTheme(page, "Dark");
    await assertAccessible(page, "local inbox with message preview (dark)");

    // Phase 15c: reading pane with nothing selected and a search without
    // results, in dark (still selected) and then light.
    for (const theme of ["Dark", "Light"] as const) {
      await page.goto(`/p/${slug}`);
      await expect(
        page.getByRole("heading", { name: "Select a message to inspect" }),
      ).toBeVisible();
      await chooseTheme(page, theme);
      await assertAccessible(page, `local inbox, nothing selected (${theme})`);
      await page.goto(`/p/${slug}?q=zzz-no-such-message`);
      await expect(page.getByRole("heading", { name: "No matching messages" })).toBeVisible();
      await assertAccessible(page, `local search without results (${theme})`);
    }

    // Command palette and shortcuts help dialogs.
    await page.keyboard.press("?");
    await expect(page.getByRole("heading", { name: "Keyboard shortcuts" })).toBeVisible();
    await assertAccessible(page, "shortcuts help dialog");
    await page.keyboard.press("Escape");

    await page.keyboard.press("Control+k");
    await expect(page.getByPlaceholder(/Jump to a project, message, or action/i)).toBeVisible();
    await assertAccessible(page, "command palette dialog");
    await page.keyboard.press("Escape");

    // Local project settings sections.
    for (const section of ["general", "smtp", "retention", "routing", "api"]) {
      await page.goto(`/p/${slug}/settings/${section}`);
      await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
      await assertAccessible(page, `local project settings /${section}`);
    }

    // Account preferences.
    await page.goto("/account/preferences");
    await expect(page.getByRole("heading", { name: "Preferences" })).toBeVisible();
    await assertAccessible(page, "local account preferences");
  });

  test("server mode screens have zero serious or critical axe violations", async ({
    browser,
    page,
  }) => {
    test.setTimeout(90_000);
    await page.emulateMedia({ reducedMotion: "reduce" });
    const srv = await startFreshServerInstance();

    try {
      // Setup wizard step 1..5.
      await page.goto(srv.httpURL);
      await expect(page).toHaveURL(`${srv.httpURL}/setup`);
      await expect(page.getByRole("heading", { name: "Step 1: Setup token" })).toBeVisible();
      await assertAccessible(page, "setup wizard step 1");

      await page.getByLabel("Setup token").fill(srv.setupToken);
      await page.getByRole("button", { name: "Continue to admin account" }).click();
      await expect(page.getByRole("heading", { name: "Step 2: Admin account" })).toBeVisible();
      await assertAccessible(page, "setup wizard step 2");

      await page.getByLabel("Full name").fill("A11y Admin");
      await page.getByLabel("Email").fill("admin@a11y.test");
      await page.getByLabel("Password", { exact: true }).fill("a11y-admin-password-1");
      await page.getByRole("button", { name: "Continue to instance" }).click();
      await expect(page.getByRole("heading", { name: "Step 3: Instance" })).toBeVisible();
      await assertAccessible(page, "setup wizard step 3");

      await page.getByLabel("Instance name").fill("A11y Mail");
      await page.getByRole("button", { name: "Complete setup" }).click();
      await expect(page.getByRole("heading", { name: "Step 4: First project" })).toBeVisible();
      await assertAccessible(page, "setup wizard step 4");

      await page.getByLabel("Project name").fill("Client Portal");
      await page.getByRole("button", { name: "Create project & continue" }).click();
      await expect(page.getByRole("heading", { name: "Step 5: Connect your app" })).toBeVisible();
      await assertAccessible(page, "setup wizard step 5");

      // Send a test message so inbox and audit views have content.
      await page.getByRole("button", { name: "Send test email" }).click();
      await expect(page.getByText("First message received!")).toBeVisible();
      await page.getByRole("button", { name: "Go to inbox" }).click();
      await expect(page).toHaveURL(`${srv.httpURL}/p/client-portal`);
      await assertAccessible(page, "server inbox");

      // Phase 15c: inbox and message view in both themes.
      await page.getByRole("option").first().click();
      await expect(page.locator('iframe[title^="Email preview:"]')).toBeVisible();
      await assertAccessible(page, "server message view");
      await chooseTheme(page, "Dark");
      await assertAccessible(page, "server message view (dark)");
      await page.goto(`${srv.httpURL}/p/client-portal`);
      await expect(
        page.getByRole("heading", { name: "Select a message to inspect" }),
      ).toBeVisible();
      await assertAccessible(page, "server inbox (dark)");
      await chooseTheme(page, "Light");

      // Server projects home.
      await page.goto(`${srv.httpURL}/`);
      await expect(page.getByRole("link", { name: /Client Portal/i })).toBeVisible();
      await assertAccessible(page, "server projects home");

      // All project settings sections (including members & audit).
      for (const section of [
        "general",
        "smtp",
        "members",
        "retention",
        "routing",
        "api",
        "audit",
      ]) {
        await page.goto(`${srv.httpURL}/p/client-portal/settings/${section}`);
        await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
        await assertAccessible(page, `server project settings /${section}`);
      }

      // Create a project invite so we can audit /invite/$token.
      await page.goto(`${srv.httpURL}/p/client-portal/settings/members`);
      await page.getByRole("button", { name: "Invite member" }).click();
      await page.getByLabel("Email address").fill("client@a11y.test");
      await page.getByRole("button", { name: "Create invite link" }).click();
      const inviteURL = await page.locator("#created-invite-url").inputValue();
      expect(inviteURL).toContain("/invite/");

      // All account sections.
      for (const section of ["profile", "security", "tokens", "preferences"]) {
        await page.goto(`${srv.httpURL}/account/${section}`);
        await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
        await assertAccessible(page, `server account /${section}`);
      }

      // All admin sections.
      for (const section of ["users", "projects", "settings", "audit", "stats"]) {
        await page.goto(`${srv.httpURL}/admin/${section}`);
        await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
        await assertAccessible(page, `server admin /${section}`);
      }

      // Generate a password reset link so we can audit /reset/$token.
      await page.goto(`${srv.httpURL}/admin/users`);
      await page.getByRole("button", { name: "Reset password for admin@a11y.test" }).click();
      const resetURL = await page.locator("#reset-link-url").inputValue();
      expect(resetURL).toContain("/reset/");

      // Audit unauthenticated screens (/login, /invite/$token, /reset/$token) in a fresh context.
      const anonContext = await browser.newContext();
      const anonPage = await anonContext.newPage();
      try {
        await anonPage.goto(`${srv.httpURL}/login`);
        await expect(anonPage.getByRole("button", { name: "Sign in" })).toBeVisible();
        await assertAccessible(anonPage, "server /login");

        await anonPage.goto(inviteURL);
        await expect(anonPage.getByRole("heading", { name: /Join Client Portal/i })).toBeVisible();
        await assertAccessible(anonPage, "server /invite/$token");

        await anonPage.goto(resetURL);
        await expect(anonPage.getByRole("heading", { name: /Set a new password/i })).toBeVisible();
        await assertAccessible(anonPage, "server /reset/$token");
      } finally {
        await anonContext.close();
      }
    } finally {
      await srv.stop();
    }
  });
});
