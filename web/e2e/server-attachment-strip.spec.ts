import { type ChildProcess, spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import { expect, test } from "@playwright/test";
import { getE2EConfig, uniqueSlug } from "./helpers";

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
  const dataDir = mkdtempSync(path.join(tmpdir(), "mailcubby-e2e-14c-"));
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

test.describe("Phase 14c: attachment strip for viewers (FR-072)", () => {
  test("a viewer with viewer_hide_technical sees the attachment strip and the Attachments tab", async ({
    browser,
  }) => {
    const srv = await startBootstrappedServer();
    const adminContext = await browser.newContext();
    const adminPage = await adminContext.newPage();
    const slug = uniqueSlug("strip-viewer");

    try {
      // 1. Admin signs in, creates the project, ingests the built-in sample
      // (HTML with an inline cid: logo + a PDF attachment) and keeps
      // viewer_hide_technical on.
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
        data: { name: "Strip Viewer", slug },
      });
      expect(created.status()).toBe(201);
      const sent = await adminPage.request.post(
        `${srv.httpURL}/api/v1/projects/${slug}/send-test`,
        { headers },
      );
      expect(sent.status()).toBe(201);
      const patched = await adminPage.request.patch(`${srv.httpURL}/api/v1/projects/${slug}`, {
        headers,
        data: { settings: { viewer_hide_technical: true } },
      });
      expect(patched.status()).toBe(200);

      const invited = await adminPage.request.post(
        `${srv.httpURL}/api/v1/projects/${slug}/invites`,
        { headers, data: { email: "viewer@strip.test", role: "viewer" } },
      );
      expect(invited.status()).toBe(201);
      const { url: inviteUrl } = (await invited.json()) as { url: string };

      // 2. A real viewer (not "Preview as viewer") joins through the invite.
      const viewerContext = await browser.newContext();
      const viewerPage = await viewerContext.newPage();
      try {
        await viewerPage.goto(inviteUrl);
        await viewerPage.getByLabel("Full name").fill("Strip Viewer");
        await viewerPage.getByLabel("Password", { exact: true }).fill("viewer-secret-pass-1");
        await viewerPage.getByRole("button", { name: "Create account & join" }).click();
        await expect(viewerPage).toHaveURL(`${srv.httpURL}/p/${slug}`);

        await viewerPage.getByRole("option").first().click();
        await expect(viewerPage).toHaveURL(new RegExp(`/p/${slug}/m/`));

        // Technical tabs are hidden for this viewer.
        await expect(viewerPage.getByRole("tab", { name: "Raw" })).toHaveCount(0);
        await expect(viewerPage.getByRole("tab", { name: "Tech" })).toHaveCount(0);

        // 3. The strip shows the PDF and leaves out the cid: logo shown in the preview.
        const strip = viewerPage.getByRole("region", { name: "Attachments" });
        await expect(strip).toBeVisible();
        await expect(strip.getByRole("link")).toHaveCount(1);
        const pdf = strip.getByRole("link", { name: /^Download sample\.pdf / });
        await expect(pdf).toBeVisible();
        await expect(strip.getByRole("link", { name: /logo\.png/ })).toHaveCount(0);

        const [download] = await Promise.all([viewerPage.waitForEvent("download"), pdf.click()]);
        expect(download.suggestedFilename()).toBe("sample.pdf");

        // 4. The Attachments tab still lists every part, including the inline logo.
        await viewerPage.getByRole("tab", { name: /Attachments/ }).click();
        const panel = viewerPage.getByRole("tabpanel");
        await expect(panel.getByText("logo.png", { exact: true })).toBeVisible();
        await expect(panel.getByText("sample.pdf", { exact: true })).toBeVisible();
      } finally {
        await viewerContext.close();
      }
    } finally {
      await adminContext.close();
      await srv.stop();
    }
  });
});
