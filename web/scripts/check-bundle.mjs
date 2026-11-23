import { readFileSync } from "node:fs";
import { dirname, posix, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";

const MAX_GZIP_BYTES = 250 * 1024;
const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url));
const DIST_DIR = resolve(SCRIPT_DIR, "..", "dist");
const INDEX_HTML_PATH = resolve(DIST_DIR, "index.html");

function collectHtmlInitialScripts(html) {
  const urls = new Set();
  const entryRegex = /<script\b[^>]*\btype=["']module["'][^>]*\bsrc=["']([^"']+)["'][^>]*>/gi;
  const preloadRegex =
    /<link\b[^>]*\brel=["']modulepreload["'][^>]*\bhref=["']([^"']+)["'][^>]*>/gi;

  for (const match of html.matchAll(entryRegex)) {
    const src = match[1];
    if (src) {
      urls.add(src);
    }
  }
  for (const match of html.matchAll(preloadRegex)) {
    const href = match[1];
    if (href) {
      urls.add(href);
    }
  }
  return Array.from(urls);
}

/**
 * Extracts relative static ES module import specifiers (excluding dynamic `import(...)`).
 */
function collectStaticImports(source) {
  const specs = [];
  const staticImportRegex =
    /(?:^|[;\n}])\s*import(?:\s+[^'"]*?\s+from\s*|\s*)["'](\.[^"']+\.js)["']/gm;
  for (const match of source.matchAll(staticImportRegex)) {
    const spec = match[1];
    if (spec) {
      specs.push(spec);
    }
  }
  return specs;
}

function normalizeDistPath(urlPath) {
  const clean = urlPath.replace(/^\/+/, "");
  return posix.normalize(clean);
}

let html;
try {
  html = readFileSync(INDEX_HTML_PATH, "utf8");
} catch {
  console.error(`check:bundle: ${INDEX_HTML_PATH} not found (run 'pnpm build' first)`);
  process.exit(1);
}

const seedUrls = collectHtmlInitialScripts(html);
if (seedUrls.length === 0) {
  console.error("check:bundle: no module entry script found in dist/index.html");
  process.exit(1);
}

const visited = new Set();
const queue = seedUrls.map(normalizeDistPath);
const chunks = [];

while (queue.length > 0) {
  const relPath = queue.shift();
  if (!relPath || visited.has(relPath)) {
    continue;
  }
  visited.add(relPath);

  const absPath = resolve(DIST_DIR, relPath);
  const raw = readFileSync(absPath);
  const gzip = gzipSync(raw, { level: 9 });
  chunks.push({
    file: relPath,
    rawBytes: raw.length,
    gzipBytes: gzip.length,
  });

  const text = raw.toString("utf8");
  const baseDir = posix.dirname(relPath);
  for (const spec of collectStaticImports(text)) {
    const resolved = posix.normalize(posix.join(baseDir, spec));
    if (!visited.has(resolved)) {
      queue.push(resolved);
    }
  }
}

chunks.sort((a, b) => b.gzipBytes - a.gzipBytes);
const totalRaw = chunks.reduce((sum, c) => sum + c.rawBytes, 0);
const totalGzip = chunks.reduce((sum, c) => sum + c.gzipBytes, 0);

const fmtKb = (bytes) => `${(bytes / 1024).toFixed(2)} KB`;

console.log("Initial JS bundle (entry + static imports):");
for (const chunk of chunks) {
  console.log(
    `  ${chunk.file.padEnd(40)} ${fmtKb(chunk.rawBytes).padStart(10)} raw  ${fmtKb(chunk.gzipBytes).padStart(10)} gzip`,
  );
}
console.log(
  `Total: ${fmtKb(totalRaw)} raw / ${fmtKb(totalGzip)} gzip (budget: ${fmtKb(MAX_GZIP_BYTES)} gzip)`,
);

if (totalGzip >= MAX_GZIP_BYTES) {
  console.error(
    `check:bundle: FAILED — initial JS gzip size ${fmtKb(totalGzip)} exceeds budget ${fmtKb(MAX_GZIP_BYTES)}`,
  );
  process.exit(1);
}
console.log("check:bundle: OK");
