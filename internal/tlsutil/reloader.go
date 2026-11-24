// Package tlsutil provides hot-reloading TLS certificate management for Mailcubby.
package tlsutil

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mailcubby/mailcubby/internal/clock"
)

// DefaultPollInterval is the interval at which Reloader polls certificate files
// for modifications (SPEC section 9.3, ARCHITECTURE section 3.3).
const DefaultPollInterval = 30 * time.Second

// Reloader serves a TLS key pair and reloads it whenever the certificate or key
// file on disk changes (mtime, size, or inode). Reload failures keep serving
// the previously loaded certificate so transient renewals never break TLS.
type Reloader struct {
	certPath string
	keyPath  string
	interval time.Duration
	clk      clock.Clock
	log      *slog.Logger

	cert atomic.Pointer[tls.Certificate]

	mu       sync.Mutex
	certStat os.FileInfo
	keyStat  os.FileInfo
}

// NewReloader loads the initial TLS key pair from certPath and keyPath and
// returns a Reloader ready to serve and poll for updates. If the initial files
// cannot be read or parsed, NewReloader returns an error naming the configured
// environment variables and paths (ARCHITECTURE section 9.3).
func NewReloader(
	certPath string,
	keyPath string,
	interval time.Duration,
	clk clock.Clock,
	log *slog.Logger,
) (*Reloader, error) {
	if certPath == "" || keyPath == "" {
		return nil, errors.New("tlsutil: MAILCUBBY_SMTP_TLS_CERT and MAILCUBBY_SMTP_TLS_KEY must both be set")
	}
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	if clk == nil {
		clk = clock.NewReal()
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	certInfo, err := os.Stat(certPath)
	if err != nil {
		return nil, fmt.Errorf("tlsutil: read MAILCUBBY_SMTP_TLS_CERT %q: %w", certPath, err)
	}
	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		return nil, fmt.Errorf("tlsutil: read MAILCUBBY_SMTP_TLS_KEY %q: %w", keyPath, err)
	}

	pair, err := loadKeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf(
			"tlsutil: load TLS key pair (MAILCUBBY_SMTP_TLS_CERT=%q, MAILCUBBY_SMTP_TLS_KEY=%q): %w",
			certPath,
			keyPath,
			err,
		)
	}

	r := &Reloader{
		certPath: certPath,
		keyPath:  keyPath,
		interval: interval,
		clk:      clk,
		log:      log,
		certStat: certInfo,
		keyStat:  keyInfo,
	}
	r.cert.Store(&pair)
	return r, nil
}

// Get returns the currently loaded TLS certificate for use as
// tls.Config.GetCertificate. It is lock-free and safe for concurrent calls.
func (r *Reloader) Get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	cert := r.cert.Load()
	if cert == nil {
		return nil, errors.New("tlsutil: no TLS certificate loaded")
	}
	return cert, nil
}

// TLSConfig returns a tls.Config enforcing TLS 1.2+ and serving certificates
// dynamically from this Reloader.
func (r *Reloader) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: r.Get,
	}
}

// Poll checks whether either the certificate or key file has changed on disk
// and reloads the key pair if so. It returns true if a new certificate was
// loaded. On transient errors (such as a partial renewal), the previous valid
// certificate remains active and the next poll retries.
func (r *Reloader) Poll() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	certInfo, err := os.Stat(r.certPath)
	if err != nil {
		r.log.Warn("failed to stat TLS certificate; keeping current certificate",
			"cert_path", r.certPath,
			"error", err,
		)
		return false
	}
	keyInfo, err := os.Stat(r.keyPath)
	if err != nil {
		r.log.Warn("failed to stat TLS key; keeping current certificate",
			"key_path", r.keyPath,
			"error", err,
		)
		return false
	}

	if !fileChanged(r.certStat, certInfo) && !fileChanged(r.keyStat, keyInfo) {
		return false
	}

	pair, err := loadKeyPair(r.certPath, r.keyPath)
	if err != nil {
		r.log.Warn("failed to reload TLS certificate; keeping current certificate",
			"cert_path", r.certPath,
			"key_path", r.keyPath,
			"error", err,
		)
		return false
	}

	r.cert.Store(&pair)
	r.certStat = certInfo
	r.keyStat = keyInfo

	if pair.Leaf != nil {
		r.log.Info("reloaded TLS certificate",
			"cert_path", r.certPath,
			"subject", pair.Leaf.Subject.CommonName,
			"serial", pair.Leaf.SerialNumber.String(),
			"not_after", pair.Leaf.NotAfter.UTC().Format(time.RFC3339),
		)
	} else {
		r.log.Info("reloaded TLS certificate", "cert_path", r.certPath)
	}
	return true
}

// Run polls for certificate changes on the configured interval until ctx is
// cancelled.
func (r *Reloader) Run(ctx context.Context) {
	ticker := r.clk.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			r.Poll()
		}
	}
}

func loadKeyPair(certPath, keyPath string) (tls.Certificate, error) {
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, err
	}
	if pair.Leaf == nil && len(pair.Certificate) > 0 {
		if leaf, parseErr := x509.ParseCertificate(pair.Certificate[0]); parseErr == nil {
			pair.Leaf = leaf
		}
	}
	return pair, nil
}

func fileChanged(prev, cur os.FileInfo) bool {
	if prev == nil || cur == nil {
		return true
	}
	return !cur.ModTime().Equal(prev.ModTime()) ||
		cur.Size() != prev.Size() ||
		!os.SameFile(prev, cur)
}
