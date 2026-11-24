import { type ChildProcess, spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import { expect, getE2EConfig, test } from "./helpers";

const DEV_APP_KEY = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
const ADMIN_EMAIL = "admin@audit-e2e.test";
const ADMIN_PASSWORD = "audit-e2e-admin-pass-123";

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
  const dataDir = mkdtempSync(path.join(tmpdir(), "mailcubby-e2e-11-"));
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
  } catch (err) {
    child.kill("SIGKILL");
    rmSync(dataDir, { recursive: true, force: true });
    throw err;
  }

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
}

test.describe("Phase 11 audit log UI (project and admin)", () => {
  test("owner views project audit log and admin filters instance audit log", async ({
    browser,
  }) => {
    const srv = await startBootstrappedServer();
    try {
      const ctx = await browser.newContext();
      const page = await ctx.newPage();

      // Sign in as bootstrapped admin.
      await page.goto(`${srv.httpURL}/login`);
      await page.getByLabel("Email").fill(ADMIN_EMAIL);
      await page.getByLabel("Password", { exact: true }).fill(ADMIN_PASSWORD);
      await page.getByRole("button", { name: "Sign in" }).click();
      await expect(page).toHaveURL(`${srv.httpURL}/`);

      // Create a project and perform audited actions via UI.
      await page.getByRole("button", { name: "New project" }).first().click();
      await page.getByLabel("Name").fill("Audit Portal");
      await page.getByRole("button", { name: "Create project" }).click();
      await expect(page).toHaveURL(/\/p\/audit-portal/);

      // Navigate to Project Settings -> SMTP to create a credential.
      await page.goto(`${srv.httpURL}/p/audit-portal/settings/smtp`);
      await page.getByRole("button", { name: /New credential/i }).click();
      await page.getByLabel("Label").fill("staging-app");
      await page.getByRole("button", { name: /Create credential/i }).click();
      await expect(page.getByText(/Credential created/i)).toBeVisible();
      await page.getByRole("button", { name: "Done" }).click();

      // Open Project Settings -> Audit log and verify project events + action filter + metadata expansion.
      await page.getByRole("link", { name: "Audit log" }).click();
      await expect(page).toHaveURL(`${srv.httpURL}/p/audit-portal/settings/audit`);
      const projectTable = page.getByRole("table");
      await expect(projectTable.getByText("smtp_credential.create")).toBeVisible();
      await expect(projectTable.getByText("project.create")).toBeVisible();

      // Filter by action.
      await page.getByLabel("Action").selectOption("smtp_credential.create");
      await expect(projectTable.getByText("smtp_credential.create")).toBeVisible();
      await expect(projectTable.getByText("project.create")).toHaveCount(0);

      // Expand metadata row.
      await projectTable.getByRole("button", { name: "Show metadata" }).first().click();
      await expect(projectTable.getByText(/"staging-app"/)).toBeVisible();

      // Open Admin -> Audit log and verify action, project, and actor filters.
      await page.goto(`${srv.httpURL}/admin/audit`);
      await expect(page.getByRole("heading", { name: "Audit log" })).toBeVisible();
      const adminTable = page.getByRole("table");
      await expect(adminTable.getByText("auth.login")).toBeVisible();
      await expect(adminTable.getByText("smtp_credential.create")).toBeVisible();

      await page.getByLabel("Project").selectOption("audit-portal");
      await page.getByLabel("Action").selectOption("project.create");
      await page.getByLabel(/Actor/).fill(ADMIN_EMAIL);

      await expect(adminTable.getByText("project.create")).toBeVisible();
      await expect(adminTable.getByText("auth.login")).toHaveCount(0);

      await ctx.close();
    } finally {
      await srv.stop();
    }
  });
});
