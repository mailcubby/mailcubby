package smtpd

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

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
