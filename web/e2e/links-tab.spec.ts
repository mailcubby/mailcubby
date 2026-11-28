import { type ChildProcess, execFile, spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { createServer as createHttpServer, type Server as HttpServer } from "node:http";
import { createServer as createNetServer } from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import AxeBuilder from "@axe-core/playwright";
import type { Page } from "@playwright/test";
import {
  chooseTheme,
  createProjectViaAPI,
  expect,
  getE2EConfig,
  sendRawMail,
  test,
  uniqueSlug,
} from "./helpers";

const DEV_APP_KEY = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
const ADMIN_EMAIL = "admin@linkcheck.test";
const ADMIN_PASSWORD = "admin-super-secret-123";

function getFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = createNetServer();
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

async function startBootstrappedServer(extraEnv?: Record<string, string>): Promise<{
  httpURL: string;
  smtpAddr: string;
  stop: () => Promise<void>;
}> {
  const { binaryPath, repoRoot } = getE2EConfig();
  const httpPort = await getFreePort();
  const smtpPort = await getFreePort();
  const dataDir = mkdtempSync(path.join(tmpdir(), "mailcubby-e2e-16e-"));
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
      ...extraEnv,
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

function sendRawMailToServer(
  rawEmail: string,
  smtpAddr: string,
  username: string,
  password: string,
): Promise<void> {
  const { binaryPath, repoRoot } = getE2EConfig();
  return new Promise((resolve, reject) => {
    const child = execFile(
      binaryPath,
      ["sendmail", "--smtp", smtpAddr, "--user", username, "--password", password, "-t"],
      { cwd: repoRoot },
      (err, _stdout, stderr) => {
        if (err) {
          reject(new Error(`sendmail failed: ${err.message}\n${stderr}`));
          return;
        }
        resolve();
      },
    );
    child.stdin?.end(rawEmail);
  });
}

async function startLocalLinkTarget(): Promise<{
  port: number;
  baseUrl: string;
  stop: () => Promise<void>;
}> {
  const server: HttpServer = createHttpServer((req, res) => {
    const reqPath = req.url ?? "/";
    if (reqPath === "/ok") {
      res.writeHead(200, { "Content-Type": "text/plain" });
      res.end("ok");
      return;
    }
    if (reqPath === "/redirect") {
      res.writeHead(302, { Location: "/final" });
      res.end();
      return;
    }
    if (reqPath === "/final") {
      res.writeHead(200, { "Content-Type": "text/plain" });
      res.end("final destination");
      return;
    }
    res.writeHead(404, { "Content-Type": "text/plain" });
    res.end("not found");
  });

  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve());
  });

  const addr = server.address();
  if (!addr || typeof addr === "string") {
    server.close();
    throw new Error("failed to bind local link target server");
  }

  return {
    port: addr.port,
    baseUrl: `http://127.0.0.1:${addr.port}`,
    stop: async () => {
      await new Promise<void>((resolve) => {
        server.close(() => resolve());
      });
    },
  };
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

const LOCAL_LINKS_EMAIL = [
  "From: Alerts <alerts@example.test>",
  "To: dev@example.test",
  "Subject: Links inspection showcase",
  "List-Unsubscribe: <https://unsubscribe.example.test/optout>, <mailto:unsub@example.test>",
  "MIME-Version: 1.0",
  'Content-Type: multipart/alternative; boundary="links-boundary"',
  "",
  "--links-boundary",
  'Content-Type: text/plain; charset="utf-8"',
  "",
  "Read the docs: https://docs.example.test/guide",
  "",
  "--links-boundary",
  'Content-Type: text/html; charset="utf-8"',
  "",
  "<!doctype html><html><body>",
  '<p><a href="https://docs.example.test/guide">Read documentation</a></p>',
  '<p><a href="http://127.0.0.1/private-admin">Local admin endpoint</a></p>',
  '<p><a href="javascript:alert(1)">Unsafe JS link</a></p>',
  "<p><a>Missing href link</a></p>",
  "</body></html>",
  "--links-boundary--",
].join("\r\n");

test.describe("Phase 16e: Links tab and active link checking (FR-120)", () => {
  test("local mode: opens Links via shortcut 8, inspects offline links, blocks private IP check, and passes axe in light and dark", async ({
    page,
  }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    const slug = uniqueSlug("links-local");
    await createProjectViaAPI("Links Local", slug);

    await sendRawMail(LOCAL_LINKS_EMAIL, { user: slug });
    await page.goto(`/p/${slug}`);

    await page.getByRole("option", { name: /Links inspection showcase/ }).click();
    await expect(page.getByRole("heading", { name: "Links inspection showcase" })).toBeVisible();

    const linksTab = page.getByRole("tab", { name: "Links" });
    await expect(linksTab).toBeVisible();

    // Shortcut `8` opens Links tab and sets ?tab=links.
    await page.keyboard.press("8");
    await expect(linksTab).toHaveAttribute("aria-selected", "true");
    await expect(page).toHaveURL(/tab=links/);

    const panel = page.getByRole("tabpanel");
    await expect(panel.getByRole("heading", { name: "Extracted Links" })).toBeVisible();
    await expect(panel.getByText(/single-use links/i)).toBeVisible();
    await expect(panel.getByRole("button", { name: "Check all links" })).toBeVisible();

    // Offline extracted rows, sources, anchor texts, and static warnings.
    await expect(panel.getByText("https://docs.example.test/guide")).toBeVisible();
    await expect(panel.getByText("Read documentation")).toBeVisible();
    await expect(panel.getByText("https://unsubscribe.example.test/optout")).toBeVisible();
    await expect(panel.getByText("http://127.0.0.1/private-admin")).toBeVisible();
    await expect(panel.getByText("javascript:alert(1)")).toBeVisible();
    await expect(panel.getByText("(missing href)")).toBeVisible();

    await expect(panel.getByText("Insecure HTTP", { exact: true })).toBeVisible();
    await expect(panel.getByText("javascript: URL", { exact: true })).toBeVisible();
    await expect(panel.getByText("Missing href", { exact: true })).toBeVisible();

    // Safe http(s) link uses rel="noopener noreferrer"; non-http(s) never renders <a href>.
    const safeOpen = panel.getByRole("link", {
      name: "Open https://docs.example.test/guide in new tab",
    });
    await expect(safeOpen).toHaveAttribute("rel", "noopener noreferrer");
    await expect(safeOpen).toHaveAttribute("target", "_blank");
    await expect(panel.locator('a[href^="javascript:"], a[href^="mailto:"]')).toHaveCount(0);
    await expect(
      panel.getByRole("button", { name: "Open in new tab unavailable for javascript link" }),
    ).toBeDisabled();
    await expect(
      panel.getByRole("button", { name: "Open in new tab unavailable for mailto link" }),
    ).toBeDisabled();

    // Per-row check on private loopback IP returns SSRF Blocked (private IP) badge.
    await panel.getByRole("button", { name: "Check http://127.0.0.1/private-admin" }).click();
    await expect(panel.getByText("Blocked (private IP)", { exact: true })).toBeVisible();
    await expect(panel.locator('[aria-live="polite"]')).toContainText(
      "Link check complete: Blocked (private IP).",
    );

    // WCAG 2.1 AA in both Light and Dark themes on Links tab with checked status.
    await assertAccessible(page, "Links tab in local mode with checked SSRF block (Light)");
    await chooseTheme(page, "Dark");
    await assertAccessible(page, "Links tab in local mode with checked SSRF block (Dark)");
    await chooseTheme(page, "Light");

    // Keyboard shortcuts help dialog lists `8` for Links tab.
    await linksTab.focus();
    await page.keyboard.press("?");
    const helpDialog = page.locator("dialog[open]");
    await expect(helpDialog).toBeVisible();
    await expect(helpDialog.getByText("Switch to Links tab")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(helpDialog).toHaveCount(0);
  });

  test("server mode default: active link checking is disabled by default with quiet info banner", async ({
    browser,
  }) => {
    const srv = await startBootstrappedServer();
    const context = await browser.newContext();
    const page = await context.newPage();
    const slug = uniqueSlug("links-srv-disabled");

    try {
      await page.goto(`${srv.httpURL}/login`);
      await page.getByLabel("Email").fill(ADMIN_EMAIL);
      await page.getByLabel("Password", { exact: true }).fill(ADMIN_PASSWORD);
      await page.getByRole("button", { name: "Sign in" }).click();
      await expect(page).toHaveURL(`${srv.httpURL}/`);

      const meRes = await page.request.get(`${srv.httpURL}/api/v1/auth/me`);
      const me = (await meRes.json()) as { csrf_token: string };
      const headers = { "X-CSRF-Token": me.csrf_token, Origin: srv.httpURL };

      const created = await page.request.post(`${srv.httpURL}/api/v1/projects`, {
        headers,
        data: { name: "Links Server Disabled", slug },
      });
      expect(created.status()).toBe(201);

      const sent = await page.request.post(`${srv.httpURL}/api/v1/projects/${slug}/send-test`, {
        headers,
      });
      expect(sent.status()).toBe(201);
      const sentMsg = (await sent.json()) as { id: string };

      await page.goto(`${srv.httpURL}/p/${slug}/m/${sentMsg.id}?tab=links`);
      const panel = page.getByRole("tabpanel");

      await expect(panel.getByRole("heading", { name: "Extracted Links" })).toBeVisible();
      await expect(panel.getByRole("status")).toContainText(
        "Active link checking is disabled on this server",
      );
      await expect(panel.getByRole("status")).toContainText("MAILCUBBY_LINK_CHECK_ENABLED=true");
      await expect(panel.getByRole("button", { name: "Check all links" })).toHaveCount(0);
      await expect(panel.getByRole("button", { name: /^Check https?:/ })).toHaveCount(0);

      // Preview as viewer hides the Links tab when viewer_hide_technical=true (project default).
      await page.getByRole("button", { name: "Preview as viewer" }).click();
      await expect(page.getByRole("tab", { name: "Links" })).toHaveCount(0);
    } finally {
      await context.close();
      await srv.stop();
    }
  });

  test("server mode enabled: checks local allowlisted targets, blocks private IP, enforces viewer restrictions, and passes axe in light and dark", async ({
    browser,
  }) => {
    const target = await startLocalLinkTarget();
    const srv = await startBootstrappedServer({
      MAILCUBBY_LINK_CHECK_ENABLED: "true",
      MAILCUBBY_LINK_CHECK_ALLOW_CIDRS: "127.0.0.1/32",
      MAILCUBBY_LINK_CHECK_ALLOWED_PORTS: `80,443,${target.port}`,
    });
    const adminContext = await browser.newContext();
    const adminPage = await adminContext.newPage();
    const slug = uniqueSlug("links-srv-enabled");

    try {
      await adminPage.emulateMedia({ reducedMotion: "reduce" });
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
        data: { name: "Links Server Enabled", slug },
      });
      expect(created.status()).toBe(201);

      const credRes = await adminPage.request.post(
        `${srv.httpURL}/api/v1/projects/${slug}/smtp-credentials`,
        { headers, data: { label: "e2e" } },
      );
      expect(credRes.status()).toBe(201);
      const cred = (await credRes.json()) as {
        credential: { username: string };
        password: string;
      };

      const rawEmail = [
        "From: QA <qa@example.test>",
        "To: team@example.test",
        "Subject: Active link check targets",
        "MIME-Version: 1.0",
        'Content-Type: text/html; charset="utf-8"',
        "",
        "<!doctype html><html><body>",
        `<p><a href="${target.baseUrl}/ok">Healthy page</a></p>`,
        `<p><a href="${target.baseUrl}/redirect">Redirected page</a></p>`,
        `<p><a href="${target.baseUrl}/missing">Broken page</a></p>`,
        '<p><a href="http://10.0.0.1/secret">Private RFC1918 host</a></p>',
        "</body></html>",
      ].join("\r\n");

      await sendRawMailToServer(rawEmail, srv.smtpAddr, cred.credential.username, cred.password);

      const listRes = await adminPage.request.get(
        `${srv.httpURL}/api/v1/projects/${slug}/messages`,
      );
      expect(listRes.status()).toBe(200);
      const list = (await listRes.json()) as { items: Array<{ id: string }> };
      const messageId = list.items[0]?.id;
      expect(messageId).toBeTruthy();

      await adminPage.goto(`${srv.httpURL}/p/${slug}/m/${messageId}?tab=links`);
      const panel = adminPage.getByRole("tabpanel");
      await expect(panel.getByRole("heading", { name: "Extracted Links" })).toBeVisible();

      // Trigger explicit "Check all links" as owner.
      await panel.getByRole("button", { name: "Check all links" }).click();

      await expect(panel.getByText("200 OK", { exact: true })).toBeVisible();
      await expect(panel.getByText("200 (1 redirect)", { exact: true })).toBeVisible();
      await expect(panel.getByText(`→ ${target.baseUrl}/final`)).toBeVisible();
      await expect(panel.getByText("404 Not Found", { exact: true })).toBeVisible();
      await expect(panel.getByText("Blocked (private IP)", { exact: true })).toBeVisible();
      await expect(panel.locator('[aria-live="polite"]')).toContainText(
        "Checked 4 links: 1 OK, 1 redirect, 1 broken, 1 blocked.",
      );

      // Axe WCAG 2.1 AA in Light and Dark themes on server-mode checked state.
      await assertAccessible(adminPage, "Server mode Links tab checked results (Light)");
      await chooseTheme(adminPage, "Dark");
      await assertAccessible(adminPage, "Server mode Links tab checked results (Dark)");
      await chooseTheme(adminPage, "Light");

      // Invite a viewer and verify both viewer_hide_technical=true and false.
      const invited = await adminPage.request.post(
        `${srv.httpURL}/api/v1/projects/${slug}/invites`,
        { headers, data: { email: "viewer@linkcheck.test", role: "viewer" } },
      );
      expect(invited.status()).toBe(201);
      const { url: inviteUrl } = (await invited.json()) as { url: string };

      const viewerContext = await browser.newContext();
      const viewerPage = await viewerContext.newPage();
      try {
        await viewerPage.goto(inviteUrl);
        await viewerPage.getByLabel("Full name").fill("Links Viewer");
        await viewerPage.getByLabel("Password", { exact: true }).fill("viewer-secret-pass-1");
        await viewerPage.getByRole("button", { name: "Create account & join" }).click();
        await expect(viewerPage).toHaveURL(`${srv.httpURL}/p/${slug}`);

        const viewerLinksRequests: string[] = [];
        const trackViewer = (req: { url: () => string }) => {
          const p = new URL(req.url()).pathname;
          if (p.endsWith("/links") || p.endsWith("/links/check")) {
            viewerLinksRequests.push(req.url());
          }
        };
        viewerPage.on("request", trackViewer);

        // With viewer_hide_technical=true (default), ?tab=links falls back and shortcut 8 is inactive.
        await viewerPage.goto(`${srv.httpURL}/p/${slug}/m/${messageId}?tab=links`);
        await expect(viewerPage.getByRole("tab", { name: "Preview" })).toHaveAttribute(
          "aria-selected",
          "true",
        );
        await expect(viewerPage.getByRole("tab", { name: "Links" })).toHaveCount(0);
        await viewerPage.keyboard.press("8");
        await expect(viewerPage.getByRole("tab", { name: "Preview" })).toHaveAttribute(
          "aria-selected",
          "true",
        );
        expect(viewerLinksRequests).toEqual([]);
        viewerPage.off("request", trackViewer);

        // With viewer_hide_technical=false, viewer can view offline links but NEVER trigger active checks.
        const unhide = await adminPage.request.patch(`${srv.httpURL}/api/v1/projects/${slug}`, {
          headers,
          data: { settings: { viewer_hide_technical: false } },
        });
        expect(unhide.status()).toBe(200);

        await viewerPage.reload();
        const viewerLinksTab = viewerPage.getByRole("tab", { name: "Links" });
        await expect(viewerLinksTab).toBeVisible();
        await viewerLinksTab.click();

        const viewerPanel = viewerPage.getByRole("tabpanel");
        await expect(viewerPanel.getByRole("heading", { name: "Extracted Links" })).toBeVisible();
        await expect(viewerPanel.getByText(`${target.baseUrl}/ok`)).toBeVisible();
        await expect(viewerPanel.getByRole("button", { name: "Check all links" })).toHaveCount(0);
        await expect(viewerPanel.getByRole("button", { name: /^Check https?:/ })).toHaveCount(0);
        await expect(viewerPanel.getByText(/single-use links/i)).toHaveCount(0);
      } finally {
        await viewerContext.close();
      }
    } finally {
      await adminContext.close();
      await srv.stop();
      await target.stop();
    }
  });
});
