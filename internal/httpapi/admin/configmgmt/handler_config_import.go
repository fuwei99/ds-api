package configmgmt

import (
	"encoding/json"
	"net/http"
	"strings"

	"ds2api/internal/config"
)

func (h *Handler) configImport(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid json"})
		return
	}

	mode := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("mode")))
	if mode == "" {
		mode = strings.TrimSpace(strings.ToLower(fieldString(req, "mode")))
	}
	if mode == "" {
		mode = "merge"
	}
	if mode != "merge" && mode != "replace" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "mode must be merge or replace"})
		return
	}

	payload := req
	if raw, ok := req["config"].(map[string]any); ok && len(raw) > 0 {
		payload = raw
	}
	rawJSON, err := json.Marshal(payload)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid config payload"})
		return
	}
	var incoming config.Config
	if err := json.Unmarshal(rawJSON, &incoming); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	incoming.ClearAccountTokens()

	importedKeys, importedAccounts := 0, 0
	err = h.Store.Update(func(c *config.Config) error {
		next := c.Clone()
		if mode == "replace" {
			next = incoming.Clone()
			next.Accounts = normalizeAndDedupeAccounts(next.Accounts)
			next.VercelSyncHash = c.VercelSyncHash
			next.VercelSyncTime = c.VercelSyncTime
			// tool_marker_secret is a server-generated, never-exported secret:
			// an imported payload cannot carry it, so a replace must keep the
			// running instance's value. Otherwise every API key's tool-call
			// marker would silently change until the next process start
			// regenerates it.
			next.ToolMarkerSecret = c.ToolMarkerSecret
			if len(incoming.Mihomo.Subscriptions) == 0 && len(incoming.Mihomo.PortMap) == 0 {
				// 订阅与端口映射由 mihomo_subscriptions.json 独立维护，
				// 导入的 config 通常不再携带；若未提供则保留现有状态，避免误删。
				next.Mihomo.Subscriptions = c.Mihomo.Subscriptions
				next.Mihomo.PortMap = c.Mihomo.PortMap
			}
			importedKeys = len(next.APIKeys)
			importedAccounts = len(next.Accounts)
		} else {
			var changed int
			next.APIKeys, changed = mergeAPIKeysPreferStructured(next.APIKeys, incoming.APIKeys)
			importedKeys += changed

			existingAccounts := map[string]struct{}{}
			for _, acc := range next.Accounts {
				acc = normalizeAccountForStorage(acc)
				key := accountDedupeKey(acc)
				if key != "" {
					existingAccounts[key] = struct{}{}
				}
			}
			for _, acc := range incoming.Accounts {
				acc = normalizeAccountForStorage(acc)
				key := accountDedupeKey(acc)
				if key == "" {
					continue
				}
				if _, ok := existingAccounts[key]; ok {
					continue
				}
				existingAccounts[key] = struct{}{}
				next.Accounts = append(next.Accounts, acc)
				importedAccounts++
			}

			// Registered settings (internal/config/settings.go) are merged
			// generically: every setting the incoming config actually carries is
			// applied, and one it does not mention is left alone. Before the
			// registry this list was hand-written and had drifted — it merged
			// the Responses TTL but silently dropped the input-file threshold
			// and the two injection prompts.
			mergeRegisteredSettings(&next, incoming)
			// Registered sections (internal/config/sections.go) merge the same
			// way: each one declares its own rule (the admin section keeps its
			// password hash, a form section merges field by field), so a new
			// section cannot be dropped from an import by forgetting it here.
			if err := config.MergeRegisteredSections(&next, incoming); err != nil {
				return newRequestError(err.Error())
			}
		}

		normalizeSettingsConfig(&next)
		if err := validateSettingsConfig(next); err != nil {
			return newRequestError(err.Error())
		}

		*c = next
		return nil
	})
	if err != nil {
		if detail, ok := requestErrorDetail(err); ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"detail": detail})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}

	h.Pool.Reset()
	writeJSON(w, http.StatusOK, map[string]any{
		"success":           true,
		"mode":              mode,
		"imported_keys":     importedKeys,
		"imported_accounts": importedAccounts,
		"message":           "config imported",
	})
}

// mergeRegisteredSettings applies every registered setting the incoming config
// carries onto next. It is generic over internal/config/settings.go, so a new
// setting is merged by the registry entry alone.
func mergeRegisteredSettings(next *config.Config, incoming config.Config) {
	for key, fields := range incoming.Settings {
		if len(fields) == 0 {
			continue
		}
		for field, value := range fields {
			next.SetSetting(key, field, value)
		}
	}
}
