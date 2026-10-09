package client

import (
	"context"
	"fmt"
	"strings"

	"ds2api/internal/auth"
)

// recoverManagedAuth 在托管账号的一次上游请求失败后决定是否重试本次请求，是
// client 层唯一决定「这次失败拿账号怎么办」的地方。
//
// 判定与动作是分开的：这里只用 shouldAttemptRefresh 判定失败是否属于「上游以
// 鉴权失败拒绝本次请求」（HTTP 401/403、鉴权类业务码，或 HTTP 200 但业务码带
// 鉴权含义），两种失败各自的动作由共享步骤与号池决定。
//
//   - 鉴权失败：交给 auth.RequestAuth.RecoverAuthFailure——先为当前账号强制刷新
//     一次（刷新成功则同一账号原地重试），刷新不成再换号。返回 AuthRecoveryNone
//     说明共享步骤已经换号失败（号池里没有下一个可用账号），这里不再调用
//     SwitchAccount：那会把本请求已经释放的账号再释放一次。
//   - 非鉴权失败（HTTP 429/5xx、非鉴权业务码等）：Token 没有被拒绝，刷新没有
//     意义，但账号本身仍应让位给下一个可调度账号——把本账号本次请求的名额让出并
//     换号一次，与收口前的调用点行为一致。因限流、风控等原因放弃账号仍由这里
//     判定后调用 SwitchAccount，见 docs/DEVELOPMENT.md §3.6。
//
// 重试计数：刷新成功是同一账号原地重试，不消耗重试预算（每账号每请求至多一次，
// 由 auth.Resolver 的刷新额度保证有界）；换号换掉了承载账号，计一次。
func (c *Client) recoverManagedAuth(ctx context.Context, a *auth.RequestAuth, attempts *int, status, code, bizCode int, msg, bizMsg string) bool {
	if c == nil || c.Auth == nil || a == nil || !a.UseConfigToken || attempts == nil {
		return false
	}
	if !shouldAttemptRefresh(status, code, bizCode, msg, bizMsg) {
		// 非鉴权失败：本账号这次不可用，换到下一个可调度账号。共享步骤只处理
		// 鉴权失败，这里不能走 RecoverAuthFailure，否则一次普通 5xx 会先白白刷新
		// 一次 Token。
		if c.Auth.SwitchAccount(ctx, a) {
			*attempts++
			return true
		}
		return false
	}
	switch a.RecoverAuthFailure(ctx) {
	case auth.AuthRecoveryRefreshed:
		return true
	case auth.AuthRecoverySwitched:
		*attempts++
		return true
	}
	return false
}

// noteLoginSuccess 在账号成功登录后清零其连续鉴权失败计数，并在账号此前被
// 判为鉴权异常时解除该状态，使其重新参与调度。
func (c *Client) noteLoginSuccess(identifier string) {
	if c == nil || c.Auth == nil || strings.TrimSpace(identifier) == "" {
		return
	}
	c.Auth.NoteLoginSuccess(identifier)
}

// ForgetAuthFailures 清理已删除账号的连续鉴权失败计数，实现账号管理侧可选的
// authFailureForgetter 接口。
func (c *Client) ForgetAuthFailures(identifier string) {
	if c == nil || c.Auth == nil || strings.TrimSpace(identifier) == "" {
		return
	}
	c.Auth.ForgetAccount(identifier)
}

// loginRejectedError 把上游明确拒绝登录的业务错误标记为 auth.ErrLoginRejected，
// 供号池区分"账号本身不可用"与"网络抖动"。
func loginRejectedError(format string, args ...any) error {
	msg := strings.TrimSpace(fmt.Sprintf(format, args...))
	if msg == "" {
		return auth.ErrLoginRejected
	}
	return fmt.Errorf("%s: %w", msg, auth.ErrLoginRejected)
}
