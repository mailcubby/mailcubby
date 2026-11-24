import { type ChildProcess, spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import { expect, test } from "@playwright/test";
import { getE2EConfig } from "./helpers";

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

async function startServerModeInstance(): Promise<{
  httpURL: string;
  setupToken: string;
  stop: () => Promise<void>;
}> {
  const { binaryPath, repoRoot } = getE2EConfig();
  const httpPort = await getFreePort();
  const smtpPort = await getFreePort();
  const dataDir = mkdtempSync(path.join(tmpdir(), "mailcubby-e2e-server-"));
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
      MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH: "true",
      MAILCUBBY_HTTP_ADDR: `127.0.0.1:${httpPort}`,
      MAILCUBBY_SMTP_ADDR: `127.0.0.1:${smtpPort}`,
      MAILCUBBY_SMTP_PUBLIC_HOST: "127.0.0.1",
      MAILCUBBY_SMTP_PUBLIC_PORT: String(smtpPort),
      MAILCUBBY_LOG_LEVEL: "warn",
    },
    stdio: ["ignore", "pipe", "pipe"],
  });

  child.stdout?.on("data", (chunk: Buffer) => {
    logs.push(chunk.toString());
  });
  child.stderr?.on("data", (chunk: Buffer) => {
    logs.push(chunk.toString());
  });

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

test.describe("Server mode setup wizard, login, account, and logout", () => {
  test("completes setup wizard from token to logged-in home, manages account, logs out, and logs back in", async ({
    page,
  }) => {
    const srv = await startServerModeInstance();

    try {
      // Visiting root while setup is pending redirects to /setup.
      await page.goto(srv.httpURL);
      await expect(page).toHaveURL(`${srv.httpURL}/setup`);
      await expect(page.getByRole("heading", { name: "Step 1: Setup token" })).toBeVisible();

      // Wrong token bounces back to step 1 at submission time (UI 3.2).
      await page.getByLabel("Setup token").fill("definitely-wrong-token");
      await page.getByRole("button", { name: "Continue to admin account" }).click();

      await expect(page.getByRole("heading", { name: "Step 2: Admin account" })).toBeVisible();
      await page.getByLabel("Full name").fill("Agency Admin");
      await page.getByLabel("Email").fill("admin@agency.test");
      await page.getByLabel("Password", { exact: true }).fill("super-secret-pass-1");
      await page.getByRole("button", { name: "Continue to instance" }).click();

      await expect(page.getByRole("heading", { name: "Step 3: Instance" })).toBeVisible();
      await page.getByLabel("Instance name").fill("Acme Staging");
      await page.getByRole("button", { name: "Complete setup" }).click();

      // Bounced back to step 1 with inline error.
      await expect(page.getByRole("heading", { name: "Step 1: Setup token" })).toBeVisible();
      await expect(page.getByRole("alert")).toContainText("Invalid setup token");

      // Paste the real token from server logs; steps 2 and 3 keep entered data.
      await page.getByLabel("Setup token").fill(srv.setupToken);
      await page.getByRole("button", { name: "Continue to admin account" }).click();
      await expect(page.getByLabel("Email")).toHaveValue("admin@agency.test");
      await page.getByRole("button", { name: "Continue to instance" }).click();
      await expect(page.getByLabel("Instance name")).toHaveValue("Acme Staging");
      await page.getByRole("button", { name: "Complete setup" }).click();

      // Advances to Step 4 (First project); skip to reach Projects home directly.
      await expect(page.getByRole("heading", { name: "Step 4: First project" })).toBeVisible();
      await page.getByRole("button", { name: "Skip for now" }).click();

      // Lands on logged-in Projects home with custom instance name.
      await expect(page).toHaveURL(`${srv.httpURL}/`);
      await expect(page.getByRole("link", { name: "Acme Staging" })).toBeVisible();

      // Open account menu -> Profile -> update display name (uses CSRF header).
      await page.getByRole("button", { name: "Account menu" }).click();
      await page.getByRole("menuitem", { name: "Profile" }).click();
      await expect(page).toHaveURL(`${srv.httpURL}/account/profile`);
      await page.getByLabel("Display name").fill("Lead Admin");
      await page.getByRole("button", { name: "Save profile" }).click();
      await expect(page.getByText("Profile updated")).toBeVisible();

      // Open Security & sessions -> shows Current session.
      await page.getByRole("link", { name: "Security" }).click();
      await expect(page).toHaveURL(`${srv.httpURL}/account/security`);
      await expect(page.getByText("Current", { exact: true })).toBeVisible();

      // Log out from account menu -> redirected to /login.
      await page.getByRole("button", { name: "Account menu" }).click();
      await page.getByRole("menuitem", { name: "Sign out" }).click();
      await expect(page).toHaveURL(`${srv.httpURL}/login`);

      // Visiting protected route while logged out redirects to /login with returnTo.
      await page.goto(`${srv.httpURL}/account/profile`);
      await expect(page).toHaveURL(/\/login\?returnTo=%2Faccount%2Fprofile/);

      // Wrong password shows generic inline error.
      await page.getByLabel("Email").fill("admin@agency.test");
      await page.getByLabel("Password", { exact: true }).fill("wrong-password-123");
      await page.getByRole("button", { name: "Sign in" }).click();
      await expect(page.getByRole("alert")).toContainText("Email or password is incorrect");

      // Correct password logs back in and returns to /account/profile.
      await page.getByLabel("Password", { exact: true }).fill("super-secret-pass-1");
      await page.getByRole("button", { name: "Sign in" }).click();
      await expect(page).toHaveURL(`${srv.httpURL}/account/profile`);
      await expect(page.getByLabel("Display name")).toHaveValue("Lead Admin");
    } finally {
      await srv.stop();
    }
  });
});
