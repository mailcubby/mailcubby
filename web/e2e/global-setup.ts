import { type ChildProcess, spawn } from "node:child_process";
import { existsSync, mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(currentDir, "../..");
const binaryPath = path.join(repoRoot, "bin", "mailcubby");

function getFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.unref();
    server.on("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      if (!address || typeof address === "string") {
        server.close(() => reject(new Error("failed to allocate free loopback port")));
        return;
      }
      const { port } = address;
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
      throw new Error(`mailcubby exited early with code ${child.exitCode}:\n${logs.join("")}`);
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

export default async function globalSetup(): Promise<() => Promise<void>> {
  if (!existsSync(binaryPath)) {
    throw new Error(
      `mailcubby binary not found at ${binaryPath}; run 'make build-all' or 'make e2e'`,
    );
  }

  const httpPort = await getFreePort();
  const smtpPort = await getFreePort();
  const dataDir = mkdtempSync(path.join(tmpdir(), "mailcubby-e2e-"));

  const httpURL = `http://127.0.0.1:${httpPort}`;
  const smtpAddr = `127.0.0.1:${smtpPort}`;
  const logs: string[] = [];

  const child = spawn(binaryPath, ["serve"], {
    cwd: repoRoot,
    env: {
      ...process.env,
      MAILCUBBY_MODE: "local",
      MAILCUBBY_DATA_DIR: dataDir,
      MAILCUBBY_HTTP_ADDR: `127.0.0.1:${httpPort}`,
      MAILCUBBY_SMTP_ADDR: smtpAddr,
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
    await waitForReady(httpURL, child, logs);
  } catch (err) {
    child.kill("SIGKILL");
    rmSync(dataDir, { recursive: true, force: true });
    throw err;
  }

  process.env.MAILCUBBY_E2E_HTTP_URL = httpURL;
  process.env.MAILCUBBY_E2E_SMTP_ADDR = smtpAddr;
  process.env.MAILCUBBY_E2E_BINARY = binaryPath;
  process.env.MAILCUBBY_E2E_REPO_ROOT = repoRoot;

  return async () => {
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
  };
}
