import { type ChildProcess, execFile, spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import { expect, test } from "@playwright/test";
import { getE2EConfig } from "./helpers";

const execFileAsync = promisify(execFile);

const DEV_APP_KEY = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
const ADMIN_EMAIL = "admin@smtp-e2e.test";
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
  smtpAddr: string;
  stop: () => Promise<void>;
}> {
  const { binaryPath, repoRoot } = getE2EConfig();
  const httpPort = await getFreePort();
  const smtpPort = await getFreePort();
  const dataDir = mkdtempSync(path.join(tmpdir(), "mailcubby-e2e-p8-"));
  const httpURL = `http://127.0.0.1:${httpPort}`;
  const smtpAddr = `127.0.0.1:${smtpPort}`;
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
      MAILCUBBY_SMTP_ADDR: smtpAddr,
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
      smtpAddr,
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

async function sendAuthenticatedEmail(
  smtpAddr: string,
  user: string,
  password: string,
  subject: string,
): Promise<void> {
  const { binaryPath, repoRoot } = getE2EConfig();
  const raw = [
    "From: App <app@shop.test>",
    "To: Customer <customer@example.test>",
    `Subject: ${subject}`,
    "Content-Type: text/plain; charset=utf-8",
    "",
    `Body for ${subject}`,
    "",
  ].join("\r\n");

  const proc = execFileAsync(
    binaryPath,
    ["sendmail", "--smtp", smtpAddr, "--user", user, "--password", password, "-t"],
    { cwd: repoRoot, maxBuffer: 1024 * 1024 },
  );
  proc.child.stdin?.end(raw);
  await proc;
}

test.describe("Phase 8: SMTP credentials sidebar filter and credential: search", () => {
  test("filters inbox by sidebar credential and credential: query, and hides credentials in viewer preview", async ({
    browser,
  }) => {
    const srv = await startBootstrappedServer();
    const context = await browser.newContext();
    const page = await context.newPage();

    try {
      // 1. Sign in as admin and create project "Shop".
      await page.goto(`${srv.httpURL}/login`);
      await page.getByLabel("Email").fill(ADMIN_EMAIL);
      await page.getByLabel("Password", { exact: true }).fill(ADMIN_PASSWORD);
      await page.getByRole("button", { name: "Sign in" }).click();
      await expect(page).toHaveURL(`${srv.httpURL}/`);

      await page.getByRole("button", { name: "New project" }).first().click();
      await page.getByLabel("Name").fill("Shop");
      await page.getByRole("button", { name: "Create project" }).click();
      await expect(page).toHaveURL(`${srv.httpURL}/p/shop`);

      // 2. Create two SMTP credentials via the authenticated API.
      const meRes = await page.request.get(`${srv.httpURL}/api/v1/auth/me`);
      const me = (await meRes.json()) as { csrf_token: string };
      const headers = {
        "X-CSRF-Token": me.csrf_token,
        Origin: srv.httpURL,
      };

      const createCred = async (label: string) => {
        const res = await page.request.post(
          `${srv.httpURL}/api/v1/projects/shop/smtp-credentials`,
          {
            headers,
            data: { label },
          },
        );
        expect(res.status()).toBe(201);
        return (await res.json()) as {
          credential: { id: number; label: string; username: string };
          password: string;
        };
      };

      const staging = await createCred("staging");
      const worker = await createCred("worker");

      // 3. Send one authenticated email with each credential via SMTP AUTH.
      await sendAuthenticatedEmail(
        srv.smtpAddr,
        staging.credential.username,
        staging.password,
        "Staging deploy notice",
      );
      await sendAuthenticatedEmail(
        srv.smtpAddr,
        worker.credential.username,
        worker.password,
        "Worker batch complete",
      );

      // Reload inbox so credentials sidebar and both messages appear.
      await page.reload();
      const sidebar = page.getByRole("complementary", { name: "Project sidebar" });
      const messageList = page.getByRole("listbox");
      await expect(
        sidebar.getByTestId(`sidebar-credential-${staging.credential.id}`),
      ).toBeVisible();
      await expect(sidebar.getByTestId(`sidebar-credential-${worker.credential.id}`)).toBeVisible();
      await expect(messageList.getByText("Staging deploy notice", { exact: true })).toBeVisible();
      await expect(messageList.getByText("Worker batch complete", { exact: true })).toBeVisible();

      // 4. Filter by clicking "staging" credential in sidebar.
      await sidebar.getByTestId(`sidebar-credential-${staging.credential.id}`).click();
      await expect(page.getByTestId("search-chip")).toContainText("credential:staging");
      await expect(messageList.getByText("Staging deploy notice", { exact: true })).toBeVisible();
      await expect(messageList.getByText("Worker batch complete", { exact: true })).toHaveCount(0);

      // Open message and verify credential attribution badge in header and Tech tab.
      await page.getByRole("option").first().click();
      await expect(page.getByText("via staging")).toBeVisible();
      await page.getByRole("tab", { name: "Tech" }).click();
      await expect(page.getByRole("tabpanel").getByText(staging.credential.username)).toBeVisible();

      // 5. Clear filter and search using credential:worker operator.
      await page.getByRole("button", { name: "Clear search" }).click();
      const searchInput = page.getByRole("combobox", { name: "Search messages" });
      await searchInput.fill("credential:worker");
      await searchInput.press("Enter");
      await expect(page.getByTestId("search-chip")).toContainText("credential:worker");
      await expect(messageList.getByText("Worker batch complete", { exact: true })).toBeVisible();
      await expect(messageList.getByText("Staging deploy notice", { exact: true })).toHaveCount(0);

      // 6. Preview as viewer hides the Credentials section in the sidebar.
      await page.getByRole("button", { name: "Preview as viewer" }).click();
      await expect(page.getByTestId("viewer-preview-banner")).toBeVisible();
      await expect(sidebar.getByRole("region", { name: "Credentials" })).toHaveCount(0);
    } finally {
      await context.close();
      await srv.stop();
    }
  });
});
