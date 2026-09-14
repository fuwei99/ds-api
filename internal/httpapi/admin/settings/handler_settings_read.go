package settings

import (
	"net/http"
	"strings"

	authn "ds2api/internal/auth"
	"ds2api/internal/config"
	"ds2api/internal/promptcompat"
)

func (h *Handler) getSettings(w http.ResponseWriter, _ *http.Request) {
	snap := h.Store.Snapshot()
	recommended := defaultRuntimeRecommended(len(snap.Accounts), h.Store.RuntimeAccountMaxInflight())
	needsSync := config.IsVercel() && snap.VercelSyncHash != "" && snap.VercelSyncHash != h.computeSyncHash()
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"admin": map[string]any{
			"has_password_hash":        strings.TrimSpace(snap.Admin.PasswordHash) != "",
			"jwt_expire_hours":         h.Store.AdminJWTExpireHours(),
			"jwt_valid_after_unix":     snap.Admin.JWTValidAfterUnix,
			"default_password_warning": authn.UsingDefaultAdminKey(h.Store),
		},
		"runtime": map[string]any{
			"account_max_inflight":         h.Store.RuntimeAccountMaxInflight(),
			"account_max_queue":            h.Store.RuntimeAccountMaxQueue(recommended),
			"global_max_inflight":          h.Store.RuntimeGlobalMaxInflight(recommended),
			"token_refresh_interval_hours": h.Store.RuntimeTokenRefreshIntervalHours(),
			"device_id_mode":               h.Store.RuntimeDeviceIDMode(),
			"mute_report": map[string]any{
				"enabled": h.Store.RuntimeMuteReportEnabled(),
				"url":     h.Store.RuntimeMuteReportURL(),
			},
			"account_rate_limit": map[string]any{
				"enabled":        h.Store.RuntimeAccountRateLimitEnabled(),
				"max_per_minute": h.Store.RuntimeAccountRateLimitMaxPerMinute(),
			},
		},
		"responses":   snap.Responses,
		"embeddings":  snap.Embeddings,
		"auto_delete": snap.AutoDelete,
		"input_file": map[string]any{
			"enabled":   h.Store.CurrentInputFileEnabled(),
			"min_chars": h.Store.CurrentInputFileMinChars(),
		},
		"thinking_injection": map[string]any{
			"enabled":        h.Store.ThinkingInjectionEnabled(),
			"prompt":         h.Store.ThinkingInjectionPrompt(),
			"default_prompt": promptcompat.DefaultThinkingInjectionPrompt,
		},
		"bottom_format_injection": map[string]any{
			"enabled":        h.Store.BottomFormatInjectionEnabled(),
			"prompt":         h.Store.BottomFormatInjectionPrompt(),
			"default_prompt": promptcompat.DefaultBottomFormatInjectionPrompt,
		},
		"expert_prompt_segment": map[string]any{
			"enabled":   h.Store.ExpertPromptSegmentEnabled(),
			"max_chars": h.Store.ExpertPromptSegmentMaxChars(),
		},
		"auto_route_vision": map[string]any{
			"enabled": h.Store.AutoRouteVisionEnabled(),
		},
		"model_aliases":     snap.ModelAliases,
		"env_backed":        h.Store.IsEnvBacked(),
		"needs_vercel_sync": needsSync,
	})
}
