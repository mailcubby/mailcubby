package tlsutil

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/clock"
)

func generateCertPEM(t *testing.T, commonName string, serial int64) (certPEM, keyPEM []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{commonName, "localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func writeCertFiles(t *testing.T, certPath, keyPath string, certPEM, keyPEM []byte, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(certPath, mtime, mtime); err != nil {
			t.Fatalf("chtimes cert: %v", err)
		}
		if err := os.Chtimes(keyPath, mtime, mtime); err != nil {
			t.Fatalf("chtimes key: %v", err)
		}
	}
}

func TestNewReloaderErrors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")

	t.Run("missing cert names env var and path", func(t *testing.T) {
		t.Parallel()
		_, err := NewReloader(filepath.Join(dir, "missing.crt"), keyPath, DefaultPollInterval, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "MAILCUBBY_SMTP_TLS_CERT") || !strings.Contains(err.Error(), "missing.crt") {
			t.Fatalf("NewReloader err = %v, want error naming MAILCUBBY_SMTP_TLS_CERT and file", err)
		}
	})

	t.Run("missing key names env var and path", func(t *testing.T) {
		t.Parallel()
		c1, _ := generateCertPEM(t, "one.example", 1)
		p := filepath.Join(dir, "only.crt")
		if err := os.WriteFile(p, c1, 0o600); err != nil {
			t.Fatalf("write cert: %v", err)
		}
		_, err := NewReloader(p, filepath.Join(dir, "missing.key"), DefaultPollInterval, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "MAILCUBBY_SMTP_TLS_KEY") || !strings.Contains(err.Error(), "missing.key") {
			t.Fatalf("NewReloader err = %v, want error naming MAILCUBBY_SMTP_TLS_KEY and file", err)
		}
	})

	t.Run("mismatched key pair fails startup", func(t *testing.T) {
		t.Parallel()
		c1, _ := generateCertPEM(t, "one.example", 1)
		_, k2 := generateCertPEM(t, "two.example", 2)
		writeCertFiles(t, certPath, keyPath, c1, k2, time.Time{})
		_, err := NewReloader(certPath, keyPath, DefaultPollInterval, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "MAILCUBBY_SMTP_TLS_CERT") || !strings.Contains(err.Error(), certPath) {
			t.Fatalf("NewReloader err = %v, want error naming env var and cert path", err)
		}
	})
}

func TestReloaderSwitchesCertAfterFakeClockPoll(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	certPath := filepath.Join(dir, "smtp.crt")
	keyPath := filepath.Join(dir, "smtp.key")

	t0 := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	c1, k1 := generateCertPEM(t, "first.example.test", 101)
	c2, k2 := generateCertPEM(t, "second.example.test", 202)
	writeCertFiles(t, certPath, keyPath, c1, k1, t0)

	clk := clock.NewFake(t0)
	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	reloader, err := NewReloader(certPath, keyPath, DefaultPollInterval, clk, log)
	if err != nil {
		t.Fatalf("NewReloader: %v", err)
	}

	got1, err := reloader.Get(nil)
	if err != nil {
		t.Fatalf("Get initial: %v", err)
	}
	if got1.Leaf == nil || got1.Leaf.SerialNumber.Int64() != 101 || got1.Leaf.Subject.CommonName != "first.example.test" {
		t.Fatalf("initial cert = %+v, want serial 101", got1.Leaf)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		reloader.Run(ctx)
	}()

	// Wait until Run registers its ticker on the fake clock.
	waitForTickerCount(t, clk, 1)

	// Replace cert files on disk with a newer mtime.
	writeCertFiles(t, certPath, keyPath, c2, k2, t0.Add(time.Minute))

	// Before the poll interval elapses, the old certificate is still served.
	clk.Advance(DefaultPollInterval - time.Second)
	stillOld, err := reloader.Get(nil)
	if err != nil || stillOld.Leaf.SerialNumber.Int64() != 101 {
		t.Fatalf("before poll cert = %v (err=%v), want serial 101", stillOld, err)
	}

	// Advancing past the 30s poll interval triggers reload without restart.
	clk.Advance(2 * time.Second)
	waitForCertSerial(t, reloader, 202)

	got2, err := reloader.Get(nil)
	if err != nil {
		t.Fatalf("Get reloaded: %v", err)
	}
	if got2.Leaf.Subject.CommonName != "second.example.test" {
		t.Fatalf("reloaded CN = %q, want second.example.test", got2.Leaf.Subject.CommonName)
	}

	cancel()
	<-done

	// Private key PEM must never appear in logs.
	if strings.Contains(logBuf.String(), "PRIVATE KEY") {
		t.Fatalf("logs unexpectedly contained private key material: %s", logBuf.String())
	}
}

func TestReloaderKeepsPreviousCertOnTransientFailureAndRecovers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	certPath := filepath.Join(dir, "smtp.crt")
	keyPath := filepath.Join(dir, "smtp.key")

	t0 := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	c1, k1 := generateCertPEM(t, "v1.example.test", 1)
	c2, k2 := generateCertPEM(t, "v2.example.test", 2)
	writeCertFiles(t, certPath, keyPath, c1, k1, t0)

	reloader, err := NewReloader(certPath, keyPath, DefaultPollInterval, clock.NewFake(t0), nil)
	if err != nil {
		t.Fatalf("NewReloader: %v", err)
	}

	// Steady state poll does not reload.
	if reloader.Poll() {
		t.Fatal("Poll() = true with unchanged files, want false")
	}

	// Simulate mid-renewal mismatch: cert updated to c2 while key is still k1.
	writeCertFiles(t, certPath, keyPath, c2, k1, t0.Add(time.Minute))
	if reloader.Poll() {
		t.Fatal("Poll() = true for mismatched cert/key, want false")
	}
	cur, err := reloader.Get(nil)
	if err != nil || cur.Leaf.SerialNumber.Int64() != 1 {
		t.Fatalf("after failed reload cert serial = %v (err=%v), want previous serial 1", cur, err)
	}

	// Now finish updating key to k2 (even with the same mtime as the failed poll).
	writeCertFiles(t, certPath, keyPath, c2, k2, t0.Add(time.Minute))
	if !reloader.Poll() {
		t.Fatal("Poll() = false after valid pair restored, want true")
	}
	cur, err = reloader.Get(nil)
	if err != nil || cur.Leaf.SerialNumber.Int64() != 2 {
		t.Fatalf("after recovery cert serial = %v (err=%v), want serial 2", cur, err)
	}

	// Atomic rename replacement (new inode, even with identical mtime).
	c3, k3 := generateCertPEM(t, "v3.example.test", 3)
	tmpCert := filepath.Join(dir, "smtp.crt.new")
	tmpKey := filepath.Join(dir, "smtp.key.new")
	writeCertFiles(t, tmpCert, tmpKey, c3, k3, t0.Add(time.Minute))
	if err := os.Rename(tmpCert, certPath); err != nil {
		t.Fatalf("rename cert: %v", err)
	}
	if err := os.Rename(tmpKey, keyPath); err != nil {
		t.Fatalf("rename key: %v", err)
	}
	if !reloader.Poll() {
		t.Fatal("Poll() = false after atomic rename, want true")
	}
	cur, err = reloader.Get(nil)
	if err != nil || cur.Leaf.SerialNumber.Int64() != 3 {
		t.Fatalf("after atomic rename cert serial = %v (err=%v), want serial 3", cur, err)
	}
}

func TestReloaderConcurrentGetAndReload(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	certPath := filepath.Join(dir, "smtp.crt")
	keyPath := filepath.Join(dir, "smtp.key")

	t0 := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	c1, k1 := generateCertPEM(t, "a.example.test", 10)
	c2, k2 := generateCertPEM(t, "b.example.test", 20)
	writeCertFiles(t, certPath, keyPath, c1, k1, t0)

	reloader, err := NewReloader(certPath, keyPath, DefaultPollInterval, clock.NewFake(t0), nil)
	if err != nil {
		t.Fatalf("NewReloader: %v", err)
	}
	tlsCfg := reloader.TLSConfig()
	if tlsCfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %v, want TLS 1.2", tlsCfg.MinVersion)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					cert, getErr := tlsCfg.GetCertificate(nil)
					if getErr != nil || cert == nil || cert.Leaf == nil {
						t.Errorf("GetCertificate returned (%v, %v)", cert, getErr)
						return
					}
				}
			}
		}()
	}

	for i := 1; i <= 5; i++ {
		if i%2 == 1 {
			writeCertFiles(t, certPath, keyPath, c2, k2, t0.Add(time.Duration(i)*time.Minute))
		} else {
			writeCertFiles(t, certPath, keyPath, c1, k1, t0.Add(time.Duration(i)*time.Minute))
		}
		reloader.Poll()
	}

	close(stop)
	wg.Wait()
}

func waitForTickerCount(t *testing.T, clk *clock.Fake, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if clk.TickerCount() >= want {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("timed out waiting for %d ticker(s), got %d", want, clk.TickerCount())
}

func waitForCertSerial(t *testing.T, r *Reloader, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		cert, err := r.Get(nil)
		if err == nil && cert != nil && cert.Leaf != nil && cert.Leaf.SerialNumber.Int64() == want {
			return
		}
		runtime.Gosched()
	}
	cert, _ := r.Get(nil)
	t.Fatalf("timed out waiting for cert serial %d, got %+v", want, cert)
}
