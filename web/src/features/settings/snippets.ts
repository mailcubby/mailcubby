export interface SnippetInput {
  mode: "local" | "server";
  host: string;
  port: number;
  tlsMode: "starttls" | "tls" | "none";
  username: string;
  password: string;
}

export interface SnippetTableRow {
  label: string;
  value: string;
}

export interface IntegrationSnippet {
  id: string;
  label: string;
  code: string;
  table?: ReadonlyArray<SnippetTableRow>;
}

export interface ApiCurlExample {
  id: string;
  title: string;
  description: string;
  code: string;
}

function escapeSingleQuotedShell(value: string): string {
  return value.replaceAll("'", "'\"'\"'");
}

export function formatWordPressSettingsTable(input: SnippetInput): ReadonlyArray<SnippetTableRow> {
  const encryption =
    input.tlsMode === "tls" ? "SSL" : input.tlsMode === "starttls" ? "TLS" : "None";
  return [
    { label: "Mailer", value: "Other SMTP" },
    { label: "SMTP Host", value: input.host },
    { label: "Encryption", value: encryption },
    { label: "SMTP Port", value: String(input.port) },
    { label: "Auto TLS", value: input.tlsMode === "starttls" ? "On" : "Off" },
    { label: "Authentication", value: "On" },
    { label: "SMTP Username", value: input.username },
    { label: "SMTP Password", value: input.password },
  ];
}

export function generateIntegrationSnippets(input: SnippetInput): IntegrationSnippet[] {
  const { mode, host, port, tlsMode, username, password } = input;
  const useTls = tlsMode === "starttls" || tlsMode === "tls";
  const isImplicitTls = tlsMode === "tls";
  const escapedUser = encodeURIComponent(username);
  const escapedPass = encodeURIComponent(password);

  const laravelScheme = isImplicitTls ? "smtps" : "smtp";
  const laravelLegacyEnc = isImplicitTls ? "ssl" : useTls ? "tls" : "null";

  const laravel = [
    "MAIL_MAILER=smtp",
    `MAIL_SCHEME=${laravelScheme}            # Laravel 10 and older: MAIL_ENCRYPTION=${laravelLegacyEnc}`,
    `MAIL_HOST=${host}`,
    `MAIL_PORT=${port}`,
    `MAIL_USERNAME=${username}`,
    `MAIL_PASSWORD=${password}`,
  ].join("\n");

  const symfonyScheme = isImplicitTls ? "smtps" : "smtp";
  const symfony = `MAILER_DSN=${symfonyScheme}://${escapedUser}:${escapedPass}@${host}:${port}`;

  const nodemailer = [
    "nodemailer.createTransport({",
    `  host: ${JSON.stringify(host)},`,
    `  port: ${port},`,
    `  secure: ${isImplicitTls ? "true" : "false"},`,
    ...(tlsMode === "starttls" ? ["  requireTLS: true,"] : []),
    "  auth: {",
    `    user: ${JSON.stringify(username)},`,
    `    pass: ${JSON.stringify(password)},`,
    "  },",
    "})",
  ].join("\n");

  const wpSsl = isImplicitTls ? "ssl" : tlsMode === "starttls" ? "tls" : "";
  const wordpress = [
    "// wp-config.php (WP Mail SMTP plugin)",
    "define('WPMS_ON', true);",
    "define('WPMS_MAILER', 'smtp');",
    `define('WPMS_SMTP_HOST', ${JSON.stringify(host)});`,
    `define('WPMS_SMTP_PORT', ${port});`,
    `define('WPMS_SSL', ${JSON.stringify(wpSsl)});`,
    "define('WPMS_SMTP_AUTH', true);",
    `define('WPMS_SMTP_USER', ${JSON.stringify(username)});`,
    `define('WPMS_SMTP_PASS', ${JSON.stringify(password)});`,
    `define('WPMS_SMTP_AUTOTLS', ${tlsMode === "starttls" ? "true" : "false"});`,
  ].join("\n");

  const django = [
    'EMAIL_BACKEND = "django.core.mail.backends.smtp.EmailBackend"',
    `EMAIL_HOST = ${JSON.stringify(host)}`,
    `EMAIL_PORT = ${port}`,
    `EMAIL_HOST_USER = ${JSON.stringify(username)}`,
    `EMAIL_HOST_PASSWORD = ${JSON.stringify(password)}`,
    ...(isImplicitTls
      ? ["EMAIL_USE_SSL = True"]
      : [`EMAIL_USE_TLS = ${tlsMode === "starttls" ? "True" : "False"}`]),
  ].join("\n");

  const rails = [
    "config.action_mailer.delivery_method = :smtp",
    "config.action_mailer.smtp_settings = {",
    `  address: ${JSON.stringify(host)},`,
    `  port: ${port},`,
    `  user_name: ${JSON.stringify(username)},`,
    `  password: ${JSON.stringify(password)},`,
    "  authentication: :plain,",
    ...(isImplicitTls
      ? ["  tls: true,"]
      : [`  enable_starttls_auto: ${tlsMode === "starttls" ? "true" : "false"},`]),
    "}",
  ].join("\n");

  const swaksTlsFlag = isImplicitTls ? " --tls-on-connect" : tlsMode === "starttls" ? " --tls" : "";
  const swaks = `swaks --to test@example.com --server ${host}:${port}${swaksTlsFlag} --auth PLAIN --auth-user ${username} --auth-password '${escapeSingleQuotedShell(password)}'`;

  const snippets: IntegrationSnippet[] = [
    { id: "laravel", label: "Laravel", code: laravel },
    { id: "symfony", label: "Symfony", code: symfony },
    { id: "nodemailer", label: "Nodemailer", code: nodemailer },
    {
      id: "wordpress",
      label: "WordPress",
      table: formatWordPressSettingsTable(input),
      code: wordpress,
    },
    { id: "django", label: "Django", code: django },
    { id: "rails", label: "Rails", code: rails },
    { id: "swaks", label: "swaks", code: swaks },
  ];

  if (mode === "local") {
    snippets.push({
      id: "php-sendmail",
      label: "PHP sendmail",
      code: `sendmail_path = "/usr/local/bin/mailcubby sendmail -t --smtp ${host}:${port} --user ${username} --password ${password}"`,
    });
  }

  return snippets;
}

export function generateApiCurlExamples(input: {
  mode: "local" | "server";
  baseUrl: string;
  projectSlug: string;
}): ApiCurlExample[] {
  const origin = input.baseUrl.replace(/\/+$/, "");
  const slug = encodeURIComponent(input.projectSlug);
  const authLine =
    input.mode === "server" ? `  -H "Authorization: Bearer $MAILCUBBY_TOKEN" \\\n` : "";

  return [
    {
      id: "list",
      title: "List messages",
      description: "Fetch the newest messages in this project (supports query, tag, and limit).",
      code: `curl -sS \\\n${authLine}  "${origin}/api/v1/projects/${slug}/messages?limit=20"`,
    },
    {
      id: "wait",
      title: "Wait for a matching message",
      description:
        "Block until a message matching your search query arrives (returns 200 or 204 on timeout).",
      code: `curl -sS \\\n${authLine}  "${origin}/api/v1/projects/${slug}/messages/wait?query=subject:welcome&timeout=30s"`,
    },
    {
      id: "delete-all",
      title: "Delete all messages",
      description:
        "Clear the inbox (requires a write token in server mode; pass ?query=... to delete a subset).",
      code: `curl -sS -X DELETE \\\n${authLine}  "${origin}/api/v1/projects/${slug}/messages"`,
    },
  ];
}
