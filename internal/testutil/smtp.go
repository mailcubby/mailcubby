package testutil

import (
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"testing"
)

// SendSMTP sends a raw RFC 5322 message over SMTP without authentication.
func SendSMTP(t testing.TB, addr, from string, to []string, msg []byte) {
	t.Helper()
	if err := TrySendSMTP(addr, nil, from, to, msg); err != nil {
		t.Fatalf("SendSMTP(%s): %v", addr, err)
	}
}

// SendSMTPPlain sends a raw message using SMTP AUTH PLAIN (works over plain loopback).
func SendSMTPPlain(t testing.TB, addr, user, pass, from string, to []string, msg []byte) {
	t.Helper()
	if err := TrySendSMTP(addr, PlainAuth(user, pass), from, to, msg); err != nil {
		t.Fatalf("SendSMTPPlain(%s, user=%q): %v", addr, user, err)
	}
}

// SendSMTPLogin sends a raw message using SMTP AUTH LOGIN (no initial response).
func SendSMTPLogin(t testing.TB, addr, user, pass, from string, to []string, msg []byte) {
	t.Helper()
	if err := TrySendSMTP(addr, LoginAuth(user, pass), from, to, msg); err != nil {
		t.Fatalf("SendSMTPLogin(%s, user=%q): %v", addr, user, err)
	}
}

// TrySendSMTP submits msg to addr using optional auth and returns any SMTP error.
func TrySendSMTP(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	defer func() { _ = c.Close() }()

	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp rcpt to %q: %w", rcpt, err)
		}
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := wc.Write(msg); err != nil {
		_ = wc.Close()
		return fmt.Errorf("smtp data write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp data close: %w", err)
	}
	if err := c.Quit(); err != nil {
		return fmt.Errorf("smtp quit: %w", err)
	}
	return nil
}

// PlainAuth returns an smtp.Auth for AUTH PLAIN that also permits unencrypted
// connections (for local loopback tests where net/smtp.PlainAuth refuses non-TLS
// on non-localhost hostnames).
func PlainAuth(username, password string) smtp.Auth {
	return &plainAuth{username: username, password: password}
}

type plainAuth struct {
	username string
	password string
}

func (a *plainAuth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	resp := []byte("\x00" + a.username + "\x00" + a.password)
	return "PLAIN", resp, nil
}

func (*plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected server challenge for PLAIN")
	}
	return nil, nil
}

// LoginAuth returns an smtp.Auth implementing SMTP AUTH LOGIN.
func LoginAuth(username, password string) smtp.Auth {
	return &loginAuth{username: username, password: password}
}

type loginAuth struct {
	username string
	password string
}

func (*loginAuth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch string(fromServer) {
	case "Username:":
		return []byte(a.username), nil
	case "Password:":
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("unexpected LOGIN challenge %q", string(fromServer))
	}
}

// SplitHostPortOrFail is a convenience wrapper for tests checking addresses.
func SplitHostPortOrFail(t testing.TB, addr string) (string, string) {
	t.Helper()
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}
	return h, p
}
