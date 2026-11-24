import { describe, expect, it } from "vitest";
import {
  generateApiCurlExamples,
  generateIntegrationSnippets,
  type SnippetInput,
} from "@/features/settings/snippets";

describe("generateIntegrationSnippets golden outputs", () => {
  it("matches golden strings for server mode (STARTTLS)", () => {
    const input: SnippetInput = {
      mode: "server",
      host: "mail.example.com",
      port: 587,
      tlsMode: "starttls",
      username: "shop-a1b2c3d4",
      password: "SecretPassword1234567890",
    };

    const snippets = generateIntegrationSnippets(input);
    expect(snippets.map((s) => s.id)).toEqual([
      "laravel",
      "symfony",
      "nodemailer",
      "wordpress",
      "django",
      "rails",
      "swaks",
    ]);

    const byId = Object.fromEntries(snippets.map((s) => [s.id, s]));

    expect(byId.laravel?.code).toBe(
      [
        "MAIL_MAILER=smtp",
        "MAIL_SCHEME=smtp            # Laravel 10 and older: MAIL_ENCRYPTION=tls",
        "MAIL_HOST=mail.example.com",
        "MAIL_PORT=587",
        "MAIL_USERNAME=shop-a1b2c3d4",
        "MAIL_PASSWORD=SecretPassword1234567890",
      ].join("\n"),
    );

    expect(byId.symfony?.code).toBe(
      "MAILER_DSN=smtp://shop-a1b2c3d4:SecretPassword1234567890@mail.example.com:587",
    );

    expect(byId.nodemailer?.code).toBe(
      [
        "nodemailer.createTransport({",
        '  host: "mail.example.com",',
        "  port: 587,",
        "  secure: false,",
        "  requireTLS: true,",
        "  auth: {",
        '    user: "shop-a1b2c3d4",',
        '    pass: "SecretPassword1234567890",',
        "  },",
        "})",
      ].join("\n"),
    );

    expect(byId.wordpress?.table).toEqual([
      { label: "Mailer", value: "Other SMTP" },
      { label: "SMTP Host", value: "mail.example.com" },
      { label: "Encryption", value: "TLS" },
      { label: "SMTP Port", value: "587" },
      { label: "Auto TLS", value: "On" },
      { label: "Authentication", value: "On" },
      { label: "SMTP Username", value: "shop-a1b2c3d4" },
      { label: "SMTP Password", value: "SecretPassword1234567890" },
    ]);

    expect(byId.wordpress?.code).toBe(
      [
        "// wp-config.php (WP Mail SMTP plugin)",
        "define('WPMS_ON', true);",
        "define('WPMS_MAILER', 'smtp');",
        "define('WPMS_SMTP_HOST', \"mail.example.com\");",
        "define('WPMS_SMTP_PORT', 587);",
        "define('WPMS_SSL', \"tls\");",
        "define('WPMS_SMTP_AUTH', true);",
        "define('WPMS_SMTP_USER', \"shop-a1b2c3d4\");",
        "define('WPMS_SMTP_PASS', \"SecretPassword1234567890\");",
        "define('WPMS_SMTP_AUTOTLS', true);",
      ].join("\n"),
    );

    expect(byId.django?.code).toBe(
      [
        'EMAIL_BACKEND = "django.core.mail.backends.smtp.EmailBackend"',
        'EMAIL_HOST = "mail.example.com"',
        "EMAIL_PORT = 587",
        'EMAIL_HOST_USER = "shop-a1b2c3d4"',
        'EMAIL_HOST_PASSWORD = "SecretPassword1234567890"',
        "EMAIL_USE_TLS = True",
      ].join("\n"),
    );

    expect(byId.rails?.code).toBe(
      [
        "config.action_mailer.delivery_method = :smtp",
        "config.action_mailer.smtp_settings = {",
        '  address: "mail.example.com",',
        "  port: 587,",
        '  user_name: "shop-a1b2c3d4",',
        '  password: "SecretPassword1234567890",',
        "  authentication: :plain,",
        "  enable_starttls_auto: true,",
        "}",
      ].join("\n"),
    );

    expect(byId.swaks?.code).toBe(
      "swaks --to test@example.com --server mail.example.com:587 --tls --auth PLAIN --auth-user shop-a1b2c3d4 --auth-password 'SecretPassword1234567890'",
    );
  });

  it("matches golden strings for server mode (implicit TLS)", () => {
    const snippets = generateIntegrationSnippets({
      mode: "server",
      host: "mail.example.com",
      port: 1465,
      tlsMode: "tls",
      username: "shop-a1b2c3d4",
      password: "********",
    });

    const byId = Object.fromEntries(snippets.map((s) => [s.id, s]));

    expect(byId.laravel?.code).toBe(
      [
        "MAIL_MAILER=smtp",
        "MAIL_SCHEME=smtps            # Laravel 10 and older: MAIL_ENCRYPTION=ssl",
        "MAIL_HOST=mail.example.com",
        "MAIL_PORT=1465",
        "MAIL_USERNAME=shop-a1b2c3d4",
        "MAIL_PASSWORD=********",
      ].join("\n"),
    );
    expect(byId.symfony?.code).toBe(
      "MAILER_DSN=smtps://shop-a1b2c3d4:********@mail.example.com:1465",
    );
    expect(byId.nodemailer?.code).toBe(
      [
        "nodemailer.createTransport({",
        '  host: "mail.example.com",',
        "  port: 1465,",
        "  secure: true,",
        "  auth: {",
        '    user: "shop-a1b2c3d4",',
        '    pass: "********",',
        "  },",
        "})",
      ].join("\n"),
    );
    expect(byId.wordpress?.table?.[2]).toEqual({ label: "Encryption", value: "SSL" });
    expect(byId.django?.code).toContain("EMAIL_USE_SSL = True");
    expect(byId.rails?.code).toContain("  tls: true,");
    expect(byId.swaks?.code).toBe(
      "swaks --to test@example.com --server mail.example.com:1465 --tls-on-connect --auth PLAIN --auth-user shop-a1b2c3d4 --auth-password '********'",
    );
  });

  it("matches golden strings for local mode (including PHP sendmail_path)", () => {
    const snippets = generateIntegrationSnippets({
      mode: "local",
      host: "localhost",
      port: 1025,
      tlsMode: "none",
      username: "shop",
      password: "dev",
    });

    expect(snippets.map((s) => s.id)).toEqual([
      "laravel",
      "symfony",
      "nodemailer",
      "wordpress",
      "django",
      "rails",
      "swaks",
      "php-sendmail",
    ]);

    const byId = Object.fromEntries(snippets.map((s) => [s.id, s]));

    expect(byId.laravel?.code).toBe(
      [
        "MAIL_MAILER=smtp",
        "MAIL_SCHEME=smtp            # Laravel 10 and older: MAIL_ENCRYPTION=null",
        "MAIL_HOST=localhost",
        "MAIL_PORT=1025",
        "MAIL_USERNAME=shop",
        "MAIL_PASSWORD=dev",
      ].join("\n"),
    );

    expect(byId.symfony?.code).toBe("MAILER_DSN=smtp://shop:dev@localhost:1025");

    expect(byId.nodemailer?.code).toBe(
      [
        "nodemailer.createTransport({",
        '  host: "localhost",',
        "  port: 1025,",
        "  secure: false,",
        "  auth: {",
        '    user: "shop",',
        '    pass: "dev",',
        "  },",
        "})",
      ].join("\n"),
    );

    expect(byId.wordpress?.table).toEqual([
      { label: "Mailer", value: "Other SMTP" },
      { label: "SMTP Host", value: "localhost" },
      { label: "Encryption", value: "None" },
      { label: "SMTP Port", value: "1025" },
      { label: "Auto TLS", value: "Off" },
      { label: "Authentication", value: "On" },
      { label: "SMTP Username", value: "shop" },
      { label: "SMTP Password", value: "dev" },
    ]);

    expect(byId.wordpress?.code).toBe(
      [
        "// wp-config.php (WP Mail SMTP plugin)",
        "define('WPMS_ON', true);",
        "define('WPMS_MAILER', 'smtp');",
        "define('WPMS_SMTP_HOST', \"localhost\");",
        "define('WPMS_SMTP_PORT', 1025);",
        "define('WPMS_SSL', \"\");",
        "define('WPMS_SMTP_AUTH', true);",
        "define('WPMS_SMTP_USER', \"shop\");",
        "define('WPMS_SMTP_PASS', \"dev\");",
        "define('WPMS_SMTP_AUTOTLS', false);",
      ].join("\n"),
    );

    expect(byId.django?.code).toBe(
      [
        'EMAIL_BACKEND = "django.core.mail.backends.smtp.EmailBackend"',
        'EMAIL_HOST = "localhost"',
        "EMAIL_PORT = 1025",
        'EMAIL_HOST_USER = "shop"',
        'EMAIL_HOST_PASSWORD = "dev"',
        "EMAIL_USE_TLS = False",
      ].join("\n"),
    );

    expect(byId.rails?.code).toBe(
      [
        "config.action_mailer.delivery_method = :smtp",
        "config.action_mailer.smtp_settings = {",
        '  address: "localhost",',
        "  port: 1025,",
        '  user_name: "shop",',
        '  password: "dev",',
        "  authentication: :plain,",
        "  enable_starttls_auto: false,",
        "}",
      ].join("\n"),
    );

    expect(byId.swaks?.code).toBe(
      "swaks --to test@example.com --server localhost:1025 --auth PLAIN --auth-user shop --auth-password 'dev'",
    );

    expect(byId["php-sendmail"]?.code).toBe(
      'sendmail_path = "/usr/local/bin/mailcubby sendmail -t --smtp localhost:1025 --user shop --password dev"',
    );
  });
});

describe("generateApiCurlExamples", () => {
  it("includes bearer header in server mode and omits it in local mode", () => {
    const serverExamples = generateApiCurlExamples({
      mode: "server",
      baseUrl: "https://mail.example.com/",
      projectSlug: "shop",
    });
    expect(serverExamples.map((e) => e.id)).toEqual(["list", "wait", "delete-all"]);
    expect(serverExamples[0]?.code).toContain("Authorization: Bearer $MAILCUBBY_TOKEN");
    expect(serverExamples[0]?.code).toContain(
      "https://mail.example.com/api/v1/projects/shop/messages",
    );
    expect(serverExamples[1]?.code).toContain("/api/v1/projects/shop/messages/wait");
    expect(serverExamples[2]?.code).toContain("-X DELETE");

    const localExamples = generateApiCurlExamples({
      mode: "local",
      baseUrl: "http://localhost:8025",
      projectSlug: "shop",
    });
    expect(localExamples[0]?.code).not.toContain("Authorization");
    expect(localExamples[0]?.code).toContain("http://localhost:8025/api/v1/projects/shop/messages");
  });
});
