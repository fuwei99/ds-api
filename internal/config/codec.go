package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

func (c Config) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	for k, v := range c.AdditionalFields {
		m[k] = v
	}
	if len(c.Keys) > 0 {
		m["keys"] = c.Keys
	}
	if len(c.APIKeys) > 0 {
		m["api_keys"] = c.APIKeys
	}
	if len(c.Accounts) > 0 {
		m["accounts"] = c.Accounts
	}
	if len(c.Proxies) > 0 {
		m["proxies"] = c.Proxies
	}
	if len(c.ModelAliases) > 0 {
		m["model_aliases"] = c.ModelAliases
	}
	if strings.TrimSpace(c.Admin.PasswordHash) != "" || c.Admin.JWTExpireHours > 0 || c.Admin.JWTValidAfterUnix > 0 {
		m["admin"] = c.Admin
	}
	if c.Runtime.AccountMaxInflight > 0 || c.Runtime.AccountMaxQueue > 0 || c.Runtime.GlobalMaxInflight > 0 || c.Runtime.TokenRefreshIntervalHours > 0 || c.Runtime.DeviceIDMode != "" || strings.TrimSpace(c.Runtime.DevidPublicKey) != "" || c.Runtime.MuteReport.Enabled != nil || strings.TrimSpace(c.Runtime.MuteReport.URL) != "" {
		m["runtime"] = c.Runtime
	}
	if c.Responses.StoreTTLSeconds > 0 {
		m["responses"] = c.Responses
	}
	if strings.TrimSpace(c.Embeddings.Provider) != "" {
		m["embeddings"] = c.Embeddings
	}
	m["auto_delete"] = c.AutoDelete
	if c.InputFile.Enabled != nil || c.InputFile.MinChars != 0 {
		m["input_file"] = c.InputFile
	}
	if c.ThinkingInjection.Enabled != nil || strings.TrimSpace(c.ThinkingInjection.Prompt) != "" {
		m["thinking_injection"] = c.ThinkingInjection
	}
	if c.BottomFormatInjection.Enabled != nil || strings.TrimSpace(c.BottomFormatInjection.Prompt) != "" {
		m["bottom_format_injection"] = c.BottomFormatInjection
	}
	if c.ExpertPromptSegment.Enabled != nil || c.ExpertPromptSegment.MaxChars != 0 {
		m["expert_prompt_segment"] = c.ExpertPromptSegment
	}
	if c.ExpertTextFileInline.Enabled != nil || c.ExpertTextFileInline.MaxFileBytes != 0 {
		m["expert_text_file_inline"] = c.ExpertTextFileInline
	}
	if c.AutoRouteVision.Enabled != nil {
		m["auto_route_vision"] = c.AutoRouteVision
	}
	if c.ElasticPool.Enabled || c.ElasticPool.PerPool || c.ElasticPool.GlobalCount != 0 || c.ElasticPool.DefaultCount != 0 || c.ElasticPool.NoToolsCount != 0 || c.ElasticPool.ToolsOnlyCount != 0 {
		m["elastic_pool"] = c.ElasticPool
	}
	if len(c.DeviceIDPool.Items) > 0 {
		m["device_id_pool"] = c.DeviceIDPool
	}
	if !IsZeroMihomoConfig(c.Mihomo) {
		m["mihomo"] = c.Mihomo
	}
	if strings.TrimSpace(c.Vercel.Token) != "" || strings.TrimSpace(c.Vercel.ProjectID) != "" || strings.TrimSpace(c.Vercel.TeamID) != "" {
		m["vercel"] = NormalizeVercelConfig(c.Vercel)
	}
	if c.VercelSyncHash != "" {
		m["_vercel_sync_hash"] = c.VercelSyncHash
	}
	if c.VercelSyncTime != 0 {
		m["_vercel_sync_time"] = c.VercelSyncTime
	}
	if strings.TrimSpace(c.ToolMarkerSecret) != "" {
		m["tool_marker_secret"] = c.ToolMarkerSecret
	}
	return json.Marshal(m)
}

func (c *Config) UnmarshalJSON(b []byte) error {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	c.AdditionalFields = map[string]any{}
	for k, v := range raw {
		switch k {
		case "keys":
			if err := json.Unmarshal(v, &c.Keys); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "api_keys":
			if err := json.Unmarshal(v, &c.APIKeys); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "accounts":
			if err := json.Unmarshal(v, &c.Accounts); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "proxies":
			if err := json.Unmarshal(v, &c.Proxies); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "claude_mapping":
		case "claude_model_mapping":
			// Removed legacy mapping fields are ignored instead of persisted.
		case "model_aliases":
			if err := json.Unmarshal(v, &c.ModelAliases); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "admin":
			if err := json.Unmarshal(v, &c.Admin); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "runtime":
			if err := json.Unmarshal(v, &c.Runtime); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "compat":
			// Removed field ignored instead of persisted.
			if Logger != nil {
				Logger.Warn("config key \"compat\" is deprecated and ignored; remove it from your configuration")
			}
		case "toolcall":
			// Legacy field ignored. Toolcall policy is fixed and no longer configurable.
		case "responses":
			if err := json.Unmarshal(v, &c.Responses); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "embeddings":
			if err := json.Unmarshal(v, &c.Embeddings); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "auto_delete":
			if err := json.Unmarshal(v, &c.AutoDelete); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "history_split":
			// Removed legacy split field is ignored instead of persisted.
		case "current_input_file":
			// Legacy key. Renamed to `input_file` so that older configs still
			// carrying an explicit `"min_chars": 0` fall back to the new
			// default threshold instead of disabling the split by accident.
			if Logger != nil {
				Logger.Warn("config key \"current_input_file\" has been renamed to \"input_file\" and is now ignored; please migrate the section manually")
			}
		case "input_file":
			if err := json.Unmarshal(v, &c.InputFile); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "thinking_injection":
			if err := json.Unmarshal(v, &c.ThinkingInjection); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "bottom_format_injection":
			if err := json.Unmarshal(v, &c.BottomFormatInjection); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "expert_prompt_segment":
			if err := json.Unmarshal(v, &c.ExpertPromptSegment); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "expert_text_file_inline":
			if err := json.Unmarshal(v, &c.ExpertTextFileInline); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "auto_route_vision":
			if err := json.Unmarshal(v, &c.AutoRouteVision); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "elastic_pool":
			if err := json.Unmarshal(v, &c.ElasticPool); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "device_id_pool":
			if err := json.Unmarshal(v, &c.DeviceIDPool); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "mihomo":
			if err := json.Unmarshal(v, &c.Mihomo); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "vercel":
			if err := json.Unmarshal(v, &c.Vercel); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "_vercel_sync_hash":
			if err := json.Unmarshal(v, &c.VercelSyncHash); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "_vercel_sync_time":
			if err := json.Unmarshal(v, &c.VercelSyncTime); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		case "tool_marker_secret":
			if err := json.Unmarshal(v, &c.ToolMarkerSecret); err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
		default:
			var anyVal any
			if err := json.Unmarshal(v, &anyVal); err == nil {
				c.AdditionalFields[k] = anyVal
			}
		}
	}
	c.NormalizeCredentials()
	return nil
}

func (c Config) Clone() Config {
	clone := Config{
		Keys:         slices.Clone(c.Keys),
		APIKeys:      slices.Clone(c.APIKeys),
		Accounts:     slices.Clone(c.Accounts),
		Proxies:      slices.Clone(c.Proxies),
		ModelAliases: cloneStringMap(c.ModelAliases),
		Admin:        c.Admin,
		Runtime:      cloneRuntimeConfig(c.Runtime),
		Responses:    c.Responses,
		Embeddings:   c.Embeddings,
		AutoDelete:   c.AutoDelete,
		InputFile: InputFileConfig{
			Enabled:  cloneBoolPtr(c.InputFile.Enabled),
			MinChars: c.InputFile.MinChars,
		},
		ThinkingInjection: ThinkingInjectionConfig{
			Enabled: cloneBoolPtr(c.ThinkingInjection.Enabled),
			Prompt:  c.ThinkingInjection.Prompt,
		},
		BottomFormatInjection: BottomFormatInjectionConfig{
			Enabled: cloneBoolPtr(c.BottomFormatInjection.Enabled),
			Prompt:  c.BottomFormatInjection.Prompt,
		},
		ExpertPromptSegment: ExpertPromptSegmentConfig{
			Enabled:  cloneBoolPtr(c.ExpertPromptSegment.Enabled),
			MaxChars: c.ExpertPromptSegment.MaxChars,
		},
		ExpertTextFileInline: ExpertTextFileInlineConfig{
			Enabled:      cloneBoolPtr(c.ExpertTextFileInline.Enabled),
			MaxFileBytes: c.ExpertTextFileInline.MaxFileBytes,
		},
		AutoRouteVision: AutoRouteVisionConfig{
			Enabled: cloneBoolPtr(c.AutoRouteVision.Enabled),
		},
		ElasticPool:      c.ElasticPool,
		DeviceIDPool:     DeviceIDPoolConfig{Items: slices.Clone(c.DeviceIDPool.Items)},
		Mihomo:           cloneMihomoConfig(c.Mihomo),
		Vercel:           c.Vercel,
		VercelSyncHash:   c.VercelSyncHash,
		VercelSyncTime:   c.VercelSyncTime,
		ToolMarkerSecret: c.ToolMarkerSecret,
		AdditionalFields: map[string]any{},
	}
	for k, v := range c.AdditionalFields {
		clone.AdditionalFields[k] = v
	}
	return clone
}

// cloneRuntimeConfig 复制运行时配置。RuntimeConfig 内仅 MuteReport.Enabled
// 是引用类型，需要单独深拷贝，避免调用方修改快照时污染 Store 内部状态。
func cloneRuntimeConfig(in RuntimeConfig) RuntimeConfig {
	out := in
	out.MuteReport.Enabled = cloneBoolPtr(in.MuteReport.Enabled)
	return out
}

// cloneMihomoConfig 深拷贝订阅/节点/端口映射切片与 map。
// 节点的 Raw map 解析完成后不再原地修改（总是整体替换），因此共享引用。
func cloneMihomoConfig(in MihomoConfig) MihomoConfig {
	out := MihomoConfig{
		Enabled:    in.Enabled,
		BinaryPath: in.BinaryPath,
		BasePort:   in.BasePort,
		APIPort:    in.APIPort,
		AutoBind:   in.AutoBind,
	}
	if len(in.Subscriptions) > 0 {
		out.Subscriptions = make([]MihomoSubscription, len(in.Subscriptions))
		for i, sub := range in.Subscriptions {
			out.Subscriptions[i] = MihomoSubscription{
				ID:        sub.ID,
				Name:      sub.Name,
				URL:       sub.URL,
				UpdatedAt: sub.UpdatedAt,
				Nodes:     slices.Clone(sub.Nodes),
			}
		}
	}
	if len(in.PortMap) > 0 {
		out.PortMap = make(map[string]int, len(in.PortMap))
		for k, v := range in.PortMap {
			out.PortMap[k] = v
		}
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneBoolPtr(in *bool) *bool {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}

func parseConfigString(raw string) (Config, error) {
	var cfg Config
	candidates := []string{raw}
	if normalized := normalizeConfigInput(raw); normalized != raw {
		candidates = append(candidates, normalized)
	}
	for _, candidate := range candidates {
		if err := json.Unmarshal([]byte(candidate), &cfg); err == nil {
			return cfg, nil
		}
	}

	base64Input := candidates[len(candidates)-1]
	decoded, err := decodeConfigBase64(base64Input)
	if err != nil {
		return Config{}, fmt.Errorf("invalid DS2API_CONFIG_JSON: %w", err)
	}
	if err := json.Unmarshal(decoded, &cfg); err != nil {
		return Config{}, fmt.Errorf("invalid DS2API_CONFIG_JSON decoded JSON: %w", err)
	}
	return cfg, nil
}

func normalizeConfigInput(raw string) string {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return normalized
	}
	for {
		changed := false
		if len(normalized) >= 2 {
			first := normalized[0]
			last := normalized[len(normalized)-1]
			if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
				normalized = strings.TrimSpace(normalized[1 : len(normalized)-1])
				changed = true
			}
		}
		if strings.HasPrefix(strings.ToLower(normalized), "base64:") {
			normalized = strings.TrimSpace(normalized[len("base64:"):])
			changed = true
		}
		if !changed {
			break
		}
	}
	return strings.TrimSpace(normalized)
}

func decodeConfigBase64(raw string) ([]byte, error) {
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, enc := range encodings {
		decoded, err := enc.DecodeString(raw)
		if err == nil {
			return decoded, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("base64 decode failed")
}
