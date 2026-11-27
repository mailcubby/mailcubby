package config

import (
	"encoding/base64"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mapLookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func testAppKeyBase64() string {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i + 1)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestParseDuration(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string
		want    time.Duration
		wantErr bool
	}{
		{name: "zero", in: "0", want: 0},
		{name: "zero days", in: "0d", want: 0},
		{name: "seconds", in: "90s", want: 90 * time.Second},
		{name: "minutes", in: "15m", want: 15 * time.Minute},
		{name: "hours", in: "24h", want: 24 * time.Hour},
		{name: "days", in: "30d", want: 30 * 24 * time.Hour},
		{name: "days and hours", in: "1d12h30m", want: 36*time.Hour + 30*time.Minute},
		{name: "fractional days", in: "1.5d", want: 36 * time.Hour},
		{name: "milliseconds", in: "250ms", want: 250 * time.Millisecond},
		{name: "empty", in: "", wantErr: true},
		{name: "negative seconds", in: "-5s", wantErr: true},
		{name: "negative days", in: "-1d", wantErr: true},
		{name: "missing unit", in: "10", wantErr: true},
		{name: "bad unit", in: "10w", wantErr: true},
		{name: "gibberish", in: "abc", wantErr: true},
		{name: "overflow days", in: "999999999999d", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseDuration(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseDuration(%q) = %v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDuration(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseDuration(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseSize(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string
		want    int64
		wantErr bool
	}{
		{name: "zero", in: "0", want: 0},
		{name: "plain bytes", in: "12345", want: 12345},
		{name: "B suffix", in: "512B", want: 512},
		{name: "KB", in: "10KB", want: 10_000},
		{name: "MB", in: "25MB", want: 25_000_000},
		{name: "GB", in: "2GB", want: 2_000_000_000},
		{name: "KiB", in: "4KiB", want: 4 * 1024},
		{name: "MiB", in: "25MiB", want: 25 * 1024 * 1024},
		{name: "GiB", in: "1GiB", want: 1024 * 1024 * 1024},
		{name: "empty", in: "", wantErr: true},
		{name: "negative", in: "-10MiB", wantErr: true},
		{name: "alpha", in: "abc", wantErr: true},
		{name: "lowercase unit", in: "10mb", wantErr: true},
		{name: "unknown unit", in: "10TiB", wantErr: true},
		{name: "overflow", in: "99999999999GiB", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseSize(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseSize(%q) = %d, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSize(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseSize(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseRate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		in        string
		allowZero bool
		want      RateLimit
		wantErr   bool
	}{
		{name: "per second", in: "5/s", want: RateLimit{Count: 5, Window: time.Second}},
		{name: "per minute", in: "600/m", want: RateLimit{Count: 600, Window: time.Minute}},
		{name: "per hour", in: "100/h", want: RateLimit{Count: 100, Window: time.Hour}},
		{name: "per 15m", in: "10/15m", want: RateLimit{Count: 10, Window: 15 * time.Minute}},
		{name: "per 1d", in: "1000/1d", want: RateLimit{Count: 1000, Window: 24 * time.Hour}},
		{name: "zero allowed", in: "0", allowZero: true, want: RateLimit{}},
		{name: "zero disallowed", in: "0", allowZero: false, wantErr: true},
		{name: "zero count", in: "0/m", allowZero: true, wantErr: true},
		{name: "negative count", in: "-5/m", wantErr: true},
		{name: "zero window", in: "5/0s", wantErr: true},
		{name: "missing slash", in: "10m", wantErr: true},
		{name: "bad window", in: "10/foo", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseRate(tc.in, tc.allowZero)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseRate(%q) = %+v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRate(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseRate(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseBool(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in      string
		want    bool
		wantErr bool
	}{
		{in: "true", want: true},
		{in: "TRUE", want: true},
		{in: "1", want: true},
		{in: "false", want: false},
		{in: "FALSE", want: false},
		{in: "0", want: false},
		{in: "yes", wantErr: true},
		{in: "no", wantErr: true},
		{in: "t", wantErr: true},
		{in: "", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := ParseBool(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseBool(%q) = %v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseBool(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseBool(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestValidateSlug(t *testing.T) {
	t.Parallel()

	cases := []struct {
		slug    string
		wantErr bool
	}{
		{slug: "default", wantErr: false},
		{slug: "shop-staging", wantErr: false},
		{slug: "a", wantErr: false},
		{slug: "a1-b2", wantErr: false},
		{slug: strings.Repeat("a", 63), wantErr: false},
		{slug: strings.Repeat("a", 64), wantErr: true},
		{slug: "", wantErr: true},
		{slug: "-abc", wantErr: true},
		{slug: "abc-", wantErr: true},
		{slug: "Shop", wantErr: true},
		{slug: "xn--punycode", wantErr: true},
		{slug: "ab--cd", wantErr: true},
		{slug: "api", wantErr: true},
		{slug: "admin", wantErr: true},
		{slug: "healthz", wantErr: true},
		{slug: "new", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.slug, func(t *testing.T) {
			t.Parallel()
			err := ValidateSlug(tc.slug)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateSlug(%q) = nil, want error", tc.slug)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateSlug(%q) unexpected error: %v", tc.slug, err)
			}
		})
	}
}

func TestLoadLocalDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := Load(mapLookup(nil))
	if err != nil {
		t.Fatalf("Load(empty) unexpected error: %v", err)
	}

	if cfg.Mode != ModeLocal {
		t.Errorf("Mode = %q, want %q", cfg.Mode, ModeLocal)
	}
	if cfg.DataDir != "./data" {
		t.Errorf("DataDir = %q, want ./data", cfg.DataDir)
	}
	if cfg.InstanceName != "Mailcubby" {
		t.Errorf("InstanceName = %q, want Mailcubby", cfg.InstanceName)
	}
	if cfg.DefaultProject != "default" {
		t.Errorf("DefaultProject = %q, want default", cfg.DefaultProject)
	}
	if cfg.LogLevel != slog.LevelInfo || cfg.LogFormat != LogFormatText {
		t.Errorf("Log = (%v, %q), want (info, text)", cfg.LogLevel, cfg.LogFormat)
	}
	if cfg.HTTPAddr != ":8025" || cfg.SMTPAddr != ":1025" {
		t.Errorf("Addrs = (%q, %q), want (:8025, :1025)", cfg.HTTPAddr, cfg.SMTPAddr)
	}
	if cfg.SMTPPublicHost != "localhost" || cfg.SMTPPublicPort != 1025 {
		t.Errorf("SMTPPublic = (%q, %d), want (localhost, 1025)", cfg.SMTPPublicHost, cfg.SMTPPublicPort)
	}
	if !cfg.SMTPAllowInsecureAuth {
		t.Errorf("SMTPAllowInsecureAuth = false, want true in local mode")
	}
	if cfg.SMTPMaxMessageBytes != 25*1024*1024 {
		t.Errorf("SMTPMaxMessageBytes = %d, want 25MiB", cfg.SMTPMaxMessageBytes)
	}
	if cfg.LoginRateLimit != (RateLimit{Count: 10, Window: 15 * time.Minute}) {
		t.Errorf("LoginRateLimit = %+v, want 10/15m", cfg.LoginRateLimit)
	}
	if cfg.APIRateLimit != (RateLimit{Count: 1200, Window: time.Minute}) {
		t.Errorf("APIRateLimit = %+v, want 1200/m", cfg.APIRateLimit)
	}
	if cfg.SMTPRateLimit != (RateLimit{Count: 600, Window: time.Minute}) {
		t.Errorf("SMTPRateLimit = %+v, want 600/m", cfg.SMTPRateLimit)
	}
	if cfg.RetentionMaxAge != 30*24*time.Hour || cfg.RetentionMaxMessages != 5000 || cfg.RetentionMaxBytes != 1024*1024*1024 {
		t.Errorf("Retention defaults mismatch: %+v", cfg)
	}
	if cfg.ZstdLevel != 3 || !cfg.HSTS || cfg.RenderRemoteImages != RenderRemoteImagesOptIn {
		t.Errorf("Other defaults mismatch: zstd=%d hsts=%v remote=%q", cfg.ZstdLevel, cfg.HSTS, cfg.RenderRemoteImages)
	}
}

func TestLoadServerDefaultsAndOverrides(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"MAILCUBBY_MODE":            "server",
		"MAILCUBBY_BASE_URL":        "https://mail.example.com/",
		"MAILCUBBY_APP_KEY":         testAppKeyBase64(),
		"MAILCUBBY_SMTP_TLS_CERT":   "/etc/mailcubby/cert.pem",
		"MAILCUBBY_SMTP_TLS_KEY":    "/etc/mailcubby/key.pem",
		"MAILCUBBY_TRUSTED_PROXIES": "10.0.0.0/8, 172.16.0.0/12",
		"MAILCUBBY_INBOX_DOMAIN":    "Staging.Example.Test",
		"MAILCUBBY_LOG_LEVEL":       "debug",
		"MAILCUBBY_LOG_FORMAT":      "json",
		"MAILCUBBY_SMTP_RATE_LIMIT": "0",
	}

	cfg, err := Load(mapLookup(env))
	if err != nil {
		t.Fatalf("Load(server) unexpected error: %v", err)
	}

	if cfg.Mode != ModeServer {
		t.Errorf("Mode = %q, want server", cfg.Mode)
	}
	if cfg.BaseURL != "https://mail.example.com" {
		t.Errorf("BaseURL = %q, want trimmed https://mail.example.com", cfg.BaseURL)
	}
	if cfg.SMTPPublicHost != "mail.example.com" {
		t.Errorf("SMTPPublicHost = %q, want mail.example.com", cfg.SMTPPublicHost)
	}
	if cfg.SMTPPublicPort != 587 {
		t.Errorf("SMTPPublicPort = %d, want 587", cfg.SMTPPublicPort)
	}
	if cfg.SMTPAllowInsecureAuth {
		t.Errorf("SMTPAllowInsecureAuth = true, want false in server mode")
	}
	if cfg.InboxDomain != "staging.example.test" {
		t.Errorf("InboxDomain = %q, want lowercase", cfg.InboxDomain)
	}
	if !cfg.SMTPRateLimit.Disabled() {
		t.Errorf("SMTPRateLimit = %+v, want disabled", cfg.SMTPRateLimit)
	}
	wantProxies := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
	}
	if len(cfg.TrustedProxies) != len(wantProxies) || cfg.TrustedProxies[0] != wantProxies[0] || cfg.TrustedProxies[1] != wantProxies[1] {
		t.Errorf("TrustedProxies = %v, want %v", cfg.TrustedProxies, wantProxies)
	}
}

func TestLoadSecretFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "app.key")
	passPath := filepath.Join(dir, "admin.pass")

	if err := os.WriteFile(keyPath, []byte("  "+testAppKeyBase64()+"\n"), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	if err := os.WriteFile(passPath, []byte("super-secret-password\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}

	cfg, err := Load(mapLookup(map[string]string{
		"MAILCUBBY_MODE":                     "server",
		"MAILCUBBY_BASE_URL":                 "https://mail.example.com",
		"MAILCUBBY_APP_KEY_FILE":             keyPath,
		"MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH": "true",
		"MAILCUBBY_ADMIN_EMAIL":              "admin@example.com",
		"MAILCUBBY_ADMIN_PASSWORD_FILE":      passPath,
	}))
	if err != nil {
		t.Fatalf("Load with _FILE secrets failed: %v", err)
	}
	if len(cfg.AppKey) != 32 {
		t.Errorf("len(AppKey) = %d, want 32", len(cfg.AppKey))
	}
	if cfg.AdminPassword != "super-secret-password" {
		t.Errorf("AdminPassword = %q, want trimmed file content", cfg.AdminPassword)
	}
}

func TestLoadValidationErrors(t *testing.T) {
	t.Parallel()

	shortKey := base64.StdEncoding.EncodeToString([]byte("short"))
	longKey := base64.StdEncoding.EncodeToString(make([]byte, 48))
	invalidKey := "not!valid!base64!!!"

	cases := []struct {
		name          string
		env           map[string]string
		wantVars      []string
		forbidSubstrs []string
	}{
		{
			name:     "invalid max message bytes",
			env:      map[string]string{"MAILCUBBY_SMTP_MAX_MESSAGE_BYTES": "abc"},
			wantVars: []string{"MAILCUBBY_SMTP_MAX_MESSAGE_BYTES"},
		},
		{
			name:     "max message bytes over 100MiB",
			env:      map[string]string{"MAILCUBBY_SMTP_MAX_MESSAGE_BYTES": "200MiB"},
			wantVars: []string{"MAILCUBBY_SMTP_MAX_MESSAGE_BYTES"},
		},
		{
			name:     "server mode missing base url, app key, and tls",
			env:      map[string]string{"MAILCUBBY_MODE": "server"},
			wantVars: []string{"MAILCUBBY_BASE_URL", "MAILCUBBY_APP_KEY", "mailcubby keygen", "MAILCUBBY_SMTP_TLS_CERT", "MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH"},
		},
		{
			name: "server mode without tls or allow insecure auth",
			env: map[string]string{
				"MAILCUBBY_MODE":     "server",
				"MAILCUBBY_BASE_URL": "https://mail.example.com",
				"MAILCUBBY_APP_KEY":  testAppKeyBase64(),
			},
			wantVars: []string{"MAILCUBBY_SMTP_TLS_CERT", "MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH"},
		},
		{
			name: "server mode base url with subpath",
			env: map[string]string{
				"MAILCUBBY_MODE":     "server",
				"MAILCUBBY_BASE_URL": "https://mail.example.com/mailcubby",
				"MAILCUBBY_APP_KEY":  testAppKeyBase64(),
			},
			wantVars: []string{"MAILCUBBY_BASE_URL"},
		},
		{
			name: "both app key and file set",
			env: map[string]string{
				"MAILCUBBY_APP_KEY":      testAppKeyBase64(),
				"MAILCUBBY_APP_KEY_FILE": "/tmp/nonexistent",
			},
			wantVars: []string{"MAILCUBBY_APP_KEY", "MAILCUBBY_APP_KEY_FILE"},
		},
		{
			name:     "missing secret file",
			env:      map[string]string{"MAILCUBBY_APP_KEY_FILE": "/nonexistent/mailcubby.key"},
			wantVars: []string{"MAILCUBBY_APP_KEY_FILE"},
		},
		{
			name: "short app key",
			env: map[string]string{
				"MAILCUBBY_APP_KEY": shortKey,
			},
			wantVars:      []string{"MAILCUBBY_APP_KEY", "mailcubby keygen"},
			forbidSubstrs: []string{shortKey},
		},
		{
			name: "long app key",
			env: map[string]string{
				"MAILCUBBY_APP_KEY": longKey,
			},
			wantVars:      []string{"MAILCUBBY_APP_KEY", "mailcubby keygen"},
			forbidSubstrs: []string{longKey},
		},
		{
			name: "invalid base64 app key",
			env: map[string]string{
				"MAILCUBBY_APP_KEY": invalidKey,
			},
			wantVars:      []string{"MAILCUBBY_APP_KEY", "mailcubby keygen"},
			forbidSubstrs: []string{invalidKey},
		},
		{
			name: "joined multiple errors",
			env: map[string]string{
				"MAILCUBBY_ZSTD_LEVEL":           "20",
				"MAILCUBBY_SMTP_PUBLIC_PORT":     "70000",
				"MAILCUBBY_RENDER_REMOTE_IMAGES": "always",
				"MAILCUBBY_DEFAULT_PROJECT":      "admin",
				"MAILCUBBY_TRUSTED_PROXIES":      "not-a-cidr",
				"MAILCUBBY_SMTP_TLS_CERT":        "/etc/cert.pem",
			},
			wantVars: []string{
				"MAILCUBBY_ZSTD_LEVEL",
				"MAILCUBBY_SMTP_PUBLIC_PORT",
				"MAILCUBBY_RENDER_REMOTE_IMAGES",
				"MAILCUBBY_DEFAULT_PROJECT",
				"MAILCUBBY_TRUSTED_PROXIES",
				"MAILCUBBY_SMTP_TLS_CERT",
			},
		},
		{
			name: "idle timeout exceeds session ttl",
			env: map[string]string{
				"MAILCUBBY_SESSION_TTL":          "1d",
				"MAILCUBBY_SESSION_IDLE_TIMEOUT": "2d",
			},
			wantVars: []string{"MAILCUBBY_SESSION_IDLE_TIMEOUT"},
		},
		{
			name: "admin password too short",
			env: map[string]string{
				"MAILCUBBY_ADMIN_EMAIL":    "admin@example.com",
				"MAILCUBBY_ADMIN_PASSWORD": "short",
			},
			wantVars: []string{"MAILCUBBY_ADMIN_PASSWORD"},
		},
		// SPEC 5.2: server mode refuses to start without an effective app key;
		// empty or whitespace values are unset (as for MAILCUBBY_BASE_URL).
		{
			name: "server mode empty app key",
			env: map[string]string{
				"MAILCUBBY_MODE":                     "server",
				"MAILCUBBY_BASE_URL":                 "https://mail.example.com",
				"MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH": "true",
				"MAILCUBBY_APP_KEY":                  "",
			},
			wantVars: []string{"MAILCUBBY_APP_KEY", "mailcubby keygen"},
		},
		{
			name: "server mode whitespace app key",
			env: map[string]string{
				"MAILCUBBY_MODE":                     "server",
				"MAILCUBBY_BASE_URL":                 "https://mail.example.com",
				"MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH": "true",
				"MAILCUBBY_APP_KEY":                  "   ",
			},
			wantVars: []string{"MAILCUBBY_APP_KEY", "mailcubby keygen"},
		},
		{
			name: "server mode empty app key and key file",
			env: map[string]string{
				"MAILCUBBY_MODE":                     "server",
				"MAILCUBBY_BASE_URL":                 "https://mail.example.com",
				"MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH": "true",
				"MAILCUBBY_APP_KEY":                  "",
				"MAILCUBBY_APP_KEY_FILE":             " ",
			},
			wantVars: []string{"MAILCUBBY_APP_KEY", "mailcubby keygen"},
		},
		// Open questions (Phase 1): admin email and password must be set together.
		{
			name: "admin email with empty password",
			env: map[string]string{
				"MAILCUBBY_ADMIN_EMAIL":    "admin@example.com",
				"MAILCUBBY_ADMIN_PASSWORD": "",
			},
			wantVars: []string{"MAILCUBBY_ADMIN_PASSWORD"},
		},
		{
			name: "admin email with empty password file",
			env: map[string]string{
				"MAILCUBBY_ADMIN_EMAIL":         "admin@example.com",
				"MAILCUBBY_ADMIN_PASSWORD_FILE": "  ",
			},
			wantVars: []string{"MAILCUBBY_ADMIN_PASSWORD"},
		},
		{
			name: "smtps addr without tls cert",
			env: map[string]string{
				"MAILCUBBY_SMTPS_ADDR": ":1465",
			},
			wantVars: []string{"MAILCUBBY_SMTPS_ADDR", "MAILCUBBY_SMTP_TLS_CERT", "MAILCUBBY_SMTP_TLS_KEY"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Load(mapLookup(tc.env))
			if err == nil {
				t.Fatalf("Load() succeeded, want error mentioning %v", tc.wantVars)
			}
			msg := err.Error()
			for _, v := range tc.wantVars {
				if !strings.Contains(msg, v) {
					t.Errorf("error %q does not mention variable %q", msg, v)
				}
			}
			for _, forbid := range tc.forbidSubstrs {
				if strings.Contains(msg, forbid) {
					t.Errorf("error %q leaked forbidden substring %q", msg, forbid)
				}
			}
		})
	}
}

// SPEC 5: config distinguishes an explicit MAILCUBBY_MODE from the local default.
func TestLoadModeExplicit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		env          map[string]string
		wantMode     Mode
		wantExplicit bool
	}{
		{name: "unset defaults to local", env: nil, wantMode: ModeLocal},
		{name: "blank counts as unset", env: map[string]string{"MAILCUBBY_MODE": "  "}, wantMode: ModeLocal},
		{name: "explicit local", env: map[string]string{"MAILCUBBY_MODE": "local"}, wantMode: ModeLocal, wantExplicit: true},
		{name: "explicit server", env: map[string]string{
			"MAILCUBBY_MODE":                     "server",
			"MAILCUBBY_BASE_URL":                 "https://mail.example.com",
			"MAILCUBBY_APP_KEY":                  testAppKeyBase64(),
			"MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH": "true",
		}, wantMode: ModeServer, wantExplicit: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := Load(mapLookup(tc.env))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Mode != tc.wantMode || cfg.ModeExplicit != tc.wantExplicit {
				t.Fatalf("Mode = (%q, explicit=%v), want (%q, %v)", cfg.Mode, cfg.ModeExplicit, tc.wantMode, tc.wantExplicit)
			}
		})
	}
}
