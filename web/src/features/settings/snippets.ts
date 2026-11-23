export interface SnippetInput {
  mode: "local" | "server";
  host: string;
  port: number;
  tlsMode: "starttls" | "tls" | "none";
  username: string;
  password: string;
}

export interface IntegrationSnippet {
  id: string;
  label: string;
  code: string;
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
  const swaks = `swaks --to test@example.com --server ${host}:${port}${swaksTlsFlag} --auth PLAIN --auth-user ${username} --auth-password '${password}'`;

  const snippets: IntegrationSnippet[] = [
    { id: "laravel", label: "Laravel", code: laravel },
    { id: "symfony", label: "Symfony", code: symfony },
    { id: "nodemailer", label: "Nodemailer", code: nodemailer },
    { id: "wordpress", label: "WordPress", code: wordpress },
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
