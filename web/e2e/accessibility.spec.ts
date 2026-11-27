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
const ADMIN_EMAIL = "admin@a11y.test";
const ADMIN_PASSWORD = "a11y-admin-password-1";
const VIEWER_EMAIL = "viewer@a11y.test";
const VIEWER_PASSWORD = "a11y-viewer-password-1";
const THEMES = ["Light", "Dark"] as const;
type Theme = (typeof THEMES)[number];

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

interface FreshServer {
  httpURL: string;
  setupToken: string;
  stop: () => Promise<void>;
}

async function startFreshServerInstance(): Promise<FreshServer> {
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
 * ApiClient seeds data through the REST API (session cookie and CSRF header in
 * server mode; plain requests in local mode, which has no accounts).
 */
class ApiClient {
  private cookie = "";
  private csrf = "";

  constructor(
    private readonly base: string,
    private readonly sendOrigin = true,
  ) {}

  static async login(base: string, email: string, password: string): Promise<ApiClient> {
    const client = new ApiClient(base);
    await client.request("POST", "/api/v1/auth/login", { email, password });
    const me = await client.request<{ csrf_token: string }>("GET", "/api/v1/auth/me");
    client.csrf = me.csrf_token;
    return client;
  }

  async request<T = unknown>(method: string, apiPath: string, body?: unknown): Promise<T> {
    const headers: Record<string, string> = { "Content-Type": "application/json" };
    if (this.sendOrigin) {
      headers.Origin = this.base;
    }
    if (this.cookie) {
      headers.Cookie = this.cookie;
    }
    if (this.csrf) {
      headers["X-CSRF-Token"] = this.csrf;
    }
    const res = await fetch(`${this.base}${apiPath}`, {
      method,
      headers,
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    for (const setCookie of res.headers.getSetCookie()) {
      const pair = setCookie.split(";")[0] ?? "";
      if (!pair.endsWith("=")) {
        this.cookie = pair;
      }
    }
    const text = await res.text();
    if (!res.ok) {
      throw new Error(`${method} ${apiPath}: HTTP ${res.status} ${text}`);
    }
    return (text ? JSON.parse(text) : null) as T;
  }
}

/** markAllSeen marks every message of a project as read, so the Unread filter is empty. */
async function markAllSeen(client: ApiClient, projectSlug: string): Promise<void> {
  const list = await client.request<{ items: Array<{ id: string }> }>(
    "GET",
    `/api/v1/projects/${projectSlug}/messages`,
  );
  for (const item of list.items) {
    await client.request("PATCH", `/api/v1/projects/${projectSlug}/messages/${item.id}`, {
      seen: true,
    });
  }
}

/**
 * Runs axe-core WCAG 2.1 AA checks on the current page and asserts zero
 * serious or critical violations (SPEC NFR-010, docs/UI.md section 5).
 */
async function assertAccessible(page: Page, screenName: string): Promise<void> {
  // Let colour transitions settle (0.01 ms under reduced motion, but still one
  // frame away), so axe never reads a half-applied state such as a toggle
  // button whose background changed before its text colour.
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

/**
 * setAuthPageTheme uses the small Light / Dark toggle in the top bar of the
 * sign-in, setup, invite and reset pages (they have no account menu).
 */
async function setAuthPageTheme(page: Page, theme: Theme): Promise<void> {
  await page.getByRole("banner").getByRole("button", { name: theme, exact: true }).click();
  if (theme === "Dark") {
    await expect(page.locator("html")).toHaveClass(/(^|\s)dark(\s|$)/);
  } else {
    await expect(page.locator("html")).not.toHaveClass(/(^|\s)dark(\s|$)/);
  }
}

/** assertAuthPageBothThemes audits a public page in light, then dark, and leaves it light. */
async function assertAuthPageBothThemes(page: Page, screenName: string): Promise<void> {
  for (const theme of THEMES) {
    await setAuthPageTheme(page, theme);
    await assertAccessible(page, `${screenName} (${theme})`);
  }
  await setAuthPageTheme(page, "Light");
}

test.describe("WCAG 2.1 AA accessibility audit (NFR-010, Phase 13 and 15e)", () => {
  test("local mode screens have zero serious or critical axe violations in light and dark", async ({
    page,
  }) => {
    test.setTimeout(180_000);
    await page.emulateMedia({ reducedMotion: "reduce" });
    const { httpURL } = getE2EConfig();
    const slug = uniqueSlug("a11y-local");
    const emptySlug = uniqueSlug("a11y-empty");
    await createProjectViaAPI("A11y Local", slug);
    await createProjectViaAPI("A11y Empty", emptySlug);
    await sendFixture("04_multipart_related_cid.eml", { user: slug });
    // Phase 15e: with every message read, the Unread filter shows "No unread messages".
    await markAllSeen(new ApiClient(httpURL, false), slug);

    for (const theme of THEMES) {
      // Projects home and the header "Preferences menu" (theme radios).
      await page.goto("/");
      await expect(page.getByRole("link", { name: new RegExp(slug) })).toBeVisible();
      await chooseTheme(page, theme);
      await assertAccessible(page, `local projects home (${theme})`);
      await page.getByRole("button", { name: "Preferences menu" }).click();
      await expect(page.getByRole("menuitemradio", { name: theme })).toHaveAttribute(
        "aria-checked",
        "true",
      );
      await assertAccessible(page, `local header menu (${theme})`);
      await page.keyboard.press("Escape");

      // Empty project inbox (setup guide).
      await page.goto(`/p/${emptySlug}`);
      await expect(page.getByRole("heading", { name: "No messages yet" })).toBeVisible();
      await assertAccessible(page, `local empty inbox (${theme})`);

      // Inbox list with nothing selected, then the message view.
      await page.goto(`/p/${slug}`);
      await expect(
        page.getByRole("heading", { name: "Select a message to inspect" }),
      ).toBeVisible();
      await assertAccessible(page, `local inbox, nothing selected (${theme})`);
      await page
        .getByRole("option", { name: /Inline CID logo preview/i })
        .first()
        .click();
      await expect(page.locator('iframe[title^="Email preview:"]')).toBeVisible();
      await assertAccessible(page, `local message view (${theme})`);

      // Search without results and the Unread filter with nothing unread.
      await page.goto(`/p/${slug}?q=zzz-no-such-message`);
      await expect(page.getByRole("heading", { name: "No matching messages" })).toBeVisible();
      await assertAccessible(page, `local search without results (${theme})`);
      await page.goto(`/p/${slug}?q=is%3Aunread`);
      await expect(page.getByRole("heading", { name: "No unread messages" })).toBeVisible();
      await assertAccessible(page, `local no unread messages (${theme})`);

      // Shortcuts help and command palette dialogs.
      await page.keyboard.press("?");
      await expect(page.getByRole("heading", { name: "Keyboard shortcuts" })).toBeVisible();
      await assertAccessible(page, `shortcuts help dialog (${theme})`);
      await page.keyboard.press("Escape");
      await page.keyboard.press("Control+k");
      await expect(page.getByPlaceholder(/Jump to a project, message, or action/i)).toBeVisible();
      await assertAccessible(page, `command palette dialog (${theme})`);
      await page.keyboard.press("Escape");

      // Local project settings sections.
      for (const section of ["general", "smtp", "retention", "routing", "api"]) {
        await page.goto(`/p/${slug}/settings/${section}`);
        await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
        await assertAccessible(page, `local project settings /${section} (${theme})`);
      }

      // Account preferences (the only account page in local mode).
      await page.goto("/account/preferences");
      await expect(page.getByRole("heading", { name: "Preferences" })).toBeVisible();
      await assertAccessible(page, `local account preferences (${theme})`);

      // Phase 15e: unknown addresses show the not-found page.
      await page.goto("/no-such-page");
      await expect(page.getByRole("heading", { name: "Page not found" })).toBeVisible();
      await assertAccessible(page, `local page not found (${theme})`);
    }

    // Phase 15e: below 768 px an open message replaces the list, so the skip
    // link is not rendered there (no control without a target).
    await page.setViewportSize({ width: 375, height: 800 });
    await page.goto(`/p/${slug}`);
    const skipLink = page.getByRole("button", { name: "Skip to message list" });
    await expect(skipLink).toHaveCount(1);
    await page
      .getByRole("option", { name: /Inline CID logo preview/i })
      .first()
      .click();
    await expect(page.getByRole("link", { name: "Back to A11y Local" })).toBeVisible();
    await expect(skipLink).toHaveCount(0);
  });

  test.describe
    .serial("server mode screens in light and dark", () => {
      let srv: FreshServer | undefined;
      let pendingInviteURL = "";
      let resetURL = "";

      test.beforeAll(async () => {
        srv = await startFreshServerInstance();
      });

      test.afterAll(async () => {
        await srv?.stop();
      });

      function server(): FreshServer {
        if (!srv) {
          throw new Error("server-mode instance not started");
        }
        return srv;
      }

      async function signIn(page: Page, email: string, password: string): Promise<void> {
        await page.goto(`${server().httpURL}/login`);
        await page.getByLabel("Email").fill(email);
        await page.getByLabel("Password", { exact: true }).fill(password);
        await page.getByRole("button", { name: "Sign in" }).click();
        await expect(page).not.toHaveURL(/\/login/);
      }

      test("setup wizard steps 1 to 5", async ({ page }) => {
        test.setTimeout(120_000);
        await page.emulateMedia({ reducedMotion: "reduce" });
        const { httpURL, setupToken } = server();

        await page.goto(httpURL);
        await expect(page).toHaveURL(`${httpURL}/setup`);
        await expect(page.getByRole("heading", { name: "Step 1: Setup token" })).toBeVisible();
        await assertAuthPageBothThemes(page, "setup wizard step 1");
        await page.getByRole("button", { name: "Continue to admin account" }).click();
        await expect(page.getByText("Paste the setup token")).toBeVisible();
        await assertAuthPageBothThemes(page, "setup wizard step 1 error");

        await page.getByLabel("Setup token").fill(setupToken);
        await page.getByRole("button", { name: "Continue to admin account" }).click();
        await expect(page.getByRole("heading", { name: "Step 2: Admin account" })).toBeVisible();
        await assertAuthPageBothThemes(page, "setup wizard step 2");

        await page.getByLabel("Full name").fill("A11y Admin");
        await page.getByLabel("Email").fill(ADMIN_EMAIL);
        await page.getByLabel("Password", { exact: true }).fill(ADMIN_PASSWORD);
        await page.getByRole("button", { name: "Continue to instance" }).click();
        await expect(page.getByRole("heading", { name: "Step 3: Instance" })).toBeVisible();
        await assertAuthPageBothThemes(page, "setup wizard step 3");

        await page.getByLabel("Instance name").fill("A11y Mail");
        await page.getByRole("button", { name: "Complete setup" }).click();
        await expect(page.getByRole("heading", { name: "Step 4: First project" })).toBeVisible();
        await assertAuthPageBothThemes(page, "setup wizard step 4");

        await page.getByLabel("Project name").fill("Client Portal");
        await page.getByRole("button", { name: "Create project & continue" }).click();
        await expect(page.getByRole("heading", { name: "Step 5: Connect your app" })).toBeVisible();
        await assertAuthPageBothThemes(page, "setup wizard step 5, waiting");

        // Audit M9: the first message turns the status into the success state.
        await page.getByRole("button", { name: "Send test email" }).click();
        await expect(page.getByText("First message received!")).toBeVisible();
        await assertAuthPageBothThemes(page, "setup wizard step 5, first message");
        await page.getByRole("button", { name: "Go to inbox" }).click();
        await expect(page).toHaveURL(`${httpURL}/p/client-portal`);
      });

      test("owner and admin screens", async ({ page }) => {
        test.setTimeout(240_000);
        await page.emulateMedia({ reducedMotion: "reduce" });
        const { httpURL } = server();

        // Seed: an empty project, a viewer who accepted an invite, a pending
        // invite and a reset link for the public pages, and no unread mail.
        const admin = await ApiClient.login(httpURL, ADMIN_EMAIL, ADMIN_PASSWORD);
        await admin.request("POST", "/api/v1/projects", { name: "Empty Box", slug: "empty-box" });
        const viewerInvite = await admin.request<{ url: string }>(
          "POST",
          "/api/v1/projects/client-portal/invites",
          { email: VIEWER_EMAIL, role: "viewer" },
        );
        const viewerToken = viewerInvite.url.split("/invite/")[1] ?? "";
        await new ApiClient(httpURL).request("POST", `/api/v1/invites/${viewerToken}/accept`, {
          name: "A11y Viewer",
          password: VIEWER_PASSWORD,
        });
        const pending = await admin.request<{ url: string }>(
          "POST",
          "/api/v1/projects/client-portal/invites",
          { email: "client@a11y.test", role: "viewer" },
        );
        pendingInviteURL = pending.url;
        const me = await admin.request<{ user: { id: number } }>("GET", "/api/v1/auth/me");
        const reset = await admin.request<{ url: string }>(
          "POST",
          `/api/v1/admin/users/${me.user.id}/password-reset`,
        );
        resetURL = reset.url;
        await markAllSeen(admin, "client-portal");

        await signIn(page, ADMIN_EMAIL, ADMIN_PASSWORD);
        await expect(page).toHaveURL(`${httpURL}/`);

        for (const theme of THEMES) {
          // Projects home and account menu.
          await page.goto(`${httpURL}/`);
          await expect(page.getByRole("link", { name: /Client Portal/i })).toBeVisible();
          await chooseTheme(page, theme);
          await assertAccessible(page, `server projects home (${theme})`);
          await page.getByRole("button", { name: /Account menu/ }).click();
          await expect(page.getByRole("menu")).toBeVisible();
          await assertAccessible(page, `server account menu (${theme})`);
          await page.keyboard.press("Escape");

          // Inbox: nothing selected, message view, no unread, no results, empty.
          await page.goto(`${httpURL}/p/client-portal`);
          await expect(
            page.getByRole("heading", { name: "Select a message to inspect" }),
          ).toBeVisible();
          await assertAccessible(page, `server inbox (${theme})`);
          await page.getByRole("option").first().click();
          await expect(page.locator('iframe[title^="Email preview:"]')).toBeVisible();
          await assertAccessible(page, `server message view (${theme})`);
          await page.goto(`${httpURL}/p/client-portal?q=is%3Aunread`);
          await expect(page.getByRole("heading", { name: "No unread messages" })).toBeVisible();
          await assertAccessible(page, `server no unread messages (${theme})`);
          await page.goto(`${httpURL}/p/client-portal?q=zzz-no-such-message`);
          await expect(page.getByRole("heading", { name: "No matching messages" })).toBeVisible();
          await assertAccessible(page, `server search without results (${theme})`);
          await page.goto(`${httpURL}/p/empty-box`);
          await expect(page.getByRole("heading", { name: "No messages yet" })).toBeVisible();
          await assertAccessible(page, `server empty inbox (${theme})`);

          // Preview as viewer (banner and viewer reading pane).
          await page.goto(`${httpURL}/p/client-portal`);
          await page.getByRole("button", { name: "Preview as viewer" }).click();
          await expect(page.getByTestId("viewer-preview-banner")).toBeVisible();
          await assertAccessible(page, `server preview as viewer (${theme})`);
          await page.getByRole("button", { name: "Exit preview" }).click();

          // Every project settings section.
          for (const section of [
            "general",
            "smtp",
            "members",
            "retention",
            "routing",
            "api",
            "audit",
          ]) {
            await page.goto(`${httpURL}/p/client-portal/settings/${section}`);
            await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
            await assertAccessible(page, `server project settings /${section} (${theme})`);
          }
          await page.goto(`${httpURL}/p/empty-box/settings/smtp`);
          await expect(
            page.getByRole("heading", { name: "No SMTP credentials yet" }),
          ).toBeVisible();
          await assertAccessible(page, `server SMTP without credentials (${theme})`);

          // Members with the invite dialog, before and after creating a link
          // (audit M7: "Invite people" in the page header).
          await page.goto(`${httpURL}/p/client-portal/settings/members`);
          await page.getByRole("button", { name: "Invite people" }).click();
          await expect(page.getByLabel("Email address")).toBeVisible();
          await assertAccessible(page, `server invite dialog (${theme})`);
          await page.getByLabel("Email address").fill(`link-${theme.toLowerCase()}@a11y.test`);
          await page.getByRole("button", { name: "Create invite link" }).click();
          await expect(page.locator("#created-invite-url")).toHaveValue(/\/invite\//);
          await assertAccessible(page, `server invite link dialog (${theme})`);
          await page.getByRole("button", { name: "Done" }).click();

          // Every account and admin section.
          for (const section of ["profile", "security", "tokens", "preferences"]) {
            await page.goto(`${httpURL}/account/${section}`);
            await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
            await assertAccessible(page, `server account /${section} (${theme})`);
          }
          for (const section of ["users", "projects", "settings", "audit", "stats"]) {
            await page.goto(`${httpURL}/admin/${section}`);
            await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
            await assertAccessible(page, `server admin /${section} (${theme})`);
          }

          // Not-found screens (audit V7).
          await page.goto(`${httpURL}/no-such-page`);
          await expect(page.getByRole("heading", { name: "Page not found" })).toBeVisible();
          await assertAccessible(page, `server page not found (${theme})`);
          await page.goto(`${httpURL}/p/no-such-project`);
          await expect(
            page.getByRole("heading", { name: "Project not found or you do not have access" }),
          ).toBeVisible();
          await assertAccessible(page, `server project not found (${theme})`);
        }
      });

      test("viewer screens", async ({ page }) => {
        test.setTimeout(120_000);
        await page.emulateMedia({ reducedMotion: "reduce" });
        const { httpURL } = server();

        // Audit V1: a viewer with one project lands in its inbox.
        await signIn(page, VIEWER_EMAIL, VIEWER_PASSWORD);
        await expect(page).toHaveURL(`${httpURL}/p/client-portal`);

        for (const theme of THEMES) {
          await page.goto(`${httpURL}/p/client-portal`);
          await expect(page.getByRole("heading", { name: "No email selected" })).toBeVisible();
          await chooseTheme(page, theme);
          await assertAccessible(page, `viewer inbox (${theme})`);
          await page.getByRole("option").first().click();
          await expect(page.locator('iframe[title^="Email preview:"]')).toBeVisible();
          await assertAccessible(page, `viewer message view (${theme})`);

          // Audit V7: settings are unavailable to viewers.
          await page.goto(`${httpURL}/p/client-portal/settings/general`);
          await expect(page.getByRole("heading", { name: "Settings unavailable" })).toBeVisible();
          await assertAccessible(page, `viewer settings unavailable (${theme})`);
        }
      });

      test("sign-in, invite and reset pages", async ({ page }) => {
        test.setTimeout(120_000);
        await page.emulateMedia({ reducedMotion: "reduce" });
        const { httpURL } = server();

        // Audit V6: the instance name is the sign-in title.
        await page.goto(`${httpURL}/login`);
        await expect(page.getByRole("heading", { level: 1, name: "A11y Mail" })).toBeVisible();
        await expect(page.getByText("Powered by Mailcubby")).toBeVisible();
        await assertAuthPageBothThemes(page, "server /login");
        await page.getByLabel("Email").fill(ADMIN_EMAIL);
        await page.getByLabel("Password", { exact: true }).fill("not-the-password-1");
        await page.getByRole("button", { name: "Sign in" }).click();
        await expect(page.getByText("Email or password is incorrect")).toBeVisible();
        await assertAuthPageBothThemes(page, "server /login error");

        await page.goto(pendingInviteURL);
        await expect(page.getByRole("heading", { name: /Join Client Portal/i })).toBeVisible();
        await assertAuthPageBothThemes(page, "server /invite/$token");
        await page.goto(`${httpURL}/invite/not-a-real-invite-token`);
        await expect(page.getByRole("heading", { name: "Invitation unavailable" })).toBeVisible();
        await assertAuthPageBothThemes(page, "server invalid invite");

        await page.goto(resetURL);
        await expect(page.getByRole("heading", { name: /Set a new password/i })).toBeVisible();
        await assertAuthPageBothThemes(page, "server /reset/$token");
      });

      test("signed-in project invite warns on email mismatch in both themes (audit S8)", async ({
        page,
      }) => {
        test.setTimeout(120_000);
        await page.emulateMedia({ reducedMotion: "reduce" });
        const { httpURL } = server();

        const admin = await ApiClient.login(httpURL, ADMIN_EMAIL, ADMIN_PASSWORD);
        const matchingInvite = await admin.request<{ url: string }>(
          "POST",
          "/api/v1/projects/empty-box/invites",
          { email: VIEWER_EMAIL.toUpperCase(), role: "viewer" },
        );
        const mismatchedInvite = await admin.request<{ url: string }>(
          "POST",
          "/api/v1/projects/empty-box/invites",
          { email: "other-client@a11y.test", role: "viewer" },
        );

        await signIn(page, VIEWER_EMAIL, VIEWER_PASSWORD);

        // Matching email (ignoring case): no mismatch warning alert.
        await page.goto(matchingInvite.url);
        await expect(page.getByRole("heading", { name: "Join Empty Box" })).toBeVisible();
        await expect(page.getByRole("alert")).toHaveCount(0);
        await expect(page.getByText(/Signed in as/)).toBeVisible();

        // Different email: clear warning alert naming both emails, accessible in both themes.
        await page.goto(mismatchedInvite.url);
        await expect(page.getByRole("heading", { name: "Join Empty Box" })).toBeVisible();
        const alert = page.getByRole("alert");
        await expect(alert).toBeVisible();
        await expect(alert).toContainText("Different email address");
        await expect(alert).toContainText("other-client@a11y.test");
        await expect(alert).toContainText(VIEWER_EMAIL);
        await expect(
          page.getByRole("button", { name: "Sign out to use a different account" }),
        ).toBeVisible();
        await assertAuthPageBothThemes(page, "server /invite/$token email mismatch");

        // Accepting still works for the signed-in account.
        await page.getByRole("button", { name: "Accept invitation" }).click();
        await expect(page).toHaveURL(`${httpURL}/p/empty-box`);
      });
    });
});
