package smtpd

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/ingest"
)

// gatedIngestor signals started when Ingest is entered, optionally waits for
// release after reading the body, and closes returned when Ingest returns.
type gatedIngestor struct {
	started  chan struct{}
	release  chan struct{} // nil: return as soon as the body is read
	returned chan struct{}
}

func newGatedIngestor(gate bool) *gatedIngestor {
	g := &gatedIngestor{started: make(chan struct{}), returned: make(chan struct{})}
	if gate {
		g.release = make(chan struct{})
	}
	return g
}

func (g *gatedIngestor) Ingest(_ context.Context, _ ingest.Envelope, _ []ingest.Target, r io.Reader) ([]ingest.MessageRef, error) {
	defer close(g.returned)
	close(g.started)
	if _, err := io.Copy(io.Discard, r); err != nil {
		return nil, err
	}
	if g.release != nil {
		<-g.release
	}
	return nil, nil
}

type shutdownTestServer struct {
	srv       *Server
	addr      string
	serveDone chan error
}

func startShutdownTestServer(t *testing.T, ing Ingestor, tlsCfg *tls.Config) *shutdownTestServer {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ts := &shutdownTestServer{
		srv:       NewWithLookup(defaultLocalConfig(t), newFakeLookup(), ing, nil),
		addr:      ln.Addr().String(),
		serveDone: make(chan error, 1),
	}
	if tlsCfg != nil {
		ts.srv.SetTLSConfig(tlsCfg)
	}
	go func() { ts.serveDone <- ts.srv.Serve(ln) }()
	return ts
}

// shutdownAsync runs Shutdown in the background and returns its result channel.
func (ts *shutdownTestServer) shutdownAsync(timeout time.Duration) <-chan error {
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		done <- ts.srv.Shutdown(ctx)
	}()
	return done
}

type smtpTestClient struct {
	conn net.Conn
	r    *bufio.Reader
}

func dialSMTPTestClient(t *testing.T, addr string) *smtpTestClient {
	t.Helper()
	var dialer net.Dialer
	conn, err := dialer.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	c := &smtpTestClient{conn: conn, r: bufio.NewReader(conn)}
	c.expect(t, "220")
	return c
}

func (c *smtpTestClient) send(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(c.conn, line+"\r\n"); err != nil {
		t.Fatalf("write %q: %v", line, err)
	}
}

// expect reads one (possibly multi-line) reply and checks its code.
func (c *smtpTestClient) expect(t *testing.T, code string) {
	t.Helper()
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			t.Fatalf("read reply (want %s): %v", code, err)
		}
		if !strings.HasPrefix(line, code) {
			t.Fatalf("reply = %q, want %s", line, code)
		}
		if len(line) > 3 && line[3] == ' ' {
			return
		}
	}
}

// startTLS upgrades the connection with STARTTLS and greets again.
func (c *smtpTestClient) startTLS(t *testing.T) {
	t.Helper()
	c.send(t, "STARTTLS")
	c.expect(t, "220")
	tlsConn := tls.Client(c.conn, &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, //nolint:gosec // runtime-generated self-signed test cert
	})
	if err := tlsConn.HandshakeContext(t.Context()); err != nil {
		t.Fatalf("TLS handshake: %v", err)
	}
	c.conn = tlsConn
	c.r = bufio.NewReader(tlsConn)
	c.send(t, "EHLO client.example")
	c.expect(t, "250")
}

// expectClosed asserts the server closed the connection (EOF or reset, not a
// read timeout).
func (c *smtpTestClient) expectClosed(t *testing.T) {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := c.r.ReadString('\n')
	if err == nil {
		t.Fatalf("expected closed connection, got %q", line)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatalf("connection still open: %v", err)
	}
}

func waitShutdown(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not return")
		return nil
	}
}

func TestShutdownClosesIdleConnectionsPromptly(t *testing.T) {
	t.Parallel()

	// ARCHITECTURE 3.1: shutdown waits only for in-flight DATA. Idle pooled
	// connections (after EHLO) and connections that never greeted are closed at
	// once instead of holding shutdown for the whole grace period.
	ts := startShutdownTestServer(t, stubIngestor{}, nil)
	idle := dialSMTPTestClient(t, ts.addr)
	idle.send(t, "EHLO client.example")
	idle.expect(t, "250")
	silent := dialSMTPTestClient(t, ts.addr)

	// The grace period is far longer than waitShutdown's bound, so a nil error
	// proves Shutdown did not wait for these connections.
	if err := waitShutdown(t, ts.shutdownAsync(time.Minute)); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	idle.expectClosed(t)
	silent.expectClosed(t)
	if err := <-ts.serveDone; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestShutdownLetsInFlightDataFinish(t *testing.T) {
	t.Parallel()

	// ARCHITECTURE 3.1: an in-flight DATA transaction completes and receives its
	// 250 reply during shutdown; idle connections are closed meanwhile. The
	// STARTTLS case covers the server-mode path, where the tracked connection
	// sits under a tls.Conn.
	for _, starttls := range []bool{false, true} {
		name := "plaintext"
		if starttls {
			name = "starttls"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			testShutdownLetsInFlightDataFinish(t, starttls)
		})
	}
}

func testShutdownLetsInFlightDataFinish(t *testing.T, starttls bool) {
	ing := newGatedIngestor(true)
	var tlsCfg *tls.Config
	if starttls {
		tlsCfg = generateSelfSignedTLSConfig(t)
	}
	ts := startShutdownTestServer(t, ing, tlsCfg)

	busy := dialSMTPTestClient(t, ts.addr)
	busy.send(t, "EHLO client.example")
	busy.expect(t, "250")
	if starttls {
		busy.startTLS(t)
	}
	busy.send(t, "MAIL FROM:<sender@example.com>")
	busy.expect(t, "250")
	busy.send(t, "RCPT TO:<dev@example.com>")
	busy.expect(t, "250")
	busy.send(t, "DATA")
	busy.expect(t, "354")
	busy.send(t, "Subject: hi\r\n\r\nbody\r\n.")
	<-ing.started

	idle := dialSMTPTestClient(t, ts.addr)
	idle.send(t, "EHLO client.example")
	idle.expect(t, "250")

	done := ts.shutdownAsync(time.Minute)
	idle.expectClosed(t)
	select {
	case err := <-done:
		t.Fatalf("Shutdown returned before in-flight DATA finished: %v", err)
	default:
	}

	close(ing.release)
	busy.expect(t, "250")
	busy.expectClosed(t)
	if err := waitShutdown(t, done); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestShutdownForceClosesStalledDataBeforeReturning(t *testing.T) {
	t.Parallel()

	// ARCHITECTURE 3.1: when the grace period expires, in-flight DATA is aborted
	// and Shutdown returns only after the session ended, so the store can be
	// closed afterwards without a session still using it.
	ing := newGatedIngestor(false)
	ts := startShutdownTestServer(t, ing, nil)

	stalled := dialSMTPTestClient(t, ts.addr)
	stalled.send(t, "EHLO client.example")
	stalled.expect(t, "250")
	stalled.send(t, "MAIL FROM:<sender@example.com>")
	stalled.expect(t, "250")
	stalled.send(t, "RCPT TO:<dev@example.com>")
	stalled.expect(t, "250")
	stalled.send(t, "DATA")
	stalled.expect(t, "354")
	stalled.send(t, "Subject: partial") // body never terminated
	<-ing.started

	err := waitShutdown(t, ts.shutdownAsync(50*time.Millisecond))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want context.DeadlineExceeded", err)
	}
	select {
	case <-ing.returned:
	default:
		t.Fatal("Shutdown returned while Ingest was still running")
	}
	stalled.expectClosed(t)
}
