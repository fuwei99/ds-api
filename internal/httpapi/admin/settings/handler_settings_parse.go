package settings

import (
	"fmt"
	"strings"

	"ds2api/internal/config"
)

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

func parseSettingsUpdateRequest(req map[string]any) (*config.AdminConfig, *config.RuntimeConfig, *config.ResponsesConfig, *config.EmbeddingsConfig, *config.AutoDeleteConfig, *config.InputFileConfig, *config.ThinkingInjectionConfig, *config.BottomFormatInjectionConfig, *config.ExpertPromptSegmentConfig, *config.AutoRouteVisionConfig, map[string]string, error) {
	var (
		adminCfg           *config.AdminConfig
		runtimeCfg         *config.RuntimeConfig
		respCfg            *config.ResponsesConfig
		embCfg             *config.EmbeddingsConfig
		autoDeleteCfg      *config.AutoDeleteConfig
		currentInputCfg    *config.InputFileConfig
		thinkingInjCfg     *config.ThinkingInjectionConfig
		bottomFmtInjCfg    *config.BottomFormatInjectionConfig
		expertPromptSegCfg *config.ExpertPromptSegmentConfig
		autoRouteVisionCfg *config.AutoRouteVisionConfig
		aliasMap           map[string]string
	)

	if raw, ok := req["admin"].(map[string]any); ok {
		cfg := &config.AdminConfig{}
		if v, exists := raw["jwt_expire_hours"]; exists {
			n := intFrom(v)
			if err := config.ValidateIntRange("admin.jwt_expire_hours", n, 1, 720, true); err != nil {
				return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, err
			}
			cfg.JWTExpireHours = n
		}
		adminCfg = cfg
	}

	if raw, ok := req["runtime"].(map[string]any); ok {
		cfg := &config.RuntimeConfig{}
		if v, exists := raw["account_max_inflight"]; exists {
			n := intFrom(v)
			if err := config.ValidateIntRange("runtime.account_max_inflight", n, 1, 256, true); err != nil {
				return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, err
			}
			cfg.AccountMaxInflight = n
		}
		if v, exists := raw["account_max_queue"]; exists {
			n := intFrom(v)
			if err := config.ValidateIntRange("runtime.account_max_queue", n, 1, 200000, true); err != nil {
				return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, err
			}
			cfg.AccountMaxQueue = n
		}
		if v, exists := raw["global_max_inflight"]; exists {
			n := intFrom(v)
			if err := config.ValidateIntRange("runtime.global_max_inflight", n, 1, 200000, true); err != nil {
				return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, err
			}
			cfg.GlobalMaxInflight = n
		}
		if v, exists := raw["token_refresh_interval_hours"]; exists {
			n := intFrom(v)
			if err := config.ValidateIntRange("runtime.token_refresh_interval_hours", n, 1, 720, true); err != nil {
				return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, err
			}
			cfg.TokenRefreshIntervalHours = n
		}
		if v, exists := raw["device_id_mode"]; exists {
			s := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", v)))
			if s != "" && s != config.DeviceIDTypeRandom && s != config.DeviceIDTypeReal {
				return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, fmt.Errorf("runtime.device_id_mode must be %q or %q", config.DeviceIDTypeRandom, config.DeviceIDTypeReal)
			}
			cfg.DeviceIDMode = config.NormalizeDeviceIDMode(s)
		}
		if rawReport, exists := raw["mute_report"].(map[string]any); exists {
			report := config.MuteReportConfig{}
			if v, ok := rawReport["enabled"]; ok {
				enabled := boolFrom(v)
				report.Enabled = &enabled
			}
			if v, ok := rawReport["url"]; ok {
				report.URL = strings.TrimSpace(fmt.Sprintf("%v", v))
			}
			if err := config.ValidateMuteReportConfig(report); err != nil {
				return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, err
			}
			cfg.MuteReport = report
		}
		if rawLimit, exists := raw["account_rate_limit"].(map[string]any); exists {
			limitCfg := config.AccountRateLimitConfig{}
			if v, ok := rawLimit["enabled"]; ok {
				enabled := boolFrom(v)
				limitCfg.Enabled = &enabled
			}
			if v, ok := rawLimit["max_per_minute"]; ok {
				n := intFrom(v)
				if err := config.ValidateIntRange("runtime.account_rate_limit.max_per_minute", n, 1, 100000, true); err != nil {
					return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, err
				}
				limitCfg.MaxPerMinute = n
			}
			cfg.AccountRateLimit = limitCfg
		}
		if cfg.AccountMaxInflight > 0 && cfg.GlobalMaxInflight > 0 && cfg.GlobalMaxInflight < cfg.AccountMaxInflight {
			return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, fmt.Errorf("runtime.global_max_inflight must be >= runtime.account_max_inflight")
		}
		runtimeCfg = cfg
	}

	if raw, ok := req["responses"].(map[string]any); ok {
		cfg := &config.ResponsesConfig{}
		if v, exists := raw["store_ttl_seconds"]; exists {
			n := intFrom(v)
			if err := config.ValidateIntRange("responses.store_ttl_seconds", n, 30, 86400, true); err != nil {
				return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, err
			}
			cfg.StoreTTLSeconds = n
		}
		respCfg = cfg
	}

	if raw, ok := req["embeddings"].(map[string]any); ok {
		cfg := &config.EmbeddingsConfig{}
		if v, exists := raw["provider"]; exists {
			p := strings.TrimSpace(fmt.Sprintf("%v", v))
			if err := config.ValidateTrimmedString("embeddings.provider", p, false); err != nil {
				return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, err
			}
			cfg.Provider = p
		}
		embCfg = cfg
	}

	if raw, ok := req["model_aliases"].(map[string]any); ok {
		if aliasMap == nil {
			aliasMap = map[string]string{}
		}
		for k, v := range raw {
			key := strings.TrimSpace(k)
			val := strings.TrimSpace(fmt.Sprintf("%v", v))
			if key == "" || val == "" {
				continue
			}
			aliasMap[key] = val
		}
	}

	if raw, ok := req["auto_delete"].(map[string]any); ok {
		cfg := &config.AutoDeleteConfig{}
		if v, exists := raw["mode"]; exists {
			mode := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", v)))
			if err := config.ValidateAutoDeleteMode(mode); err != nil {
				return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, err
			}
			if mode == "" {
				mode = "none"
			}
			cfg.Mode = mode
		}
		if v, exists := raw["sessions"]; exists {
			cfg.Sessions = boolFrom(v)
		}
		autoDeleteCfg = cfg
	}

	if raw, ok := req["input_file"].(map[string]any); ok {
		cfg := &config.InputFileConfig{}
		if v, exists := raw["enabled"]; exists {
			enabled := boolFrom(v)
			cfg.Enabled = &enabled
		}
		if v, exists := raw["min_chars"]; exists {
			n := intFrom(v)
			if err := config.ValidateIntRange("input_file.min_chars", n, 0, 100000000, true); err != nil {
				return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, err
			}
			cfg.MinChars = n
		}
		if err := config.ValidateInputFileConfig(*cfg); err != nil {
			return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, err
		}
		currentInputCfg = cfg
	}

	if raw, ok := req["thinking_injection"].(map[string]any); ok {
		cfg := &config.ThinkingInjectionConfig{}
		if v, exists := raw["enabled"]; exists {
			b := boolFrom(v)
			cfg.Enabled = &b
		}
		if v, exists := raw["prompt"]; exists {
			cfg.Prompt = strings.TrimSpace(fmt.Sprintf("%v", v))
		}
		thinkingInjCfg = cfg
	}

	if raw, ok := req["bottom_format_injection"].(map[string]any); ok {
		cfg := &config.BottomFormatInjectionConfig{}
		if v, exists := raw["enabled"]; exists {
			b := boolFrom(v)
			cfg.Enabled = &b
		}
		if v, exists := raw["prompt"]; exists {
			cfg.Prompt = strings.TrimSpace(fmt.Sprintf("%v", v))
		}
		bottomFmtInjCfg = cfg
	}

	if raw, ok := req["expert_prompt_segment"].(map[string]any); ok {
		cfg := &config.ExpertPromptSegmentConfig{}
		if v, exists := raw["enabled"]; exists {
			b := boolFrom(v)
			cfg.Enabled = &b
		}
		if v, exists := raw["max_chars"]; exists {
			n := intFrom(v)
			if err := config.ValidateIntRange("expert_prompt_segment.max_chars", n, 0, 100000000, true); err != nil {
				return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, err
			}
			cfg.MaxChars = n
		}
		if err := config.ValidateExpertPromptSegmentConfig(*cfg); err != nil {
			return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, err
		}
		expertPromptSegCfg = cfg
	}

	if raw, ok := req["auto_route_vision"].(map[string]any); ok {
		cfg := &config.AutoRouteVisionConfig{}
		if v, exists := raw["enabled"]; exists {
			b := boolFrom(v)
			cfg.Enabled = &b
		}
		autoRouteVisionCfg = cfg
	}

	return adminCfg, runtimeCfg, respCfg, embCfg, autoDeleteCfg, currentInputCfg, thinkingInjCfg, bottomFmtInjCfg, expertPromptSegCfg, autoRouteVisionCfg, aliasMap, nil
}
