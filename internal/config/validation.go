package config

import (
	"fmt"
	"net/url"
	"strings"
)

func ValidateConfig(c Config) error {
	if err := ValidateProxyConfig(c.Proxies); err != nil {
		return err
	}
	if err := ValidateAdminConfig(c.Admin); err != nil {
		return err
	}
	if err := ValidateRuntimeConfig(c.Runtime); err != nil {
		return err
	}
	if err := ValidateResponsesConfig(c.Responses); err != nil {
		return err
	}
	if err := ValidateEmbeddingsConfig(c.Embeddings); err != nil {
		return err
	}
	if err := ValidateAutoDeleteConfig(c.AutoDelete); err != nil {
		return err
	}
	if err := ValidateInputFileConfig(c.InputFile); err != nil {
		return err
	}
	if err := ValidateExpertPromptSegmentConfig(c.ExpertPromptSegment); err != nil {
		return err
	}
	if err := ValidateExpertTextFileInlineConfig(c.ExpertTextFileInline); err != nil {
		return err
	}
	if err := ValidateAutoRouteVisionConfig(c.AutoRouteVision); err != nil {
		return err
	}
	if err := ValidateMihomoConfig(c.Mihomo); err != nil {
		return err
	}
	if err := ValidateAccountProxyReferences(c.Accounts, c.Proxies); err != nil {
		return err
	}
	return nil
}

func ValidateProxyConfig(proxies []Proxy) error {
	seen := make(map[string]struct{}, len(proxies))
	for _, proxy := range proxies {
		proxy = NormalizeProxy(proxy)
		if err := ValidateTrimmedString("proxies.id", proxy.ID, true); err != nil {
			return err
		}
		switch proxy.Type {
		case "socks5", "socks5h":
		default:
			return fmt.Errorf("proxies.type must be one of socks5, socks5h")
		}
		if err := ValidateTrimmedString("proxies.host", proxy.Host, true); err != nil {
			return err
		}
		if err := ValidateIntRange("proxies.port", proxy.Port, 1, 65535, true); err != nil {
			return err
		}
		if _, ok := seen[proxy.ID]; ok {
			return fmt.Errorf("duplicate proxy id: %s", proxy.ID)
		}
		seen[proxy.ID] = struct{}{}
	}
	return nil
}

func ValidateAccountProxyReferences(accounts []Account, proxies []Proxy) error {
	if len(accounts) == 0 {
		return nil
	}
	ids := make(map[string]struct{}, len(proxies))
	for _, proxy := range proxies {
		ids[NormalizeProxy(proxy).ID] = struct{}{}
	}
	for _, acc := range accounts {
		proxyID := strings.TrimSpace(acc.ProxyID)
		if proxyID == "" {
			continue
		}
		if _, ok := ids[proxyID]; !ok {
			return fmt.Errorf("account proxy_id references unknown proxy: %s", proxyID)
		}
	}
	return nil
}

func ValidateAdminConfig(admin AdminConfig) error {
	return ValidateIntRange("admin.jwt_expire_hours", admin.JWTExpireHours, 1, 720, false)
}

func ValidateRuntimeConfig(runtime RuntimeConfig) error {
	if err := ValidateIntRange("runtime.account_max_inflight", runtime.AccountMaxInflight, 1, 256, false); err != nil {
		return err
	}
	if err := ValidateIntRange("runtime.account_max_queue", runtime.AccountMaxQueue, 1, 200000, false); err != nil {
		return err
	}
	if err := ValidateIntRange("runtime.global_max_inflight", runtime.GlobalMaxInflight, 1, 200000, false); err != nil {
		return err
	}
	if err := ValidateIntRange("runtime.token_refresh_interval_hours", runtime.TokenRefreshIntervalHours, 1, 720, false); err != nil {
		return err
	}
	if runtime.AccountMaxInflight > 0 && runtime.GlobalMaxInflight > 0 && runtime.GlobalMaxInflight < runtime.AccountMaxInflight {
		return fmt.Errorf("runtime.global_max_inflight must be >= runtime.account_max_inflight")
	}
	if err := ValidateAccountRateLimitConfig(runtime.AccountRateLimit); err != nil {
		return err
	}
	return ValidateMuteReportConfig(runtime.MuteReport)
}

// ValidateAccountRateLimitConfig 校验单账号速率限制参数。
func ValidateAccountRateLimitConfig(cfg AccountRateLimitConfig) error {
	if cfg.MaxPerMinute < 0 {
		return fmt.Errorf("runtime.account_rate_limit.max_per_minute must be >= 1")
	}
	if cfg.MaxPerMinute > 0 {
		return ValidateIntRange("runtime.account_rate_limit.max_per_minute", cfg.MaxPerMinute, 1, 100000, false)
	}
	return nil
}

// ValidateMuteReportConfig 校验禁言/封号上报地址。
// 空地址视为合法（关闭或使用默认地址）；非空时必须是带 host 的 http(s) URL。
func ValidateMuteReportConfig(cfg MuteReportConfig) error {
	raw := strings.TrimSpace(cfg.URL)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("runtime.mute_report.url is not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("runtime.mute_report.url must use http or https scheme")
	}
	if strings.TrimSpace(u.Host) == "" {
		return fmt.Errorf("runtime.mute_report.url must include a host")
	}
	return nil
}

func ValidateResponsesConfig(responses ResponsesConfig) error {
	return ValidateIntRange("responses.store_ttl_seconds", responses.StoreTTLSeconds, 30, 86400, false)
}

func ValidateEmbeddingsConfig(embeddings EmbeddingsConfig) error {
	return ValidateTrimmedString("embeddings.provider", embeddings.Provider, false)
}

func ValidateAutoDeleteConfig(autoDelete AutoDeleteConfig) error {
	return ValidateAutoDeleteMode(autoDelete.Mode)
}

func ValidateInputFileConfig(inputFile InputFileConfig) error {
	if inputFile.MinChars != 0 {
		return ValidateIntRange("input_file.min_chars", inputFile.MinChars, 0, 100000000, true)
	}
	return nil
}

func ValidateExpertPromptSegmentConfig(cfg ExpertPromptSegmentConfig) error {
	if cfg.MaxChars != 0 {
		if err := ValidateIntRange("expert_prompt_segment.max_chars", cfg.MaxChars, 1000, 100000000, true); err != nil {
			return err
		}
	}
	return nil
}

func ValidateExpertTextFileInlineConfig(cfg ExpertTextFileInlineConfig) error {
	if cfg.MaxFileBytes != 0 {
		if err := ValidateIntRange("expert_text_file_inline.max_file_bytes", cfg.MaxFileBytes, 1, 100<<20, true); err != nil {
			return err
		}
	}
	return nil
}

func ValidateAutoRouteVisionConfig(_ AutoRouteVisionConfig) error {
	return nil
}

func ValidateIntRange(name string, value, min, max int, required bool) error {
	if value == 0 && !required {
		return nil
	}
	if value < min || value > max {
		return fmt.Errorf("%s must be between %d and %d", name, min, max)
	}
	return nil
}

func ValidateTrimmedString(name, value string, required bool) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if !required && value == "" {
			return nil
		}
		return fmt.Errorf("%s cannot be empty", name)
	}
	return nil
}

func ValidateAutoDeleteMode(mode string) error {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "", "none", "single", "all":
		return nil
	default:
		return fmt.Errorf("auto_delete.mode must be one of none, single, all")
	}
}
