import { type ChildProcess, execFile, spawn } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
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
  test,
  uniqueSlug,
} from "./helpers";

const WANT_PDF_CSP =
  "default-src 'none'; object-src 'self'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'";

function sendRawEmail(raw: Buffer | string, projectSlug: string, label: string): Promise<void> {
  const { binaryPath, smtpAddr, repoRoot } = getE2EConfig();
  const args = ["sendmail", "--smtp", smtpAddr, "--user", projectSlug, "--password", "dev", "-t"];

  return new Promise((resolve, reject) => {
    const child = execFile(binaryPath, args, { cwd: repoRoot }, (err, _stdout, stderr) => {
      if (err) {
        reject(new Error(`sendmail failed for ${label}: ${err.message}\n${stderr}`));
        return;
      }
      resolve();
    });
    child.stdin?.end(raw);
  });
}

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
      throw new Error(`mailcubby exited early (${child.exitCode}):\n${logs.join("")}`);
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

  throw new Error(`mailcubby did not become ready (${lastError}):\n${logs.join("")}`);
}

async function startLocalServerWithInlinePdfDisabled(): Promise<{
  httpURL: string;
  stop: () => Promise<void>;
}> {
  const { binaryPath, repoRoot } = getE2EConfig();
  const httpPort = await getFreePort();
  const smtpPort = await getFreePort();
  const dataDir = mkdtempSync(path.join(tmpdir(), "mailcubby-e2e-16a-off-"));
  const httpURL = `http://127.0.0.1:${httpPort}`;
  const logs: string[] = [];

  const child = spawn(binaryPath, ["serve"], {
    cwd: repoRoot,
    env: {
      ...process.env,
      MAILCUBBY_MODE: "local",
      MAILCUBBY_DATA_DIR: dataDir,
      MAILCUBBY_HTTP_ADDR: `127.0.0.1:${httpPort}`,
      MAILCUBBY_SMTP_ADDR: `127.0.0.1:${smtpPort}`,
      MAILCUBBY_RENDER_INLINE_PDF: "false",
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

async function assertDialogAccessible(page: Page, screenName: string): Promise<void> {
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
    // Exclude untrusted email HTML iframe and Chromium's built-in PDF viewer iframe.
    .exclude('iframe[title^="Email preview:"]')
    .exclude('iframe[title^="PDF preview:"]')
    .analyze();
  const blocking = results.violations.filter(
    (v) => v.impact === "serious" || v.impact === "critical",
  );
  expect(
    blocking.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`),
    `WCAG 2.1 AA violations on "${screenName}"`,
  ).toEqual([]);
}

test.describe("Phase 16a: inline PDF preview (FR-074, ADR 0009)", () => {
  test("local operator previews send-test PDF from strip and Attachments tab, passes axe in light and dark", async ({
    page,
  }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    const { httpURL } = getE2EConfig();
    const slug = uniqueSlug("pdf-local");
    await createProjectViaAPI("PDF Local", slug);

    const sendRes = await fetch(`${httpURL}/api/v1/projects/${slug}/send-test`, {
      method: "POST",
    });
    expect(sendRes.status).toBe(201);
    const sent = (await sendRes.json()) as { id: string };

    await page.goto(`/p/${slug}/m/${sent.id}`);
    const strip = page.getByRole("region", { name: "Attachment downloads" });
    await expect(strip).toBeVisible();

    const previewBtn = strip.getByRole("button", { name: /^Preview sample\.pdf / });
    await expect(previewBtn).toBeVisible();

    const [pdfResponse] = await Promise.all([
      page.waitForResponse((res) =>
        new RegExp(`/render/${slug}/${sent.id}/attachments/\\d+$`).test(
          new URL(res.url()).pathname,
        ),
      ),
      previewBtn.click(),
    ]);

    expect(pdfResponse.status()).toBe(200);
    const headers = pdfResponse.headers();
    expect(headers["content-type"]).toBe("application/pdf");
    expect(headers["content-disposition"]).toMatch(/^inline; filename="sample\.pdf"/);
    expect(headers["x-content-type-options"]).toBe("nosniff");
    expect(headers["content-security-policy"]).toBe(WANT_PDF_CSP);
    expect(headers["referrer-policy"]).toBe("no-referrer");
    expect(headers["cache-control"]).toBe("private, no-store");
    expect(headers["cross-origin-resource-policy"]).toBe("same-origin");
    expect(headers["x-dns-prefetch-control"]).toBe("off");
    expect(headers["x-frame-options"]).toBeUndefined();

    const dialog = page.locator("dialog[open]");
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole("heading", { name: "sample.pdf" })).toBeVisible();

    const iframe = dialog.locator('iframe[title="PDF preview: sample.pdf"]');
    await expect(iframe).toBeVisible();
    expect(await iframe.getAttribute("src")).toMatch(
      new RegExp(`^/render/${slug}/${sent.id}/attachments/\\d+$`),
    );
    expect(await iframe.getAttribute("referrerpolicy")).toBe("no-referrer");
    expect(await iframe.getAttribute("sandbox")).toBeNull();

    // Header and footer both provide Open in new tab and Download actions.
    await expect(dialog.getByRole("link", { name: "Open in new tab" })).toHaveCount(2);
    await expect(dialog.getByRole("link", { name: "Download" })).toHaveCount(2);

    const [downloadEvent] = await Promise.all([
      page.waitForEvent("download"),
      dialog.getByRole("link", { name: "Download" }).first().click(),
    ]);
    expect(downloadEvent.suggestedFilename()).toBe("sample.pdf");

    // WCAG 2.1 AA axe check with the PDF preview modal open in both Light and Dark themes.
    await assertDialogAccessible(page, "PDF preview dialog (Light)");
    // Closing via the Close button must restore focus natively to the triggering Preview button.
    await dialog.getByRole("button", { name: "Close dialog" }).click();
    await expect(dialog).toHaveCount(0);
    await expect(previewBtn).toBeFocused();

    await chooseTheme(page, "Dark");
    await previewBtn.click();
    await expect(dialog).toBeVisible();
    await assertDialogAccessible(page, "PDF preview dialog (Dark)");
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await expect(previewBtn).toBeFocused();

    // Restore Light theme for subsequent tests in the same context.
    await chooseTheme(page, "Light");

    // Attachments tab also opens the PDF preview modal.
    await page.getByRole("tab", { name: /Attachments/ }).click();
    const tabPanel = page.getByRole("tabpanel");
    await tabPanel.getByRole("button", { name: "Preview sample.pdf" }).click();
    await expect(dialog).toBeVisible();
    await expect(dialog.locator('iframe[title="PDF preview: sample.pdf"]')).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
  });

  test("hostile HTML and SVG disguised as application/pdf are rejected with 415 and execute zero script", async ({
    page,
  }) => {
    const { repoRoot, httpURL } = getE2EConfig();
    const slug = uniqueSlug("pdf-hostile");
    await createProjectViaAPI("PDF Hostile", slug);

    const fixturePath = path.join(repoRoot, "testdata", "hostile", "13-forged-pdf-html.eml");
    await sendRawEmail(readFileSync(fixturePath), slug, "13-forged-pdf-html.eml");

    const listRes = await fetch(`${httpURL}/api/v1/projects/${slug}/messages`);
    expect(listRes.ok).toBe(true);
    const listData = (await listRes.json()) as { items: Array<{ id: string }> };
    const msgId = listData.items[0]?.id;
    expect(msgId).toBeTruthy();

    const expectedOrigin = new URL(httpURL).origin;
    const outboundRequests: string[] = [];
    await page.route("**/*", async (route) => {
      const reqURL = route.request().url();
      if (
        !reqURL.startsWith("data:") &&
        !reqURL.startsWith("blob:") &&
        !reqURL.startsWith("about:")
      ) {
        try {
          if (new URL(reqURL).origin !== expectedOrigin) {
            outboundRequests.push(reqURL);
            await route.abort("blockedbyclient");
            return;
          }
        } catch {
          outboundRequests.push(reqURL);
          await route.abort("blockedbyclient");
          return;
        }
      }
      await route.continue();
    });

    const targetPath = `/p/${slug}/m/${msgId}`;
    await page.goto(targetPath);
    const strip = page.getByRole("region", { name: "Attachment downloads" });
    await expect(strip).toBeVisible();

    for (const forgedName of ["invoice-evil.pdf", "diagram-evil.pdf"]) {
      const btn = strip.getByRole("button", { name: new RegExp(`^Preview ${forgedName} `) });
      await expect(btn).toBeVisible();

      const [res] = await Promise.all([
        page.waitForResponse((r) =>
          new RegExp(`/render/${slug}/${msgId}/attachments/\\d+$`).test(new URL(r.url()).pathname),
        ),
        btn.click(),
      ]);

      expect(res.status()).toBe(415);
      expect(res.headers()["x-content-type-options"]).toBe("nosniff");
      expect(res.headers()["x-frame-options"]).toBe("DENY");

      // Let the browser settle after the rejected iframe response.
      await page.waitForTimeout(250);

      const topPwned = await page.evaluate(
        () => (window as unknown as { __pwned?: unknown }).__pwned,
      );
      expect(topPwned, `top window.__pwned set by ${forgedName}`).toBeUndefined();

      for (const childFrame of page.frames()) {
        if (childFrame === page.mainFrame()) {
          continue;
        }
        try {
          const framePwned = await childFrame.evaluate(
            () => (window as unknown as { __pwned?: unknown }).__pwned,
          );
          expect(framePwned, `frame window.__pwned set by ${forgedName}`).toBeUndefined();
        } catch {
          // Opaque/error frame confirms script cannot execute.
        }
      }

      const currentURL = new URL(page.url());
      expect(currentURL.origin).toBe(expectedOrigin);
      expect(currentURL.pathname).toBe(targetPath);

      await page.keyboard.press("Escape");
      await expect(page.locator("dialog[open]")).toHaveCount(0);
    }

    expect(outboundRequests).toEqual([]);
  });

  test("MAILCUBBY_RENDER_INLINE_PDF=false hides preview actions and returns 404 from /render/.../attachments/...", async ({
    browser,
  }) => {
    const srv = await startLocalServerWithInlinePdfDisabled();
    const ctx = await browser.newContext();
    const page = await ctx.newPage();

    try {
      const metaRes = await fetch(`${srv.httpURL}/api/v1/meta`);
      expect(metaRes.status).toBe(200);
      const meta = (await metaRes.json()) as { features: { inline_pdf: boolean } };
      expect(meta.features.inline_pdf).toBe(false);

      const slug = uniqueSlug("pdf-off");
      const createRes = await fetch(`${srv.httpURL}/api/v1/projects`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: "PDF Off", slug }),
      });
      expect(createRes.status).toBe(201);

      const sendRes = await fetch(`${srv.httpURL}/api/v1/projects/${slug}/send-test`, {
        method: "POST",
      });
      expect(sendRes.status).toBe(201);
      const sent = (await sendRes.json()) as { id: string };

      const detRes = await fetch(`${srv.httpURL}/api/v1/projects/${slug}/messages/${sent.id}`);
      const det = (await detRes.json()) as {
        attachments: Array<{ id: number; content_type: string }>;
      };
      const pdfAtt = det.attachments.find((a) =>
        a.content_type.toLowerCase().startsWith("application/pdf"),
      );
      expect(pdfAtt).toBeTruthy();

      const renderRes = await fetch(
        `${srv.httpURL}/render/${slug}/${sent.id}/attachments/${pdfAtt?.id}`,
      );
      expect(renderRes.status).toBe(404);

      await page.goto(`${srv.httpURL}/p/${slug}/m/${sent.id}`);
      const strip = page.getByRole("region", { name: "Attachment downloads" });
      await expect(strip).toBeVisible();
      // Falls back to download-only card: no Preview button.
      await expect(strip.getByRole("button", { name: /Preview/ })).toHaveCount(0);
      await expect(strip.getByRole("link", { name: /^Download sample\.pdf / })).toBeVisible();

      await page.getByRole("tab", { name: /Attachments/ }).click();
      const panel = page.getByRole("tabpanel");
      await expect(panel.getByRole("button", { name: /Preview/ })).toHaveCount(0);
      await expect(panel.getByRole("link", { name: "Download sample.pdf" })).toBeVisible();
    } finally {
      await ctx.close();
      await srv.stop();
    }
  });
});
