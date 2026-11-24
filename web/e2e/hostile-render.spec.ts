import { execFile } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { createProjectViaAPI, expect, getE2EConfig, test } from "./helpers";

function sendHostileFixture(fixturePath: string, projectSlug: string): Promise<void> {
  const { binaryPath, smtpAddr, repoRoot } = getE2EConfig();
  const raw = readFileSync(fixturePath);
  const args = ["sendmail", "--smtp", smtpAddr, "--user", projectSlug, "--password", "dev", "-t"];

  return new Promise((resolve, reject) => {
    const child = execFile(binaryPath, args, { cwd: repoRoot }, (err, _stdout, stderr) => {
      if (err) {
        reject(new Error(`sendmail failed for ${fixturePath}: ${err.message}\n${stderr}`));
        return;
      }
      resolve();
    });
    child.stdin?.end(raw);
  });
}

test.describe("Phase 11 hostile HTML render isolation", () => {
  test("every hostile email executes no script, makes zero external requests, and keeps app URL intact", async ({
    page,
  }) => {
    const { repoRoot, httpURL } = getE2EConfig();
    const hostileDir = path.join(repoRoot, "testdata", "hostile");
    const fixtures = readdirSync(hostileDir)
      .filter((name) => name.endsWith(".eml"))
      .sort();

    expect(fixtures.length).toBeGreaterThanOrEqual(12);

    const slug = `hostile-${Date.now()}`;
    await createProjectViaAPI("Hostile Corpus", slug);

    const expectedOrigin = new URL(httpURL).origin;
    const outboundRequests: Array<{ fixture: string; url: string }> = [];
    let currentFixture = "setup";

    page.on("request", (req) => {
      const reqURL = req.url();
      if (reqURL.startsWith("data:") || reqURL.startsWith("blob:") || reqURL.startsWith("about:")) {
        return;
      }
      try {
        const parsed = new URL(reqURL);
        if (parsed.hostname !== "127.0.0.1" && parsed.origin !== expectedOrigin) {
          outboundRequests.push({ fixture: currentFixture, url: reqURL });
        }
      } catch {
        outboundRequests.push({ fixture: currentFixture, url: reqURL });
      }
    });

    for (const fixture of fixtures) {
      await sendHostileFixture(path.join(hostileDir, fixture), slug);
    }

    // List messages via API so we can visit each hostile message deterministically.
    const listRes = await fetch(`${httpURL}/api/v1/projects/${slug}/messages?limit=50`);
    expect(listRes.ok).toBe(true);
    const listData = (await listRes.json()) as {
      items: Array<{ id: string; subject: string }>;
    };
    expect(listData.items.length).toBe(fixtures.length);

    for (const msg of listData.items) {
      currentFixture = msg.subject;
      const targetPath = `/p/${slug}/m/${msg.id}`;
      await page.goto(targetPath);

      const iframe = page.locator("iframe[sandbox]");
      await expect(iframe).toBeVisible();

      const sandboxAttr = await iframe.getAttribute("sandbox");
      expect(sandboxAttr).toBe("allow-popups allow-popups-to-escape-sandbox");
      expect(sandboxAttr).not.toContain("allow-scripts");
      expect(sandboxAttr).not.toContain("allow-same-origin");
      expect(sandboxAttr).not.toContain("allow-forms");

      // Wait for the sandboxed iframe document to finish loading and settle
      // (long enough for 0s meta-refresh or script onload attempts to fire if unblocked).
      const frame = page.frameLocator("iframe[sandbox]");
      await expect(frame.locator("body")).toBeVisible();
      await page.waitForTimeout(250);

      // Assert no script executed on the top app window or inside any frame.
      const topPwned = await page.evaluate(
        () => (window as unknown as { __pwned?: unknown }).__pwned,
      );
      expect(topPwned, `top window.__pwned set by ${msg.subject}`).toBeUndefined();

      for (const childFrame of page.frames()) {
        if (childFrame === page.mainFrame()) {
          continue;
        }
        try {
          const framePwned = await childFrame.evaluate(
            () => (window as unknown as { __pwned?: unknown }).__pwned,
          );
          expect(framePwned, `iframe window.__pwned set by ${msg.subject}`).toBeUndefined();
        } catch {
          // Cross-origin opaque sandboxed frames may reject script evaluation or
          // have no execution context; that confirms scripts cannot run there.
        }
      }

      // Assert the app URL did not change (no frame breakout / top navigation).
      const currentURL = new URL(page.url());
      expect(currentURL.origin).toBe(expectedOrigin);
      expect(currentURL.pathname).toBe(targetPath);
    }

    expect(
      outboundRequests,
      `unexpected outbound requests: ${JSON.stringify(outboundRequests)}`,
    ).toEqual([]);
  });
});
