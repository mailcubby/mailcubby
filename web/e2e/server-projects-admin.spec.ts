import { type ChildProcess, spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import { expect, test } from "@playwright/test";
import { getE2EConfig } from "./helpers";

const DEV_APP_KEY = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
const ADMIN_EMAIL = "admin@agency.test";
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
  const dataDir = mkdtempSync(path.join(tmpdir(), "mailcubby-e2e-7b-"));
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

test.describe("Phase 7b: Multi-tenant projects, invites, viewer mode, and admin UI", () => {
  test("admin invites a viewer who sees only that project without restricted controls, and preview as viewer hides the same controls", async ({
    browser,
  }) => {
    const srv = await startBootstrappedServer();
    const adminContext = await browser.newContext();
    const adminPage = await adminContext.newPage();

    try {
      // 1. Admin signs in.
      await adminPage.goto(`${srv.httpURL}/login`);
      await adminPage.getByLabel("Email").fill(ADMIN_EMAIL);
      await adminPage.getByLabel("Password", { exact: true }).fill(ADMIN_PASSWORD);
      await adminPage.getByRole("button", { name: "Sign in" }).click();
      await expect(adminPage).toHaveURL(`${srv.httpURL}/`);

      // 2. Admin creates two projects: Client Portal and Internal Ops.
      await adminPage.getByRole("button", { name: "New project" }).first().click();
      await adminPage.getByLabel("Name").fill("Client Portal");
      await adminPage.getByRole("button", { name: "Create project" }).click();
      await expect(adminPage).toHaveURL(`${srv.httpURL}/p/client-portal`);

      // Send a test email into Client Portal.
      await adminPage.getByRole("button", { name: "Send test email" }).click();
      await expect(adminPage).toHaveURL(/\/p\/client-portal\/m\//);
      await expect(adminPage.getByRole("tab", { name: "Preview" })).toBeVisible();
      await expect(adminPage.getByRole("tab", { name: "HTML source" })).toBeVisible();
      await expect(adminPage.getByRole("tab", { name: "Headers" })).toBeVisible();
      await expect(adminPage.getByRole("tab", { name: "Raw" })).toBeVisible();
      await expect(adminPage.getByRole("tab", { name: "Tech" })).toBeVisible();

      // Create second project that viewer must NOT see.
      await adminPage.goto(`${srv.httpURL}/`);
      await adminPage.getByRole("button", { name: "New project" }).first().click();
      await adminPage.getByLabel("Name").fill("Internal Ops");
      await adminPage.getByRole("button", { name: "Create project" }).click();
      await expect(adminPage).toHaveURL(`${srv.httpURL}/p/internal-ops`);

      // 3. Open Client Portal settings -> General (rename) -> Members & invites -> invite viewer.
      await adminPage.goto(`${srv.httpURL}/p/client-portal`);
      await adminPage.getByRole("link", { name: "Settings" }).click();
      await expect(adminPage).toHaveURL(`${srv.httpURL}/p/client-portal/settings/general`);

      await adminPage.getByLabel("Project name").fill("Client Portal Staging");
      await adminPage.getByRole("button", { name: "Save changes" }).click();
      await expect(adminPage.getByText("Project settings saved")).toBeVisible();

      await adminPage.getByRole("link", { name: "Members & invites" }).click();
      await expect(adminPage).toHaveURL(`${srv.httpURL}/p/client-portal/settings/members`);

      // Phase 15d (audit M7): the invite action moved to the page header as "Invite people".
      await adminPage.getByRole("button", { name: "Invite people" }).click();
      const inviteDialog = adminPage.getByRole("dialog");
      await inviteDialog.getByLabel("Email address").fill("client@portal.test");
      await inviteDialog.getByLabel("Role", { exact: true }).selectOption("viewer");
      await inviteDialog.getByRole("button", { name: "Create invite link" }).click();

      const inviteUrlInput = inviteDialog.getByLabel("Invite link");
      await expect(inviteUrlInput).toBeVisible();
      const inviteUrl = await inviteUrlInput.inputValue();
      expect(inviteUrl).toContain(`${srv.httpURL}/invite/`);
      await inviteDialog.getByRole("button", { name: "Done" }).click();
      await expect(adminPage.getByText("client@portal.test", { exact: true })).toBeVisible();

      // 4. Open invite link in a fresh browser context -> viewer creates account.
      const viewerContext = await browser.newContext();
      const viewerPage = await viewerContext.newPage();
      try {
        await viewerPage.goto(inviteUrl);
        await expect(
          viewerPage.getByRole("heading", { name: "Join Client Portal Staging" }),
        ).toBeVisible();
        await viewerPage.getByLabel("Full name").fill("Client Reviewer");
        await viewerPage.getByLabel("Password", { exact: true }).fill("viewer-secret-pass-1");
        await viewerPage.getByRole("button", { name: "Create account & join" }).click();

        // Lands in the invited project inbox.
        await expect(viewerPage).toHaveURL(`${srv.httpURL}/p/client-portal`);

        // Phase 15b (audit A4): from page load, the skip link is the first focus
        // stop and reaches the message list in two stops.
        await viewerPage.reload();
        await expect(viewerPage.getByRole("listbox")).toBeVisible();
        await viewerPage.keyboard.press("Tab");
        const skipLink = viewerPage.getByRole("button", { name: "Skip to message list" });
        await expect(skipLink).toBeFocused();
        await expect(skipLink).toBeVisible();
        await viewerPage.keyboard.press("Enter");
        await expect(viewerPage.getByRole("listbox")).toBeFocused();

        // Viewer header (audit V3): logo, search and account menu only; no
        // shortcuts icon and no theme buttons. Shortcuts stay on "?".
        const viewerHeader = viewerPage.getByRole("banner");
        await expect(viewerHeader.getByRole("button", { name: "Search" })).toBeVisible();
        await expect(viewerHeader.getByRole("button", { name: /Account menu/ })).toBeVisible();
        await expect(viewerHeader.getByRole("button", { name: "Keyboard shortcuts" })).toHaveCount(
          0,
        );
        expect(await viewerHeader.locator("a[href], button").count()).toBeLessThanOrEqual(3);
        await viewerPage.keyboard.press("?");
        await expect(viewerPage.getByRole("heading", { name: "Keyboard shortcuts" })).toBeVisible();
        await viewerPage.keyboard.press("Escape");

        // Audit V4: with a single project there is no "Back to all projects".
        await expect(viewerPage.getByRole("link", { name: "Back to all projects" })).toHaveCount(0);

        // Open the test message.
        await viewerPage.getByRole("option").first().click();
        await expect(viewerPage).toHaveURL(/\/p\/client-portal\/m\//);

        // Viewer sees non-technical tabs only.
        await expect(viewerPage.getByRole("tab", { name: "Preview" })).toBeVisible();
        await expect(viewerPage.getByRole("tab", { name: "Text" })).toBeVisible();
        await expect(viewerPage.getByRole("tab", { name: /Attachments/ })).toBeVisible();

        // Technical tabs, .eml download, delete, tag edit, mark-read, and Settings are hidden.
        await expect(viewerPage.getByRole("tab", { name: "HTML source" })).toHaveCount(0);
        await expect(viewerPage.getByRole("tab", { name: "Headers" })).toHaveCount(0);
        await expect(viewerPage.getByRole("tab", { name: "Raw" })).toHaveCount(0);
        await expect(viewerPage.getByRole("tab", { name: "Tech" })).toHaveCount(0);
        await expect(viewerPage.getByRole("link", { name: "Download .eml" })).toHaveCount(0);
        await expect(viewerPage.getByRole("button", { name: "Delete message" })).toHaveCount(0);
        await expect(viewerPage.getByRole("button", { name: /Add tag|Edit tags/ })).toHaveCount(0);
        await expect(viewerPage.getByRole("button", { name: "New tag" })).toHaveCount(0);
        await expect(viewerPage.getByRole("button", { name: /Mark read|Mark unread/ })).toHaveCount(
          0,
        );
        await expect(viewerPage.getByRole("button", { name: "Delete all" })).toHaveCount(0);
        await expect(viewerPage.getByRole("link", { name: "Settings" })).toHaveCount(0);

        // Phase 15c (audit V1, FR-035): with a single project the projects home
        // opens that inbox, with no New project and no Admin link.
        await viewerPage.goto(`${srv.httpURL}/`);
        await expect(viewerPage).toHaveURL(`${srv.httpURL}/p/client-portal`);
        await expect(viewerPage.getByRole("button", { name: "New project" })).toHaveCount(0);
        await expect(viewerPage.getByRole("link", { name: "Admin" })).toHaveCount(0);

        // The project switcher lists only the viewer's project: Client Portal
        // Staging, not Internal Ops.
        await viewerPage
          .getByRole("complementary", { name: "Project sidebar" })
          .getByRole("button", { name: "Client Portal Staging" })
          .click();
        const switcherList = viewerPage.getByRole("listbox", { name: "Projects" });
        await expect(
          switcherList.getByRole("option", { name: "Client Portal Staging" }),
        ).toBeVisible();
        await expect(switcherList.getByRole("option")).toHaveCount(1);
        await expect(switcherList.getByText("Internal Ops")).toHaveCount(0);
        await viewerPage.keyboard.press("Escape");
        await expect(switcherList).toHaveCount(0);

        // Audit V2: the viewer's empty reading pane uses plain language, no shortcuts.
        await expect(viewerPage.getByRole("heading", { name: "No email selected" })).toBeVisible();
        await expect(
          viewerPage.getByText("Choose an email from the list to read it."),
        ).toBeVisible();
        await expect(viewerPage.getByText("next / prev")).toHaveCount(0);

        // Audit V1: after signing in again, the viewer lands in the inbox and
        // reaches the first email in one click.
        await viewerPage.getByRole("button", { name: /Account menu/ }).click();
        await viewerPage.getByRole("menuitem", { name: "Sign out" }).click();
        await expect(viewerPage).toHaveURL(`${srv.httpURL}/login`);
        await viewerPage.getByLabel("Email").fill("client@portal.test");
        await viewerPage.getByLabel("Password", { exact: true }).fill("viewer-secret-pass-1");
        await viewerPage.getByRole("button", { name: "Sign in" }).click();
        await expect(viewerPage).toHaveURL(`${srv.httpURL}/p/client-portal`);
        await viewerPage.getByRole("option").first().click();
        await expect(viewerPage).toHaveURL(/\/p\/client-portal\/m\//);
        await expect(viewerPage.locator('iframe[title^="Email preview:"]')).toBeVisible();

        // An explicit return URL still wins over the single-project landing.
        await viewerPage.getByRole("button", { name: /Account menu/ }).click();
        await viewerPage.getByRole("menuitem", { name: "Sign out" }).click();
        await expect(viewerPage).toHaveURL(`${srv.httpURL}/login`);
        await viewerPage.goto(`${srv.httpURL}/account/profile`);
        await expect(viewerPage).toHaveURL(/\/login\?returnTo=%2Faccount%2Fprofile/);
        await viewerPage.getByLabel("Email").fill("client@portal.test");
        await viewerPage.getByLabel("Password", { exact: true }).fill("viewer-secret-pass-1");
        await viewerPage.getByRole("button", { name: "Sign in" }).click();
        await expect(viewerPage).toHaveURL(`${srv.httpURL}/account/profile`);
      } finally {
        await viewerContext.close();
      }

      // Audit M4: the accepted invite is hidden from the invites table by default
      // (the person is in the members table) and shown again with "Show accepted".
      await adminPage.goto(`${srv.httpURL}/p/client-portal/settings/members`);
      const invitesCard = adminPage.getByRole("region", { name: "Invites" });
      const membersCard = adminPage.getByRole("region", { name: "Members" });
      await expect(membersCard.getByText("client@portal.test", { exact: true })).toBeVisible();
      await expect(invitesCard.getByText("No pending invites.")).toBeVisible();
      await expect(invitesCard.getByText("client@portal.test", { exact: true })).toHaveCount(0);
      await invitesCard.getByLabel("Show accepted").check();
      await expect(invitesCard.getByText("client@portal.test", { exact: true })).toBeVisible();
      await expect(invitesCard.getByText("accepted", { exact: true })).toBeVisible();

      // 5. As owner on the same message, toggle "Preview as viewer" and verify the same controls hide.
      await adminPage.goto(`${srv.httpURL}/p/client-portal`);
      await adminPage.getByRole("option").first().click();
      await expect(adminPage).toHaveURL(/\/p\/client-portal\/m\//);

      // Owner controls and technical tabs are visible before toggling.
      await expect(adminPage.getByRole("button", { name: "Delete message" })).toBeVisible();
      await expect(adminPage.getByRole("button", { name: /Add tag|Edit tags/ })).toBeVisible();
      await expect(adminPage.getByRole("tab", { name: "HTML source" })).toBeVisible();
      await expect(adminPage.getByRole("tab", { name: "Tech" })).toBeVisible();
      await expect(adminPage.getByRole("link", { name: "Settings" })).toBeVisible();

      // Toggle Preview as viewer.
      await expect(adminPage.getByRole("button", { name: "Keyboard shortcuts" })).toBeVisible();
      await expect(adminPage.getByRole("link", { name: "Back to all projects" })).toBeVisible();
      await adminPage.getByRole("button", { name: "Preview as viewer" }).click();
      await expect(adminPage.getByTestId("viewer-preview-banner")).toBeVisible();
      await expect(adminPage.getByRole("button", { name: "Keyboard shortcuts" })).toHaveCount(0);

      // Same controls and technical tabs are now hidden.
      await expect(adminPage.getByRole("tab", { name: "Preview" })).toBeVisible();
      await expect(adminPage.getByRole("tab", { name: "Text" })).toBeVisible();
      await expect(adminPage.getByRole("tab", { name: "HTML source" })).toHaveCount(0);
      await expect(adminPage.getByRole("tab", { name: "Headers" })).toHaveCount(0);
      await expect(adminPage.getByRole("tab", { name: "Raw" })).toHaveCount(0);
      await expect(adminPage.getByRole("tab", { name: "Tech" })).toHaveCount(0);
      await expect(adminPage.getByRole("link", { name: "Download .eml" })).toHaveCount(0);
      await expect(adminPage.getByRole("button", { name: "Delete message" })).toHaveCount(0);
      await expect(adminPage.getByRole("button", { name: /Add tag|Edit tags/ })).toHaveCount(0);
      await expect(adminPage.getByRole("button", { name: "New tag" })).toHaveCount(0);
      await expect(adminPage.getByRole("button", { name: /Mark read|Mark unread/ })).toHaveCount(0);
      await expect(adminPage.getByRole("link", { name: "Settings" })).toHaveCount(0);

      // Exit preview restores owner controls.
      await adminPage.getByRole("button", { name: "Exit preview" }).click();
      await expect(adminPage.getByTestId("viewer-preview-banner")).toHaveCount(0);
      await expect(adminPage.getByRole("button", { name: "Delete message" })).toBeVisible();
      await expect(adminPage.getByRole("tab", { name: "HTML source" })).toBeVisible();
    } finally {
      await adminContext.close();
      await srv.stop();
    }
  });

  test("admin creates a user, opens instance invite to set password, lands on projects home, and manages admin pages", async ({
    browser,
  }) => {
    const srv = await startBootstrappedServer();
    const adminContext = await browser.newContext();
    const adminPage = await adminContext.newPage();

    try {
      // 1. Admin signs in and opens Admin > Users.
      await adminPage.goto(`${srv.httpURL}/login`);
      await adminPage.getByLabel("Email").fill(ADMIN_EMAIL);
      await adminPage.getByLabel("Password", { exact: true }).fill(ADMIN_PASSWORD);
      await adminPage.getByRole("button", { name: "Sign in" }).click();
      await expect(adminPage).toHaveURL(`${srv.httpURL}/`);

      await adminPage.getByRole("link", { name: "Admin" }).click();
      await expect(adminPage).toHaveURL(`${srv.httpURL}/admin/users`);

      // 2. Admin creates a new user with can_create_projects enabled.
      await adminPage.getByRole("button", { name: "Invite user" }).click();
      const createUserDialog = adminPage.getByRole("dialog");
      await createUserDialog.getByLabel("Full name").fill("Agency Dev");
      await createUserDialog.getByLabel("Email address").fill("dev@agency.test");
      await createUserDialog.getByRole("checkbox", { name: /Can create projects/ }).check();
      await createUserDialog.getByRole("button", { name: "Create user & invite link" }).click();

      const inviteUrlInput = createUserDialog.getByLabel("Invite link");
      await expect(inviteUrlInput).toBeVisible();
      const inviteUrl = await inviteUrlInput.inputValue();
      expect(inviteUrl).toContain(`${srv.httpURL}/invite/`);
      await createUserDialog.getByRole("button", { name: "Done" }).click();
      await expect(adminPage.getByText("dev@agency.test", { exact: true })).toBeVisible();

      // 3. New user opens the instance invite link in a fresh browser context, sets password, lands on /.
      const devContext = await browser.newContext();
      const devPage = await devContext.newPage();
      try {
        await devPage.goto(inviteUrl);
        await expect(devPage.getByText("dev@agency.test", { exact: true })).toBeVisible();
        await devPage.getByLabel("Password", { exact: true }).fill("dev-initial-pass-123");
        await devPage.getByRole("button", { name: "Set password & sign in" }).click();

        await expect(devPage).toHaveURL(`${srv.httpURL}/`);

        // User has can_create_projects so "New project" is available; Admin link is not.
        await expect(devPage.getByRole("link", { name: "Admin" })).toHaveCount(0);
        await devPage.getByRole("button", { name: "New project" }).first().click();
        await devPage.getByLabel("Name").fill("Dev Sandbox");
        await devPage.getByRole("button", { name: "Create project" }).click();
        await expect(devPage).toHaveURL(`${srv.httpURL}/p/dev-sandbox`);
      } finally {
        await devContext.close();
      }

      // 4. Admin sees the new project with owner on Admin > Projects.
      await adminPage.getByRole("link", { name: "Projects", exact: true }).click();
      await expect(adminPage).toHaveURL(`${srv.httpURL}/admin/projects`);
      await expect(adminPage.getByRole("link", { name: "Dev Sandbox", exact: true })).toBeVisible();
      await expect(adminPage.getByText("Agency Dev", { exact: true })).toBeVisible();

      // 5. Admin generates a password reset link for dev@agency.test and reset page works end-to-end.
      await adminPage.getByRole("link", { name: "Users", exact: true }).click();
      await expect(adminPage).toHaveURL(`${srv.httpURL}/admin/users`);
      await adminPage.getByRole("button", { name: "Reset password for dev@agency.test" }).click();
      const resetDialog = adminPage.getByRole("dialog");
      const resetUrlInput = resetDialog.getByLabel("Reset URL");
      await expect(resetUrlInput).toBeVisible();
      const resetUrl = await resetUrlInput.inputValue();
      expect(resetUrl).toContain(`${srv.httpURL}/reset/`);
      await resetDialog.getByRole("button", { name: "Done" }).click();

      const resetContext = await browser.newContext();
      const resetPage = await resetContext.newPage();
      try {
        await resetPage.goto(resetUrl);
        await resetPage.getByLabel("New password").fill("dev-reset-pass-456");
        await resetPage.getByRole("button", { name: "Reset password" }).click();
        await expect(resetPage.getByRole("link", { name: "Sign in" })).toBeVisible();
        await resetPage.getByRole("link", { name: "Sign in" }).click();

        await resetPage.getByLabel("Email").fill("dev@agency.test");
        await resetPage.getByLabel("Password", { exact: true }).fill("dev-reset-pass-456");
        await resetPage.getByRole("button", { name: "Sign in" }).click();
        // Phase 15e (audit V1, FR-035): dev@agency.test can create projects, so
        // with one project they keep the projects home and its "New project".
        await expect(resetPage).toHaveURL(`${srv.httpURL}/`);
        await expect(resetPage.getByRole("link", { name: /Dev Sandbox/ })).toBeVisible();
        await expect(resetPage.getByRole("button", { name: "New project" })).toBeVisible();
      } finally {
        await resetContext.close();
      }
    } finally {
      await adminContext.close();
      await srv.stop();
    }
  });
});
