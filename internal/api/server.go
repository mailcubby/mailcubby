// Package api implements the Mailcubby HTTP/REST API server, middleware chain,
// route table, OpenAPI specification, and health/readiness endpoints.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/store"
)

const (
	httpReadHeaderTimeout = 10 * time.Second
	httpReadTimeout       = 60 * time.Second
	httpWriteTimeout      = 60 * time.Second
	httpIdleTimeout       = 120 * time.Second
	httpMaxHeaderBytes    = 64 << 10
	sparklineCacheTTL     = 30 * time.Second
)

// Options configures optional dependencies for constructing an API Server.
type Options struct {
	Version        string
	Clock          clock.Clock
	Logger         *slog.Logger
	WebHandler     http.Handler
	ListenersReady func() bool
}

// Server serves the Mailcubby REST API, health endpoints, and embedded SPA.
type Server struct {
	cfg            config.Config
	store          *store.Store
	blobs          *blob.Store
	bus            *events.Bus
	clk            clock.Clock
	log            *slog.Logger
	version        string
	webHandler     http.Handler
	listenersReady func() bool

	routes  []Route
	handler http.Handler

	sparkMu    sync.Mutex
	sparkCache map[int64]sparklineEntry
	unsubSpark func()
	sparkDone  chan struct{}
	closeOnce  sync.Once
}

type sparklineEntry struct {
	startHour int64
	expiresAt time.Time
	values    [24]int64
}

// New constructs a new API Server wired to st, blobs, and bus.
// Call Close when shutting down to stop the sparkline cache invalidation listener.
func New(cfg config.Config, st *store.Store, blobs *blob.Store, bus *events.Bus, opts Options) *Server {
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

	s := &Server{
		cfg:            cfg,
		store:          st,
		blobs:          blobs,
		bus:            bus,
		clk:            clk,
		log:            log,
		version:        ver,
		webHandler:     opts.WebHandler,
		listenersReady: opts.ListenersReady,
		sparkCache:     make(map[int64]sparklineEntry),
	}
	s.routes = s.buildRoutes()
	s.handler = s.withMiddleware(s.buildMux())

	if bus != nil {
		sub, unsub := bus.Subscribe(func(e events.Event) bool {
			return e.Type == events.TypeMessageCreated ||
				e.Type == events.TypeMessageDeleted ||
				e.Type == events.TypeProjectDeleted
		})
		s.unsubSpark = unsub
		s.sparkDone = make(chan struct{})
		// Goroutine exits when sub.Events() is closed via Close() or bus.Close().
		go func() {
			defer close(s.sparkDone)
			for ev := range sub.Events() {
				s.invalidateSparkline(ev.ProjectID)
			}
		}()
	}

	return s
}

// NewHTTPServer builds an *http.Server configured with the timeouts and header
// limits specified in ARCHITECTURE 3.10.
func NewHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
		MaxHeaderBytes:    httpMaxHeaderBytes,
	}
}

// Handler returns the root http.Handler wrapped in the middleware chain.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// Routes returns a copy of the registered API route table.
func (s *Server) Routes() []Route {
	out := make([]Route, len(s.routes))
	copy(out, s.routes)
	return out
}

// Close releases background subscriptions owned by Server. It is safe to call multiple times.
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		if s.unsubSpark != nil {
			s.unsubSpark()
		}
		if s.sparkDone != nil {
			<-s.sparkDone
		}
	})
}

func (s *Server) getSparkline(ctx context.Context, projectID int64) ([24]int64, error) {
	now := s.clk.Now().UTC()
	startHour := (now.UnixMilli() / 3600000) - 23

	s.sparkMu.Lock()
	if entry, ok := s.sparkCache[projectID]; ok && entry.startHour == startHour && now.Before(entry.expiresAt) {
		s.sparkMu.Unlock()
		return entry.values, nil
	}
	s.sparkMu.Unlock()

	values, err := s.store.ProjectSparkline(ctx, projectID, now)
	if err != nil {
		return [24]int64{}, err
	}

	s.sparkMu.Lock()
	s.sparkCache[projectID] = sparklineEntry{
		startHour: startHour,
		expiresAt: now.Add(sparklineCacheTTL),
		values:    values,
	}
	s.sparkMu.Unlock()
	return values, nil
}

func (s *Server) invalidateSparkline(projectID int64) {
	s.sparkMu.Lock()
	delete(s.sparkCache, projectID)
	s.sparkMu.Unlock()
}
