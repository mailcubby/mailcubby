package smtpd

import (
	"context"
	"errors"
	"testing"

	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/store"
)

type fakeLookup struct {
	projects map[string]store.Project
	nextID   int64
	err      error
}

func newFakeLookup() *fakeLookup {
	return &fakeLookup{
		projects: make(map[string]store.Project),
		nextID:   1,
	}
}

func (f *fakeLookup) EnsureProject(_ context.Context, slug, name string) (store.Project, error) {
	if f.err != nil {
		return store.Project{}, f.err
	}
	if p, ok := f.projects[slug]; ok {
		return p, nil
	}
	p := store.Project{
		ID:       f.nextID,
		Slug:     slug,
		Name:     name,
		Color:    defaultProjectColor,
		Settings: store.DefaultProjectSettings(),
	}
	f.nextID++
	f.projects[slug] = p
	return p, nil
}

func (f *fakeLookup) GetProjectBySlug(_ context.Context, slug string) (store.Project, error) {
	if f.err != nil {
		return store.Project{}, f.err
	}
	p, ok := f.projects[slug]
	if !ok {
		return store.Project{}, store.ErrNotFound
	}
	return p, nil
}

func TestParseRecipientRoute(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		rcpt        string
		inboxDomain string
		wantSlug    string
		wantTag     string
		wantRouted  bool
	}{
		{name: "no inbox domain", rcpt: "orders+urgent@shop.inbox.test", inboxDomain: "", wantSlug: "", wantTag: "urgent", wantRouted: false},
		{name: "subdomain form", rcpt: "orders+urgent@Shop.Inbox.Test", inboxDomain: "inbox.test", wantSlug: "shop", wantTag: "urgent", wantRouted: true},
		{name: "local-part form", rcpt: "shop@inbox.test", inboxDomain: "inbox.test", wantSlug: "shop", wantTag: "", wantRouted: true},
		{name: "local-part with plus tag", rcpt: "Shop+invoice@INBOX.TEST", inboxDomain: "inbox.test", wantSlug: "shop", wantTag: "invoice", wantRouted: true},
		{name: "nested subdomain does not route", rcpt: "a@b.c.inbox.test", inboxDomain: "inbox.test", wantSlug: "", wantTag: "", wantRouted: false},
		{name: "reserved slug does not route", rcpt: "api@inbox.test", inboxDomain: "inbox.test", wantSlug: "", wantTag: "", wantRouted: false},
		{name: "unrelated domain", rcpt: "user+tag@other.test", inboxDomain: "inbox.test", wantSlug: "", wantTag: "tag", wantRouted: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			slug, tag, routed := ParseRecipientRoute(tc.rcpt, tc.inboxDomain)
			if slug != tc.wantSlug || tag != tc.wantTag || routed != tc.wantRouted {
				t.Fatalf("ParseRecipientRoute(%q, %q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.rcpt, tc.inboxDomain, slug, tag, routed, tc.wantSlug, tc.wantTag, tc.wantRouted)
			}
		})
	}
}

func TestResolveLocalMode(t *testing.T) {
	t.Parallel()

	// FR-020: SPEC 7.2 local mode routing column.
	cases := []struct {
		name        string
		auth        AuthResult
		rcpt        string
		inboxDomain string
		wantSlug    string
		wantName    string
		wantTag     string
	}{
		{
			name:        "rule 1: AUTH overrides recipient routing",
			auth:        AuthResult{Authenticated: true, Username: "Shop Staging"},
			rcpt:        "other+vip@inbox.test",
			inboxDomain: "inbox.test",
			wantSlug:    "shop-staging",
			wantName:    "Shop Staging",
			wantTag:     "vip",
		},
		{
			name:        "rule 2: subdomain routing without AUTH",
			rcpt:        "orders+urgent@acme.inbox.test",
			inboxDomain: "inbox.test",
			wantSlug:    "acme",
			wantName:    "acme",
			wantTag:     "urgent",
		},
		{
			name:        "rule 2: local-part routing without AUTH",
			rcpt:        "billing+paid@inbox.test",
			inboxDomain: "inbox.test",
			wantSlug:    "billing",
			wantName:    "billing",
			wantTag:     "paid",
		},
		{
			name:        "rule 3: non-routed recipient goes to default",
			rcpt:        "alice+qa@external.test",
			inboxDomain: "inbox.test",
			wantSlug:    "default",
			wantName:    "default",
			wantTag:     "qa",
		},
		{
			name:        "rule 4: no AUTH and empty inbox domain goes to default",
			rcpt:        "bob@example.test",
			inboxDomain: "",
			wantSlug:    "default",
			wantName:    "default",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lookup := newFakeLookup()
			target, smtpErr := Resolve(context.Background(), config.ModeLocal, tc.auth, tc.rcpt, tc.inboxDomain, "default", lookup)
			if smtpErr != nil {
				t.Fatalf("Resolve unexpected error: %v", smtpErr)
			}
			if target.ProjectSlug != tc.wantSlug {
				t.Fatalf("ProjectSlug = %q, want %q", target.ProjectSlug, tc.wantSlug)
			}
			if lookup.projects[tc.wantSlug].Name != tc.wantName {
				t.Fatalf("auto-created project Name = %q, want %q", lookup.projects[tc.wantSlug].Name, tc.wantName)
			}
			if tc.wantTag != "" {
				if len(target.PlusTags) != 1 || target.PlusTags[0] != tc.wantTag {
					t.Fatalf("PlusTags = %v, want [%q]", target.PlusTags, tc.wantTag)
				}
			}
		})
	}

	t.Run("lookup error maps to 451 4.3.0", func(t *testing.T) {
		t.Parallel()
		lookup := newFakeLookup()
		lookup.err = errors.New("db down")
		_, smtpErr := Resolve(context.Background(), config.ModeLocal, AuthResult{}, "a@b.test", "", "default", lookup)
		if smtpErr == nil || smtpErr.Code != 451 || smtpErr.EnhancedCode != [3]int{4, 3, 0} {
			t.Fatalf("got %v, want 451 4.3.0", smtpErr)
		}
	})
}

func TestResolveServerMode(t *testing.T) {
	t.Parallel()

	// FR-020: SPEC 7.2 server mode routing column.
	credID := int64(42)
	setupLookup := func() *fakeLookup {
		fl := newFakeLookup()
		sEnabled := store.DefaultProjectSettings()
		sEnabled.RecipientRoutingEnabled = true
		sDisabled := store.DefaultProjectSettings()
		sDisabled.RecipientRoutingEnabled = false
		fl.projects["routed"] = store.Project{ID: 10, Slug: "routed", Name: "Routed", Settings: sEnabled}
		fl.projects["closed"] = store.Project{ID: 11, Slug: "closed", Name: "Closed", Settings: sDisabled}
		return fl
	}

	cases := []struct {
		name        string
		auth        AuthResult
		rcpt        string
		inboxDomain string
		wantSlug    string
		wantCredID  *int64
		wantTag     string
		wantCode    int
		wantEnh     [3]int
		wantMsg     string
	}{
		{
			name: "rule 1: AUTH overrides recipient routing completely",
			auth: AuthResult{
				Authenticated:   true,
				Username:        "authproj-abcd1234",
				ProjectID:       99,
				ProjectSlug:     "authproj",
				CredentialID:    &credID,
				CredentialLabel: "staging",
				PlusAddressTags: true,
			},
			rcpt:        "routed+vip@inbox.test",
			inboxDomain: "inbox.test",
			wantSlug:    "authproj",
			wantCredID:  &credID,
			wantTag:     "vip",
		},
		{
			name:        "rule 2: subdomain routing accepted when recipient_routing_enabled=true",
			rcpt:        "orders+urgent@routed.inbox.test",
			inboxDomain: "inbox.test",
			wantSlug:    "routed",
			wantTag:     "urgent",
		},
		{
			name:        "rule 2: local-part routing accepted when recipient_routing_enabled=true",
			rcpt:        "routed+invoice@inbox.test",
			inboxDomain: "inbox.test",
			wantSlug:    "routed",
			wantTag:     "invoice",
		},
		{
			name:        "rule 2: routed project with recipient_routing_enabled=false rejected 550 5.1.1",
			rcpt:        "closed@inbox.test",
			inboxDomain: "inbox.test",
			wantCode:    550,
			wantEnh:     [3]int{5, 1, 1},
			wantMsg:     "Mailbox unavailable",
		},
		{
			name:        "rule 2: unknown routed project rejected 550 5.1.1",
			rcpt:        "missing@inbox.test",
			inboxDomain: "inbox.test",
			wantCode:    550,
			wantEnh:     [3]int{5, 1, 1},
			wantMsg:     "Mailbox unavailable",
		},
		{
			name:        "rule 3: non-routed recipient rejected 550 5.1.1",
			rcpt:        "alice@external.test",
			inboxDomain: "inbox.test",
			wantCode:    550,
			wantEnh:     [3]int{5, 1, 1},
			wantMsg:     "Mailbox unavailable",
		},
		{
			name:        "rule 4: no AUTH and empty inbox domain rejected 550 5.1.1 at Resolve",
			rcpt:        "alice@external.test",
			inboxDomain: "",
			wantCode:    550,
			wantEnh:     [3]int{5, 1, 1},
			wantMsg:     "Mailbox unavailable",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lookup := setupLookup()
			target, smtpErr := Resolve(context.Background(), config.ModeServer, tc.auth, tc.rcpt, tc.inboxDomain, "default", lookup)
			if tc.wantCode != 0 {
				if smtpErr == nil {
					t.Fatalf("expected SMTP error %d, got nil", tc.wantCode)
				}
				if smtpErr.Code != tc.wantCode || smtpErr.EnhancedCode != tc.wantEnh || smtpErr.Message != tc.wantMsg {
					t.Fatalf("got %d %v %q, want %d %v %q", smtpErr.Code, smtpErr.EnhancedCode, smtpErr.Message, tc.wantCode, tc.wantEnh, tc.wantMsg)
				}
				return
			}
			if smtpErr != nil {
				t.Fatalf("unexpected error: %v", smtpErr)
			}
			if target.ProjectSlug != tc.wantSlug {
				t.Fatalf("ProjectSlug = %q, want %q", target.ProjectSlug, tc.wantSlug)
			}
			if tc.wantCredID != nil && (target.CredentialID == nil || *target.CredentialID != *tc.wantCredID) {
				t.Fatalf("CredentialID = %v, want %d", target.CredentialID, *tc.wantCredID)
			}
			if tc.wantTag != "" && (len(target.PlusTags) != 1 || target.PlusTags[0] != tc.wantTag) {
				t.Fatalf("PlusTags = %v, want [%q]", target.PlusTags, tc.wantTag)
			}
		})
	}
}
