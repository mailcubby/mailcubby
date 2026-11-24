// Package auth implements password hashing (argon2id), server-side sessions,
// CSRF/Origin validation, and request principal handling (SPEC 6.4, FR-042 to FR-047).
package auth

import "context"

type ctxKey int

const ctxKeyPrincipal ctxKey = 1

// PrincipalKind identifies how an HTTP request was authenticated.
type PrincipalKind string

const (
	// PrincipalLocal represents the implicit operator in local mode (SPEC 5.1).
	PrincipalLocal PrincipalKind = "local"
	// PrincipalUser represents a browser session authenticated via cookie (SPEC FR-044).
	PrincipalUser PrincipalKind = "user"
	// PrincipalToken represents a bearer API token caller (SPEC FR-065, Phase 9).
	PrincipalToken PrincipalKind = "token"
	// PrincipalAnonymous represents an unauthenticated caller.
	PrincipalAnonymous PrincipalKind = "anonymous"
)

// Principal holds the authenticated caller identity attached to a request context.
type Principal struct {
	Kind              PrincipalKind
	UserID            int64
	Email             string
	Name              string
	IsAdmin           bool
	CanCreateProjects bool
	SessionID         string
	CSRFToken         string
	TokenID           int64
	TokenPrefix       string
	TokenScope        string
	TokenProjectID    *int64
	TokenProjectSlug  *string
}

// IsAuthenticated reports whether the principal represents a signed-in user,
// API token, or local operator.
func (p Principal) IsAuthenticated() bool {
	return p.Kind == PrincipalLocal || p.Kind == PrincipalUser || p.Kind == PrincipalToken
}

// LocalPrincipal returns the synthetic local operator principal (SPEC 5.1).
func LocalPrincipal() Principal {
	return Principal{
		Kind:              PrincipalLocal,
		UserID:            0,
		Email:             "local@localhost",
		Name:              "Local Operator",
		IsAdmin:           true,
		CanCreateProjects: true,
	}
}

// WithPrincipal attaches p to ctx.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKeyPrincipal, p)
}

// PrincipalFromContext retrieves the Principal from ctx, defaulting to PrincipalAnonymous.
func PrincipalFromContext(ctx context.Context) Principal {
	p, ok := ctx.Value(ctxKeyPrincipal).(Principal)
	if !ok {
		return Principal{Kind: PrincipalAnonymous}
	}
	return p
}
