package smtpd

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// temporaryAcceptError mimics EMFILE: a net.Error whose Temporary() is true.
type temporaryAcceptError struct{}

func (temporaryAcceptError) Error() string   { return "accept: too many open files" }
func (temporaryAcceptError) Timeout() bool   { return false }
func (temporaryAcceptError) Temporary() bool { return true }

// flakyListener returns one temporary error from its first Accept without
// consuming a connection, then delegates to the wrapped listener.
type flakyListener struct {
	net.Listener
	failed atomic.Bool
}

func (f *flakyListener) Accept() (net.Conn, error) {
	if f.failed.CompareAndSwap(false, true) {
		return nil, temporaryAcceptError{}
	}
	return f.Listener.Accept()
}

func TestServeSurvivesTemporaryAcceptError(t *testing.T) {
	t.Parallel()

	// ARCHITECTURE 3.3 / FR-005: a temporary accept error (EMFILE spike) must not
	// stop the SMTP listener; later clients still receive the 220 greeting.
	var lc net.ListenConfig
	rawLn, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ln := &flakyListener{Listener: rawLn}

	srv := NewWithLookup(defaultLocalConfig(t), newFakeLookup(), stubIngestor{}, nil)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		if serveErr := <-done; serveErr != nil {
			t.Errorf("srv.Serve returned error: %v", serveErr)
		}
	})

	var dialer net.Dialer
	conn, err := dialer.DialContext(context.Background(), "tcp", rawLn.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read greeting after temporary accept error: %v", err)
	}
	if !strings.HasPrefix(line, "220 ") {
		t.Fatalf("greeting = %q, want 220", line)
	}
	if !ln.failed.Load() {
		t.Fatal("flaky listener never returned its temporary error")
	}
}

func TestLimitListenerPerIPAndTotalLimits(t *testing.T) {
	t.Parallel()

	// FR-005: 21st concurrent connection from one IP gets 421 4.7.0 and closes.
	var lc net.ListenConfig
	rawLn, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	const maxPerIP = 20
	ll := NewLimitListener(rawLn, 200, maxPerIP)
	defer func() { _ = ll.Close() }()

	// Accept admitted connections in the background and hold them open.
	accepted := make(chan net.Conn, maxPerIP)
	go func() {
		for {
			c, err := ll.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()

	var dialer net.Dialer
	clients := make([]net.Conn, 0, maxPerIP)
	serverConns := make([]net.Conn, 0, maxPerIP)
	defer func() {
		for _, c := range clients {
			_ = c.Close()
		}
		for _, c := range serverConns {
			_ = c.Close()
		}
	}()

	for i := range maxPerIP {
		c, err := dialer.DialContext(context.Background(), "tcp", ll.Addr().String())
		if err != nil {
			t.Fatalf("dial %d: %v", i+1, err)
		}
		clients = append(clients, c)
		select {
		case sc := <-accepted:
			serverConns = append(serverConns, sc)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for admitted conn %d", i+1)
		}
	}

	// 21st connection from 127.0.0.1 must receive 421 4.7.0 Too many connections.
	extra, err := dialer.DialContext(context.Background(), "tcp", ll.Addr().String())
	if err != nil {
		t.Fatalf("dial 21st: %v", err)
	}
	defer func() { _ = extra.Close() }()
	_ = extra.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(extra).ReadString('\n')
	if err != nil {
		t.Fatalf("read 421 line: %v", err)
	}
	if !strings.HasPrefix(line, "421 4.7.0") {
		t.Fatalf("21st conn reply = %q, want prefix '421 4.7.0'", line)
	}

	// Closing one admitted connection frees a slot so a new connection is admitted.
	_ = serverConns[0].Close()
	_ = clients[0].Close()

	// Wait briefly for release to propagate (Close calls onClose synchronously).
	nextClient, err := dialer.DialContext(context.Background(), "tcp", ll.Addr().String())
	if err != nil {
		t.Fatalf("dial after release: %v", err)
	}
	defer func() { _ = nextClient.Close() }()
	select {
	case sc := <-accepted:
		_ = sc.Close()
	case <-time.After(2 * time.Second):
		t.Fatalf("expected new connection to be admitted after closing one")
	}
}
