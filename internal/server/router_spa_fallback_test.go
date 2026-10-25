package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newAppWithStaticAdmin boots the router with a minimal built WebUI on disk so
// the /admin SPA fallback can actually serve index.html.
func newAppWithStaticAdmin(t *testing.T) *App {
	t.Helper()
	t.Setenv("DS2API_CONFIG_JSON", `{"keys":["k1"],"accounts":[{"email":"u@example.com","password":"p"}]}`)
	t.Setenv("DS2API_ENV_WRITEBACK", "0")
	t.Setenv("DS2API_AUTO_BUILD_WEBUI", "0")

	staticDir := filepath.Join(t.TempDir(), "admin")
	if err := os.MkdirAll(staticDir, 0o755); err != nil {
		t.Fatalf("mkdir static admin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("spa-index"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	t.Setenv("DS2API_STATIC_ADMIN_DIR", staticDir)

	app, err := NewApp()
	if err != nil {
		t.Fatalf("NewApp() error: %v", err)
	}
	return app
}

// TestAdminSPARouteRefreshServesIndexForPostOnlyPaths covers /admin/test and
// /admin/import: both are SPA routes whose paths are also admin API endpoints
// registered for POST only. chi answers a GET on those paths with 405 before any
// handler runs, so a hard refresh of the page used to fail with HTTP 405 instead
// of loading the SPA.
func TestAdminSPARouteRefreshServesIndexForPostOnlyPaths(t *testing.T) {
	app := newAppWithStaticAdmin(t)

	for _, path := range []string{"/admin/test", "/admin/import"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
			rec := httptest.NewRecorder()
			app.Router.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
			}
			if body := rec.Body.String(); body != "spa-index" {
				t.Fatalf("body = %q, want SPA index.html", body)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Fatalf("Content-Type = %q, want text/html prefix", ct)
			}
		})
	}
}

// TestAdminPostOnlyPathsStill405ForAPIClients guards the SPA fallback from
// swallowing genuine method errors: API callers (non-HTML Accept, or a bearer
// token) must keep receiving 405 with an Allow header listing POST.
func TestAdminPostOnlyPathsStill405ForAPIClients(t *testing.T) {
	app := newAppWithStaticAdmin(t)

	cases := []struct {
		name    string
		headers map[string]string
	}{
		{"json accept", map[string]string{"Accept": "application/json"}},
		{"wildcard accept", map[string]string{"Accept": "*/*"}},
		{"authorized html accept", map[string]string{"Accept": "text/html", "Authorization": "Bearer some-token"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/test", nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			app.Router.ServeHTTP(rec, req)

			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405; body: %s", rec.Code, rec.Body.String())
			}
			if allow := rec.Header().Values("Allow"); len(allow) == 0 || !containsString(allow, http.MethodPost) {
				t.Fatalf("Allow = %v, want it to contain POST", allow)
			}
		})
	}
}

// TestNonAdminMethodMismatchStays405 ensures the override did not turn method
// mismatches on API routes into SPA responses.
func TestNonAdminMethodMismatchStays405(t *testing.T) {
	app := newAppWithStaticAdmin(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405; body: %s", rec.Code, rec.Body.String())
	}
	if allow := rec.Header().Values("Allow"); !containsString(allow, http.MethodPost) {
		t.Fatalf("Allow = %v, want it to contain POST", allow)
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), want) {
				return true
			}
		}
	}
	return false
}
