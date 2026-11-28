import type { BadgeVariant } from "@/components/ui/badge";
import type {
  ExtractedLink,
  LinkCheckResult,
  LinkCheckSummary,
  LinkSource,
  LinkWarning,
} from "@/lib/api";
import { formatCount } from "@/lib/format";

export interface LinkWarningMeta {
  label: string;
  variant: BadgeVariant;
  description: string;
}

export interface LinkCheckStatusMeta {
  label: string;
  variant: BadgeVariant;
  detail: string | null;
}

const SOURCE_LABELS: Record<LinkSource, string> = {
  html: "HTML",
  text: "Text",
  header: "Header",
};

const WARNING_META: Record<LinkWarning, LinkWarningMeta> = {
  javascript_url: {
    label: "javascript: URL",
    variant: "destructive",
    description: "Script URI cannot be opened or actively checked.",
  },
  data_url: {
    label: "data: URL",
    variant: "destructive",
    description: "Embedded data URI cannot be opened or actively checked.",
  },
  credentials_in_url: {
    label: "Credentials in URL",
    variant: "destructive",
    description: "URL contains embedded username or password.",
  },
  http_insecure: {
    label: "Insecure HTTP",
    variant: "warning",
    description: "Unencrypted http:// link.",
  },
  missing_href: {
    label: "Missing href",
    variant: "warning",
    description: "Anchor element has a missing or empty href attribute.",
  },
};

const HTTP_SHORT_REASONS: Readonly<Record<number, string>> = {
  200: "OK",
  201: "Created",
  202: "Accepted",
  204: "No Content",
  400: "Bad Request",
  401: "Unauthorized",
  403: "Forbidden",
  404: "Not Found",
  405: "Method Not Allowed",
  408: "Request Timeout",
  410: "Gone",
  429: "Too Many Requests",
  500: "Server Error",
  502: "Bad Gateway",
  503: "Unavailable",
  504: "Gateway Timeout",
};

const secondsFormatter = new Intl.NumberFormat(undefined, {
  maximumFractionDigits: 1,
});

const EXPLICIT_HTTP_AUTHORITY = /^https?:\/\/[^/\\]/i;

function hasAsciiControlOrEdgeWhitespace(value: string): boolean {
  if (value !== value.trim()) {
    return true;
  }
  for (let i = 0; i < value.length; i += 1) {
    const code = value.charCodeAt(i);
    if (code <= 0x1f || code === 0x7f) {
      return true;
    }
  }
  return false;
}

/**
 * Validates that a URL is an explicit http:// or https:// URL with an explicit
 * authority delimiter (preventing same-origin relative navigation like
 * `https:/api/v1/meta` or `http:foo`) and no embedded credentials before
 * allowing it in an external anchor href.
 */
export function isSafeHttpUrl(rawUrl: string): boolean {
  if (!rawUrl || hasAsciiControlOrEdgeWhitespace(rawUrl)) {
    return false;
  }
  if (!EXPLICIT_HTTP_AUTHORITY.test(rawUrl)) {
    return false;
  }
  try {
    const parsed = new URL(rawUrl);
    if (parsed.protocol !== "https:" && parsed.protocol !== "http:") {
      return false;
    }
    if (!parsed.hostname) {
      return false;
    }
    if (parsed.username !== "" || parsed.password !== "") {
      return false;
    }
    return true;
  } catch {
    return false;
  }
}

export function canOpenLinkInNewTab(link: Pick<ExtractedLink, "url" | "scheme">): boolean {
  const scheme = link.scheme.toLowerCase();
  return (scheme === "http" || scheme === "https") && isSafeHttpUrl(link.url);
}

export function getLinkSourceLabel(source: LinkSource): string {
  return SOURCE_LABELS[source] ?? source;
}

export function getLinkWarningMeta(warning: LinkWarning): LinkWarningMeta {
  return (
    WARNING_META[warning] ?? {
      label: warning,
      variant: "warning",
      description: warning,
    }
  );
}

function shortHttpReason(code: number, fallback: string): string {
  return HTTP_SHORT_REASONS[code] ?? fallback;
}

function getBlockedLabel(errorCode: string | null): string {
  switch (errorCode) {
    case "blocked_ip":
      return "Blocked (private IP)";
    case "blocked_host":
      return "Blocked (local host)";
    case "blocked_port":
      return "Blocked (port)";
    case "credentials_in_url":
      return "Blocked (credentials)";
    case "blocked_scheme":
      return "Blocked (scheme)";
    case "invalid_url":
      return "Blocked (invalid URL)";
    default:
      return "Blocked";
  }
}

export function getLinkCheckStatusMeta(result: LinkCheckResult): LinkCheckStatusMeta {
  const finalDiffers =
    result.final_url !== null && result.final_url !== "" && result.final_url !== result.url;

  switch (result.status) {
    case "ok": {
      const label =
        result.status_code !== null
          ? `${result.status_code} ${shortHttpReason(result.status_code, "OK")}`
          : "OK";
      return { label, variant: "success", detail: null };
    }
    case "redirect": {
      let label: string;
      if (result.redirect_count > 0 && result.status_code !== null) {
        const hops = result.redirect_count === 1 ? "redirect" : "redirects";
        label = `${result.status_code} (${formatCount(result.redirect_count)} ${hops})`;
      } else if (result.status_code !== null) {
        label = `${result.status_code} Redirect`;
      } else {
        label = "Redirect";
      }
      return {
        label,
        variant: "info",
        detail: finalDiffers ? result.final_url : null,
      };
    }
    case "client_error": {
      const label =
        result.status_code !== null
          ? `${result.status_code} ${shortHttpReason(result.status_code, "Client error")}`
          : "4xx Error";
      return {
        label,
        variant: "destructive",
        detail: finalDiffers ? result.final_url : null,
      };
    }
    case "server_error": {
      const label =
        result.status_code !== null
          ? `${result.status_code} ${shortHttpReason(result.status_code, "Server error")}`
          : "5xx Error";
      return {
        label,
        variant: "destructive",
        detail: finalDiffers ? result.final_url : null,
      };
    }
    case "blocked":
      return {
        label: getBlockedLabel(result.error_code),
        variant: "warning",
        detail: result.error,
      };
    case "timeout":
      return {
        label: "Timeout",
        variant: "warning",
        detail: result.error,
      };
    case "dns_error":
      return {
        label: "DNS error",
        variant: "destructive",
        detail: result.error,
      };
    case "tls_error":
      return {
        label: "TLS error",
        variant: "destructive",
        detail: result.error,
      };
    case "too_many_redirects":
      return {
        label: "Too many redirects",
        variant: "warning",
        detail: result.error,
      };
    case "skipped_limit":
      return {
        label: "Skipped (limit)",
        variant: "neutral",
        detail: result.error,
      };
    default:
      return {
        label: "Network error",
        variant: "destructive",
        detail: result.error,
      };
  }
}

/**
 * Aggregates checked link results using the same bucket classification as
 * internal/linkcheck/checker.go summarizeResults.
 */
export function summarizeCheckResults(results: ReadonlyArray<LinkCheckResult>): LinkCheckSummary {
  let ok = 0;
  let redirect = 0;
  let broken = 0;
  let blocked = 0;
  let error = 0;
  let skipped = 0;

  for (const r of results) {
    switch (r.status) {
      case "ok":
        ok += 1;
        break;
      case "redirect":
        redirect += 1;
        break;
      case "client_error":
      case "server_error":
        broken += 1;
        break;
      case "blocked":
        blocked += 1;
        break;
      case "skipped_limit":
        skipped += 1;
        break;
      default:
        error += 1;
        break;
    }
  }

  return {
    total: results.length,
    ok,
    redirect,
    broken,
    blocked,
    error,
    skipped,
  };
}

export function formatCheckAnnouncement(
  results: ReadonlyArray<LinkCheckResult>,
  scope: "all" | "single",
): string {
  if (results.length === 0) {
    return "No checkable links to check.";
  }
  if (scope === "single" && results.length === 1) {
    const first = results[0];
    if (first) {
      const meta = getLinkCheckStatusMeta(first);
      return `Link check complete: ${meta.label}.`;
    }
  }

  const s = summarizeCheckResults(results);
  const parts: string[] = [];
  if (s.ok > 0) {
    parts.push(`${formatCount(s.ok)} OK`);
  }
  if (s.redirect > 0) {
    parts.push(`${formatCount(s.redirect)} ${s.redirect === 1 ? "redirect" : "redirects"}`);
  }
  if (s.broken > 0) {
    parts.push(`${formatCount(s.broken)} broken`);
  }
  if (s.blocked > 0) {
    parts.push(`${formatCount(s.blocked)} blocked`);
  }
  if (s.error > 0) {
    parts.push(`${formatCount(s.error)} ${s.error === 1 ? "error" : "errors"}`);
  }
  if (s.skipped > 0) {
    parts.push(`${formatCount(s.skipped)} skipped`);
  }
  const noun = s.total === 1 ? "link" : "links";
  return `Checked ${formatCount(s.total)} ${noun}: ${parts.join(", ")}.`;
}

export function formatDurationMs(durationMs: number): string {
  if (!Number.isFinite(durationMs) || durationMs <= 0) {
    return "< 1 ms";
  }
  if (durationMs >= 1000) {
    return `${secondsFormatter.format(durationMs / 1000)} s`;
  }
  return `${formatCount(Math.round(durationMs))} ms`;
}
