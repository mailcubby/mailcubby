// Package smtpd wraps github.com/emersion/go-smtp to implement Mailcubby's
// SMTP listener, SASL PLAIN/LOGIN authentication, routing, and connection limits.
package smtpd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/store"
)

const smtpMaxLineLength = 2000

// Ingestor is the consumer-declared interface used by SMTP sessions on DATA.
type Ingestor interface {
	Ingest(ctx context.Context, env ingest.Envelope, targets []ingest.Target, r io.Reader) ([]ingest.MessageRef, error)
}

// ErrQuotaExceeded is returned by ingest hooks when instance storage quota is hit (Phase 10).
var ErrQuotaExceeded = errors.New("smtpd: instance storage quota exceeded")

// ErrRateLimited is returned by ingest hooks when a project exceeds its SMTP rate limit (Phase 10).
var ErrRateLimited = errors.New("smtpd: project rate limit exceeded")

// Server runs Mailcubby's SMTP listener with connection limits and graceful shutdown.
type Server struct {
	cfg    config.Config
	smtp   *smtp.Server
	log    *slog.Logger
	lookup ProjectLookup

	mu        sync.Mutex
	closed    bool
	listeners []*LimitListener
}

// New constructs an SMTP Server configured from cfg.
func New(cfg config.Config, st *store.Store, ing Ingestor, clk clock.Clock, log *slog.Logger) *Server {
	if clk == nil {
		clk = clock.NewReal()
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return NewWithLookup(cfg, &storeProjectLookup{store: st, clk: clk}, ing, log)
}

// NewWithLookup constructs an SMTP Server with a custom ProjectLookup (used in tests).
func NewWithLookup(cfg config.Config, lookup ProjectLookup, ing Ingestor, log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	be := &backend{
		cfg:    cfg,
		lookup: lookup,
		ingest: ing,
		log:    log,
	}

	s := smtp.NewServer(be)
	s.Domain = cfg.SMTPHostname
	s.MaxMessageBytes = cfg.SMTPMaxMessageBytes
	s.MaxRecipients = cfg.SMTPMaxRecipients
	s.MaxLineLength = smtpMaxLineLength
	s.ReadTimeout = cfg.SMTPTimeout
	s.WriteTimeout = cfg.SMTPTimeout
	s.AllowInsecureAuth = cfg.SMTPAllowInsecureAuth
	s.EnableSMTPUTF8 = true
	s.ErrorLog = slogSMTPLogger{log: log}

	return &Server{
		cfg:    cfg,
		smtp:   s,
		log:    log,
		lookup: lookup,
	}
}

// SetTLSConfig configures TLS (STARTTLS) on the underlying SMTP server.
// Must be called before Serve.
func (s *Server) SetTLSConfig(tlsCfg *tls.Config) {
	s.smtp.TLSConfig = tlsCfg
}

// Serve wraps ln in a LimitListener enforcing total and per-IP connection limits
// and serves SMTP until Shutdown or Close is called.
func (s *Server) Serve(ln net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = ln.Close()
		return nil
	}
	limited := NewLimitListener(ln, s.cfg.SMTPMaxConnections, s.cfg.SMTPMaxConnectionsPerIP)
	s.listeners = append(s.listeners, limited)
	s.mu.Unlock()

	err := s.smtp.Serve(limited)
	if err != nil && !errors.Is(err, smtp.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("smtpd: serve: %w", err)
	}
	return nil
}

// Shutdown gracefully stops accepting new connections and waits for active SMTP
// transactions until ctx expires.
func (s *Server) Shutdown(ctx context.Context) error {
	s.closeListeners()
	if err := s.smtp.Shutdown(ctx); err != nil {
		return fmt.Errorf("smtpd: shutdown: %w", err)
	}
	return nil
}

// Close immediately closes listeners and active connections.
func (s *Server) Close() error {
	s.closeListeners()
	if err := s.smtp.Close(); err != nil {
		return fmt.Errorf("smtpd: close: %w", err)
	}
	return nil
}

func (s *Server) closeListeners() {
	s.mu.Lock()
	s.closed = true
	listeners := append([]*LimitListener(nil), s.listeners...)
	s.listeners = nil
	s.mu.Unlock()

	for _, l := range listeners {
		_ = l.Close()
	}
}

type backend struct {
	cfg    config.Config
	lookup ProjectLookup
	ingest Ingestor
	log    *slog.Logger
}

func (b *backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	remoteIP := ""
	if addr := c.Conn().RemoteAddr(); addr != nil {
		if ip, ok := extractRemoteIP(addr); ok {
			remoteIP = ip.String()
		} else {
			remoteIP = addr.String()
		}
	}
	_, isTLS := c.TLSConnectionState()
	return &session{
		be:       b,
		conn:     c,
		remoteIP: remoteIP,
		helo:     c.Hostname(),
		tls:      isTLS,
	}, nil
}

type session struct {
	be       *backend
	conn     *smtp.Conn
	remoteIP string
	helo     string
	tls      bool

	auth     AuthResult
	mailFrom string
	rcpts    []string
	targets  []ingest.Target
}

var _ smtp.AuthSession = (*session)(nil)

func (s *session) AuthMechanisms() []string {
	return []string{sasl.Plain, LoginMechanism}
}

func (s *session) Auth(mech string) (sasl.Server, error) {
	switch strings.ToUpper(mech) {
	case sasl.Plain:
		return sasl.NewPlainServer(func(identity, username, password string) error {
			user := username
			if user == "" {
				user = identity
			}
			return s.authenticate(user, password)
		}), nil
	case LoginMechanism:
		return NewLoginServer(func(username, password string) error {
			return s.authenticate(username, password)
		}), nil
	default:
		// Bare *smtp.SMTPError (504 5.7.4).
		return nil, smtp.ErrAuthUnknownMechanism
	}
}

func (s *session) authenticate(username, _ string) error {
	if s.be.cfg.Mode == config.ModeLocal {
		s.auth = AuthResult{
			Authenticated: true,
			Username:      username,
		}
		return nil
	}
	// Server mode credential authentication is implemented in Phase 8.
	return smtp.ErrAuthFailed
}

func (s *session) Mail(from string, opts *smtp.MailOptions) error {
	if opts != nil && s.be.cfg.SMTPMaxMessageBytes > 0 && opts.Size > s.be.cfg.SMTPMaxMessageBytes {
		return smtp.ErrDataTooLarge
	}
	if s.be.cfg.Mode == config.ModeServer && !s.auth.Authenticated {
		// TODO(phase-8): allow unauthenticated MAIL FROM when InboxDomain is set for server-mode recipient routing.
		return errAuthRequired
	}
	s.mailFrom = from
	s.rcpts = nil
	s.targets = nil
	return nil
}

func (s *session) Rcpt(to string, _ *smtp.RcptOptions) error {
	ctx := context.Background()
	target, smtpErr := Resolve(
		ctx,
		s.be.cfg.Mode,
		s.auth,
		to,
		s.be.cfg.InboxDomain,
		s.be.cfg.DefaultProject,
		s.be.lookup,
	)
	if smtpErr != nil {
		if smtpErr.Code == 451 {
			s.be.log.Error("smtp routing lookup failed", "remote_ip", s.remoteIP, "auth_user", s.auth.Username)
		}
		return smtpErr
	}
	s.rcpts = append(s.rcpts, to)
	s.targets = append(s.targets, target)
	return nil
}

func (s *session) Data(r io.Reader) error {
	if len(s.targets) == 0 {
		_, _ = io.Copy(io.Discard, r)
		return errNoValidRecipients
	}

	// Refresh TLS and HELO state in case STARTTLS ran after NewSession.
	if s.conn != nil {
		if _, isTLS := s.conn.TLSConnectionState(); isTLS {
			s.tls = true
		}
		if h := s.conn.Hostname(); h != "" {
			s.helo = h
		}
	}

	env := ingest.Envelope{
		MailFrom: s.mailFrom,
		RcptTo:   append([]string(nil), s.rcpts...),
		RemoteIP: s.remoteIP,
		HELO:     s.helo,
		TLS:      s.tls,
		AuthUser: s.auth.Username,
	}

	refs, err := s.be.ingest.Ingest(context.Background(), env, s.targets, r)
	if err != nil {
		smtpErr := mapDataError(err)
		if smtpErr.Code == 451 {
			s.be.log.Error(
				"smtp ingest failed",
				"remote_ip", s.remoteIP,
				"auth_user", s.auth.Username,
				"rcpt_count", len(s.rcpts),
				"result_code", smtpErr.Code,
				"error", err,
			)
		} else {
			s.be.log.Warn(
				"smtp message rejected",
				"remote_ip", s.remoteIP,
				"auth_user", s.auth.Username,
				"rcpt_count", len(s.rcpts),
				"result_code", smtpErr.Code,
			)
		}
		return smtpErr
	}

	msgIDs := make([]string, 0, len(refs))
	for _, ref := range refs {
		msgIDs = append(msgIDs, ref.ID.String())
	}
	s.be.log.Info(
		"smtp message accepted",
		"remote_ip", s.remoteIP,
		"auth_user", s.auth.Username,
		"rcpt_count", len(s.rcpts),
		"result_code", 250,
		"message_ids", msgIDs,
	)
	return nil
}

func (s *session) Reset() {
	s.mailFrom = ""
	s.rcpts = nil
	s.targets = nil
}

func (s *session) Logout() error {
	return nil
}

// mapDataError always returns a bare *smtp.SMTPError per ARCHITECTURE 3.3.
func mapDataError(err error) *smtp.SMTPError {
	if errors.Is(err, smtp.ErrDataTooLarge) {
		return smtp.ErrDataTooLarge
	}
	if errors.Is(err, ErrQuotaExceeded) {
		return errQuotaExceeded
	}
	if errors.Is(err, ErrRateLimited) {
		return errRateLimited
	}
	var smtpErr *smtp.SMTPError
	if errors.As(err, &smtpErr) {
		return smtpErr
	}
	return errTempFailure
}

type storeProjectLookup struct {
	store *store.Store
	clk   clock.Clock
}

func (l *storeProjectLookup) EnsureProject(ctx context.Context, slug, name string) (store.Project, error) {
	proj, _, err := l.store.EnsureProject(ctx, store.CreateProjectParams{
		Slug:     slug,
		Name:     name,
		Color:    defaultProjectColor,
		Settings: store.DefaultProjectSettings(),
		Now:      l.clk.Now().UTC(),
	})
	return proj, err
}

func (l *storeProjectLookup) GetProjectBySlug(ctx context.Context, slug string) (store.Project, error) {
	return l.store.GetProjectBySlug(ctx, slug)
}

type slogSMTPLogger struct {
	log *slog.Logger
}

func (l slogSMTPLogger) Printf(format string, v ...any) {
	l.log.Warn("go-smtp internal", "detail", fmt.Sprintf(format, v...))
}

func (l slogSMTPLogger) Println(v ...any) {
	l.log.Warn("go-smtp internal", "detail", fmt.Sprint(v...))
}
