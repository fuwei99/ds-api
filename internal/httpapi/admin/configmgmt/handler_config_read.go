package configmgmt

import (
	"net/http"
	"strings"

	"ds2api/internal/config"
)

func (h *Handler) getConfig(w http.ResponseWriter, _ *http.Request) {
	snap := h.Store.Snapshot()
	safe := map[string]any{
		"keys":                  snap.Keys,
		"api_keys":              snap.APIKeys,
		"accounts":              []map[string]any{},
		"proxies":               []map[string]any{},
		"env_backed":            h.Store.IsEnvBacked(),
		"env_source_present":    h.Store.HasEnvConfigSource(),
		"env_writeback_enabled": h.Store.IsEnvWritebackEnabled(),
		"config_path":           h.Store.ConfigPath(),
		"model_aliases":         snap.ModelAliases,
		// 凭据开关（internal/config/credential_flags.go）由注册表声明，前端据此
		// 为每个开关泛化渲染一个控件与一枚列表徽标；新增开关无需改前端组件。
		"credential_flag_specs": credentialFlagSpecPayload(),
		"elastic_pool": map[string]any{
			"enabled":          snap.ElasticPool.Enabled,
			"per_pool":         snap.ElasticPool.PerPool,
			"global_count":     snap.ElasticPool.GlobalCount,
			"default_count":    snap.ElasticPool.DefaultCount,
			"no_tools_count":   snap.ElasticPool.NoToolsCount,
			"tools_only_count": snap.ElasticPool.ToolsOnlyCount,
		},
		// 账号管理界面据此决定是否显示「管理device_id」入口与剩余数量标签。
		"runtime": map[string]any{
			"device_id_mode": h.Store.RuntimeDeviceIDMode(),
		},
		"vercel": map[string]any{
			"has_token":     strings.TrimSpace(snap.Vercel.Token) != "",
			"token_preview": maskSecretPreview(snap.Vercel.Token),
			"project_id":    snap.Vercel.ProjectID,
			"team_id":       snap.Vercel.TeamID,
		},
	}
	accounts := make([]map[string]any, 0, len(snap.Accounts))
	for _, acc := range snap.Accounts {
		token := strings.TrimSpace(acc.Token)
		accounts = append(accounts, map[string]any{
			"identifier":    acc.Identifier(),
			"name":          acc.Name,
			"remark":        acc.Remark,
			"email":         acc.Email,
			"mobile":        acc.Mobile,
			"proxy_id":      acc.ProxyID,
			"has_password":  strings.TrimSpace(acc.Password) != "",
			"has_token":     token != "",
			"token_preview": maskSecretPreview(token),
			// 绑定方式（手动绑定 / 强制直连）供账号管理界面回显当前选择。
			"manual_binding": acc.ManualBinding,
			"no_proxy":       acc.NoProxy,
		})
	}
	safe["accounts"] = accounts
	proxies := make([]map[string]any, 0, len(snap.Proxies))
	for _, proxy := range snap.Proxies {
		proxy = config.NormalizeProxy(proxy)
		proxies = append(proxies, map[string]any{
			"id":           proxy.ID,
			"name":         proxy.Name,
			"type":         proxy.Type,
			"host":         proxy.Host,
			"port":         proxy.Port,
			"username":     proxy.Username,
			"has_password": strings.TrimSpace(proxy.Password) != "",
		})
	}
	safe["proxies"] = proxies
	writeJSON(w, http.StatusOK, safe)
}

// credentialFlagSpecPayload describes the registered credential flags for the
// web UI, which renders one toggle per declaration and one list badge per flag
// that carries a badge label. The keys are locale keys, so translation stays in
// the locale files and a new flag needs no component change.
func credentialFlagSpecPayload() []map[string]any {
	specs := config.CredentialFlagSpecs()
	out := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		out = append(out, map[string]any{
			"name":      spec.Name,
			"default":   spec.Default,
			"label_key": spec.LabelKey,
			"help_key":  spec.HelpKey,
			"badge_key": spec.BadgeKey,
		})
	}
	return out
}

func (h *Handler) exportConfig(w http.ResponseWriter, _ *http.Request) {
	h.configExport(w, nil)
}

func (h *Handler) configExport(w http.ResponseWriter, _ *http.Request) {
	snap := h.Store.Snapshot()
	jsonStr, b64, err := h.Store.ExportJSONAndBase64()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"config":  snap,
		"json":    jsonStr,
		"base64":  b64,
	})
}
