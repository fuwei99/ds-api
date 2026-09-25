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

func (s *Store) ResponsesStoreTTLSeconds() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.Responses.StoreTTLSeconds > 0 {
		return s.cfg.Responses.StoreTTLSeconds
	}
	return 900
}

func (s *Store) EmbeddingsProvider() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.cfg.Embeddings.Provider)
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

// RuntimeDeviceIDMode 返回全局设备 ID 模式（manual/real，默认 manual）。
// Vercel 部署强制 manual：serverless 文件系统只读（仅 /tmp 可写）且实例随用随弃，
// 设备档案（data/device_profiles）与数美接入配置均无法持久化，每次冷启动重新
// 生成都会得到新的 device_id/设备指纹，频繁变更反而放大风控暴露，故 real 不可用。
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

// CurrentInputFileEnabled 返回是否开启上下文拆分上传（默认关闭）。
// 经过最新实测，deepseek网页端不再限制单次输入长度。此功能已不再需要。因此默认关闭。
func (s *Store) CurrentInputFileEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.InputFile.Enabled == nil {
		return false
	}
	return *s.cfg.InputFile.Enabled
}

func (s *Store) CurrentInputFileMinChars() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.InputFile.MinChars > 0 {
		return s.cfg.InputFile.MinChars
	}
	return 0
}

func (s *Store) ThinkingInjectionEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.ThinkingInjection.Enabled == nil {
		return false
	}
	return *s.cfg.ThinkingInjection.Enabled
}

func (s *Store) ThinkingInjectionPrompt() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.cfg.ThinkingInjection.Prompt)
}

// BottomFormatInjectionEnabled 报告是否在 prompt 末尾注入工具调用格式提醒。
// 与其它行为开关相反，本开关默认开启：未配置(nil)即视为开启，只有显式写入
// false 才会关闭注入。
func (s *Store) BottomFormatInjectionEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.BottomFormatInjection.Enabled == nil {
		return true
	}
	return *s.cfg.BottomFormatInjection.Enabled
}

// BottomFormatInjectionPrompt 返回用户自定义的末尾格式提醒；空字符串表示
// 使用内置默认提醒。
func (s *Store) BottomFormatInjectionPrompt() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.cfg.BottomFormatInjection.Prompt)
}

func (s *Store) ExpertPromptSegmentEnabled() bool {
	return false
}

func (s *Store) ExpertPromptSegmentMaxChars() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.ExpertPromptSegment.MaxChars > 0 {
		return s.cfg.ExpertPromptSegment.MaxChars
	}
	return 160000
}

func (s *Store) ExpertTextFileInlineEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.ExpertTextFileInline.Enabled == nil {
		return true
	}
	return *s.cfg.ExpertTextFileInline.Enabled
}

func (s *Store) ExpertTextFileInlineMaxFileBytes() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.ExpertTextFileInline.MaxFileBytes > 0 {
		return s.cfg.ExpertTextFileInline.MaxFileBytes
	}
	return 3 * 1024 * 1024 // 3 MiB
}

func (s *Store) AutoRouteVisionEnabled() bool {
	return false
}
