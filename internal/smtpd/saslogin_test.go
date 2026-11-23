package smtpd

import (
	"errors"
	"testing"
)

func TestLoginServer_NoInitialResponse(t *testing.T) {
	t.Parallel()

	// FR-003: AUTH LOGIN without initial response prompts Username: then Password:.
	var gotUser, gotPass string
	srv := NewLoginServer(func(username, password string) error {
		gotUser = username
		gotPass = password
		return nil
	})

	ch, done, err := srv.Next(nil)
	if err != nil || done || string(ch) != "Username:" {
		t.Fatalf("step 0: ch=%q done=%v err=%v", ch, done, err)
	}
	ch, done, err = srv.Next([]byte("Shop Staging"))
	if err != nil || done || string(ch) != "Password:" {
		t.Fatalf("step 1: ch=%q done=%v err=%v", ch, done, err)
	}
	ch, done, err = srv.Next([]byte("secret"))
	if err != nil || !done || len(ch) != 0 {
		t.Fatalf("step 2: ch=%q done=%v err=%v", ch, done, err)
	}
	if gotUser != "Shop Staging" || gotPass != "secret" {
		t.Fatalf("got (%q, %q), want (Shop Staging, secret)", gotUser, gotPass)
	}
}

func TestLoginServer_WithInitialResponse(t *testing.T) {
	t.Parallel()

	// FR-003: AUTH LOGIN <base64 username> skips Username: challenge.
	var gotUser, gotPass string
	srv := NewLoginServer(func(username, password string) error {
		gotUser = username
		gotPass = password
		return nil
	})

	ch, done, err := srv.Next([]byte("alice"))
	if err != nil || done || string(ch) != "Password:" {
		t.Fatalf("step 0 with IR: ch=%q done=%v err=%v", ch, done, err)
	}
	ch, done, err = srv.Next([]byte("pw"))
	if err != nil || !done || len(ch) != 0 {
		t.Fatalf("step 2: ch=%q done=%v err=%v", ch, done, err)
	}
	if gotUser != "alice" || gotPass != "pw" {
		t.Fatalf("got (%q, %q), want (alice, pw)", gotUser, gotPass)
	}
}

func TestLoginServer_AuthErrorPropagatedVerbatim(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("auth failed")
	srv := NewLoginServer(func(_, _ string) error {
		return wantErr
	})
	_, _, _ = srv.Next([]byte("u"))
	_, done, err := srv.Next([]byte("p"))
	if !done || !errors.Is(err, wantErr) {
		t.Fatalf("got done=%v err=%v, want done=true err=%v", done, err, wantErr)
	}
}
