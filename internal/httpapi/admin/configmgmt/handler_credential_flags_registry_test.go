package configmgmt

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"ds2api/internal/config"
)

// The credential-flag registry (internal/config/credential_flags.go) exists so
// that a new per-key switch needs a registry entry plus the code that acts on
// it, and nothing else: no admin handler, no codec case, no web UI component.
// These tests drive a flag registered at test time through the admin endpoints
// and the config payload, which is the same path force_disable_tools takes.

const (
	testRegistryFlagName    = "test_admin_registry_flag"
	testRegistryFlagDefault = false
)

var testRegistryFlagOnce sync.Once

func registerAdminTestFlag(t *testing.T) string {
	t.Helper()
	testRegistryFlagOnce.Do(func() {
		config.RegisterCredentialFlag(config.CredentialFlagSpec{
			Name:        testRegistryFlagName,
			Default:     testRegistryFlagDefault,
			Restrictive: true,
			LabelKey:    "accountManager.testFlagLabel",
			HelpKey:     "accountManager.testFlagHint",
			BadgeKey:    "accountManager.testFlagOn",
		})
	})
	return testRegistryFlagName
}

// A flag that only the registry knows about must be settable and readable
// through the key endpoints, with no code change in the handler.
func TestKeyEndpointsPersistRegisteredCredentialFlag(t *testing.T) {
	flag := registerAdminTestFlag(t)
	h := newAdminTestHandler(t, `{"api_keys":[{"key":"k1","name":"primary"}]}`)

	r := chi.NewRouter()
	r.Post("/admin/keys", h.addKey)
	r.Put("/admin/keys/{key}", h.updateKey)

	addBody, _ := json.Marshal(map[string]any{"key": "k2", flag: true})
	addRec := httptest.NewRecorder()
	r.ServeHTTP(addRec, httptest.NewRequest(http.MethodPost, "/admin/keys", bytes.NewReader(addBody)))
	if addRec.Code != http.StatusOK {
		t.Fatalf("add status=%d body=%s", addRec.Code, addRec.Body.String())
	}
	if !h.Store.APIKeyFlag("k2", flag) {
		t.Fatalf("expected %s=true to be persisted on create", flag)
	}

	// 只传开关，name 应保持原值。
	updateBody, _ := json.Marshal(map[string]any{flag: true})
	updateRec := httptest.NewRecorder()
	r.ServeHTTP(updateRec, httptest.NewRequest(http.MethodPut, "/admin/keys/k1", bytes.NewReader(updateBody)))
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updateRec.Code, updateRec.Body.String())
	}
	snap := h.Store.Snapshot()
	if !snap.APIKeys[0].Flag(flag) {
		t.Fatalf("expected %s to be persisted: %#v", flag, snap.APIKeys[0])
	}
	if snap.APIKeys[0].Name != "primary" {
		t.Fatalf("a partial update must not clear metadata: %#v", snap.APIKeys[0])
	}

	// 再关闭，确认开关可双向翻转，且关到默认值后不再落盘。
	updateBody, _ = json.Marshal(map[string]any{flag: false})
	updateRec = httptest.NewRecorder()
	r.ServeHTTP(updateRec, httptest.NewRequest(http.MethodPut, "/admin/keys/k1", bytes.NewReader(updateBody)))
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update-off status=%d body=%s", updateRec.Code, updateRec.Body.String())
	}
	if h.Store.APIKeyFlag("k1", flag) {
		t.Fatalf("expected %s to be off after disabling", flag)
	}
	encoded, _ := json.Marshal(h.Store.Snapshot().APIKeys[0])
	if strings.Contains(string(encoded), flag) {
		t.Fatalf("a flag at its declared default must not be written: %s", encoded)
	}
}

// 显式 null 与字段缺失同义，这条规则由注册表驱动，因此新注册的开关自动继承。
func TestUpdateKeyExplicitNullKeepsRegisteredCredentialFlag(t *testing.T) {
	flag := registerAdminTestFlag(t)
	h := newAdminTestHandler(t, `{"api_keys":[{"key":"k1","`+flag+`":true}]}`)
	if !h.Store.APIKeyFlag("k1", flag) {
		t.Fatalf("precondition: %s must load from the config JSON", flag)
	}

	r := chi.NewRouter()
	r.Put("/admin/keys/{key}", h.updateKey)

	body, _ := json.Marshal(map[string]any{flag: nil})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/admin/keys/k1", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !h.Store.APIKeyFlag("k1", flag) {
		t.Fatalf("an explicit null must not clear %s", flag)
	}
}

// The settings page renders one control per declaration, so the specs have to
// travel with the config response — otherwise a new flag would need a component.
func TestGetConfigReportsCredentialFlagSpecs(t *testing.T) {
	flag := registerAdminTestFlag(t)
	h := newAdminTestHandler(t, `{"api_keys":[{"key":"k1"}]}`)

	rec := httptest.NewRecorder()
	h.getConfig(rec, httptest.NewRequest(http.MethodGet, "/admin/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	specs, _ := body["credential_flag_specs"].([]any)
	if len(specs) == 0 {
		t.Fatalf("credential_flag_specs must be reported: %v", body)
	}
	var found, production bool
	for _, raw := range specs {
		spec, _ := raw.(map[string]any)
		switch spec["name"] {
		case flag:
			found = true
			if spec["label_key"] != "accountManager.testFlagLabel" || spec["badge_key"] != "accountManager.testFlagOn" {
				t.Fatalf("spec is incomplete: %#v", spec)
			}
			if spec["default"] != false {
				t.Fatalf("spec must report the declared default: %#v", spec)
			}
		case config.CredentialFlagForceDisableTools:
			production = true
		}
	}
	if !found {
		t.Fatalf("the registered flag is missing from the specs: %v", specs)
	}
	if !production {
		t.Fatalf("the production flag is missing from the specs: %v", specs)
	}
}

// A flag absent from an import must not be dropped: the merge policy comes from
// the registry, so an import cannot silently clear a restriction.
func TestConfigImportKeepsRegisteredCredentialFlag(t *testing.T) {
	flag := registerAdminTestFlag(t)
	h := newAdminTestHandler(t, `{"api_keys":[{"key":"k1","name":"primary","`+flag+`":true}]}`)

	if !h.Store.APIKeyFlag("k1", flag) {
		t.Fatalf("expected the flag to load from the config JSON")
	}

	// 合并导入同一 key，但不带开关。
	imported, _ := json.Marshal(map[string]any{
		"mode":     "merge",
		"api_keys": []any{map[string]any{"key": "k1", "name": "primary"}},
	})
	rec := httptest.NewRecorder()
	h.configImport(rec, httptest.NewRequest(http.MethodPost, "/admin/config/import", bytes.NewReader(imported)))
	if rec.Code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !h.Store.APIKeyFlag("k1", flag) {
		t.Fatal("an import must not silently drop a restrictive flag")
	}
}
