import { type ChildProcess, spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import { createProjectViaAPI, expect, getE2EConfig, test } from "./helpers";

const DEV_APP_KEY = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
const ADMIN_EMAIL = "admin@retention.test";
const ADMIN_PASSWORD = "retention-admin-pass-123";

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
  const dataDir = mkdtempSync(path.join(tmpdir(), "mailcubby-e2e-10-"));
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

test.describe("Phase 10: Project retention settings and admin settings/stats", () => {
  test("owner sets max messages on a project and retention section shows usage against limit", async ({
    page,
  }) => {
    const slug = `ret-${Date.now()}`;
    await createProjectViaAPI("Retention Demo", slug);

    // Send 3 test messages into the project so usage is non-zero.
    const { httpURL } = getE2EConfig();
    for (let i = 0; i < 3; i += 1) {
      const res = await fetch(`${httpURL}/api/v1/projects/${slug}/send-test`, { method: "POST" });
      expect(res.ok).toBe(true);
    }

    await page.goto(`/p/${slug}/settings/retention`);
    await expect(page.getByRole("heading", { name: "Current usage" })).toBeVisible();
    await expect(page.getByText("3 / 5,000 messages")).toBeVisible();

    // Uncheck "Inherit instance default (5,000 messages)" and set a custom limit of 10 messages.
    const inheritMessagesCheckbox = page.getByLabel(/Inherit instance default \(5,000 messages\)/);
    await expect(inheritMessagesCheckbox).toBeChecked();
    await inheritMessagesCheckbox.uncheck();

    const maxMessagesInput = page.getByLabel("Max messages", { exact: true });
    await maxMessagesInput.fill("10");
    await page.getByRole("button", { name: "Save changes" }).click();

    await expect(page.getByText("Retention settings saved")).toBeVisible();
    await expect(page.getByText("3 / 10 messages")).toBeVisible();
    await expect(page.getByText("30%")).toBeVisible();

    const msgBar = page.getByRole("progressbar", { name: "Messages retention usage" });
    await expect(msgBar).toHaveAttribute("aria-valuenow", "3");
    await expect(msgBar).toHaveAttribute("aria-valuemax", "10");

    // Reload and verify the override and usage bar persist.
    await page.reload();
    await expect(page.getByText("3 / 10 messages")).toBeVisible();
    await expect(page.getByLabel("Max messages", { exact: true })).toHaveValue("10");
  });

  test("admin manages instance settings overrides and views stats page in server mode", async ({
    browser,
  }) => {
    const srv = await startBootstrappedServer();
    const context = await browser.newContext();
    const page = await context.newPage();

    try {
      // 1. Sign in as admin.
      await page.goto(`${srv.httpURL}/login`);
      await page.getByLabel("Email").fill(ADMIN_EMAIL);
      await page.getByLabel("Password", { exact: true }).fill(ADMIN_PASSWORD);
      await page.getByRole("button", { name: "Sign in" }).click();
      await expect(page).toHaveURL(`${srv.httpURL}/`);

      // 2. Open Admin > Settings, verify "From env" badges, override instance name and default max messages.
      await page.goto(`${srv.httpURL}/admin/settings`);
      await expect(page.getByRole("heading", { name: "Instance settings" })).toBeVisible();
      await expect(page.getByText("From env").first()).toBeVisible();

      await page.getByLabel("Instance name").fill("Acme Mailcubby");
      await page.getByLabel("Default max messages").fill("250");
      await page.getByRole("button", { name: "Save settings" }).click();

      await expect(page.getByText("Instance settings saved")).toBeVisible();
      await expect(page.getByText("Overridden").first()).toBeVisible();
      await expect(page.getByRole("link", { name: "Acme Mailcubby" })).toBeVisible();

      // 3. Create a project, send a test email, verify project retention inherits the new 250 default.
      await page.goto(`${srv.httpURL}/`);
      await page.getByRole("button", { name: "New project" }).first().click();
      await page.getByLabel("Name").fill("Client App");
      await page.getByRole("button", { name: "Create project" }).click();
      await expect(page).toHaveURL(`${srv.httpURL}/p/client-app`);

      await page.getByRole("button", { name: "Send test email" }).click();
      await expect(page).toHaveURL(/\/p\/client-app\/m\//);

      await page.goto(`${srv.httpURL}/p/client-app/settings/retention`);
      await expect(page.getByText("1 / 250 messages")).toBeVisible();
      await expect(page.getByLabel(/Inherit instance default \(250 messages\)/)).toBeChecked();

      // 4. Reset default max messages back to env on Admin > Settings.
      await page.goto(`${srv.httpURL}/admin/settings`);
      await page.getByRole("button", { name: "Reset max messages to environment default" }).click();
      await expect(page.getByText("Instance settings saved")).toBeVisible();
      await expect(page.getByLabel("Default max messages")).toHaveValue("5000");

      // 5. Open Admin > Stats and verify counts, storage, and runtime sections render.
      await page.getByRole("link", { name: "Stats" }).click();
      await expect(page).toHaveURL(`${srv.httpURL}/admin/stats`);
      await expect(page.getByRole("heading", { name: "Instance statistics" })).toBeVisible();
      await expect(page.getByText("Stored message bytes")).toBeVisible();
      await expect(page.getByText("Blob files on disk")).toBeVisible();
      await expect(page.getByText("SQLite database")).toBeVisible();
      await expect(page.getByText("Uptime")).toBeVisible();
    } finally {
      await context.close();
      await srv.stop();
    }
  });
});
