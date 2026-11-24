// Package smtpd wraps github.com/emersion/go-smtp to implement Mailcubby's
// SMTP listener, SASL PLAIN/LOGIN authentication, routing, and connection limits.
package smtpd

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/ratelimit"
	"github.com/mailcubby/mailcubby/internal/secrets"
	"github.com/mailcubby/mailcubby/internal/store"
)

const (
	smtpMaxLineLength     = 2000
	authFailureDelay      = time.Second
	lastUsedTouchInterval = time.Minute
)

// Ingestor is the consumer-declared interface used by SMTP sessions on DATA.
type Ingestor interface {
	Ingest(ctx context.Context, env ingest.Envelope, targets []ingest.Target, r io.Reader) ([]ingest.MessageRef, error)
}

// CredentialStore is the consumer-declared interface used by server-mode SMTP AUTH
// to verify credentials, throttle last_used_at updates, and audit lockouts.
type CredentialStore interface {
	LookupSMTPCredentialByUsername(ctx context.Context, username string) (store.SMTPCredentialAuthLookup, error)
	TouchSMTPCredentialLastUsed(ctx context.Context, projectID, id int64, now time.Time) error
	InsertAudit(ctx context.Context, p store.InsertAuditParams) (store.AuditEntry, error)
}

// ErrQuotaExceeded is returned by ingest hooks when instance storage quota is hit (Phase 10).
var ErrQuotaExceeded = errors.New("smtpd: instance storage quota exceeded")

// ErrRateLimited is returned by ingest hooks when a project exceeds its SMTP rate limit (Phase 10).
var ErrRateLimited = errors.New("smtpd: project rate limit exceeded")

// Server runs Mailcubby's SMTP listener with connection limits and graceful shutdown.
type Server struct {
	cfg        config.Config
	smtp       *smtp.Server
	be         *backend
	log        *slog.Logger
	lookup     ProjectLookup
	shutdownCh chan struct{}
	shutOnce   sync.Once

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
	var credStore CredentialStore
	if st != nil {
		credStore = st
	}
	var cipher *secrets.Cipher
	if len(cfg.AppKey) == secrets.KeySize {
		c, err := secrets.NewSMTPCredentialCipher(cfg.AppKey)
		if err != nil {
			log.Error("failed to initialize smtp credential cipher", "error", err)
		} else {
			cipher = c
		}
	}
	return newServer(cfg, &storeProjectLookup{store: st, clk: clk}, credStore, cipher, ing, clk, log)
}

// NewWithLookup constructs an SMTP Server with a custom ProjectLookup (used in tests).
func NewWithLookup(cfg config.Config, lookup ProjectLookup, ing Ingestor, log *slog.Logger) *Server {
	return newServer(cfg, lookup, nil, nil, ing, clock.NewReal(), log)
}

func newServer(
	cfg config.Config,
	lookup ProjectLookup,
	creds CredentialStore,
	cipher *secrets.Cipher,
	ing Ingestor,
	clk clock.Clock,
	log *slog.Logger,
) *Server {
	if clk == nil {
		clk = clock.NewReal()
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	shutdownCh := make(chan struct{})
	be := &backend{
		cfg:              cfg,
		lookup:           lookup,
		creds:            creds,
		cipher:           cipher,
		ingest:           ing,
		clk:              clk,
		log:              log,
		authLimiter:      ratelimit.NewSMTPAuthLimiter(cfg.SMTPAuthMaxFailures, cfg.SMTPAuthLockout, clk),
		authFailureDelay: authFailureDelay,
		shutdownCh:       shutdownCh,
		lastTouched:      make(map[int64]time.Time),
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
		cfg:        cfg,
		smtp:       s,
		be:         be,
		log:        log,
		lookup:     lookup,
		shutdownCh: shutdownCh,
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

// Shutdown stops accepting new connections, closes idle connections at once
// and waits until ctx expires for in-flight DATA transactions, then closes the
// rest (ARCHITECTURE 3.1). It returns only after every session has ended, so
// callers may close the store afterwards.
func (s *Server) Shutdown(ctx context.Context) error {
	s.signalShutdown()
	s.be.beginDrain()
	listeners := s.closeListeners()
	for _, l := range listeners {
		l.closeIdle()
	}
	err := s.smtp.Shutdown(ctx)
	if err != nil {
		// go-smtp's Close is a no-op after Shutdown, so force-close through
		// our own connection tracking.
		for _, l := range listeners {
			l.closeAll()
		}
	}
	// Every connection is closed by now, so remaining sessions end promptly.
	s.be.active.Wait()
	if err != nil {
		return fmt.Errorf("smtpd: shutdown: %w", err)
	}
	return nil
}

// Close immediately closes listeners and active connections.
func (s *Server) Close() error {
	s.signalShutdown()
	s.closeListeners()
	if err := s.smtp.Close(); err != nil {
		return fmt.Errorf("smtpd: close: %w", err)
	}
	return nil
}

func (s *Server) signalShutdown() {
	s.shutOnce.Do(func() {
		close(s.shutdownCh)
	})
}

func (s *Server) closeListeners() []*LimitListener {
	s.mu.Lock()
	s.closed = true
	listeners := append([]*LimitListener(nil), s.listeners...)
	s.listeners = nil
	s.mu.Unlock()

	for _, l := range listeners {
		_ = l.Close()
	}
	return listeners
}

type backend struct {
	cfg              config.Config
	lookup           ProjectLookup
	creds            CredentialStore
	cipher           *secrets.Cipher
	ingest           Ingestor
	clk              clock.Clock
	log              *slog.Logger
	authLimiter      *ratelimit.SMTPAuthLimiter
	authFailureDelay time.Duration
	shutdownCh       <-chan struct{}

	touchMu     sync.Mutex
	lastTouched map[int64]time.Time

	// active counts live sessions and running Data calls so Shutdown can wait
	// for them before the store is closed; draining refuses new ones.
	activeMu sync.Mutex
	draining bool
	active   sync.WaitGroup
}

// beginActivity registers a session or Data call, or returns false once
// shutdown has started.
func (b *backend) beginActivity() bool {
	b.activeMu.Lock()
	defer b.activeMu.Unlock()
	if b.draining {
		return false
	}
	b.active.Add(1)
	return true
}

func (b *backend) beginDrain() {
	b.activeMu.Lock()
	b.draining = true
	b.activeMu.Unlock()
}

func (b *backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	if !b.beginActivity() {
		return nil, errTempFailure
	}
	remoteIP := ""
	if addr := c.Conn().RemoteAddr(); addr != nil {
		if ip, ok := extractRemoteIP(addr); ok {
			remoteIP = ip.String()
		} else {
			remoteIP = addr.String()
		}
	}
	_, isTLS := c.TLSConnectionState()
	sess := &session{
		be:       b,
		conn:     c,
		remoteIP: remoteIP,
		helo:     c.Hostname(),
		tls:      isTLS,
	}
	sess.active.Store(true)
	return sess, nil
}

func (b *backend) shouldTouchLastUsed(credID int64, dbLastUsed *time.Time, now time.Time) bool {
	if dbLastUsed != nil && now.Sub(*dbLastUsed) < lastUsedTouchInterval {
		return false
	}
	b.touchMu.Lock()
	defer b.touchMu.Unlock()
	if b.lastTouched == nil {
		b.lastTouched = make(map[int64]time.Time)
	}
	if prev, ok := b.lastTouched[credID]; ok && now.Sub(prev) < lastUsedTouchInterval {
		return false
	}
	b.lastTouched[credID] = now
	return true
}

type session struct {
	be       *backend
	conn     *smtp.Conn
	remoteIP string
	helo     string
	tls      bool
	// active is true while the session is registered in be.active. Atomic
	// because go-smtp may call Logout from Server.Close and STARTTLS concurrently.
	active atomic.Bool

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

func (s *session) authenticate(username, password string) error {
	if s.be.cfg.Mode == config.ModeLocal {
		s.auth = AuthResult{
			Authenticated: true,
			Username:      username,
		}
		return nil
	}

	// SPEC 12.4: check brute-force lockout before any credential lookup.
	if s.be.authLimiter != nil && s.be.authLimiter.IsLocked(s.remoteIP, username) {
		return errAuthLocked
	}

	if s.be.creds == nil || s.be.cipher == nil {
		return s.failAuth(username, nil)
	}

	ctx := context.Background()
	lookup, err := s.be.creds.LookupSMTPCredentialByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return s.failAuth(username, nil)
		}
		if s.be.log != nil {
			s.be.log.Error("smtp credential lookup failed",
				"remote_ip", s.remoteIP,
				"auth_user", username,
				"error", err,
			)
		}
		return errTempFailure
	}

	projID := lookup.Credential.ProjectID
	if lookup.Credential.DisabledAt != nil {
		return s.failAuth(username, &projID)
	}

	plain, err := s.be.cipher.Open(lookup.Credential.PasswordEnc, []byte(lookup.Credential.Username))
	if err != nil {
		if s.be.log != nil {
			s.be.log.Error("smtp credential decrypt failed",
				"remote_ip", s.remoteIP,
				"project_id", lookup.Credential.ProjectID,
				"credential_id", lookup.Credential.ID,
				"error", err,
			)
		}
		return s.failAuth(username, &projID)
	}

	if subtle.ConstantTimeCompare(plain, []byte(password)) != 1 {
		return s.failAuth(username, &projID)
	}

	credID := lookup.Credential.ID
	s.auth = AuthResult{
		Authenticated:   true,
		Username:        lookup.Credential.Username,
		ProjectID:       lookup.Credential.ProjectID,
		ProjectSlug:     lookup.ProjectSlug,
		CredentialID:    &credID,
		CredentialLabel: lookup.Credential.Label,
		PlusAddressTags: lookup.ProjectPlusAddressTags,
	}

	// FR-062: update last_used_at at most once per minute.
	if s.be.clk != nil {
		now := s.be.clk.Now().UTC()
		if s.be.shouldTouchLastUsed(credID, lookup.Credential.LastUsedAt, now) {
			if touchErr := s.be.creds.TouchSMTPCredentialLastUsed(ctx, lookup.Credential.ProjectID, credID, now); touchErr != nil && s.be.log != nil {
				s.be.log.Warn("failed to update smtp credential last_used_at",
					"project_id", lookup.Credential.ProjectID,
					"credential_id", credID,
					"error", touchErr,
				)
			}
		}
	}

	return nil
}

// failAuth records a failed AUTH attempt, emits a one-time smtp.auth_lockout
// audit entry if the lockout threshold is crossed, waits 1 s through the
// injected clock, and returns bare errAuthFailed (535 5.7.8) (SPEC 7.4, 12.4).
func (s *session) failAuth(username string, projectID *int64) *smtp.SMTPError {
	if s.be.authLimiter != nil {
		if newlyLocked, reason := s.be.authLimiter.RecordFailure(s.remoteIP, username); newlyLocked {
			s.auditAuthLockout(username, projectID, reason)
		}
	}
	s.waitAuthFailureDelay()
	return errAuthFailed
}

func (s *session) auditAuthLockout(username string, projectID *int64, reason string) {
	if s.be.creds == nil {
		return
	}
	now := time.Now().UTC()
	if s.be.clk != nil {
		now = s.be.clk.Now().UTC()
	}
	targetType := "smtp_auth"
	targetID := username
	if targetID == "" {
		targetID = s.remoteIP
	}
	var ipPtr *string
	if s.remoteIP != "" {
		ip := s.remoteIP
		ipPtr = &ip
	}
	var actorLabel *string
	if username != "" {
		u := username
		actorLabel = &u
	}
	if _, err := s.be.creds.InsertAudit(context.Background(), store.InsertAuditParams{
		At:         now,
		ActorType:  store.AuditActorSMTP,
		ActorLabel: actorLabel,
		ProjectID:  projectID,
		Action:     "smtp.auth_lockout",
		TargetType: &targetType,
		TargetID:   &targetID,
		IP:         ipPtr,
		Metadata: map[string]any{
			"reason":    reason,
			"username":  username,
			"remote_ip": s.remoteIP,
		},
	}); err != nil && s.be.log != nil {
		s.be.log.Warn("failed to write smtp.auth_lockout audit entry",
			"remote_ip", s.remoteIP,
			"auth_user", username,
			"error", err,
		)
	}
}

func (s *session) waitAuthFailureDelay() {
	if s.be.clk == nil || s.be.authFailureDelay <= 0 {
		return
	}
	timer := s.be.clk.NewTimer(s.be.authFailureDelay)
	defer timer.Stop()

	if s.be.shutdownCh != nil {
		select {
		case <-timer.C():
		case <-s.be.shutdownCh:
		}
		return
	}
	<-timer.C()
}

func (s *session) Mail(from string, opts *smtp.MailOptions) error {
	if opts != nil && s.be.cfg.SMTPMaxMessageBytes > 0 && opts.Size > s.be.cfg.SMTPMaxMessageBytes {
		return smtp.ErrDataTooLarge
	}
	// SPEC 7.2 rule 4: server mode without AUTH and without INBOX_DOMAIN rejects MAIL FROM with 530 5.7.0.
	if s.be.cfg.Mode == config.ModeServer && !s.auth.Authenticated && strings.TrimSpace(s.be.cfg.InboxDomain) == "" {
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
		if smtpErr.Code == 451 && s.be.log != nil {
			s.be.log.Error("smtp routing lookup failed", "remote_ip", s.remoteIP, "auth_user", s.auth.Username)
		}
		return smtpErr
	}
	s.rcpts = append(s.rcpts, to)
	s.targets = append(s.targets, target)
	return nil
}

func (s *session) Data(r io.Reader) error {
	if !s.be.beginActivity() {
		return errTempFailure
	}
	defer s.be.active.Done()
	// Cleared in Reset, which go-smtp calls after writing the DATA reply.
	if tc := trackedFromSMTP(s.conn); tc != nil {
		tc.beginTransaction()
	}

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
		if s.be.log != nil {
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
		}
		return smtpErr
	}

	if s.be.log != nil {
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
	}
	return nil
}

func (s *session) Reset() {
	s.mailFrom = ""
	s.rcpts = nil
	s.targets = nil
	if tc := trackedFromSMTP(s.conn); tc != nil {
		tc.endTransaction()
	}
}

func (s *session) Logout() error {
	if s.active.CompareAndSwap(true, false) {
		s.be.active.Done()
	}
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
