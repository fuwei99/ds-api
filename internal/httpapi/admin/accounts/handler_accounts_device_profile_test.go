package accounts

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"ds2api/internal/config"
)

func TestDeleteAccountRemovesDeviceProfile(t *testing.T) {
	profileDir := t.TempDir()
	t.Setenv("DS2API_DEVICE_PROFILE_DIR", profileDir)
	h := newAdminTestHandler(t, `{
		"accounts":[{"email":"u@example.com","password":"pwd"}]
	}`)

	profilePath := config.DeviceProfilePath("u@example.com")
	if err := os.MkdirAll(filepath.Dir(profilePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := chi.NewRouter()
	r.Delete("/admin/accounts/{identifier}", h.deleteAccount)
	req := httptest.NewRequest(http.MethodDelete, "/admin/accounts/u@example.com", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(profilePath); !os.IsNotExist(err) {
		t.Fatalf("device profile should be removed on account delete, stat err=%v", err)
	}
	if len(h.Store.Snapshot().Accounts) != 0 {
		t.Fatal("account should be removed")
	}
}

func TestDeleteAccountWithoutProfileIsSilent(t *testing.T) {
	t.Setenv("DS2API_DEVICE_PROFILE_DIR", t.TempDir())
	h := newAdminTestHandler(t, `{
		"accounts":[{"email":"u@example.com","password":"pwd"}]
	}`)
	r := chi.NewRouter()
	r.Delete("/admin/accounts/{identifier}", h.deleteAccount)
	req := httptest.NewRequest(http.MethodDelete, "/admin/accounts/u@example.com", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}
