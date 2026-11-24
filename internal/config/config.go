// Package config loads and validates Mailcubby configuration from environment
// variables as defined in SPEC section 9.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Mode selects the runtime operating mode of Mailcubby.
type Mode string

const (
	// ModeLocal runs Mailcubby without authentication for local development.
	ModeLocal Mode = "local"
	// ModeServer runs Mailcubby with multi-tenant authentication and isolation.
	ModeServer Mode = "server"
)

// LogFormat selects the slog output format.
type LogFormat string

const (
	// LogFormatText emits human-readable key-value logs.
	LogFormatText LogFormat = "text"
	// LogFormatJSON emits JSON-formatted structured logs.
	LogFormatJSON LogFormat = "json"
)

// RenderRemoteImages controls whether the HTML preview allows opting in to remote images.
type RenderRemoteImages string

const (
	// RenderRemoteImagesOptIn blocks remote images by default with a per-message toggle.
	RenderRemoteImagesOptIn RenderRemoteImages = "opt-in"
	// RenderRemoteImagesOff disables the remote images toggle completely.
	RenderRemoteImagesOff RenderRemoteImages = "off"
)

const (
	defaultDataDir                  = "./data"
	defaultInstanceName             = "Mailcubby"
	defaultProject                  = "default"
	defaultSendmailSMTP             = "localhost:1025"
	defaultHTTPAddr                 = ":8025"
	defaultSessionTTL               = 30 * dayDuration
	defaultSessionIdleTimeout       = 7 * dayDuration
	defaultInviteTTL                = 7 * dayDuration
	defaultLoginRateCount           = 10
	defaultLoginRateWindow          = 15 * time.Minute
	defaultAPIRateCount             = 1200
	defaultAPIRateWindow            = time.Minute
	defaultAPIWaitMaxTimeout        = 120 * time.Second
	defaultSSEMaxConnectionsPerUser = 10
	defaultSMTPAddr                 = ":1025"
	defaultSMTPHostname             = "mailcubby"
	defaultSMTPPublicHost           = "localhost"
	defaultSMTPPublicPortLocal      = 1025
	defaultSMTPPublicPortServer     = 587
	defaultSMTPMaxMessageBytes      = 25 * 1024 * 1024
	maxAllowedSMTPMessageBytes      = 100 * 1024 * 1024
	defaultSMTPMaxRecipients        = 100
	defaultSMTPMaxConnections       = 200
	defaultSMTPMaxConnectionsPerIP  = 20
	defaultSMTPTimeout              = 60 * time.Second
	defaultSMTPAuthMaxFailures      = 5
	defaultSMTPAuthLockout          = 15 * time.Minute
	defaultSMTPRateCount            = 600
	defaultSMTPRateWindow           = time.Minute
	defaultRetentionMaxAge          = 30 * dayDuration
	defaultRetentionMaxMessages     = 5000
	defaultRetentionMaxBytes        = 1 * 1024 * 1024 * 1024
	defaultRetentionInterval        = 5 * time.Minute
	defaultAuditRetention           = 180 * dayDuration
	defaultZstdLevel                = 3
	minAdminPasswordLen             = 10
	maxAdminPasswordLen             = 256
)

// Config holds all validated runtime configuration for Mailcubby.
type Config struct {
	// General (SPEC 9.1)
	Mode           Mode
	DataDir        string
	BaseURL        string
	AppKey         []byte
	InstanceName   string
	DefaultProject string
	InboxDomain    string
	LogLevel       slog.Level
	LogFormat      LogFormat
	AdminEmail     string
	AdminPassword  string
	SendmailSMTP   string

	// HTTP (SPEC 9.2)
	HTTPAddr                 string
	TrustedProxies           []netip.Prefix
	SessionTTL               time.Duration
	SessionIdleTimeout       time.Duration
	InviteTTL                time.Duration
	HSTS                     bool
	LoginRateLimit           RateLimit
	APIRateLimit             RateLimit
	APIWaitMaxTimeout        time.Duration
	SSEMaxConnectionsPerUser int
	RenderRemoteImages       RenderRemoteImages

	// SMTP (SPEC 9.3)
	SMTPAddr                string
	SMTPSAddr               string
	SMTPHostname            string
	SMTPPublicHost          string
	SMTPPublicPort          int
	SMTPTLSCert             string
	SMTPTLSKey              string
	SMTPAllowInsecureAuth   bool
	SMTPMaxMessageBytes     int64
	SMTPMaxRecipients       int
	SMTPMaxConnections      int
	SMTPMaxConnectionsPerIP int
	SMTPTimeout             time.Duration
	SMTPAuthMaxFailures     int
	SMTPAuthLockout         time.Duration
	SMTPRateLimit           RateLimit

	// Retention and storage (SPEC 9.4)
	RetentionMaxAge      time.Duration
	RetentionMaxMessages int64
	RetentionMaxBytes    int64
	RetentionInterval    time.Duration
	InstanceMaxBytes     int64
	AuditRetention       time.Duration
	ZstdLevel            int
}

// Load parses and validates all MAILCUBBY_* environment variables using lookup.
// All validation errors are returned joined via errors.Join, each naming its variable.
func Load(lookup func(string) (string, bool)) (Config, error) {
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}

	var errs []error
	addErr := func(name string, err error) {
		errs = append(errs, fmt.Errorf("%s: %w", name, err))
	}

	get := func(name string) (string, bool) {
		v, ok := lookup(name)
		if !ok || strings.TrimSpace(v) == "" {
			return "", false
		}
		return strings.TrimSpace(v), true
	}

	getSecret := func(name string) (string, bool) {
		fileVar := name + "_FILE"
		val, hasVal := lookup(name)
		filePath, hasFile := lookup(fileVar)
		hasVal = hasVal && strings.TrimSpace(val) != ""
		hasFile = hasFile && strings.TrimSpace(filePath) != ""

		if hasVal && hasFile {
			addErr(name, fmt.Errorf("cannot set both %s and %s", name, fileVar))
			return "", false
		}
		if hasVal {
			return strings.TrimSpace(val), true
		}
		if !hasFile {
			return "", false
		}
		cleanPath := filepath.Clean(strings.TrimSpace(filePath))
		//nolint:gosec // G304: secret file path is explicitly configured via MAILCUBBY_*_FILE
		data, err := os.ReadFile(cleanPath)
		if err != nil {
			addErr(fileVar, fmt.Errorf("read secret file %q: %w", cleanPath, err))
			return "", false
		}
		trimmed := strings.TrimSpace(string(data))
		if trimmed == "" {
			addErr(fileVar, errors.New("secret file is empty"))
			return "", false
		}
		return trimmed, true
	}

	cfg := Config{
		Mode:                     ModeLocal,
		DataDir:                  defaultDataDir,
		InstanceName:             defaultInstanceName,
		DefaultProject:           defaultProject,
		LogLevel:                 slog.LevelInfo,
		LogFormat:                LogFormatText,
		SendmailSMTP:             defaultSendmailSMTP,
		HTTPAddr:                 defaultHTTPAddr,
		SessionTTL:               defaultSessionTTL,
		SessionIdleTimeout:       defaultSessionIdleTimeout,
		InviteTTL:                defaultInviteTTL,
		HSTS:                     true,
		LoginRateLimit:           RateLimit{Count: defaultLoginRateCount, Window: defaultLoginRateWindow},
		APIRateLimit:             RateLimit{Count: defaultAPIRateCount, Window: defaultAPIRateWindow},
		APIWaitMaxTimeout:        defaultAPIWaitMaxTimeout,
		SSEMaxConnectionsPerUser: defaultSSEMaxConnectionsPerUser,
		RenderRemoteImages:       RenderRemoteImagesOptIn,
		SMTPAddr:                 defaultSMTPAddr,
		SMTPHostname:             defaultSMTPHostname,
		SMTPMaxMessageBytes:      defaultSMTPMaxMessageBytes,
		SMTPMaxRecipients:        defaultSMTPMaxRecipients,
		SMTPMaxConnections:       defaultSMTPMaxConnections,
		SMTPMaxConnectionsPerIP:  defaultSMTPMaxConnectionsPerIP,
		SMTPTimeout:              defaultSMTPTimeout,
		SMTPAuthMaxFailures:      defaultSMTPAuthMaxFailures,
		SMTPAuthLockout:          defaultSMTPAuthLockout,
		SMTPRateLimit:            RateLimit{Count: defaultSMTPRateCount, Window: defaultSMTPRateWindow},
		RetentionMaxAge:          defaultRetentionMaxAge,
		RetentionMaxMessages:     defaultRetentionMaxMessages,
		RetentionMaxBytes:        defaultRetentionMaxBytes,
		RetentionInterval:        defaultRetentionInterval,
		InstanceMaxBytes:         0,
		AuditRetention:           defaultAuditRetention,
		ZstdLevel:                defaultZstdLevel,
	}

	// General (SPEC 9.1)
	if v, ok := get("MAILCUBBY_MODE"); ok {
		switch Mode(strings.ToLower(v)) {
		case ModeLocal:
			cfg.Mode = ModeLocal
		case ModeServer:
			cfg.Mode = ModeServer
		default:
			addErr("MAILCUBBY_MODE", fmt.Errorf("invalid mode %q: expected local or server", v))
		}
	}

	if v, ok := get("MAILCUBBY_DATA_DIR"); ok {
		cfg.DataDir = v
	}

	var baseURLHost string
	if v, ok := get("MAILCUBBY_BASE_URL"); ok {
		u, err := parseBaseURL(v)
		if err != nil {
			addErr("MAILCUBBY_BASE_URL", err)
		} else {
			cfg.BaseURL = strings.TrimRight(u.String(), "/")
			baseURLHost = u.Hostname()
		}
	}

	if v, ok := getSecret("MAILCUBBY_APP_KEY"); ok {
		key, err := parseBase64Key32(v)
		if err != nil {
			addErr("MAILCUBBY_APP_KEY", err)
		} else {
			cfg.AppKey = key
		}
	}

	if v, ok := get("MAILCUBBY_INSTANCE_NAME"); ok {
		cfg.InstanceName = v
	}

	if v, ok := get("MAILCUBBY_DEFAULT_PROJECT"); ok {
		if err := ValidateSlug(v); err != nil {
			addErr("MAILCUBBY_DEFAULT_PROJECT", err)
		} else {
			cfg.DefaultProject = v
		}
	}

	if v, ok := get("MAILCUBBY_INBOX_DOMAIN"); ok {
		lower := strings.ToLower(v)
		if err := validateDomain(lower); err != nil {
			addErr("MAILCUBBY_INBOX_DOMAIN", err)
		} else {
			cfg.InboxDomain = lower
		}
	}

	if v, ok := get("MAILCUBBY_LOG_LEVEL"); ok {
		lvl, err := parseLogLevel(v)
		if err != nil {
			addErr("MAILCUBBY_LOG_LEVEL", err)
		} else {
			cfg.LogLevel = lvl
		}
	}

	if v, ok := get("MAILCUBBY_LOG_FORMAT"); ok {
		switch LogFormat(strings.ToLower(v)) {
		case LogFormatText:
			cfg.LogFormat = LogFormatText
		case LogFormatJSON:
			cfg.LogFormat = LogFormatJSON
		default:
			addErr("MAILCUBBY_LOG_FORMAT", fmt.Errorf("invalid format %q: expected text or json", v))
		}
	}

	if v, ok := get("MAILCUBBY_ADMIN_EMAIL"); ok {
		cfg.AdminEmail = v
	}
	if v, ok := getSecret("MAILCUBBY_ADMIN_PASSWORD"); ok {
		if len(v) < minAdminPasswordLen || len(v) > maxAdminPasswordLen {
			addErr("MAILCUBBY_ADMIN_PASSWORD", fmt.Errorf("length must be between %d and %d characters", minAdminPasswordLen, maxAdminPasswordLen))
		} else {
			cfg.AdminPassword = v
		}
	}
	if (cfg.AdminEmail != "") != (cfg.AdminPassword != "") {
		// Only report pairing mismatch if neither variable already failed to load/validate.
		_, hasEmailEnv := get("MAILCUBBY_ADMIN_EMAIL")
		// Empty or whitespace-only values count as unset, as in getSecret.
		_, hasPassEnv := get("MAILCUBBY_ADMIN_PASSWORD")
		_, hasPassFileEnv := get("MAILCUBBY_ADMIN_PASSWORD_FILE")
		if hasEmailEnv && !hasPassEnv && !hasPassFileEnv {
			addErr("MAILCUBBY_ADMIN_PASSWORD", errors.New("must be set when MAILCUBBY_ADMIN_EMAIL is set"))
		} else if !hasEmailEnv && cfg.AdminPassword != "" {
			addErr("MAILCUBBY_ADMIN_EMAIL", errors.New("must be set when MAILCUBBY_ADMIN_PASSWORD is set"))
		}
	}

	if v, ok := get("MAILCUBBY_SENDMAIL_SMTP"); ok {
		cfg.SendmailSMTP = v
	}

	// HTTP (SPEC 9.2)
	if v, ok := get("MAILCUBBY_HTTP_ADDR"); ok {
		cfg.HTTPAddr = v
	}

	if v, ok := get("MAILCUBBY_TRUSTED_PROXIES"); ok {
		prefixes, err := ParseTrustedProxies(v)
		if err != nil {
			addErr("MAILCUBBY_TRUSTED_PROXIES", err)
		} else {
			cfg.TrustedProxies = prefixes
		}
	}

	parsePositiveDuration("MAILCUBBY_SESSION_TTL", get, &cfg.SessionTTL, addErr)
	parsePositiveDuration("MAILCUBBY_SESSION_IDLE_TIMEOUT", get, &cfg.SessionIdleTimeout, addErr)
	if cfg.SessionIdleTimeout > cfg.SessionTTL {
		addErr("MAILCUBBY_SESSION_IDLE_TIMEOUT", errors.New("must not exceed MAILCUBBY_SESSION_TTL"))
	}
	parsePositiveDuration("MAILCUBBY_INVITE_TTL", get, &cfg.InviteTTL, addErr)

	if v, ok := get("MAILCUBBY_HSTS"); ok {
		b, err := ParseBool(v)
		if err != nil {
			addErr("MAILCUBBY_HSTS", err)
		} else {
			cfg.HSTS = b
		}
	}

	if v, ok := get("MAILCUBBY_LOGIN_RATE_LIMIT"); ok {
		r, err := ParseRate(v, false)
		if err != nil {
			addErr("MAILCUBBY_LOGIN_RATE_LIMIT", err)
		} else {
			cfg.LoginRateLimit = r
		}
	}

	if v, ok := get("MAILCUBBY_API_RATE_LIMIT"); ok {
		r, err := ParseRate(v, false)
		if err != nil {
			addErr("MAILCUBBY_API_RATE_LIMIT", err)
		} else {
			cfg.APIRateLimit = r
		}
	}

	parsePositiveDuration("MAILCUBBY_API_WAIT_MAX_TIMEOUT", get, &cfg.APIWaitMaxTimeout, addErr)
	parsePositiveInt("MAILCUBBY_SSE_MAX_CONNECTIONS_PER_USER", get, &cfg.SSEMaxConnectionsPerUser, addErr)

	if v, ok := get("MAILCUBBY_RENDER_REMOTE_IMAGES"); ok {
		switch RenderRemoteImages(strings.ToLower(v)) {
		case RenderRemoteImagesOptIn:
			cfg.RenderRemoteImages = RenderRemoteImagesOptIn
		case RenderRemoteImagesOff:
			cfg.RenderRemoteImages = RenderRemoteImagesOff
		default:
			addErr("MAILCUBBY_RENDER_REMOTE_IMAGES", fmt.Errorf("invalid value %q: expected opt-in or off", v))
		}
	}

	// Mode-dependent SMTP defaults (SPEC 9.3)
	if cfg.Mode == ModeServer {
		cfg.SMTPPublicPort = defaultSMTPPublicPortServer
		cfg.SMTPAllowInsecureAuth = false
	} else {
		cfg.SMTPPublicPort = defaultSMTPPublicPortLocal
		cfg.SMTPAllowInsecureAuth = true
	}
	if baseURLHost != "" {
		cfg.SMTPPublicHost = baseURLHost
	} else {
		cfg.SMTPPublicHost = defaultSMTPPublicHost
	}

	if v, ok := get("MAILCUBBY_SMTP_ADDR"); ok {
		cfg.SMTPAddr = v
	}
	if v, ok := get("MAILCUBBY_SMTPS_ADDR"); ok {
		cfg.SMTPSAddr = v
	}
	if v, ok := get("MAILCUBBY_SMTP_HOSTNAME"); ok {
		cfg.SMTPHostname = v
	}
	if v, ok := get("MAILCUBBY_SMTP_PUBLIC_HOST"); ok {
		cfg.SMTPPublicHost = v
	}
	if v, ok := get("MAILCUBBY_SMTP_PUBLIC_PORT"); ok {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			addErr("MAILCUBBY_SMTP_PUBLIC_PORT", fmt.Errorf("invalid port %q: expected integer in 1..65535", v))
		} else {
			cfg.SMTPPublicPort = p
		}
	}

	if v, ok := get("MAILCUBBY_SMTP_TLS_CERT"); ok {
		cfg.SMTPTLSCert = v
	}
	if v, ok := get("MAILCUBBY_SMTP_TLS_KEY"); ok {
		cfg.SMTPTLSKey = v
	}
	if (cfg.SMTPTLSCert != "") != (cfg.SMTPTLSKey != "") {
		addErr("MAILCUBBY_SMTP_TLS_CERT", errors.New("MAILCUBBY_SMTP_TLS_CERT and MAILCUBBY_SMTP_TLS_KEY must both be set"))
	}
	if cfg.SMTPSAddr != "" && (cfg.SMTPTLSCert == "" || cfg.SMTPTLSKey == "") {
		addErr("MAILCUBBY_SMTPS_ADDR", errors.New("requires MAILCUBBY_SMTP_TLS_CERT and MAILCUBBY_SMTP_TLS_KEY"))
	}

	if v, ok := get("MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH"); ok {
		b, err := ParseBool(v)
		if err != nil {
			addErr("MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH", err)
		} else {
			cfg.SMTPAllowInsecureAuth = b
		}
	}

	if v, ok := get("MAILCUBBY_SMTP_MAX_MESSAGE_BYTES"); ok {
		sz, err := ParseSize(v)
		switch {
		case err != nil:
			addErr("MAILCUBBY_SMTP_MAX_MESSAGE_BYTES", err)
		case sz <= 0 || sz > maxAllowedSMTPMessageBytes:
			addErr("MAILCUBBY_SMTP_MAX_MESSAGE_BYTES", fmt.Errorf("must be between 1 byte and 100MiB (%d bytes), got %d", maxAllowedSMTPMessageBytes, sz))
		default:
			cfg.SMTPMaxMessageBytes = sz
		}
	}

	parsePositiveInt("MAILCUBBY_SMTP_MAX_RECIPIENTS", get, &cfg.SMTPMaxRecipients, addErr)
	parsePositiveInt("MAILCUBBY_SMTP_MAX_CONNECTIONS", get, &cfg.SMTPMaxConnections, addErr)
	parsePositiveInt("MAILCUBBY_SMTP_MAX_CONNECTIONS_PER_IP", get, &cfg.SMTPMaxConnectionsPerIP, addErr)
	parsePositiveDuration("MAILCUBBY_SMTP_TIMEOUT", get, &cfg.SMTPTimeout, addErr)
	parsePositiveInt("MAILCUBBY_SMTP_AUTH_MAX_FAILURES", get, &cfg.SMTPAuthMaxFailures, addErr)
	parsePositiveDuration("MAILCUBBY_SMTP_AUTH_LOCKOUT", get, &cfg.SMTPAuthLockout, addErr)

	if v, ok := get("MAILCUBBY_SMTP_RATE_LIMIT"); ok {
		r, err := ParseRate(v, true)
		if err != nil {
			addErr("MAILCUBBY_SMTP_RATE_LIMIT", err)
		} else {
			cfg.SMTPRateLimit = r
		}
	}

	// Retention and storage (SPEC 9.4)
	if v, ok := get("MAILCUBBY_RETENTION_MAX_AGE"); ok {
		d, err := ParseDuration(v)
		if err != nil {
			addErr("MAILCUBBY_RETENTION_MAX_AGE", err)
		} else {
			cfg.RetentionMaxAge = d
		}
	}

	if v, ok := get("MAILCUBBY_RETENTION_MAX_MESSAGES"); ok {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			addErr("MAILCUBBY_RETENTION_MAX_MESSAGES", fmt.Errorf("invalid count %q: expected non-negative integer", v))
		} else {
			cfg.RetentionMaxMessages = n
		}
	}

	if v, ok := get("MAILCUBBY_RETENTION_MAX_BYTES"); ok {
		sz, err := ParseSize(v)
		if err != nil {
			addErr("MAILCUBBY_RETENTION_MAX_BYTES", err)
		} else {
			cfg.RetentionMaxBytes = sz
		}
	}

	parsePositiveDuration("MAILCUBBY_RETENTION_INTERVAL", get, &cfg.RetentionInterval, addErr)

	if v, ok := get("MAILCUBBY_INSTANCE_MAX_BYTES"); ok {
		sz, err := ParseSize(v)
		if err != nil {
			addErr("MAILCUBBY_INSTANCE_MAX_BYTES", err)
		} else {
			cfg.InstanceMaxBytes = sz
		}
	}

	parsePositiveDuration("MAILCUBBY_AUDIT_RETENTION", get, &cfg.AuditRetention, addErr)

	if v, ok := get("MAILCUBBY_ZSTD_LEVEL"); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 19 {
			addErr("MAILCUBBY_ZSTD_LEVEL", fmt.Errorf("invalid level %q: expected integer in 1..19", v))
		} else {
			cfg.ZstdLevel = n
		}
	}

	// Server mode startup checks (SPEC 5.2).
	if cfg.Mode == ModeServer {
		if _, hasBase := get("MAILCUBBY_BASE_URL"); !hasBase {
			addErr("MAILCUBBY_BASE_URL", errors.New("required in server mode"))
		}
		// Empty or whitespace-only values count as unset, as in getSecret; a set
		// but invalid value has already been reported above.
		_, hasKey := get("MAILCUBBY_APP_KEY")
		_, hasKeyFile := get("MAILCUBBY_APP_KEY_FILE")
		if len(cfg.AppKey) == 0 && !hasKey && !hasKeyFile {
			addErr("MAILCUBBY_APP_KEY", errors.New("required in server mode (or set MAILCUBBY_APP_KEY_FILE)"))
		}
		if cfg.SMTPTLSCert == "" && cfg.SMTPTLSKey == "" && !cfg.SMTPAllowInsecureAuth {
			addErr("MAILCUBBY_SMTP_TLS_CERT", errors.New("required in server mode unless MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH=true"))
		}
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return cfg, nil
}

func parseBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("invalid URL %q: scheme must be http or https", raw)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("invalid URL %q: host is required", raw)
	}
	if u.User != nil {
		return nil, fmt.Errorf("invalid URL %q: credentials are not allowed", raw)
	}
	if u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("invalid URL %q: sub-path hosting is not supported in v1", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid URL %q: query and fragment are not allowed", raw)
	}
	return u, nil
}

func parseLogLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("invalid log level %q: expected debug, info, warn, or error", raw)
	}
}

func parsePositiveDuration(name string, get func(string) (string, bool), dst *time.Duration, addErr func(string, error)) {
	v, ok := get(name)
	if !ok {
		return
	}
	d, err := ParseDuration(v)
	if err != nil {
		addErr(name, err)
		return
	}
	if d <= 0 {
		addErr(name, errors.New("must be greater than 0"))
		return
	}
	*dst = d
}

func parsePositiveInt(name string, get func(string) (string, bool), dst *int, addErr func(string, error)) {
	v, ok := get(name)
	if !ok {
		return
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		addErr(name, fmt.Errorf("invalid integer %q: expected positive integer", v))
		return
	}
	*dst = n
}
