package auth

import "context"

// AuthRecovery 是一次「托管账号的凭据被上游拒绝」恢复尝试的结果。
type AuthRecovery int

const (
	// AuthRecoveryNone 表示没有可用的恢复手段：调用方按原本的失败路径处理。
	AuthRecoveryNone AuthRecovery = iota
	// AuthRecoveryRefreshed 表示已为当前账号强制重新登录并拿到新 Token，
	// 调用方应当用同一账号重试本次请求。
	AuthRecoveryRefreshed
	// AuthRecoverySwitched 表示已切到另一个可调度账号，且新账号已持有可用
	// Token，调用方应当重试本次请求。
	AuthRecoverySwitched
)

// authRefreshStillRejectedReason 是「刷新成功但新 Token 仍被上游拒绝」时写入
// disabled_reason 的说明：这种失败说明账号本身不可用，而不是 Token 过期。
const authRefreshStillRejectedReason = "重新登录后 Token 仍被上游拒绝"

// RecoverAuthFailure 处理一次「托管账号的 Token 被上游拒绝」，返回调用方应当
// 采取的恢复动作。这是请求路径上唯一决定「刷新还是换号」的地方：
//
//  1. 本次请求内已经为该账号刷新过、新 Token 仍被拒绝 → 账号本身不可用，记一次
//     账号级鉴权失败（连续达到阈值后移出号池），随后继续尝试换号；
//  2. 本次请求内还没为该账号刷新过 → 强制重新登录一次，成功即返回
//     AuthRecoveryRefreshed，由调用方用同一账号原地重试；
//  3. 刷新失败或换号不成 → 返回 AuthRecoveryNone。
//
// 之所以不把 401 直接透传给客户端：弹性号池的意义就是在单个账号失效时自动顶上
// 下一个账号，让请求继续跑完。
//
// 恢复顺序与「每账号每请求一次刷新」的额度（见 RefreshTokenOnce）都只在这里
// 实现。client 层的上游调用、completionruntime 的重试链与 Vercel 路径都经由本
// 函数，因此新增一种恢复手段只改这一处，不必再去各调用点补齐。
//
// 调用方只负责「失败是否属于鉴权失败」的判定与重试计数：判定上游各异（HTTP
// 状态码、业务码、错误码），恢复动作是共享的。
func (r *Resolver) RecoverAuthFailure(ctx context.Context, a *RequestAuth) AuthRecovery {
	if r == nil || a == nil || !a.UseConfigToken {
		return AuthRecoveryNone
	}
	if a.AuthRefreshAlreadySucceeded() {
		// 刷新本身成功过，新 Token 仍被上游拒绝，说明账号已不可用。刷新失败的原因
		// （登录被上游拒绝 / 网络抖动）已在 RefreshTokenErr 内按错误类型判定并
		// 计数，这里不能重复计。
		r.MarkAuthFailure(a, authRefreshStillRejectedReason)
	} else if r.RefreshTokenOnce(ctx, a) {
		return AuthRecoveryRefreshed
	}
	if r.SwitchAccount(ctx, a) {
		return AuthRecoverySwitched
	}
	return AuthRecoveryNone
}

// RecoverAuthFailure 见 Resolver.RecoverAuthFailure。
func (a *RequestAuth) RecoverAuthFailure(ctx context.Context) AuthRecovery {
	if a == nil || a.resolver == nil {
		return AuthRecoveryNone
	}
	return a.resolver.RecoverAuthFailure(ctx, a)
}
