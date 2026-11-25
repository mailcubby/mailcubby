// Package app wires all Mailcubby subsystems (store, blob, events, ingest,
// smtpd, HTTP) and manages their startup and graceful shutdown lifecycle.
package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/mailcubby/mailcubby/internal/api"
	"github.com/mailcubby/mailcubby/internal/auth"
	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/ratelimit"
	"github.com/mailcubby/mailcubby/internal/render"
	"github.com/mailcubby/mailcubby/internal/retention"
	"github.com/mailcubby/mailcubby/internal/secrets"
	"github.com/mailcubby/mailcubby/internal/smtpd"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/tlsutil"
	"github.com/mailcubby/mailcubby/internal/web"
)

const (
	shutdownTimeout      = 30 * time.Second
	defaultColor         = "slate"
	settingSetupHashKey  = "setup_token_hash"
	settingSetupDoneKey  = "setup_completed_at"
	defaultBootstrapName = "Admin"
)

// ErrImplicitLocalMode is returned by Open when MAILCUBBY_MODE is not set (so
// it defaults to local) but the data directory has users, i.e. it belongs to a
// server-mode instance (SPEC 5).
var ErrImplicitLocalMode = errors.New("the data directory has users (it was used in server mode) but MAILCUBBY_MODE is not set; " +
	"set MAILCUBBY_MODE=server to keep login and project isolation, or MAILCUBBY_MODE=local to open every project without login on purpose")

// Options customizes dependencies for App construction (defaults are production-ready).
type Options struct {
	Clock       clock.Clock
	Logger      *slog.Logger
	Version     string
	HTTPHandler http.Handler
}

// App holds all initialized Mailcubby components and bound listeners.
type App struct {
	cfg          config.Config
	clk          clock.Clock
	log          *slog.Logger
	version      string
	store        *store.Store
	blobs        *blob.Store
	bus          *events.Bus
	ingestor     *ingest.Ingestor
	sweeper      *retention.Sweeper
	orphanGC     *retention.OrphanGC
	housekeeping *retention.Housekeeping
	tlsReloader  *tlsutil.Reloader
	apiSrv       *api.Server
	smtpSrv      *smtpd.Server
	httpSrv      *http.Server
	cancelHTTP   context.CancelFunc
	httpActive   sync.WaitGroup
	smtpLn       net.Listener
	smtpsLn      net.Listener
	httpLn       net.Listener

	listenersReady atomic.Bool
	closeOnce      sync.Once
}

// Open initializes storage, setup/bootstrap state, event bus, ingestor, SMTP
// server, HTTP server, and binds both listeners without starting to serve yet.
func Open(ctx context.Context, cfg config.Config, opts Options) (*App, error) {
	clk := opts.Clock
	if clk == nil {
		clk = clock.NewReal()
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	ver := opts.Version
	if ver == "" {
		ver = "dev"
	}

	var (
		tlsReloader   *tlsutil.Reloader
		smtpTLSConfig *tls.Config
	)
	if cfg.SMTPTLSCert != "" && cfg.SMTPTLSKey != "" {
		reloader, err := tlsutil.NewReloader(cfg.SMTPTLSCert, cfg.SMTPTLSKey, tlsutil.DefaultPollInterval, clk, log)
		if err != nil {
			return nil, fmt.Errorf("app: load SMTP TLS certificate: %w", err)
		}
		tlsReloader = reloader
		smtpTLSConfig = reloader.TLSConfig()
	}

	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, store.DBFilename))
	if err != nil {
		return nil, fmt.Errorf("app: open store: %w", err)
	}

	if cfg.Mode == config.ModeLocal {
		// SPEC 5: a data dir with users is a server-mode data dir. Opening it in
		// local mode exposes every project without login, so that needs an
		// explicit MAILCUBBY_MODE=local. Checked before app.key is generated so a
		// refused start leaves nothing behind.
		userCount, err := st.CountUsers(ctx)
		if err != nil {
			_ = st.Close()
			return nil, fmt.Errorf("app: check user count: %w", err)
		}
		if userCount > 0 {
			if !cfg.ModeExplicit {
				_ = st.Close()
				return nil, fmt.Errorf("app: %w", ErrImplicitLocalMode)
			}
			log.Warn("switching from server mode to local mode: all projects are visible without login", "user_count", userCount)
		}
		log.Warn("running in local mode without authentication; do not expose to the internet")
		if len(cfg.AppKey) == 0 {
			key, err := secrets.LoadOrCreateLocalKey(cfg.DataDir)
			if err != nil {
				_ = st.Close()
				return nil, fmt.Errorf("app: init local app key: %w", err)
			}
			cfg.AppKey = key
		}
	}

	blobs, err := blob.NewStore(cfg.DataDir, cfg.ZstdLevel)
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("app: init blob store: %w", err)
	}
	if err := blobs.CleanTmp(); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("app: clean tmp dir: %w", err)
	}

	switch cfg.Mode {
	case config.ModeLocal:
		if _, _, err := st.EnsureProject(ctx, store.CreateProjectParams{
			Slug:     cfg.DefaultProject,
			Name:     cfg.DefaultProject,
			Color:    defaultColor,
			Settings: store.DefaultProjectSettings(),
			Now:      clk.Now().UTC(),
		}); err != nil {
			_ = st.Close()
			return nil, fmt.Errorf("app: ensure default project %q: %w", cfg.DefaultProject, err)
		}
	case config.ModeServer:
		if err := initServerSetup(ctx, cfg, st, auth.NewHasher(), clk, log); err != nil {
			_ = st.Close()
			return nil, err
		}
	}

	bus := events.NewBus()
	ing := ingest.New(st, blobs, clk, bus, log)
	sweeper := retention.NewSweeper(st, blobs, bus, clk, log, cfg)
	ing.SetRetentionNudge(sweeper.MaybeNudge)
	orphanGC := retention.NewOrphanGC(st, blobs, clk, log)
	housekeeping := retention.NewHousekeeping(st, clk, log, cfg)

	smtpRateLimiter := ratelimit.NewSlidingWindow(cfg.SMTPRateLimit.Count, cfg.SMTPRateLimit.Window, clk)
	ing.SetQuotaCheck(buildIngestQuotaCheck(cfg, st, smtpRateLimiter))

	smtpSrv := smtpd.New(cfg, st, ing, clk, log)
	smtpSrv.SetRateLimiter(smtpRateLimiter)
	if smtpTLSConfig != nil {
		smtpSrv.SetTLSConfig(smtpTLSConfig)
	}

	var lc net.ListenConfig
	smtpLn, err := lc.Listen(ctx, "tcp", cfg.SMTPAddr)
	if err != nil {
		bus.Close()
		_ = st.Close()
		return nil, fmt.Errorf("app: listen smtp %s: %w", cfg.SMTPAddr, err)
	}

	// FR-063: in local mode the announced SMTP port (meta, quick copy, snippets)
	// is the port actually bound from MAILCUBBY_SMTP_ADDR, unless
	// MAILCUBBY_SMTP_PUBLIC_PORT overrides it (e.g. a Docker port mapping).
	if cfg.Mode == config.ModeLocal && !cfg.SMTPPublicPortExplicit {
		if tcpAddr, ok := smtpLn.Addr().(*net.TCPAddr); ok {
			cfg.SMTPPublicPort = tcpAddr.Port
		}
	}

	var smtpsLn net.Listener
	if cfg.SMTPSAddr != "" {
		if smtpTLSConfig == nil {
			_ = smtpLn.Close()
			bus.Close()
			_ = st.Close()
			return nil, errors.New("app: MAILCUBBY_SMTPS_ADDR requires MAILCUBBY_SMTP_TLS_CERT and MAILCUBBY_SMTP_TLS_KEY")
		}
		smtpsLn, err = lc.Listen(ctx, "tcp", cfg.SMTPSAddr)
		if err != nil {
			_ = smtpLn.Close()
			bus.Close()
			_ = st.Close()
			return nil, fmt.Errorf("app: listen smtps %s: %w", cfg.SMTPSAddr, err)
		}
	}

	httpLn, err := lc.Listen(ctx, "tcp", cfg.HTTPAddr)
	if err != nil {
		if smtpsLn != nil {
			_ = smtpsLn.Close()
		}
		_ = smtpLn.Close()
		bus.Close()
		_ = st.Close()
		return nil, fmt.Errorf("app: listen http %s: %w", cfg.HTTPAddr, err)
	}

	a := &App{
		cfg:          cfg,
		clk:          clk,
		log:          log,
		version:      ver,
		store:        st,
		blobs:        blobs,
		bus:          bus,
		ingestor:     ing,
		sweeper:      sweeper,
		orphanGC:     orphanGC,
		housekeeping: housekeeping,
		tlsReloader:  tlsReloader,
		smtpSrv:      smtpSrv,
		smtpLn:       smtpLn,
		smtpsLn:      smtpsLn,
		httpLn:       httpLn,
	}
	a.listenersReady.Store(true)

	handler := opts.HTTPHandler
	if handler == nil {
		renderer := render.New(blobs)
		a.apiSrv = api.New(cfg, st, blobs, bus, api.Options{
			Version:        ver,
			Clock:          clk,
			Logger:         log,
			Ingestor:       ing,
			Renderer:       renderer,
			WebHandler:     web.Handler(),
			ListenersReady: a.listenersReady.Load,
		})
		handler = a.apiSrv.Handler()
	}
	httpBaseCtx, cancelHTTP := context.WithCancel(context.Background())
	a.cancelHTTP = cancelHTTP
	trackedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.httpActive.Add(1)
		defer a.httpActive.Done()
		handler.ServeHTTP(w, r)
	})
	a.httpSrv = api.NewHTTPServer(trackedHandler)
	a.httpSrv.BaseContext = func(net.Listener) context.Context {
		return httpBaseCtx
	}

	return a, nil
}

// Run opens and serves Mailcubby until ctx is cancelled or a listener fails.
func Run(ctx context.Context, cfg config.Config, opts Options) error {
	a, err := Open(ctx, cfg, opts)
	if err != nil {
		return err
	}
	return a.Serve(ctx)
}

// Serve runs the SMTP and HTTP servers plus background workers under errgroup
// and performs graceful shutdown in the order defined by ARCHITECTURE 3.1 when
// ctx is cancelled: stop SMTP/HTTP -> wait for in-flight -> stop workers -> close bus/store.
func (a *App) Serve(ctx context.Context) error {
	workersCtx, cancelWorkers := context.WithCancel(context.Background())
	var workersWG sync.WaitGroup
	if a.sweeper != nil {
		workersWG.Add(1)
		go func() {
			defer workersWG.Done()
			a.sweeper.Run(workersCtx)
		}()
	}
	if a.orphanGC != nil {
		workersWG.Add(1)
		go func() {
			defer workersWG.Done()
			a.orphanGC.Run(workersCtx)
		}()
	}
	if a.housekeeping != nil {
		workersWG.Add(1)
		go func() {
			defer workersWG.Done()
			a.housekeeping.Run(workersCtx)
		}()
	}
	if a.tlsReloader != nil {
		workersWG.Add(1)
		go func() {
			defer workersWG.Done()
			a.tlsReloader.Run(workersCtx)
		}()
	}

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		a.log.Info("smtp listening", "smtp_addr", a.smtpLn.Addr().String())
		if err := a.smtpSrv.Serve(a.smtpLn); err != nil {
			return fmt.Errorf("app: smtp server: %w", err)
		}
		return nil
	})

	if a.smtpsLn != nil {
		g.Go(func() error {
			a.log.Info("smtps listening", "smtps_addr", a.smtpsLn.Addr().String())
			if err := a.smtpSrv.ServeTLS(a.smtpsLn); err != nil {
				return fmt.Errorf("app: smtps server: %w", err)
			}
			return nil
		})
	}

	g.Go(func() error {
		a.log.Info("http listening", "http_addr", a.httpLn.Addr().String(), "version", a.version)
		if err := a.httpSrv.Serve(a.httpLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("app: http server: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		<-gctx.Done()
		a.listenersReady.Store(false)
		a.log.Info("shutting down")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		// Signal long-lived SSE and wait handlers to exit before http.Server.Shutdown (ARCHITECTURE 3.10).
		if a.apiSrv != nil {
			a.apiSrv.BeginShutdown()
		}

		// 1. Stop accepting SMTP and HTTP connections and wait for in-flight work.
		var sg errgroup.Group
		sg.Go(func() error {
			return a.smtpSrv.Shutdown(shutdownCtx)
		})
		sg.Go(func() error {
			err := a.httpSrv.Shutdown(shutdownCtx)
			if err != nil {
				// Force-close lingering connections and cancel in-flight handler contexts
				// so handlers finish before we close the SQLite store.
				a.cancelHTTP()
				_ = a.httpSrv.Close()
				return fmt.Errorf("app: shutdown http: %w", err)
			}
			return nil
		})
		waitErr := sg.Wait()
		a.httpActive.Wait()
		return waitErr
	})

	serveErr := g.Wait()

	// 2. Stop background workers cleanly before closing the event bus and SQLite store.
	cancelWorkers()
	workersWG.Wait()

	// 3. Close event bus and SQLite store.
	closeErr := a.Close()
	return errors.Join(serveErr, closeErr)
}

// Close releases listeners, closes the event bus, and closes the SQLite store.
// It is idempotent and safe to call if Serve was never started.
func (a *App) Close() error {
	var err error
	a.closeOnce.Do(func() {
		a.listenersReady.Store(false)
		var errs []error
		if a.smtpLn != nil {
			if cerr := a.smtpLn.Close(); cerr != nil && !errors.Is(cerr, net.ErrClosed) {
				errs = append(errs, cerr)
			}
		}
		if a.smtpsLn != nil {
			if cerr := a.smtpsLn.Close(); cerr != nil && !errors.Is(cerr, net.ErrClosed) {
				errs = append(errs, cerr)
			}
		}
		if a.httpLn != nil {
			if cerr := a.httpLn.Close(); cerr != nil && !errors.Is(cerr, net.ErrClosed) {
				errs = append(errs, cerr)
			}
		}
		if a.cancelHTTP != nil {
			a.cancelHTTP()
		}
		if a.apiSrv != nil {
			a.apiSrv.Close()
		}
		if a.bus != nil {
			a.bus.Close()
		}
		if a.store != nil {
			if cerr := a.store.Close(); cerr != nil {
				errs = append(errs, cerr)
			}
		}
		err = errors.Join(errs...)
	})
	return err
}

// SMTPAddr returns the bound TCP address of the SMTP listener.
func (a *App) SMTPAddr() string {
	return a.smtpLn.Addr().String()
}

// SMTPSAddr returns the bound TCP address of the implicit TLS SMTP listener,
// or an empty string if implicit TLS is not enabled.
func (a *App) SMTPSAddr() string {
	if a.smtpsLn == nil {
		return ""
	}
	return a.smtpsLn.Addr().String()
}

// HTTPAddr returns the bound TCP address of the HTTP listener.
func (a *App) HTTPAddr() string {
	return a.httpLn.Addr().String()
}

// Store returns the initialized SQLite Store.
func (a *App) Store() *store.Store {
	return a.store
}

// Blobs returns the initialized blob Store.
func (a *App) Blobs() *blob.Store {
	return a.blobs
}

// Bus returns the in-process event bus.
func (a *App) Bus() *events.Bus {
	return a.bus
}

// Ingestor returns the message ingestor.
func (a *App) Ingestor() *ingest.Ingestor {
	return a.ingestor
}

// Sweeper returns the retention sweeper worker.
func (a *App) Sweeper() *retention.Sweeper {
	return a.sweeper
}

// OrphanGC returns the orphan blob GC and incremental vacuum worker.
func (a *App) OrphanGC() *retention.OrphanGC {
	return a.orphanGC
}

// Housekeeping returns the daily housekeeping worker.
func (a *App) Housekeeping() *retention.Housekeeping {
	return a.housekeeping
}

// Config returns the resolved application configuration.
func (a *App) Config() config.Config {
	return a.cfg
}

// initServerSetup implements SPEC FR-040: headless bootstrap when
// MAILCUBBY_ADMIN_EMAIL/PASSWORD are set and no users exist, or fresh random
// setup token generation on every startup while setup is pending.
func initServerSetup(
	ctx context.Context,
	cfg config.Config,
	st *store.Store,
	hasher *auth.Hasher,
	clk clock.Clock,
	log *slog.Logger,
) error {
	userCount, err := st.CountUsers(ctx)
	if err != nil {
		return fmt.Errorf("app: check user count: %w", err)
	}
	if userCount > 0 {
		return nil
	}

	now := clk.Now().UTC()
	if cfg.AdminEmail != "" && cfg.AdminPassword != "" {
		email, err := auth.ValidateEmail(cfg.AdminEmail)
		if err != nil {
			return fmt.Errorf("app: invalid MAILCUBBY_ADMIN_EMAIL: %w", err)
		}
		passwordHash, err := hasher.Hash(ctx, cfg.AdminPassword)
		if err != nil {
			return fmt.Errorf("app: hash MAILCUBBY_ADMIN_PASSWORD: %w", err)
		}

		completedJSON, err := json.Marshal(now.Format(time.RFC3339Nano))
		if err != nil {
			return fmt.Errorf("app: marshal setup timestamp: %w", err)
		}

		var created bool
		if err := st.WithTx(ctx, func(tx *store.Tx) error {
			n, err := tx.CountUsers(ctx)
			if err != nil {
				return err
			}
			if n > 0 {
				return nil
			}
			user, err := tx.CreateUser(ctx, store.CreateUserParams{
				Email:             email,
				Name:              defaultBootstrapName,
				PasswordHash:      &passwordHash,
				IsAdmin:           true,
				CanCreateProjects: true,
				Now:               now,
			})
			if err != nil {
				return err
			}
			if err := tx.SetSetting(ctx, settingSetupDoneKey, string(completedJSON), now); err != nil {
				return err
			}
			if err := tx.DeleteSetting(ctx, settingSetupHashKey); err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			targetType := "user"
			targetID := strconv.FormatInt(user.ID, 10)
			if _, err := tx.InsertAudit(ctx, store.InsertAuditParams{
				At:         now,
				ActorType:  store.AuditActorSystem,
				ActorLabel: &email,
				Action:     "setup.complete",
				TargetType: &targetType,
				TargetID:   &targetID,
				Metadata:   map[string]any{"method": "env_bootstrap"},
			}); err != nil {
				return err
			}
			created = true
			return nil
		}); err != nil {
			return fmt.Errorf("app: bootstrap admin user: %w", err)
		}
		if created {
			log.Info("bootstrapped initial admin user from environment", "email", email)
		}
		return nil
	}

	token, err := auth.GenerateToken()
	if err != nil {
		return fmt.Errorf("app: generate setup token: %w", err)
	}
	hashJSON, err := json.Marshal(auth.HashToken(token))
	if err != nil {
		return fmt.Errorf("app: marshal setup token hash: %w", err)
	}
	if err := st.SetSetting(ctx, settingSetupHashKey, string(hashJSON), now); err != nil {
		return fmt.Errorf("app: store setup token hash: %w", err)
	}

	log.Warn("setup token: "+token, "setup_url", cfg.BaseURL+"/setup")
	return nil
}

// buildIngestQuotaCheck constructs the step 2 ingest hook enforcing
// MAILCUBBY_INSTANCE_MAX_BYTES (FR-096) and MAILCUBBY_SMTP_RATE_LIMIT (FR-097).
func buildIngestQuotaCheck(
	cfg config.Config,
	st *store.Store,
	rateLimiter *ratelimit.SlidingWindow,
) func(ctx context.Context, sizeBytes int64, targets []ingest.Target) error {
	return func(ctx context.Context, sizeBytes int64, targets []ingest.Target) error {
		if cfg.InstanceMaxBytes > 0 {
			totals, err := st.GetInstanceProjectTotals(ctx)
			if err != nil {
				return fmt.Errorf("app: check instance quota: %w", err)
			}
			added := sizeBytes * int64(len(targets))
			if totals.TotalBytes >= cfg.InstanceMaxBytes || totals.TotalBytes+added > cfg.InstanceMaxBytes {
				return ingest.ErrQuotaExceeded
			}
		}

		if rateLimiter != nil && cfg.SMTPRateLimit.Count > 0 {
			type reservedSlot struct {
				key string
				at  time.Time
			}
			reserved := make([]reservedSlot, 0, len(targets))
			for _, t := range targets {
				key := strconv.FormatInt(t.ProjectID, 10)
				at, allowed, _ := rateLimiter.Reserve(key)
				if !allowed {
					for _, r := range reserved {
						rateLimiter.Cancel(r.key, r.at)
					}
					return ingest.ErrRateLimited
				}
				reserved = append(reserved, reservedSlot{key: key, at: at})
			}
		}
		return nil
	}
}
