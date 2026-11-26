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

async function startFreshServerInstance(): Promise<{
  httpURL: string;
  setupToken: string;
  stop: () => Promise<void>;
}> {
  const { binaryPath, repoRoot } = getE2EConfig();
  const httpPort = await getFreePort();
  const smtpPort = await getFreePort();
  const dataDir = mkdtempSync(path.join(tmpdir(), "mailcubby-e2e-p9-"));
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
      MAILCUBBY_INBOX_DOMAIN: "inbox.agency.test",
      MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH: "true",
      MAILCUBBY_HTTP_ADDR: `127.0.0.1:${httpPort}`,
      MAILCUBBY_SMTP_ADDR: `127.0.0.1:${smtpPort}`,
      MAILCUBBY_SMTP_PUBLIC_HOST: "mail.agency.test",
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

test.describe("Phase 9: Setup wizard steps 1-5, project settings UI, and API tokens", () => {
  test("completes 5-step wizard with live SSE first-message wait, manages project settings, and verifies API tokens", async ({
    page,
  }) => {
    const srv = await startFreshServerInstance();

    try {
      // 1. Setup wizard steps 1 to 5 on a fresh server instance.
      await page.goto(srv.httpURL);
      await expect(page).toHaveURL(`${srv.httpURL}/setup`);

      // Step 1: Setup token
      await expect(page.getByRole("heading", { name: "Step 1: Setup token" })).toBeVisible();
      // The wizard card keeps one width on every step (Phase 15 follow-up).
      const setupCard = page.getByRole("main").locator(":scope > div");
      const stepOneWidth = (await setupCard.boundingBox())?.width;
      expect(stepOneWidth).toBeGreaterThan(0);
      await page.getByLabel("Setup token").fill(srv.setupToken);
      await page.getByRole("button", { name: "Continue to admin account" }).click();

      // Step 2: Admin account
      await expect(page.getByRole("heading", { name: "Step 2: Admin account" })).toBeVisible();
      await page.getByLabel("Full name").fill("Agency Owner");
      await page.getByLabel("Email").fill("owner@agency.test");
      await page.getByLabel("Password", { exact: true }).fill("super-secret-pass-9");
      await page.getByRole("button", { name: "Continue to instance" }).click();

      // Step 3: Instance
      await expect(page.getByRole("heading", { name: "Step 3: Instance" })).toBeVisible();
      await page.getByLabel("Instance name").fill("Agency Mail");
      await page.getByRole("button", { name: "Complete setup" }).click();

      // Step 4: First project
      await expect(page.getByRole("heading", { name: "Step 4: First project" })).toBeVisible();
      await page.getByLabel("Project name").fill("Shop Staging");
      await expect(page.getByLabel("Project slug")).toHaveValue("shop-staging");
      await page.getByRole("button", { name: "Create project & continue" }).click();

      // Step 5: Connect your app -> live waiting indicator -> Send test email -> success -> Go to inbox
      await expect(page.getByRole("heading", { name: "Step 5: Connect your app" })).toBeVisible();
      await expect(page.getByText("Waiting for first message...")).toBeVisible();
      await expect(page.getByText("MAIL_HOST=mail.agency.test")).toBeVisible();
      expect((await setupCard.boundingBox())?.width).toBe(stepOneWidth);
      // No connection value is cut off at that width (default and WordPress tabs).
      const truncatedValues = () =>
        page
          .getByRole("main")
          .locator("dd span.truncate")
          .evaluateAll((spans) =>
            spans.filter((s) => s.scrollWidth > s.clientWidth).map((s) => s.textContent),
          );
      expect(await truncatedValues()).toEqual([]);
      await page.getByRole("tab", { name: "WordPress" }).click();
      await expect(page.getByText("WP Mail SMTP plugin settings")).toBeVisible();
      expect(await truncatedValues()).toEqual([]);

      await page.getByRole("button", { name: "Send test email" }).click();
      await expect(page.getByText("First message received!")).toBeVisible({ timeout: 5_000 });

      await page.getByRole("button", { name: "Go to inbox" }).click();
      await expect(page).toHaveURL(`${srv.httpURL}/p/shop-staging`);
      await expect(page.getByText("Welcome to Mailcubby")).toBeVisible();

      // 2. Project settings: SMTP section (credentials table + integration snippets)
      await page.getByRole("link", { name: "Settings" }).click();
      await expect(page).toHaveURL(`${srv.httpURL}/p/shop-staging/settings/general`);

      await page.getByRole("link", { name: "SMTP", exact: true }).click();
      await expect(page).toHaveURL(`${srv.httpURL}/p/shop-staging/settings/smtp`);
      await expect(page.getByText("mail.agency.test").first()).toBeVisible();
      await expect(page.getByText("default", { exact: true }).first()).toBeVisible();

      // Reveal default credential password and verify WordPress settings table tab.
      await page.getByRole("button", { name: "Reveal password for default" }).click();
      await expect(page.getByRole("button", { name: "Hide password for default" })).toBeVisible();
      await page.getByRole("tab", { name: "WordPress" }).click();
      await expect(page.getByText("WP Mail SMTP plugin settings")).toBeVisible();
      await expect(page.getByText("Other SMTP")).toBeVisible();

      // Create a second SMTP credential, rotate it, disable it, and delete it.
      await page.getByRole("button", { name: "New credential" }).click();
      await page.getByLabel("Label").fill("ci-runner");
      await page.getByRole("button", { name: "Create credential" }).click();
      await expect(page.getByRole("heading", { name: "SMTP credential created" })).toBeVisible();
      await page.getByRole("button", { name: "Done" }).click();

      await expect(page.getByText("ci-runner", { exact: true })).toBeVisible();
      await page.getByRole("button", { name: "Disable ci-runner" }).click();
      await expect(page.getByText("Disabled", { exact: true })).toBeVisible();
      await page.getByRole("button", { name: "Delete ci-runner" }).click();
      await page.getByRole("button", { name: "Delete credential" }).click();
      await expect(page.getByText("ci-runner", { exact: true })).toHaveCount(0);

      // 3. Project settings: Routing & client view toggles
      await page.getByRole("link", { name: "Routing & client view" }).click();
      await expect(page).toHaveURL(`${srv.httpURL}/p/shop-staging/settings/routing`);
      await expect(page.getByText("*@shop-staging.inbox.agency.test")).toBeVisible();

      const recipientSwitch = page.getByRole("switch", {
        name: "Recipient routing (unauthenticated SMTP)",
      });
      await expect(recipientSwitch).not.toBeChecked();
      await recipientSwitch.click();
      await expect(recipientSwitch).toBeChecked();

      const membersRevealSwitch = page.getByRole("switch", {
        name: "Members can reveal SMTP passwords",
      });
      await expect(membersRevealSwitch).not.toBeChecked();
      await membersRevealSwitch.click();
      await expect(membersRevealSwitch).toBeChecked();

      await page.reload();
      await expect(
        page.getByRole("switch", { name: "Recipient routing (unauthenticated SMTP)" }),
      ).toBeChecked();
      await expect(
        page.getByRole("switch", { name: "Members can reveal SMTP passwords" }),
      ).toBeChecked();

      // 4. Project settings: API section -> shortcut to Account > API tokens
      await page.getByRole("link", { name: "API", exact: true }).click();
      await expect(page).toHaveURL(`${srv.httpURL}/p/shop-staging/settings/api`);
      await expect(page.getByText("/api/v1/projects/shop-staging/messages/wait")).toBeVisible();

      await page.getByRole("link", { name: "Create token for this project" }).click();
      await expect(page).toHaveURL(`${srv.httpURL}/account/tokens?project=shop-staging`);

      // Create read token restricted to shop-staging (dialog opened via ?project= shortcut).
      await page.getByLabel("Name").fill("E2E Read Token");
      await page.getByRole("button", { name: "Create token" }).click();
      const readToken = (await page.getByTestId("created-api-token").textContent())?.trim() ?? "";
      expect(readToken).toMatch(/^mailcubby_[0-9A-Za-z]{40}$/);
      await page.getByRole("button", { name: "Done" }).click();

      // Create write token.
      await page.getByRole("button", { name: "New API token" }).click();
      await page.getByLabel("Name").fill("E2E Write Token");
      await page.getByLabel("Scope").selectOption("write");
      await page.getByRole("button", { name: "Create token" }).click();
      const writeToken = (await page.getByTestId("created-api-token").textContent())?.trim() ?? "";
      expect(writeToken).toMatch(/^mailcubby_[0-9A-Za-z]{40}$/);
      await page.getByRole("button", { name: "Done" }).click();

      // Verify bearer token enforcement against live server (FR-065, FR-066).
      const listRes = await fetch(`${srv.httpURL}/api/v1/projects/shop-staging/messages`, {
        headers: { Authorization: `Bearer ${readToken}` },
      });
      expect(listRes.status).toBe(200);
      const listBody = (await listRes.json()) as { items: Array<{ id: string }> };
      expect(listBody.items.length).toBeGreaterThanOrEqual(1);
      const firstItem = listBody.items[0];
      if (!firstItem) {
        throw new Error("expected at least one message in shop-staging");
      }
      const msgId = firstItem.id;

      const waitRes = await fetch(
        `${srv.httpURL}/api/v1/projects/shop-staging/messages/wait?since=00000000000000000000000000&timeout=1s`,
        { headers: { Authorization: `Bearer ${readToken}` } },
      );
      expect(waitRes.status).toBe(200);

      const readDeleteRes = await fetch(
        `${srv.httpURL}/api/v1/projects/shop-staging/messages/${msgId}`,
        {
          method: "DELETE",
          headers: { Authorization: `Bearer ${readToken}` },
        },
      );
      expect(readDeleteRes.status).toBe(403);

      const readMembersRes = await fetch(`${srv.httpURL}/api/v1/projects/shop-staging/members`, {
        headers: { Authorization: `Bearer ${readToken}` },
      });
      expect(readMembersRes.status).toBe(403);

      const writeMembersRes = await fetch(`${srv.httpURL}/api/v1/projects/shop-staging/members`, {
        headers: { Authorization: `Bearer ${writeToken}` },
      });
      expect(writeMembersRes.status).toBe(403);

      const writeDeleteRes = await fetch(
        `${srv.httpURL}/api/v1/projects/shop-staging/messages/${msgId}`,
        {
          method: "DELETE",
          headers: { Authorization: `Bearer ${writeToken}` },
        },
      );
      expect(writeDeleteRes.status).toBe(204);

      // Revoke read token from Account > API tokens UI -> immediately returns 401.
      await page.getByRole("button", { name: "Revoke E2E Read Token" }).click();
      await page.getByRole("button", { name: "Revoke token" }).click();
      await expect(page.getByText("E2E Read Token")).toHaveCount(0);

      const revokedRes = await fetch(`${srv.httpURL}/api/v1/projects/shop-staging/messages`, {
        headers: { Authorization: `Bearer ${readToken}` },
      });
      expect(revokedRes.status).toBe(401);
    } finally {
      await srv.stop();
    }
  });
});
