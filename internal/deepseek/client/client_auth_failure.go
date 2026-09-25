package client

import (
	"context"
	"fmt"
	"strings"

	"ds2api/internal/auth"
)

// handleManagedAuthFailure 处理一次"上游以鉴权失败拒绝本次请求"的托管账号响应。
//
// 返回 true 表示已经拿到新 Token，调用方应当用同一账号立即重试本次请求；
// 返回 false 表示本账号这次救不回来，调用方应换到下一个可调度账号。
//
// 之所以不直接把 401 透传给客户端：弹性号池的意义就是在单个账号失效时自动
// 顶上下一个账号，让请求继续跑完。处理顺序为
//  1. 本账号本次请求内强制重新登录一次（refreshed 为每账号一次的额度）；
//     重新登录被上游明确拒绝时，auth.Resolver 会顺带上报一次账号级鉴权失败，
//     连续失败达到阈值后账号被移出号池，弹性号池随即让休眠账号补位；
//  2. 重新登录成功但 Token 仍被上游拒绝，说明账号本身已不可用，直接上报。
func (c *Client) handleManagedAuthFailure(ctx context.Context, a *auth.RequestAuth, refreshed *bool) bool {
	if c == nil || c.Auth == nil || a == nil || !a.UseConfigToken || refreshed == nil {
		return false
	}
	if *refreshed {
		c.Auth.MarkAuthFailure(a, "重新登录后 Token 仍被上游拒绝")
		return false
	}
	if c.Auth.RefreshTokenErr(ctx, a) == nil {
		*refreshed = true
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
