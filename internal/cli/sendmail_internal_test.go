package cli

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestIsLoopbackSMTPHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		addr string
		want bool
	}{
		{"localhost:1025", true},
		{"LOCALHOST:587", true},
		{"127.0.0.1:1025", true},
		{"127.0.1.1:1025", true},
		{"[::1]:1025", true},
		{"mail.example.com:587", false},
		{"10.0.0.5:587", false},
		{"192.168.1.10:1025", false},
	}

	for _, tc := range cases {
		if got := isLoopbackSMTPHost(tc.addr); got != tc.want {
			t.Errorf("isLoopbackSMTPHost(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

func TestParseSendmailAllowInsecureAuthFlag(t *testing.T) {
	t.Parallel()

	opts, err := parseSendmailArgs([]string{"--smtp", "mail.example.test:587", "--user", "u", "--password", "p", "--allow-insecure-auth", "a@b.test"}, defaultSendmailSMTP)
	if err != nil {
		t.Fatalf("parseSendmailArgs: %v", err)
	}
	if !opts.allowInsecureAuth {
		t.Fatalf("expected allowInsecureAuth=true")
	}
}

func TestSubmitSMTP_TimesOutOnHangingServer(t *testing.T) {
	t.Parallel()

	// FR-115: sendmail must fail promptly when the SMTP server accepts TCP
	// connections but never sends a banner or stalls mid-session.
	cases := []struct {
		name       string
		sendBanner bool
	}{
		{name: "hangs before greeting", sendBanner: false},
		{name: "hangs after greeting", sendBanner: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var lc net.ListenConfig
			ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			release := make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, aerr := ln.Accept()
				if aerr != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				if tc.sendBanner {
					_, _ = io.WriteString(conn, "220 hanging.test ESMTP\r\n")
				}
				<-release
			}()
			t.Cleanup(func() {
				close(release)
				_ = ln.Close()
				<-done
			})

			start := time.Now()
			var stderr bytes.Buffer
			err = submitSMTP(
				t.Context(),
				ln.Addr().String(),
				nil,
				false,
				"sender@example.test",
				[]string{"rcpt@example.test"},
				[]byte("Subject: hi\r\n\r\nbody\r\n"),
				&stderr,
				50*time.Millisecond,
				100*time.Millisecond,
			)
			elapsed := time.Since(start)
			if err == nil {
				t.Fatalf("expected timeout error from hanging SMTP server, got nil")
			}
			var netErr net.Error
			if !errors.As(err, &netErr) || !netErr.Timeout() {
				t.Fatalf("expected timeout net.Error, got: %v", err)
			}
			if elapsed > 2*time.Second {
				t.Fatalf("submitSMTP took %v, expected prompt timeout", elapsed)
			}
		})
	}
}
