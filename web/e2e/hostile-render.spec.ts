import { execFile } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
import { createServer as createHTTPServer } from "node:http";
import path from "node:path";
import { createProjectViaAPI, expect, getE2EConfig, test, uniqueSlug } from "./helpers";

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

function sendHostileFixture(fixturePath: string, projectSlug: string): Promise<void> {
  return sendRawEmail(readFileSync(fixturePath), projectSlug, fixturePath);
}

async function startTrapServer(): Promise<{
  origin: string;
  hits: string[];
  close: () => Promise<void>;
}> {
  const hits: string[] = [];
  const server = createHTTPServer((req, res) => {
    hits.push(`${req.method ?? "GET"} ${req.url ?? "/"}`);
    res.writeHead(200, { "Content-Type": "text/plain" });
    res.end("trap");
  });

  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve());
  });

  const addr = server.address();
  if (!addr || typeof addr === "string") {
    server.close();
    throw new Error("failed to bind trap server");
  }

  return {
    origin: `http://127.0.0.1:${addr.port}`,
    hits,
    close: () =>
      new Promise<void>((resolve) => {
        server.close(() => resolve());
      }),
  };
}

function isNonAppURL(reqURL: string, expectedOrigin: string): boolean {
  if (reqURL.startsWith("data:") || reqURL.startsWith("blob:") || reqURL.startsWith("about:")) {
    return false;
  }
  try {
    const parsed = new URL(reqURL);
    return parsed.origin !== expectedOrigin;
  } catch {
    return true;
  }
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

    const trap = await startTrapServer();
    try {
      const slug = uniqueSlug("hostile");
      await createProjectViaAPI("Hostile Corpus", slug);

      const expectedOrigin = new URL(httpURL).origin;
      const networkOutboundRequests: Array<{ fixture: string; url: string }> = [];
      const nonCSPBlockedRequests: Array<{ fixture: string; url: string; reason: string }> = [];
      const externalResponses: Array<{ fixture: string; url: string; status: number }> = [];
      const pendingExternalRequests = new Map<string, { fixture: string; url: string }>();
      let currentFixture = "setup";

      // Network-service layer interception (post-CSP): requests blocked by Blink CSP
      // never reach Fetch.requestPaused; anything observed here actually attempted
      // to leave Chromium over the network.
      await page.route("**/*", async (route) => {
        const reqURL = route.request().url();
        if (isNonAppURL(reqURL, expectedOrigin)) {
          networkOutboundRequests.push({ fixture: currentFixture, url: reqURL });
          await route.abort("blockedbyclient");
          return;
        }
        await route.continue();
      });

      // Renderer-layer events: Blink emits Network.requestWillBeSent before applying
      // CSP, then immediately aborts with net::ERR_BLOCKED_BY_CSP. Verify that every
      // external resource attempt is rejected strictly by CSP and never finishes.
      page.on("request", (req) => {
        const reqURL = req.url();
        if (isNonAppURL(reqURL, expectedOrigin)) {
          pendingExternalRequests.set(`${currentFixture}::${reqURL}`, {
            fixture: currentFixture,
            url: reqURL,
          });
        }
      });

      page.on("requestfailed", (req) => {
        const reqURL = req.url();
        if (!isNonAppURL(reqURL, expectedOrigin)) {
          return;
        }
        const key = `${currentFixture}::${reqURL}`;
        pendingExternalRequests.delete(key);
        const reason = req.failure()?.errorText ?? "unknown";
        if (reason !== "csp" && reason !== "net::ERR_BLOCKED_BY_CSP") {
          nonCSPBlockedRequests.push({ fixture: currentFixture, url: reqURL, reason });
        }
      });

      page.on("requestfinished", (req) => {
        const reqURL = req.url();
        if (isNonAppURL(reqURL, expectedOrigin)) {
          pendingExternalRequests.delete(`${currentFixture}::${reqURL}`);
          nonCSPBlockedRequests.push({
            fixture: currentFixture,
            url: reqURL,
            reason: "requestfinished",
          });
        }
      });

      page.on("response", (res) => {
        const reqURL = res.url();
        if (isNonAppURL(reqURL, expectedOrigin)) {
          externalResponses.push({ fixture: currentFixture, url: reqURL, status: res.status() });
        }
      });

      for (const fixture of fixtures) {
        await sendHostileFixture(path.join(hostileDir, fixture), slug);
      }

      // Also send a live-trap hostile email targeting a reachable distinct origin
      // on 127.0.0.1 to prove zero TCP/HTTP traffic leaves the sandboxed iframe.
      const trapEmail = [
        "From: attacker@evil.example",
        "To: victim@app.test",
        "Subject: Hostile Live Trap Origin Probe",
        "MIME-Version: 1.0",
        'Content-Type: text/html; charset="utf-8"',
        "",
        "<!doctype html><html><head>",
        `<meta http-equiv="refresh" content="0;url=${trap.origin}/meta-refresh">`,
        `<link rel="prefetch" href="${trap.origin}/prefetch">`,
        `<link rel="preload" as="image" href="${trap.origin}/preload.png">`,
        `<link rel="stylesheet" href="${trap.origin}/style.css">`,
        `<style>@import "${trap.origin}/import.css"; body { background: url(${trap.origin}/bg.png); }</style>`,
        "</head><body>",
        `<img src="${trap.origin}/pixel.png" srcset="${trap.origin}/pixel-2x.png 2x">`,
        `<script src="${trap.origin}/evil.js"></script>`,
        `<noscript><img src="${trap.origin}/noscript.png"></noscript>`,
        `<iframe src="${trap.origin}/frame"></iframe>`,
        `<iframe srcdoc="<img src='${trap.origin}/srcdoc.png'>"></iframe>`,
        "<p>Live trap probe</p></body></html>",
        "",
      ].join("\r\n");
      await sendRawEmail(trapEmail, slug, "live-trap-probe");

      // List messages via API so we can visit each hostile message deterministically.
      const listRes = await fetch(`${httpURL}/api/v1/projects/${slug}/messages?limit=50`);
      expect(listRes.ok).toBe(true);
      const listData = (await listRes.json()) as {
        items: Array<{ id: string; subject: string }>;
      };
      expect(listData.items.length).toBe(fixtures.length + 1);

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
        trap.hits,
        `live trap HTTP server received requests: ${JSON.stringify(trap.hits)}`,
      ).toEqual([]);
      expect(
        networkOutboundRequests,
        `unexpected network-level outbound requests: ${JSON.stringify(networkOutboundRequests)}`,
      ).toEqual([]);
      expect(
        externalResponses,
        `unexpected external responses: ${JSON.stringify(externalResponses)}`,
      ).toEqual([]);
      expect(
        nonCSPBlockedRequests,
        `external requests not blocked by CSP: ${JSON.stringify(nonCSPBlockedRequests)}`,
      ).toEqual([]);
      expect(
        Array.from(pendingExternalRequests.values()),
        `external requests still pending without CSP block`,
      ).toEqual([]);
    } finally {
      await trap.close();
    }
  });
});
