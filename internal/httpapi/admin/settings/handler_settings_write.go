package settings

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	authn "ds2api/internal/auth"
	"ds2api/internal/config"
	adminshared "ds2api/internal/httpapi/admin/shared"
)

func (h *Handler) updateSettings(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid json"})
		return
	}

	// The registered sections a request carries, straight from the registry: the
	// write path applies what it is given and never names a section, so a new
	// section is a registry entry alone. Its fields are coerced and range-checked
	// by the declaration, and a value that is only invalid next to what the
	// config already holds is rejected on the merged config, before it commits.
	sections := config.SectionUpdates(req)
	registered, err := coerceRegisteredSettings(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	aliasMap, err := parseModelAliases(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}

	if err := h.Store.Update(func(c *config.Config) error {
		if err := config.ApplySections(c, sections); err != nil {
			return adminshared.NewRequestError(err.Error())
		}
		if err := config.ValidateAppliedSections(*c, sections); err != nil {
			return adminshared.NewRequestError(err.Error())
		}
		if aliasMap != nil {
			c.ModelAliases = aliasMap
		}
		applyRegisteredSettings(c, registered)
		return nil
	}); err != nil {
		if detail, ok := adminshared.RequestErrorDetail(err); ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"detail": detail})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}

	runSectionPostWriteActions(h, sections)
	needsSync := config.IsVercel() || h.Store.IsEnvBacked()
	writeJSON(w, http.StatusOK, map[string]any{
		"success":             true,
		"message":             "settings updated and hot reloaded",
		"env_backed":          h.Store.IsEnvBacked(),
		"needs_vercel_sync":   needsSync,
		"manual_sync_message": "配置已保存。Vercel 部署请在 Vercel Sync 页面手动同步。",
	})
}

// sectionPostWriteActions pushes a section's new values into a running
// component after the update is committed. It lives here rather than in the
// section declaration because the component it talks to (the account pool) is
// not config's to know about; the registry decides *whether* to run one, by
// telling this handler which sections the request carried.
var sectionPostWriteActions = map[string]func(*Handler){
	"runtime": (*Handler).applyRuntimeSettings,
}

func runSectionPostWriteActions(h *Handler, sections map[string]map[string]any) {
	for _, key := range config.SectionKeys() {
		if _, touched := sections[key]; !touched {
			continue
		}
		if action, ok := sectionPostWriteActions[key]; ok {
			action(h)
		}
	}
}

// boolFrom reads a boolean out of a settings payload, accepting the string form
// a form post can produce.
func boolFrom(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.ToLower(strings.TrimSpace(x)) == "true"
	default:
		return false
	}
}

// parseModelAliases reads the model alias map, which is edited as JSON text and
// merged whole rather than field by field. A request without the key leaves the
// aliases alone (nil), which is what every other partial update does.
func parseModelAliases(req map[string]any) (map[string]string, error) {
	raw, ok := req["model_aliases"].(map[string]any)
	if !ok {
		return nil, nil
	}
	aliases := map[string]string{}
	for k, v := range raw {
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(fmt.Sprintf("%v", v))
		if key == "" || val == "" {
			continue
		}
		aliases[key] = val
	}
	return aliases, nil
}

func (h *Handler) updateSettingsPassword(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid json"})
		return
	}
	newPassword := strings.TrimSpace(fieldString(req, "new_password"))
	if newPassword == "" {
		newPassword = strings.TrimSpace(fieldString(req, "password"))
	}
	if len(newPassword) < 4 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "new password must be at least 4 characters"})
		return
	}

	now := time.Now().Unix()
	hash := authn.HashAdminPassword(newPassword)
	if err := h.Store.Update(func(c *config.Config) error {
		c.Admin.PasswordHash = hash
		c.Admin.JWTValidAfterUnix = now
		return nil
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":              true,
		"message":              "password updated",
		"force_relogin":        true,
		"jwt_valid_after_unix": now,
	})
}

// coerceRegisteredSettings validates the registered settings a request carries
// and returns them in canonical form, so a bad value is rejected with a 400
// before the update closure mutates the config, and the merge does not have to
// coerce the same values twice.
//
// Settings are declared in internal/config/settings.go; this function is generic
// over the registry, so adding or changing one is a registry entry plus the code
// that reads it, with no change here.
func coerceRegisteredSettings(req map[string]any) (map[string]map[string]any, error) {
	out := map[string]map[string]any{}
	for _, spec := range config.SettingSpecs() {
		raw, ok := req[spec.Key].(map[string]any)
		if !ok {
			continue
		}
		for _, f := range spec.Fields {
			value, present, err := config.CoerceSettingField(spec, f, raw[f.Name])
			if err != nil {
				return nil, err
			}
			if !present {
				continue
			}
			if out[spec.Key] == nil {
				out[spec.Key] = map[string]any{}
			}
			out[spec.Key][f.Name] = value
		}
	}
	return out, nil
}

// applyRegisteredSettings merges the coerced values into the config. A field the
// request does not carry is left untouched, so a partial settings update never
// resets it — the same rule the registered sections follow.
func applyRegisteredSettings(c *config.Config, values map[string]map[string]any) {
	for key, fields := range values {
		for field, value := range fields {
			c.SetSetting(key, field, value)
		}
	}
}
