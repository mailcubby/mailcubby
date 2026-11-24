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
	"github.com/mailcubby/mailcubby/internal/render"
	"github.com/mailcubby/mailcubby/internal/secrets"
	"github.com/mailcubby/mailcubby/internal/smtpd"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/web"
)

const (
	shutdownTimeout      = 30 * time.Second
	defaultColor         = "slate"
	settingSetupHashKey  = "setup_token_hash"
	settingSetupDoneKey  = "setup_completed_at"
	defaultBootstrapName = "Admin"
)

// Options customizes dependencies for App construction (defaults are production-ready).
type Options struct {
	Clock       clock.Clock
	Logger      *slog.Logger
	Version     string
	HTTPHandler http.Handler
}

// App holds all initialized Mailcubby components and bound listeners.
type App struct {
	cfg      config.Config
	clk      clock.Clock
	log      *slog.Logger
	version  string
	store    *store.Store
	blobs    *blob.Store
	bus      *events.Bus
	ingestor *ingest.Ingestor
	apiSrv   *api.Server
	smtpSrv  *smtpd.Server
	httpSrv  *http.Server
	smtpLn   net.Listener
	httpLn   net.Listener

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

	var smtpTLSConfig *tls.Config
	if cfg.SMTPTLSCert != "" && cfg.SMTPTLSKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.SMTPTLSCert, cfg.SMTPTLSKey)
		if err != nil {
			return nil, fmt.Errorf(
				"app: load SMTP TLS certificate (MAILCUBBY_SMTP_TLS_CERT=%q, MAILCUBBY_SMTP_TLS_KEY=%q): %w",
				cfg.SMTPTLSCert,
				cfg.SMTPTLSKey,
				err,
			)
		}
		// TODO(phase-12): replace static startup cert with internal/tlsutil.Reloader.
		smtpTLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				return &cert, nil
			},
		}
	}

	if cfg.Mode == config.ModeLocal {
		log.Warn("running in local mode without authentication; do not expose to the internet")
		if len(cfg.AppKey) == 0 {
			key, err := secrets.LoadOrCreateLocalKey(cfg.DataDir)
			if err != nil {
				return nil, fmt.Errorf("app: init local app key: %w", err)
			}
			cfg.AppKey = key
		}
	}

	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, store.DBFilename))
	if err != nil {
		return nil, fmt.Errorf("app: open store: %w", err)
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
		if userCount, err := st.CountUsers(ctx); err == nil && userCount > 0 {
			log.Warn("switching from server mode to local mode: all projects are visible without login", "user_count", userCount)
		}
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
	smtpSrv := smtpd.New(cfg, st, ing, clk, log)
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

	httpLn, err := lc.Listen(ctx, "tcp", cfg.HTTPAddr)
	if err != nil {
		_ = smtpLn.Close()
		bus.Close()
		_ = st.Close()
		return nil, fmt.Errorf("app: listen http %s: %w", cfg.HTTPAddr, err)
	}

	a := &App{
		cfg:      cfg,
		clk:      clk,
		log:      log,
		version:  ver,
		store:    st,
		blobs:    blobs,
		bus:      bus,
		ingestor: ing,
		smtpSrv:  smtpSrv,
		smtpLn:   smtpLn,
		httpLn:   httpLn,
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
	a.httpSrv = api.NewHTTPServer(handler)

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

// Serve runs the SMTP and HTTP servers under errgroup and performs graceful
// shutdown in the order defined by ARCHITECTURE 3.1 when ctx is cancelled.
func (a *App) Serve(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		a.log.Info("smtp listening", "smtp_addr", a.smtpLn.Addr().String())
		if err := a.smtpSrv.Serve(a.smtpLn); err != nil {
			return fmt.Errorf("app: smtp server: %w", err)
		}
		return nil
	})

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
			if err := a.httpSrv.Shutdown(shutdownCtx); err != nil {
				return fmt.Errorf("app: shutdown http: %w", err)
			}
			return nil
		})
		return sg.Wait()
	})

	serveErr := g.Wait()
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
		if a.httpLn != nil {
			if cerr := a.httpLn.Close(); cerr != nil && !errors.Is(cerr, net.ErrClosed) {
				errs = append(errs, cerr)
			}
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
