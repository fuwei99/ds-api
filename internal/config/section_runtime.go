package config

import (
	"encoding/json"
	"strings"
)

// RuntimeConfig 是 runtime 段：并发/队列上限、Token 复用时长、设备 ID 来源、
// 禁言上报与单账号速率限制。它同时是一个注册段（见文件末尾的 RegisterSection），
// 所以编解码、Clone、校验、归一化、导入合并与设置页读写都从那一处派生。
type RuntimeConfig struct {
	AccountMaxInflight int `json:"account_max_inflight,omitempty"`
	AccountMaxQueue    int `json:"account_max_queue,omitempty"`
	GlobalMaxInflight  int `json:"global_max_inflight,omitempty"`
	// TokenRefreshIntervalHours 是托管账号 Token 的最长复用时长（小时，默认 6）。
	// 语义是"取号时的 TTL 检查"，不是定时刷新：仅当账号被请求调度到时，
	// auth.Resolver.shouldForceRefresh 才比较距上次登录的时长，超过则先重新
	// 登录再处理本次请求。因此长期无请求的账号不会被刷新；且 Token 与计时
	// 都只存在于进程内存中，服务重启后一并清零。
	TokenRefreshIntervalHours int `json:"token_refresh_interval_hours,omitempty"`
	// DeviceIDMode 控制账号设备 ID 的来源："manual"（默认，使用用户在账号
	// 管理界面手工录入的 device_id 号池）或 "auto"（数美 fp SDK 自动生成）。
	// 历史配置中的 "random" 会被自动迁移为 "manual"，旧名 "real" 会被自动
	// 迁移为 "auto"。
	// Vercel 部署下强制 manual（serverless 无法持久化设备档案与数美配置，
	// 每次冷启动都会生成新 device_id），配置的 auto 会被忽略。
	DeviceIDMode string `json:"device_id_mode,omitempty"`
	// DevidPublicKey 覆盖数美 fp SDK 的公钥。
	// 为空时回退读取 data/devid/public_key 文件。
	DevidPublicKey string `json:"devid_public_key,omitempty"`
	// MuteReport 控制账号被上游临时禁言/永久封号时的外部上报。
	MuteReport MuteReportConfig `json:"mute_report,omitempty"`
	// AccountRateLimit 控制单个账号每分钟最大请求数的速率限制。
	AccountRateLimit AccountRateLimitConfig `json:"account_rate_limit,omitempty"`
	// AccountDailyLimit 控制单个账号每天最大请求数的速率限制（"24h 速率限制"）。
	AccountDailyLimit AccountDailyLimitConfig `json:"account_daily_limit,omitempty"`
}

// AccountRateLimitConfig 描述单账号速率限制的开关与每分钟最大请求数。
type AccountRateLimitConfig struct {
	Enabled      *bool `json:"enabled,omitempty"`
	MaxPerMinute int   `json:"max_per_minute,omitempty"`
}

// AccountDailyLimitConfig 描述 24h 速率限制的开关与每天最大请求数。
// MaxPerDay 刻意没有默认值：未填写（0，配置文件里表现为该键不存在）时即使
// 开关打开也不生效，所以出厂状态是"关闭且不填数值"，由操作者自己决定上限。
// 计数按 GMT+8 自然日分桶（与用量账本同一口径），跨天重置，并持久化到
// data/account_daily_limit.json，使重启沿用它。
type AccountDailyLimitConfig struct {
	Enabled   *bool `json:"enabled,omitempty"`
	MaxPerDay int   `json:"max_per_day,omitempty"`
}

// DefaultAccountRateLimitMaxPerMinute 是单账号每分钟最大请求数的默认值。
const DefaultAccountRateLimitMaxPerMinute = 5

// MaxAccountDailyLimitMaxPerDay 是每天最大请求数可填的上限。下限是 0 且 0 表示
// "没填"：与 account_max_queue / global_max_inflight 一样，写 0 等于把这个数值
// 交回给默认行为（这里是不限额），也让接口能明确表达"清除该数值"。
const MaxAccountDailyLimitMaxPerDay = 1000000

// MuteReportConfig 描述禁言/封号事件上报的开关与目标地址。
// 仅在 Enabled 为 true 且 URL 非空时上报；URL 为空时回退默认地址
// DefaultMuteReportURL。
type MuteReportConfig struct {
	Enabled *bool  `json:"enabled,omitempty"`
	URL     string `json:"url,omitempty"`
}

// DefaultMuteReportURL 是禁言/封号上报的默认目标地址。
// 面板上以该值预填输入框，服务端在 URL 为空时同样回退到这里。
const DefaultMuteReportURL = "http://127.0.0.1:8100"

// DefaultRuntimeRecommended 是并发/队列上限的推荐值：账号数 × 单账号并发，
// 没有可用账号时退化为单账号并发。设置接口报给前端的队列与全局上限、以及
// 更新后写回号池的上限都用它，所以它只写在这里。
func DefaultRuntimeRecommended(accountCount, maxPer int) int {
	if maxPer <= 0 {
		maxPer = 1
	}
	if accountCount <= 0 {
		return maxPer
	}
	return accountCount * maxPer
}

// recommendedRuntimeMaxQueue 是当前账号规模下的推荐队列上限，作为
// runtime.account_max_queue / global_max_inflight 未配置时的读取值。
func recommendedRuntimeMaxQueue(s *Store) int {
	if s == nil {
		return DefaultRuntimeRecommended(0, 1)
	}
	return DefaultRuntimeRecommended(len(s.Accounts()), s.RuntimeAccountMaxInflight())
}

// cloneRuntimeConfig 复制运行时配置。RuntimeConfig 内仅 MuteReport.Enabled 与
// AccountRateLimit.Enabled / AccountDailyLimit.Enabled 是引用类型，需要单独
// 深拷贝，避免调用方修改快照时污染 Store 内部状态。
func cloneRuntimeConfig(in RuntimeConfig) RuntimeConfig {
	out := in
	out.MuteReport.Enabled = cloneBoolPtr(in.MuteReport.Enabled)
	out.AccountRateLimit.Enabled = cloneBoolPtr(in.AccountRateLimit.Enabled)
	out.AccountDailyLimit.Enabled = cloneBoolPtr(in.AccountDailyLimit.Enabled)
	return out
}

// runtimeConfigIsSet 报告 runtime 段是否带有需要落盘的内容：任何一项非零都要
// 写出，否则只配置了 account_rate_limit 或 devid_public_key 的实例会在保存时
// 静默丢掉它们。
func runtimeConfigIsSet(rt RuntimeConfig) bool {
	return rt.AccountMaxInflight > 0 || rt.AccountMaxQueue > 0 || rt.GlobalMaxInflight > 0 ||
		rt.TokenRefreshIntervalHours > 0 || rt.DeviceIDMode != "" || strings.TrimSpace(rt.DevidPublicKey) != "" ||
		rt.MuteReport.Enabled != nil || strings.TrimSpace(rt.MuteReport.URL) != "" ||
		rt.AccountRateLimit.Enabled != nil || rt.AccountRateLimit.MaxPerMinute > 0 ||
		rt.AccountDailyLimit.Enabled != nil || rt.AccountDailyLimit.MaxPerDay > 0
}

func init() {
	RegisterSection(SectionSpec{
		Key: "runtime",
		Form: &SectionForm{
			Order:    2,
			TitleKey: "settings.runtimeTitle",
			Fields: []SectionField{
				SectionInt("runtime.account_max_inflight", "settings.accountMaxInflight", 1, 256,
					func(s *Store) int { return s.RuntimeAccountMaxInflight() },
					func(c *Config, v int) { c.Runtime.AccountMaxInflight = v }),
				SectionInt("runtime.account_max_queue", "settings.accountMaxQueue", 1, 200000,
					func(s *Store) int { return s.RuntimeAccountMaxQueue(recommendedRuntimeMaxQueue(s)) },
					func(c *Config, v int) { c.Runtime.AccountMaxQueue = v }),
				SectionInt("runtime.global_max_inflight", "settings.globalMaxInflight", 1, 200000,
					func(s *Store) int { return s.RuntimeGlobalMaxInflight(recommendedRuntimeMaxQueue(s)) },
					func(c *Config, v int) { c.Runtime.GlobalMaxInflight = v }),
				SectionInt("runtime.token_refresh_interval_hours", "settings.tokenRefreshIntervalHours", 1, 720,
					func(s *Store) int { return s.RuntimeTokenRefreshIntervalHours() },
					func(c *Config, v int) { c.Runtime.TokenRefreshIntervalHours = v }),
				// 旧名 real 在写入侧一并迁移：仍发旧名的客户端与携带旧配置的
				// 合并导入都按 auto 生效，而不是被枚举校验拒绝。
				withRenamedValues(SectionEnum("runtime.device_id_mode", "settings.deviceIdMode",
					[]SectionOption{
						{Value: DeviceIDTypeManual, LabelKey: "settings.deviceIdModeManual"},
						{Value: DeviceIDTypeAuto, LabelKey: "settings.deviceIdModeAuto"},
					},
					func(s *Store) string { return s.RuntimeDeviceIDMode() },
					func(c *Config, v string) error {
						c.Runtime.DeviceIDMode = NormalizeDeviceIDMode(v)
						return nil
					}),
					map[string]string{deviceIDTypeLegacyReal: DeviceIDTypeAuto}),
				SectionGroup("runtime.mute_report", "",
					withHelp(SectionBool("runtime.mute_report.enabled", "settings.muteReportEnabled",
						func(s *Store) bool { return s.RuntimeMuteReportEnabled() },
						func(c *Config, v bool) { c.Runtime.MuteReport.Enabled = &v }),
						"settings.muteReportDesc"),
					withHelp(SectionString("runtime.mute_report.url", "settings.muteReportUrl",
						func(s *Store) string { return s.RuntimeMuteReportURL() },
						func(c *Config, v string) { c.Runtime.MuteReport.URL = v }),
						"settings.muteReportUrlDesc"),
				),
				SectionGroup("runtime.account_rate_limit", "",
					withHelp(SectionBool("runtime.account_rate_limit.enabled", "settings.accountRateLimitEnabled",
						func(s *Store) bool { return s.RuntimeAccountRateLimitEnabled() },
						func(c *Config, v bool) { c.Runtime.AccountRateLimit.Enabled = &v }),
						"settings.accountRateLimitDesc"),
					withHelp(SectionInt("runtime.account_rate_limit.max_per_minute", "settings.accountRateLimitLimit", 1, 100000,
						func(s *Store) int { return s.RuntimeAccountRateLimitMaxPerMinute() },
						func(c *Config, v int) { c.Runtime.AccountRateLimit.MaxPerMinute = v }),
						"settings.accountRateLimitHelp"),
				),
				// 每天上限刻意用"未填即不生效"的写法（SectionOptionalInt）：开关
				// 打开但没填数值时，界面上是一个空输入框，而不是一个用户没输入过的
				// 0。写入侧把 0 存回去，等效于清除该数值。
				SectionGroup("runtime.account_daily_limit", "",
					withHelp(SectionBool("runtime.account_daily_limit.enabled", "settings.accountDailyLimitEnabled",
						func(s *Store) bool { return s.RuntimeAccountDailyLimitEnabled() },
						func(c *Config, v bool) { c.Runtime.AccountDailyLimit.Enabled = &v }),
						"settings.accountDailyLimitDesc"),
					withHelp(SectionOptionalInt("runtime.account_daily_limit.max_per_day", "settings.accountDailyLimitMaxPerDay",
						0, MaxAccountDailyLimitMaxPerDay,
						runtimeAccountDailyLimitMaxPerDay,
						func(c *Config, v int) { c.Runtime.AccountDailyLimit.MaxPerDay = v }),
						"settings.accountDailyLimitHelp"),
				),
			},
		},
		Decode: func(c *Config, raw json.RawMessage) error {
			return json.Unmarshal(raw, &c.Runtime)
		},
		Encode: func(c Config) (any, bool) {
			if !runtimeConfigIsSet(c.Runtime) {
				return nil, false
			}
			return c.Runtime, true
		},
		Clone: func(dst, src *Config) {
			dst.Runtime = cloneRuntimeConfig(src.Runtime)
		},
		Normalize: func(c *Config) {
			c.Runtime.DeviceIDMode = NormalizeDeviceIDMode(c.Runtime.DeviceIDMode)
			c.Runtime.MuteReport.URL = strings.TrimSpace(c.Runtime.MuteReport.URL)
		},
		Validate: func(c Config) error {
			return ValidateRuntimeConfig(c.Runtime)
		},
	})
}

// withHelp attaches a help locale key to a declared field, so a field helper's
// call stays one expression.
func withHelp(f SectionField, helpKey string) SectionField {
	f.HelpKey = helpKey
	return f
}

// runtimeAccountDailyLimitMaxPerDay 报告每天的请求上限，以及它是否真的被填了值。
// SectionOptionalInt 需要这个区分：未填时设置接口下发 null，前端渲染成空输入框，
// 而不是一个用户没输入过的 0。
func runtimeAccountDailyLimitMaxPerDay(s *Store) (int, bool) {
	v := s.RuntimeAccountDailyLimitMaxPerDay()
	return v, v > 0
}
