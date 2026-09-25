package config

import (
	"strings"
	"time"
)

const (
	// PoolTypeDefault 允许无工具调用和含工具调用的请求使用此账号。
	PoolTypeDefault = "default"
	// PoolTypeNoTools 仅允许无工具调用的请求使用此账号。
	// 携带工具定义（请求 body 的 tools 非空）的请求不会调度到此账号。
	PoolTypeNoTools = "no_tools"
	// PoolTypeToolsOnly 仅允许含工具调用的请求。
	// 不携带工具定义（请求 body 的 tools 为空）的请求不会调度到此账号。
	PoolTypeToolsOnly = "tools_only"
)

const (
	// DeviceIDTypeManual 表示 device_id 由用户在管理界面手工录入的号池分配（默认）。
	DeviceIDTypeManual = "manual"
	// DeviceIDTypeReal 表示 device_id 由数美 fp SDK 真实生成。
	DeviceIDTypeReal = "real"
	// deviceIDTypeLegacyRandom 是已废弃的"本地随机生成"标注。上游已对随机
	// 生成的设备指纹统一返回 RISK_DEVICE_DETECTED，因此该模式被删除，
	// 读到历史值时一律迁移为 manual。
	deviceIDTypeLegacyRandom = "random"
)

// NormalizeDeviceIDMode 规范化全局设备 ID 生成模式。
// 空值、未知值以及历史遗留的 random 一律视为 manual。
func NormalizeDeviceIDMode(mode string) string {
	if strings.ToLower(strings.TrimSpace(mode)) == DeviceIDTypeReal {
		return DeviceIDTypeReal
	}
	return DeviceIDTypeManual
}

// EffectiveDeviceIDMode 返回实际生效的设备 ID 模式：Vercel 部署无法持久化
// 设备档案与数美配置，real 不可用，一律按 manual 处理。
func EffectiveDeviceIDMode(mode string) string {
	if IsVercel() {
		return DeviceIDTypeManual
	}
	return NormalizeDeviceIDMode(mode)
}

// NormalizeDeviceIDType 规范化账号 device_id 的类型标注。
// 历史遗留的 random 会被迁移为 manual（随机 ID 已无法使用）；
// 空值保持为空：空类型表示 device_id 来源未标注（历史遗留或用户手动指定），
// 视为已固定的设备，不参与模式切换触发的重新生成。
func NormalizeDeviceIDType(deviceIDType string) string {
	switch strings.ToLower(strings.TrimSpace(deviceIDType)) {
	case DeviceIDTypeReal:
		return DeviceIDTypeReal
	case DeviceIDTypeManual, deviceIDTypeLegacyRandom:
		return DeviceIDTypeManual
	default:
		return ""
	}
}

// DeviceBindingEligible 报告账号当前是否应持有 device_id 绑定。
// 账号被手动关闭、被弹性号池休眠、被禁言、被封禁或鉴权异常时都应解除绑定，
// 下次重新启用后再分配新的 device_id。
func DeviceBindingEligible(acc Account) bool {
	return acc.IsEnabled() && !acc.IsMuted() && !acc.IsBanned() && !acc.IsAuthFailed()
}

func (a Account) Identifier() string {
	if strings.TrimSpace(a.Email) != "" {
		return strings.TrimSpace(a.Email)
	}
	if mobile := NormalizeMobileForStorage(a.Mobile); mobile != "" {
		return mobile
	}
	return ""
}

// IsEnabled reports whether the account is eligible for scheduling.
// Disabled accounts are skipped by the pool.
func (a Account) IsEnabled() bool {
	return !a.Disabled
}

// IsMuted reports whether the account is currently muted (banned from chatting).
// Returns true only when MutedUntil is set and has not yet expired.
func (a Account) IsMuted() bool {
	if a.MutedUntil <= 0 {
		return false
	}
	return a.MutedUntil > float64(time.Now().Unix())
}

// IsBanned reports whether the account has been suspended by upstream
// (USER_IS_BANNED). A banned account stays out of the pool even if an admin
// manually re-enables it; only a successful token refresh can clear the flag.
func (a Account) IsBanned() bool {
	return a.Banned
}

// IsAuthFailed reports whether the account has been classified as an
// authentication anomaly: upstream kept rejecting its token and a forced
// re-login could not mint a usable one (wrong password, deactivated account).
// Like a banned account it is excluded from scheduling and does not occupy an
// elastic-pool slot; a successful login or a manual re-enable clears the flag.
func (a Account) IsAuthFailed() bool {
	return a.AuthFailed
}

// IsCoolingDown reports whether the account is in a local risk-control cooldown
// after a captcha challenge. Like IsMuted this expires on its own, so the pool
// picks the account back up without any sweeper.
func (a Account) IsCoolingDown() bool {
	if a.CooldownUntil <= 0 {
		return false
	}
	return a.CooldownUntil > float64(time.Now().Unix())
}

// IsSchedulable reports whether the pool may hand this account to a request.
func (a Account) IsSchedulable() bool {
	return a.IsEnabled() && !a.IsMuted() && !a.IsBanned() && !a.IsAuthFailed() && !a.IsCoolingDown()
}

// NormalizePoolType 规范化账号号池类型，空值视为 default。
func NormalizePoolType(poolType string) string {
	switch strings.ToLower(strings.TrimSpace(poolType)) {
	case PoolTypeNoTools:
		return PoolTypeNoTools
	case PoolTypeToolsOnly:
		return PoolTypeToolsOnly
	default:
		return PoolTypeDefault
	}
}

// MatchesPoolType 判断账号是否可被指定工具开关的请求调用。
//   - no_tools 账号仅匹配不含工具定义（toolsPresent=false）的请求
//   - tools_only 账号仅匹配含工具定义（toolsPresent=true）的请求
//   - default / 未知 账号总是匹配
//
// toolsPresent 表示本次请求 body 是否携带非空的 tools 字段，由请求链路在
// 标准化前判定。
func (a Account) MatchesPoolType(toolsPresent bool) bool {
	switch NormalizePoolType(a.PoolType) {
	case PoolTypeNoTools:
		return !toolsPresent
	case PoolTypeToolsOnly:
		return toolsPresent
	default:
		return true
	}
}
