package smtpd

import (
	"context"
	"encoding/base64"
	"net"
	"net/textproto"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/secrets"
)

// smtplibLogin mimics Python smtplib's SMTP.login() against a server that
// advertises "AUTH PLAIN LOGIN": it tries PLAIN and, on any reply other than
// 235/503, falls through to LOGIN on the same connection. It returns the
// final reply code of each AUTH command it sent.
func smtplibLogin(addr, username, password string) ([]int, error) {
	var d net.Dialer
	conn, err := d.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	tp := textproto.NewConn(conn)
	if _, _, err := tp.ReadResponse(220); err != nil {
		return nil, err
	}
	if err := tp.PrintfLine("EHLO client.test"); err != nil {
		return nil, err
	}
	if _, _, err := tp.ReadResponse(250); err != nil {
		return nil, err
	}

	var codes []int
	plain := base64.StdEncoding.EncodeToString([]byte("\x00" + username + "\x00" + password))
	if err := tp.PrintfLine("AUTH PLAIN %s", plain); err != nil {
		return nil, err
	}
	code, _, err := tp.ReadResponse(0)
	if err != nil && code == 0 {
		return nil, err
	}
	codes = append(codes, code)
	if code == 235 || code == 503 {
		return codes, nil
	}

	if err := tp.PrintfLine("AUTH LOGIN %s", base64.StdEncoding.EncodeToString([]byte(username))); err != nil {
		return nil, err
	}
	if _, _, err = tp.ReadResponse(334); err != nil {
		return nil, err
	}
	if err := tp.PrintfLine("%s", base64.StdEncoding.EncodeToString([]byte(password))); err != nil {
		return nil, err
	}
	code, _, err = tp.ReadResponse(0)
	if err != nil && code == 0 {
		return nil, err
	}
	codes = append(codes, code)
	_ = tp.PrintfLine("QUIT")
	return codes, nil
}

// pumpFakeClock runs fn while advancing ts.clk past each 1 s AUTH failure
// delay, and returns fn's result.
func pumpFakeClock[T any](t *testing.T, ts *testSMTPServer, fn func() T) T {
	t.Helper()
	done := make(chan T, 1)
	go func() { done <- fn() }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case v := <-done:
			return v
		default:
		}
		if ts.clk.TimerCount() > 0 {
			ts.clk.Advance(authFailureDelay)
		}
		runtime.Gosched()
	}
	t.Fatal("timed out waiting for SMTP client")
	var zero T
	return zero
}

type loginResult struct {
	codes []int
	err   error
}

func login(t *testing.T, ts *testSMTPServer, username, password string) []int {
	t.Helper()
	r := pumpFakeClock(t, ts, func() loginResult {
		codes, err := smtplibLogin(ts.addr, username, password)
		return loginResult{codes, err}
	})
	if r.err != nil {
		t.Fatalf("login(%s): %v", username, r.err)
	}
	return r.codes
}

// SPEC 12.4: lockout is per (IP, username). A client retrying with a stale
// password through Python smtplib (2 failed AUTH commands per login()) locks
// only itself; another client behind the same IP keeps working.
func TestServerMode_AuthLockoutPerIPAndUsername_Smtplib(t *testing.T) {
	t.Parallel()

	ts := startServerModeSMTP(t, nil) // SMTP_AUTH_MAX_FAILURES=5
	_, credA := seedProjectAndCred(t, ts, "client-a", "staging", "client-a-aaaa1111", "PasswordA123456789012345", false)
	_, credB := seedProjectAndCred(t, ts, "client-b", "staging", "client-b-bbbb2222", "PasswordB123456789012345", false)

	steps := []struct {
		name string
		user string
		pass string
		want []int
	}{
		{name: "A wrong login() 1: PLAIN and LOGIN fail (failures 1-2)", user: credA.Username, pass: "stale", want: []int{535, 535}},
		{name: "A wrong login() 2: failures 3-4", user: credA.Username, pass: "stale", want: []int{535, 535}},
		{name: "A wrong login() 3: PLAIN is failure 5, LOGIN is locked", user: credA.Username, pass: "stale", want: []int{535, 454}},
		{name: "A right password is locked too", user: credA.Username, pass: "PasswordA123456789012345", want: []int{454, 454}},
		{name: "B from the same IP still authenticates", user: credB.Username, pass: "PasswordB123456789012345", want: []int{235}},
	}
	for _, st := range steps {
		if got := login(t, ts, st.user, st.pass); !slices.Equal(got, st.want) {
			t.Fatalf("%s: codes = %v, want %v", st.name, got, st.want)
		}
	}

	// Lockout ends after the window.
	ts.clk.Advance(15*time.Minute + time.Second)
	if got := login(t, ts, credA.Username, "PasswordA123456789012345"); !slices.Equal(got, []int{235}) {
		t.Fatalf("A after window: codes = %v, want [235]", got)
	}
}

// SPEC 12.4: the per-IP ceiling (5x the pair limit) still locks an IP that
// cycles through usernames.
func TestServerMode_AuthLockoutIPCeiling(t *testing.T) {
	t.Parallel()

	ts := startServerModeSMTP(t, func(cfg *config.Config) { cfg.SMTPAuthMaxFailures = 1 }) // IP ceiling 5
	_, cred := seedProjectAndCred(t, ts, "victim", "staging", "victim-cccc3333", "PasswordC123456789012345", false)

	// Five different unknown usernames, one failure each: each pair locks, then the IP.
	for i, user := range []string{"guess-1", "guess-2", "guess-3", "guess-4", "guess-5"} {
		r := pumpFakeClock(t, ts, func() loginResult {
			code, _, err := sendAuthPlainRaw(ts.addr, user, "x")
			return loginResult{[]int{code}, err}
		})
		if r.err != nil || !slices.Equal(r.codes, []int{535}) {
			t.Fatalf("guess %d: codes = %v err = %v, want [535]", i+1, r.codes, r.err)
		}
	}
	code, _, err := sendAuthPlainRaw(ts.addr, cred.Username, "PasswordC123456789012345")
	if err != nil || code != 454 {
		t.Fatalf("valid credential from a ceiling-locked IP = (%d, %v), want 454", code, err)
	}
}

// SPEC 12.4, FR-061: a credential that cannot be decrypted (encrypted under
// another MAILCUBBY_APP_KEY) answers 454 4.7.0 without the failure delay and
// does not count towards lockout, so mail flows as soon as the key is fixed.
func TestServerMode_AuthDecryptFailureIs454AndNotCounted(t *testing.T) {
	t.Parallel()

	ts := startServerModeSMTP(t, nil) // SMTP_AUTH_MAX_FAILURES=5
	const username = "shop-key00001"
	const password = "RightPassword12345678901"
	proj, cred := seedProjectAndCred(t, ts, "shop", "staging", username, password, false)

	otherKey := make([]byte, secrets.KeySize)
	for i := range otherKey {
		otherKey[i] = 0xCD
	}
	otherCipher, err := secrets.NewSMTPCredentialCipher(otherKey)
	if err != nil {
		t.Fatalf("NewSMTPCredentialCipher: %v", err)
	}
	wrongSealed, err := otherCipher.Seal([]byte(password), []byte(username))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := ts.store.UpdateSMTPCredentialPassword(t.Context(), proj.ID, cred.ID, wrongSealed); err != nil {
		t.Fatalf("UpdateSMTPCredentialPassword: %v", err)
	}

	// More attempts than the lockout limit; each is answered at once (no fake-clock delay).
	for i := 1; i <= 7; i++ {
		code, msg, err := sendAuthPlainRaw(ts.addr, username, password)
		if err != nil || code != 454 || !strings.Contains(msg, "4.7.0") {
			t.Fatalf("attempt %d = (%d, %q, %v), want 454 4.7.0", i, code, msg, err)
		}
	}
	if ts.srv.be.authLimiter.IsLocked("127.0.0.1", username) {
		t.Fatal("decrypt failures counted towards lockout")
	}

	// The key is fixed (credential re-encrypted under the server key): AUTH succeeds at once.
	rightSealed, err := ts.cipher.Seal([]byte(password), []byte(username))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := ts.store.UpdateSMTPCredentialPassword(t.Context(), proj.ID, cred.ID, rightSealed); err != nil {
		t.Fatalf("UpdateSMTPCredentialPassword: %v", err)
	}
	if code, msg, err := sendAuthPlainRaw(ts.addr, username, password); err != nil || code != 235 {
		t.Fatalf("AUTH after key fix = (%d, %q, %v), want 235", code, msg, err)
	}
}
