package api

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"

	"github.com/mailcubby/mailcubby/internal/store"
)

//go:embed openapi.yaml
var openAPISpec []byte

type metaFeatures struct {
	RemoteImages     string `json:"remote_images"`
	RecipientRouting bool   `json:"recipient_routing"`
}

type smtpConnectionInfo struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	TLSMode string `json:"tls_mode"`
}

type metaResponse struct {
	Mode          string             `json:"mode"`
	Version       string             `json:"version"`
	InstanceName  string             `json:"instance_name"`
	SetupRequired bool               `json:"setup_required"`
	BaseURL       string             `json:"base_url"`
	Features      metaFeatures       `json:"features"`
	InboxDomain   string             `json:"inbox_domain"`
	SMTP          smtpConnectionInfo `json:"smtp"`
}

type authUserResponse struct {
	ID                int64  `json:"id"`
	Email             string `json:"email"`
	Name              string `json:"name"`
	IsAdmin           bool   `json:"is_admin"`
	CanCreateProjects bool   `json:"can_create_projects"`
}

type authMembershipResponse struct {
	Project string `json:"project"`
	Role    string `json:"role"`
}

type authTokenResponse struct {
	Scope   string  `json:"scope"`
	Project *string `json:"project,omitempty"`
}

type authMeResponse struct {
	User        authUserResponse         `json:"user"`
	CSRFToken   string                   `json:"csrf_token"`
	Memberships []authMembershipResponse `json:"memberships"`
	Token       *authTokenResponse       `json:"token,omitempty"`
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openAPISpec)
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	instanceName := s.resolveInstanceName(r.Context())

	resp := metaResponse{
		Mode:          string(s.cfg.Mode),
		Version:       s.version,
		InstanceName:  instanceName,
		SetupRequired: false,
		BaseURL:       s.cfg.BaseURL,
		Features: metaFeatures{
			RemoteImages:     string(s.cfg.RenderRemoteImages),
			RecipientRouting: s.cfg.InboxDomain != "",
		},
		InboxDomain: s.cfg.InboxDomain,
		SMTP:        s.smtpConnectionInfo(),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	principal := PrincipalFromContext(r.Context())
	projects, err := s.store.ListProjects(r.Context())
	if err != nil {
		s.log.Error("list projects for auth/me failed",
			"request_id", RequestIDFromContext(r.Context()),
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	memberships := make([]authMembershipResponse, 0, len(projects))
	for _, p := range projects {
		memberships = append(memberships, authMembershipResponse{
			Project: p.Slug,
			Role:    "owner",
		})
	}

	writeJSON(w, http.StatusOK, authMeResponse{
		User: authUserResponse{
			ID:                principal.UserID,
			Email:             principal.Email,
			Name:              principal.Name,
			IsAdmin:           principal.IsAdmin,
			CanCreateProjects: principal.CanCreateProjects,
		},
		CSRFToken:   "",
		Memberships: memberships,
	})
}

func (s *Server) resolveInstanceName(ctx context.Context) string {
	setting, err := s.store.GetSetting(ctx, "instance_name")
	if err == nil {
		var name string
		if jsonErr := json.Unmarshal([]byte(setting.Value), &name); jsonErr == nil && name != "" {
			return name
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		s.log.Warn("failed to read instance_name setting", "error", err)
	}
	return s.cfg.InstanceName
}

func (s *Server) smtpConnectionInfo() smtpConnectionInfo {
	tlsMode := "none"
	if s.cfg.SMTPTLSCert != "" && s.cfg.SMTPTLSKey != "" {
		tlsMode = "starttls"
		if s.cfg.SMTPSAddr != "" {
			if _, portStr, err := net.SplitHostPort(s.cfg.SMTPSAddr); err == nil {
				if p, err := strconv.Atoi(portStr); err == nil && p == s.cfg.SMTPPublicPort {
					tlsMode = "tls"
				}
			}
		}
	}
	return smtpConnectionInfo{
		Host:    s.cfg.SMTPPublicHost,
		Port:    s.cfg.SMTPPublicPort,
		TLSMode: tlsMode,
	}
}
