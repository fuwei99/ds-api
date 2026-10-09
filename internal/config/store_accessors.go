package config

import (
	"os"
	"strconv"
	"strings"
)

func (s *Store) ModelAliases() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := DefaultModelAliases()
	for k, v := range s.cfg.ModelAliases {
		key := strings.TrimSpace(lower(k))
		val := strings.TrimSpace(lower(v))
		if key == "" || val == "" {
			continue
		}
		out[key] = val
	}
	return out
}

func (s *Store) ToolcallMode() string {
	return "feature_match"
}

func (s *Store) ToolcallEarlyEmitConfidence() string {
	return "high"
}

// The accessors below read registered settings (see settings.go). The registry
// owns the storage, the default and the range; an accessor is only the named
// read site, so changing a default or a bound is a change to the registry entry
// alone. Where a setting's effective value is deliberately not what the config
// says — a kill switch — the accessor keeps that decision, because it is
// behaviour rather than plumbing.

func (s *Store) ResponsesStoreTTLSeconds() int {
	return s.SettingInt("responses", "store_ttl_seconds")
}

func (s *Store) EmbeddingsProvider() string {
	return s.SettingString("embeddings", "provider")
}

func (s *Store) AutoDeleteMode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	mode := strings.ToLower(strings.TrimSpace(s.cfg.AutoDelete.Mode))
	switch mode {
	case "none", "single", "all":
		return mode
	}
	if s.cfg.AutoDelete.Sessions {
		return "all"
	}
	return "none"
}

func (s *Store) AdminPasswordHash() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.cfg.Admin.PasswordHash)
}

func (s *Store) AdminJWTExpireHours() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.Admin.JWTExpireHours > 0 {
		return s.cfg.Admin.JWTExpireHours
	}
	if raw := strings.TrimSpace(os.Getenv("DS2API_JWT_EXPIRE_HOURS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return 24
}

func (s *Store) AdminJWTValidAfterUnix() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Admin.JWTValidAfterUnix
}

func (s *Store) RuntimeAccountMaxInflight() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.Runtime.AccountMaxInflight > 0 {
		return s.cfg.Runtime.AccountMaxInflight
	}
	if raw := strings.TrimSpace(os.Getenv("DS2API_ACCOUNT_MAX_INFLIGHT")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return 2
}

func (s *Store) RuntimeAccountMaxQueue(defaultSize int) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.Runtime.AccountMaxQueue > 0 {
		return s.cfg.Runtime.AccountMaxQueue
	}
	if raw := strings.TrimSpace(os.Getenv("DS2API_ACCOUNT_MAX_QUEUE")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			return n
		}
	}
	if defaultSize < 0 {
		return 0
	}
	return defaultSize
}

func (s *Store) RuntimeGlobalMaxInflight(defaultSize int) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.Runtime.GlobalMaxInflight > 0 {
		return s.cfg.Runtime.GlobalMaxInflight
	}
	if raw := strings.TrimSpace(os.Getenv("DS2API_GLOBAL_MAX_INFLIGHT")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	if defaultSize < 0 {
		return 0
	}
	return defaultSize
}

func (s *Store) RuntimeTokenRefreshIntervalHours() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.Runtime.TokenRefreshIntervalHours > 0 {
		return s.cfg.Runtime.TokenRefreshIntervalHours
	}
	return 6
}

// RuntimeDeviceIDMode 返回全局设备 ID 模式（manual/auto，默认 manual）。
// Vercel 部署强制 manual：serverless 文件系统只读（仅 /tmp 可写）且实例随用随弃，
// 设备档案（data/device_profiles）与数美接入配置均无法持久化，每次冷启动重新
// 生成都会得到新的 device_id/设备指纹，频繁变更反而放大风控暴露，故 auto 不可用。
func (s *Store) RuntimeDeviceIDMode() string {
	if IsVercel() {
		return DeviceIDTypeManual
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return NormalizeDeviceIDMode(s.cfg.Runtime.DeviceIDMode)
}

// DeviceIDPoolItems 返回手工 device_id 号池的副本。
func (s *Store) DeviceIDPoolItems() []DeviceIDPoolItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.cfg.DeviceIDPool.Items) == 0 {
		return nil
	}
	out := make([]DeviceIDPoolItem, len(s.cfg.DeviceIDPool.Items))
	copy(out, s.cfg.DeviceIDPool.Items)
	return out
}

// DeviceIDPoolSize 返回号池中可用的 device_id 数量。
func (s *Store) DeviceIDPoolSize() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.cfg.DeviceIDPool.Items)
}

// FindDeviceIDPoolItem 按 id 查找号池条目。
func (s *Store) FindDeviceIDPoolItem(deviceID string) (DeviceIDPoolItem, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.cfg.DeviceIDPool.Items {
		if item.ID == strings.TrimSpace(deviceID) {
			return item, true
		}
	}
	return DeviceIDPoolItem{}, false
}

// RuntimeMuteReportEnabled 返回是否开启账号禁言/封号上报（默认关闭）。
func (s *Store) RuntimeMuteReportEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.Runtime.MuteReport.Enabled == nil {
		return false
	}
	return *s.cfg.Runtime.MuteReport.Enabled
}

// RuntimeMuteReportURL 返回禁言/封号上报地址。未配置时回退默认地址。
func (s *Store) RuntimeMuteReportURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if raw := strings.TrimSpace(s.cfg.Runtime.MuteReport.URL); raw != "" {
		return raw
	}
	return DefaultMuteReportURL
}

// RuntimeDevidPublicKey 返回 config 中覆盖的数美公钥（可为空）。
func (s *Store) RuntimeDevidPublicKey() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.cfg.Runtime.DevidPublicKey)
}

func (s *Store) AutoDeleteSessions() bool {
	return s.AutoDeleteMode() != "none"
}

// RuntimeAccountRateLimitEnabled 返回是否开启单账号速率限制（默认关闭）。
func (s *Store) RuntimeAccountRateLimitEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.Runtime.AccountRateLimit.Enabled == nil {
		return false
	}
	return *s.cfg.Runtime.AccountRateLimit.Enabled
}

// RuntimeAccountRateLimitMaxPerMinute 返回单账号每分钟最大请求数（默认 5）。
func (s *Store) RuntimeAccountRateLimitMaxPerMinute() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.Runtime.AccountRateLimit.MaxPerMinute > 0 {
		return s.cfg.Runtime.AccountRateLimit.MaxPerMinute
	}
	return DefaultAccountRateLimitMaxPerMinute
}

// RuntimeAccountDailyLimitEnabled 返回是否开启单账号 24h 速率限制（默认关闭）。
func (s *Store) RuntimeAccountDailyLimitEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.Runtime.AccountDailyLimit.Enabled == nil {
		return false
	}
	return *s.cfg.Runtime.AccountDailyLimit.Enabled
}

// RuntimeAccountDailyLimitMaxPerDay 返回单账号每天最大请求数。它刻意没有默认值：
// 未填写时返回 0，读取方（号池）据此判定"开关开着但没有上限，因此不生效"。
func (s *Store) RuntimeAccountDailyLimitMaxPerDay() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Runtime.AccountDailyLimit.MaxPerDay
}

// CurrentInputFileEnabled 返回是否开启上下文拆分上传（默认关闭）。
// 经过最新实测，deepseek网页端不再限制单次输入长度。此功能已不再需要。因此默认关闭。
func (s *Store) CurrentInputFileEnabled() bool {
	return s.SettingBool("input_file", "enabled")
}

func (s *Store) CurrentInputFileMinChars() int {
	return s.SettingInt("input_file", "min_chars")
}

func (s *Store) ThinkingInjectionEnabled() bool {
	return s.SettingBool("thinking_injection", "enabled")
}

func (s *Store) ThinkingInjectionPrompt() string {
	return s.SettingString("thinking_injection", "prompt")
}

// BottomFormatInjectionEnabled 报告是否在 prompt 末尾注入工具调用格式提醒。
// 与其它行为开关相反，本开关默认开启：未配置即视为开启，只有显式写入 false
// 才会关闭注入。默认值由注册表声明。
func (s *Store) BottomFormatInjectionEnabled() bool {
	return s.SettingBool("bottom_format_injection", "enabled")
}

// BottomFormatInjectionPrompt 返回用户自定义的末尾格式提醒；空字符串表示
// 使用内置默认提醒。
func (s *Store) BottomFormatInjectionPrompt() string {
	return s.SettingString("bottom_format_injection", "prompt")
}

// ExpertPromptSegmentEnabled 恒为 false：该功能已停用。注册表里的 enabled 字段
// 声明了 ForceValue:false，因此这里、设置接口和配置文件读取三处得到的都是 false，
// 历史配置里的 enabled=true 不会把已停用的分段逻辑重新打开。
func (s *Store) ExpertPromptSegmentEnabled() bool {
	return s.SettingBool("expert_prompt_segment", "enabled")
}

func (s *Store) ExpertPromptSegmentMaxChars() int {
	return s.SettingInt("expert_prompt_segment", "max_chars")
}

func (s *Store) ExpertTextFileInlineEnabled() bool {
	return s.SettingBool("expert_text_file_inline", "enabled")
}

func (s *Store) ExpertTextFileInlineMaxFileBytes() int {
	return s.SettingInt("expert_text_file_inline", "max_file_bytes")
}

// AutoRouteVisionEnabled 恒为 false：自动视觉路由已停用，理由同
// ExpertPromptSegmentEnabled（注册表里同样是 ForceValue:false）。
func (s *Store) AutoRouteVisionEnabled() bool {
	return s.SettingBool("auto_route_vision", "enabled")
}
