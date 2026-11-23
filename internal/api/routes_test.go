package api_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/mailcubby/mailcubby/internal/api"
	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

func TestRoutesMatchOpenAPISpec(t *testing.T) {
	t.Parallel()

	st := testutil.NewStore(t)
	blobs, err := blob.NewStore(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	srv := api.New(cfg, st, blobs, nil, api.Options{Version: "test"})
	defer srv.Close()

	// Verify GET /api/v1/openapi.yaml serves the exact file on disk.
	diskSpec, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/openapi.yaml", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/openapi.yaml status = %d, want 200", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), diskSpec) {
		t.Fatalf("served openapi.yaml differs from disk openapi.yaml")
	}

	specRoutes := parseOpenAPIRoutes(t, string(diskSpec))
	regRoutes := make(map[string]struct{}, len(srv.Routes()))
	for _, rt := range srv.Routes() {
		if rt.Need == "" {
			t.Errorf("route %s %s has empty Permission", rt.Method, rt.Pattern)
		}
		key := rt.Method + " " + rt.Pattern
		if _, dup := regRoutes[key]; dup {
			t.Errorf("duplicate registered route: %s", key)
		}
		regRoutes[key] = struct{}{}
	}

	var missingInSpec []string
	for k := range regRoutes {
		if _, ok := specRoutes[k]; !ok {
			missingInSpec = append(missingInSpec, k)
		}
	}
	var missingInCode []string
	for k := range specRoutes {
		if _, ok := regRoutes[k]; !ok {
			missingInCode = append(missingInCode, k)
		}
	}
	sort.Strings(missingInSpec)
	sort.Strings(missingInCode)

	if len(missingInSpec) > 0 {
		t.Errorf("registered routes missing from openapi.yaml: %v", missingInSpec)
	}
	if len(missingInCode) > 0 {
		t.Errorf("openapi.yaml routes not registered in server: %v", missingInCode)
	}
}

// parseOpenAPIRoutes extracts "METHOD /path" keys from the top-level paths section
// of openapi.yaml without adding an external YAML dependency.
func parseOpenAPIRoutes(t *testing.T, yamlDoc string) map[string]struct{} {
	t.Helper()

	httpMethods := map[string]string{
		"get":     http.MethodGet,
		"post":    http.MethodPost,
		"put":     http.MethodPut,
		"patch":   http.MethodPatch,
		"delete":  http.MethodDelete,
		"head":    http.MethodHead,
		"options": http.MethodOptions,
	}

	routes := make(map[string]struct{})
	inPaths := false
	currentPath := ""

	for _, rawLine := range strings.Split(yamlDoc, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 0 {
			if trimmed == "paths:" {
				inPaths = true
				currentPath = ""
				continue
			}
			inPaths = false
			currentPath = ""
			continue
		}

		if !inPaths {
			continue
		}

		if indent == 2 && strings.HasSuffix(trimmed, ":") {
			p := strings.TrimSuffix(trimmed, ":")
			p = strings.Trim(p, `"'`)
			if !strings.HasPrefix(p, "/") {
				t.Fatalf("openapi.yaml path %q must be absolute (start with '/')", p)
			}
			currentPath = p
			continue
		}

		if indent == 4 && currentPath != "" && strings.HasSuffix(trimmed, ":") {
			key := strings.ToLower(strings.TrimSuffix(trimmed, ":"))
			if method, ok := httpMethods[key]; ok {
				routes[method+" "+currentPath] = struct{}{}
			}
		}
	}

	if len(routes) == 0 {
		t.Fatalf("parsed 0 routes from openapi.yaml")
	}
	return routes
}
