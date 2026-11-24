import { execFile } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";
import { test as base, expect } from "@playwright/test";

export interface E2EConfig {
  httpURL: string;
  smtpAddr: string;
  binaryPath: string;
  repoRoot: string;
}

export function getE2EConfig(): E2EConfig {
  const httpURL = process.env.MAILCUBBY_E2E_HTTP_URL;
  const smtpAddr = process.env.MAILCUBBY_E2E_SMTP_ADDR;
  const binaryPath = process.env.MAILCUBBY_E2E_BINARY;
  const repoRoot = process.env.MAILCUBBY_E2E_REPO_ROOT;

  if (!httpURL || !smtpAddr || !binaryPath || !repoRoot) {
    throw new Error("E2E environment variables are not initialized; check globalSetup");
  }

  return { httpURL, smtpAddr, binaryPath, repoRoot };
}

export const test = base.extend({
  // biome-ignore lint/correctness/noEmptyPattern: Playwright requires object destructuring in fixture functions.
  baseURL: async ({}, use) => {
    await use(getE2EConfig().httpURL);
  },
});

export { expect };

export function sendFixture(
  fixtureName: string,
  options?: { user?: string; recipients?: string[] },
): Promise<void> {
  const { binaryPath, smtpAddr, repoRoot } = getE2EConfig();
  const fixturePath = path.join(repoRoot, "testdata", "mime", fixtureName);
  const raw = readFileSync(fixturePath);

  const args = ["sendmail", "--smtp", smtpAddr];
  if (options?.user) {
    args.push("--user", options.user, "--password", "dev");
  }
  if (options?.recipients && options.recipients.length > 0) {
    args.push(...options.recipients);
  } else {
    args.push("-t");
  }

  return new Promise((resolve, reject) => {
    const child = execFile(binaryPath, args, { cwd: repoRoot }, (err, _stdout, stderr) => {
      if (err) {
        reject(new Error(`sendmail failed for ${fixtureName}: ${err.message}\n${stderr}`));
        return;
      }
      resolve();
    });
    child.stdin?.end(raw);
  });
}

export function sendRawMail(
  rawEmail: string,
  options?: { user?: string; recipients?: string[] },
): Promise<void> {
  const { binaryPath, smtpAddr, repoRoot } = getE2EConfig();

  const args = ["sendmail", "--smtp", smtpAddr];
  if (options?.user) {
    args.push("--user", options.user, "--password", "dev");
  }
  if (options?.recipients && options.recipients.length > 0) {
    args.push(...options.recipients);
  } else {
    args.push("-t");
  }

  return new Promise((resolve, reject) => {
    const child = execFile(binaryPath, args, { cwd: repoRoot }, (err, _stdout, stderr) => {
      if (err) {
        reject(new Error(`sendmail failed: ${err.message}\n${stderr}`));
        return;
      }
      resolve();
    });
    child.stdin?.end(rawEmail);
  });
}

export async function createProjectViaAPI(
  name: string,
  slug?: string,
): Promise<{ slug: string; name: string }> {
  const { httpURL } = getE2EConfig();
  const res = await fetch(`${httpURL}/api/v1/projects`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(slug ? { name, slug } : { name }),
  });
  if (!res.ok) {
    throw new Error(`createProjectViaAPI failed: HTTP ${res.status} ${await res.text()}`);
  }
  return (await res.json()) as { slug: string; name: string };
}
