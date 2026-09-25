package config

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
)

type Config struct {
	Keys                  []string                    `json:"keys,omitempty"`
	APIKeys               []APIKey                    `json:"api_keys,omitempty"`
	Accounts              []Account                   `json:"accounts,omitempty"`
	Proxies               []Proxy                     `json:"proxies,omitempty"`
	ModelAliases          map[string]string           `json:"model_aliases,omitempty"`
	Admin                 AdminConfig                 `json:"admin,omitempty"`
	Runtime               RuntimeConfig               `json:"runtime,omitempty"`
	Responses             ResponsesConfig             `json:"responses,omitempty"`
	Embeddings            EmbeddingsConfig            `json:"embeddings,omitempty"`
	AutoDelete            AutoDeleteConfig            `json:"auto_delete"`
	InputFile             InputFileConfig             `json:"input_file,omitempty"`
	ThinkingInjection     ThinkingInjectionConfig     `json:"thinking_injection,omitempty"`
	BottomFormatInjection BottomFormatInjectionConfig `json:"bottom_format_injection,omitempty"`
	ExpertTextFileInline  ExpertTextFileInlineConfig  `json:"expert_text_file_inline,omitempty"`
	ExpertPromptSegment   ExpertPromptSegmentConfig   `json:"expert_prompt_segment,omitempty"`
	AutoRouteVision       AutoRouteVisionConfig       `json:"auto_route_vision,omitempty"`
	DeviceIDPool          DeviceIDPoolConfig          `json:"device_id_pool,omitempty"`
	ElasticPool           ElasticPoolConfig           `json:"elastic_pool,omitempty"`
	Mihomo                MihomoConfig                `json:"mihomo,omitempty"`
	Vercel                VercelConfig                `json:"vercel,omitempty"`
	VercelSyncHash        string                      `json:"_vercel_sync_hash,omitempty"`
	VercelSyncTime        int64                       `json:"_vercel_sync_time,omitempty"`
	// ToolMarkerSecret 是派生按 Key 随机工具调用标识的服务端密钥。
	// 首次加载时自动生成并持久化；可通过 DS2API_TOOL_MARKER_SECRET 覆盖。
	ToolMarkerSecret string         `json:"tool_marker_secret,omitempty"`
	AdditionalFields map[string]any `json:"-"`
}

type Account struct {
	Name         string `json:"name,omitempty"`
	Remark       string `json:"remark,omitempty"`
	Email        string `json:"email,omitempty"`
	Mobile       string `json:"mobile,omitempty"`
	Password     string `json:"password,omitempty"`
	Token        string `json:"token,omitempty"`
	DeviceID     string `json:"device_id,omitempty"`
	DeviceIDType string `json:"device_id_type,omitempty"`
	ProxyID      string `json:"proxy_id,omitempty"`
	PoolType     string `json:"pool_type,omitempty"`
	// Priority 是账号在弹性号池选取时的优先级，数值越大越优先被启用。
	// 0 为默认优先级；负数表示低于默认，仅在更高优先级账号不足时才会被启用。
	// 同优先级的账号按配置中的原始顺序选取。
	Priority       int     `json:"priority,omitempty"`
	Locale         string  `json:"locale,omitempty"`
	Disabled       bool    `json:"disabled,omitempty"`
	DisabledReason string  `json:"disabled_reason,omitempty"`
	Banned         bool    `json:"banned,omitempty"`
	MutedUntil     float64 `json:"muted_until,omitempty"`
	// AuthFailed 表示账号被判定为"鉴权异常"：上游连续以 Token 失效拒绝请求，
	// 且重新登录也无法拿到可用 Token（例如密码被改、账号被上游注销）。
	// 与 Banned 相同，弹性号池把它视为不可调度且不占名额——被判定后立即让出
	// 名额，由后面的休眠账号自动补位。成功刷新 Token 或管理员手动启用后清除。
	AuthFailed bool `json:"auth_failed,omitempty"`
	// CooldownUntil 是本地风控冷却到期时间（Unix 秒）。
	// 与 MutedUntil 不同：MutedUntil 由上游下发，CooldownUntil 是我们自己
	// 在收到验证码挑战后主动设置的——挑战意味着风控已经盯上这个账号，
	// 继续用它只会把情况变得更糟。
	CooldownUntil float64 `json:"cooldown_until,omitempty"`
	// NodeCooldownUntil 是坏节点自动安全转移后账号的换号冷却到期时间（Unix 秒）。
	// 冷却期内自动调度不会再次切换该账号的节点，避免故障节点间反复横跳。
	NodeCooldownUntil float64 `json:"node_cooldown_until,omitempty"`
	// NoProxy 表示账号被显式解绑、强制走直连（不走代理）。置位后自动调度
	// （auto_bind 补位 / 一键分配）不再给该账号分配节点，直到用户显式重新绑定。
	NoProxy bool `json:"no_proxy,omitempty"`
}

type APIKey struct {
	Key    string `json:"key"`
	Name   string `json:"name,omitempty"`
	Remark string `json:"remark,omitempty"`
	// ForceDisableTools 强制禁用工具调用，默认关闭。
	// 开启后，使用该 Key 的请求会在标准化前被强制清除工具定义
	// （结构化 tools / tool_choice），system 文本工具关键词判定一并失效，
	// 且不注入任何工具提示词。
	ForceDisableTools bool `json:"force_disable_tools,omitempty"`
}

type Proxy struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Type     string `json:"type,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

func NormalizeProxy(p Proxy) Proxy {
	p.ID = strings.TrimSpace(p.ID)
	p.Name = strings.TrimSpace(p.Name)
	p.Type = strings.ToLower(strings.TrimSpace(p.Type))
	p.Host = strings.TrimSpace(p.Host)
	p.Username = strings.TrimSpace(p.Username)
	p.Password = strings.TrimSpace(p.Password)
	if p.ID == "" {
		p.ID = StableProxyID(p)
	}
	if p.Name == "" && p.Host != "" && p.Port > 0 {
		p.Name = fmt.Sprintf("%s:%d", p.Host, p.Port)
	}
	return p
}

func StableProxyID(p Proxy) string {
	sum := sha1.Sum([]byte(strings.ToLower(strings.TrimSpace(p.Type)) + "|" + strings.ToLower(strings.TrimSpace(p.Host)) + "|" + fmt.Sprintf("%d", p.Port) + "|" + strings.TrimSpace(p.Username)))
	return "proxy_" + hex.EncodeToString(sum[:6])
}

func (c *Config) ClearAccountTokens() {
	if c == nil {
		return
	}
	for i := range c.Accounts {
		c.Accounts[i].Token = ""
	}
}

func (c *Config) NormalizeCredentials() {
	if c == nil {
		return
	}
	normalizedAPIKeys := normalizeAPIKeys(c.APIKeys)
	if len(normalizedAPIKeys) > 0 {
		c.APIKeys = normalizedAPIKeys
		c.Keys = apiKeysToStrings(c.APIKeys)
	} else {
		c.Keys = normalizeKeys(c.Keys)
		c.APIKeys = apiKeysFromStrings(c.Keys, nil)
	}

	for i := range c.Accounts {
		c.Accounts[i].Name = strings.TrimSpace(c.Accounts[i].Name)
		c.Accounts[i].Remark = strings.TrimSpace(c.Accounts[i].Remark)
		c.Accounts[i].DeviceID = strings.TrimSpace(c.Accounts[i].DeviceID)
		c.Accounts[i].DeviceIDType = NormalizeDeviceIDType(c.Accounts[i].DeviceIDType)
		c.Accounts[i].Locale = strings.TrimSpace(c.Accounts[i].Locale)
		c.Accounts[i].PoolType = NormalizePoolType(c.Accounts[i].PoolType)
	}

	c.Runtime.DeviceIDMode = NormalizeDeviceIDMode(c.Runtime.DeviceIDMode)
	c.Runtime.MuteReport.URL = strings.TrimSpace(c.Runtime.MuteReport.URL)
	c.DeviceIDPool.Items = NormalizeDeviceIDPool(c.DeviceIDPool.Items)
	// 号池的已绑定账号数是派生数据：每次配置变更后按账号实际绑定情况重算，
	// 顺带解除不可用账号（已关闭/休眠/禁言/封禁）的绑定。
	ReconcileDeviceIDBindings(c)

	c.Vercel = NormalizeVercelConfig(c.Vercel)
	c.Mihomo = NormalizeMihomoConfig(c.Mihomo)
	c.normalizeModelAliases()
}

// DropInvalidAccounts removes accounts that cannot be addressed by admin APIs
// (no email and no normalizable mobile). This prevents legacy token-only
// records from becoming orphaned empty entries after token stripping.
func (c *Config) DropInvalidAccounts() {
	if c == nil || len(c.Accounts) == 0 {
		return
	}
	kept := make([]Account, 0, len(c.Accounts))
	for _, acc := range c.Accounts {
		if acc.Identifier() == "" {
			continue
		}
		kept = append(kept, acc)
	}
	c.Accounts = kept
}

func (c *Config) normalizeModelAliases() {
	if c == nil {
		return
	}

	aliases := map[string]string{}
	for k, v := range c.ModelAliases {
		key := strings.TrimSpace(lower(k))
		val := strings.TrimSpace(lower(v))
		if key == "" || val == "" {
			continue
		}
		aliases[key] = val
	}
	if len(aliases) == 0 {
		c.ModelAliases = nil
	} else {
		c.ModelAliases = aliases
	}
}

type AdminConfig struct {
	PasswordHash      string `json:"password_hash,omitempty"`
	JWTExpireHours    int    `json:"jwt_expire_hours,omitempty"`
	JWTValidAfterUnix int64  `json:"jwt_valid_after_unix,omitempty"`
}

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
	// 管理界面手工录入的 device_id 号池）或 "real"（数美 fp SDK 生成）。
	// 历史配置中的 "random" 会被自动迁移为 "manual"。
	// Vercel 部署下强制 manual（serverless 无法持久化设备档案与数美配置，
	// 每次冷启动都会生成新 device_id），配置的 real 会被忽略。
	DeviceIDMode string `json:"device_id_mode,omitempty"`
	// DevidPublicKey 覆盖数美 fp SDK 的公钥。
	// 为空时回退读取 data/devid/public_key 文件。
	DevidPublicKey string `json:"devid_public_key,omitempty"`
	// MuteReport 控制账号被上游临时禁言/永久封号时的外部上报。
	MuteReport MuteReportConfig `json:"mute_report,omitempty"`
	// AccountRateLimit 控制单个账号每分钟最大请求数的速率限制。
	AccountRateLimit AccountRateLimitConfig `json:"account_rate_limit,omitempty"`
}

// AccountRateLimitConfig 描述单账号速率限制的开关与每分钟最大请求数。
type AccountRateLimitConfig struct {
	Enabled      *bool `json:"enabled,omitempty"`
	MaxPerMinute int   `json:"max_per_minute,omitempty"`
}

// DefaultAccountRateLimitMaxPerMinute 是单账号每分钟最大请求数的默认值。
const DefaultAccountRateLimitMaxPerMinute = 5

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

type ResponsesConfig struct {
	StoreTTLSeconds int `json:"store_ttl_seconds,omitempty"`
}

type EmbeddingsConfig struct {
	Provider string `json:"provider,omitempty"`
}

type AutoDeleteConfig struct {
	Mode     string `json:"mode,omitempty"`
	Sessions bool   `json:"sessions,omitempty"`
}

// InputFileConfig 独立拆分配置。
// 经过最新实测，deepseek网页端不再限制单次输入长度。此功能已不再需要。因此默认关闭。
type InputFileConfig struct {
	Enabled  *bool `json:"enabled,omitempty"`
	MinChars int   `json:"min_chars,omitempty"`
}

type ThinkingInjectionConfig struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
}

// BottomFormatInjectionConfig 是「底部格式提示注入」行为设置。
// 与其它行为开关不同，本开关默认开启：Enabled 为 nil 视为开启，只有显式
// 写入 false 才关闭。Prompt 非空时覆盖内置的末尾工具调用格式提醒。
type BottomFormatInjectionConfig struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
}

type ExpertPromptSegmentConfig struct {
	Enabled  *bool `json:"enabled,omitempty"`
	MaxChars int   `json:"max_chars,omitempty"`
}

type ExpertTextFileInlineConfig struct {
	Enabled      *bool `json:"enabled,omitempty"`
	MaxFileBytes int   `json:"max_file_bytes,omitempty"`
}

type AutoRouteVisionConfig struct {
	Enabled *bool `json:"enabled,omitempty"`
}

// ElasticPoolConfig 控制弹性号池行为。
// 开启后按账号优先级从高到低在可调度账号中启用前 N 个，
// 其余禁用；封号(muted/banned)账号一律禁用且不占名额。
// PerPool=false 时所有账号共用 GlobalCount；PerPool=true 时
// default/no_tools/tools_only 三种号池类型分别使用各自的 Count。
type ElasticPoolConfig struct {
	Enabled        bool `json:"enabled,omitempty"`
	PerPool        bool `json:"per_pool,omitempty"`
	GlobalCount    int  `json:"global_count,omitempty"`
	DefaultCount   int  `json:"default_count,omitempty"`
	NoToolsCount   int  `json:"no_tools_count,omitempty"`
	ToolsOnlyCount int  `json:"tools_only_count,omitempty"`
}

type VercelConfig struct {
	Token     string `json:"token,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	TeamID    string `json:"team_id,omitempty"`
}

func NormalizeVercelConfig(v VercelConfig) VercelConfig {
	return VercelConfig{
		Token:     strings.TrimSpace(v.Token),
		ProjectID: strings.TrimSpace(v.ProjectID),
		TeamID:    strings.TrimSpace(v.TeamID),
	}
}

func (c *Config) ClearVercelCredentials() {
	if c == nil {
		return
	}
	c.Vercel = VercelConfig{}
}
