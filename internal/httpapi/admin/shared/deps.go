package shared

import (
	"context"
	"net/http"

	"ds2api/internal/account"
	"ds2api/internal/auth"
	"ds2api/internal/config"
	dsclient "ds2api/internal/deepseek/client"
)

type ConfigStore interface {
	Snapshot() config.Config
	Keys() []string
	HasAPIKey(k string) bool
	APIKeyFlag(k, flag string) bool
	Accounts() []config.Account
	FindAccount(identifier string) (config.Account, bool)
	UpdateAccountToken(identifier, token string) error
	UpdateAccountTestStatus(identifier, status string) error
	AccountTestStatus(identifier string) (string, bool)
	Update(mutator func(*config.Config) error) error
	ExportJSONAndBase64() (string, string, error)
	IsEnvBacked() bool
	IsEnvWritebackEnabled() bool
	HasEnvConfigSource() bool
	ConfigPath() string
	SetVercelSync(hash string, ts int64) error
	AdminPasswordHash() string
	AdminJWTExpireHours() int
	AdminJWTValidAfterUnix() int64
	RuntimeAccountMaxInflight() int
	RuntimeAccountMaxQueue(defaultSize int) int
	RuntimeGlobalMaxInflight(defaultSize int) int
	RuntimeTokenRefreshIntervalHours() int
	RuntimeDeviceIDMode() string
	DeviceIDPoolItems() []config.DeviceIDPoolItem
	DeviceIDPoolSize() int
	RuntimeMuteReportEnabled() bool
	RuntimeMuteReportURL() string
	RuntimeAccountRateLimitEnabled() bool
	RuntimeAccountRateLimitMaxPerMinute() int
	RuntimeAccountDailyLimitEnabled() bool
	RuntimeAccountDailyLimitMaxPerDay() int
	AutoDeleteMode() string
	AutoDeleteSessions() bool
	// The setting readers below cover every setting declared in
	// internal/config/settings.go. They are declared once for the whole admin
	// surface, so a new registered setting never needs a new interface method.
	// SettingValues reports the effective value of every field of one setting,
	// which is what the settings API hands to the web UI.
	SettingValues(key string) map[string]any
	SettingBool(key, field string) bool
	SettingInt(key, field string) int
	SettingString(key, field string) string
	// SectionValues reports the effective value of every field of one registered
	// section (internal/config/sections.go), nested groups included. Like the
	// setting readers above it is generic over the registry, so a new section —
	// or a new field on one — never needs a new interface method.
	SectionValues(key string) map[string]any
}

type PoolController interface {
	Reset()
	Status() map[string]any
	SchedulableCount() int
	ApplyRuntimeLimits(maxInflightPerAccount, maxQueueSize, globalMaxInflight int)
	SyncRateLimits()
	SyncDailyLimits()
	// InUseIDs 返回正在生成回复（有在途请求）的账号标识快照，供弹性号池
	// 重算时保护在途账号不被踢下线（见 account.ReconcileElasticPoolWithBusy）。
	InUseIDs() map[string]bool
}

type OpenAIChatCaller interface {
	ChatCompletions(w http.ResponseWriter, r *http.Request)
}

type DeepSeekCaller interface {
	Login(ctx context.Context, acc config.Account) (string, error)
	CreateSession(ctx context.Context, a *auth.RequestAuth, maxAttempts int) (string, error)
	GetPow(ctx context.Context, a *auth.RequestAuth, maxAttempts int) (string, error)
	CallCompletion(ctx context.Context, a *auth.RequestAuth, payload map[string]any, powResp string, maxAttempts int) (*http.Response, error)
	GetSessionCountForToken(ctx context.Context, token string) (*dsclient.SessionStats, error)
	DeleteAllSessionsForToken(ctx context.Context, token string) error
}

var _ ConfigStore = (*config.Store)(nil)
var _ PoolController = (*account.Pool)(nil)
var _ DeepSeekCaller = (*dsclient.Client)(nil)
