package auth

import (
	"context"
	"errors"
	"strings"

	"ds2api/internal/account"
	"ds2api/internal/config"
)

var (
	// ErrLoginRejected 表示上游明确拒绝了本次账号登录（密码错误、账号不存在
	// 或被注销等），属于账号级异常而非网络抖动。client 层的 Login 在拿到这类
	// 业务拒绝时包装该哨兵值，供号池判断是否要把账号判为"鉴权异常"。
	ErrLoginRejected = errors.New("login rejected by upstream")
	// ErrNotManagedAccount 表示当前请求并非由托管账号承载（例如调用方直传 Token），
	// 因此没有可刷新的账号 Token。
	ErrNotManagedAccount = errors.New("not a managed account")
)

// authFailureThreshold 是账号被判定为"鉴权异常"前允许的相邻鉴权失败次数。
//
// 统计的是"相邻请求"意义上的连续失败：账号在一次请求中鉴权失败后，若下一次
// 真正使用它（成功拿到可用 Token 并跑完请求）时没有再失败，历史失败计数会在
// 请求结束（Release）时清零。只有连续 authFailureThreshold 次使用都鉴权失败，
// 才说明账号本身不可用，被移出号池并由弹性号池自动补位。
const authFailureThreshold = 2

// DefaultAuthFailedReason 是账号被判定为鉴权异常时写入 disabled_reason 的说明。
const DefaultAuthFailedReason = "账号鉴权持续失败（Token 失效且重新登录失败），已移出号池"

// 本次请求内的强制刷新结果，供 RequestAuth.authRefreshState 使用。
const (
	authRefreshSucceeded = "succeeded"
	authRefreshFailed    = "failed"
)

// RefreshTokenErr 与 RefreshToken 行为一致，但把失败原因一并返回，
// 供调用方判断该失败是否属于账号级鉴权异常（见 ErrLoginRejected）。
// 未托管账号返回 ErrNotManagedAccount。
//
// 上游明确拒绝重新登录（ErrLoginRejected）时，这里会顺带上报一次账号级鉴权
// 失败：连续失败达到阈值后账号被移出号池，由弹性号池自动补位。
func (r *Resolver) RefreshTokenErr(ctx context.Context, a *RequestAuth) error {
	if a == nil || !a.UseConfigToken || a.AccountID == "" {
		return ErrNotManagedAccount
	}
	_ = r.Store.UpdateAccountToken(a.AccountID, "")
	a.Account.Token = ""
	if err := r.loginAndPersist(ctx, a); err != nil {
		config.Logger.Error("[refresh_token] failed", "account", a.AccountID, "error", err)
		if errors.Is(err, ErrLoginRejected) {
			r.MarkAuthFailure(a, "重新登录被上游拒绝："+err.Error())
		}
		return err
	}
	return nil
}

// RefreshTokenOnce 在本次请求内最多为同一账号强制重新登录一次。
// Token 在请求途中失效时先刷新、再换号，既避免无谓换号，也避免反复刷新
// 同一账号造成死循环。
func (r *Resolver) RefreshTokenOnce(ctx context.Context, a *RequestAuth) bool {
	if r == nil || a == nil || !a.UseConfigToken || a.AccountID == "" {
		return false
	}
	if a.authRefreshState == nil {
		a.authRefreshState = map[string]string{}
	}
	if _, done := a.authRefreshState[a.AccountID]; done {
		return false
	}
	if r.RefreshToken(ctx, a) {
		a.authRefreshState[a.AccountID] = authRefreshSucceeded
		return true
	}
	a.authRefreshState[a.AccountID] = authRefreshFailed
	return false
}

// MarkAuthFailure 记录一次账号级鉴权失败（上游以 Token 失效拒绝请求，且强制
// 重新登录也没能拿到可用 Token）。
//
// 第一次失败只计数并返回 false——调用方应当换到下一个账号继续本次请求，
// 保证流程不中断；同一账号在相邻请求上连续失败达到 authFailureThreshold 次后
// 返回 true，账号被持久化为"鉴权异常"并从号池移除，弹性号池随即让休眠账号
// 补位。两次失败之间只要该账号被成功使用过一次，计数就会在请求结束时清零
// （见 settleAuthFailures）。
func (r *Resolver) MarkAuthFailure(a *RequestAuth, reason string) bool {
	if r == nil || a == nil || !a.UseConfigToken || strings.TrimSpace(a.AccountID) == "" {
		return false
	}
	a.markAuthFailureForRequest()
	failures := r.noteAuthFailure(a.AccountID)
	if failures < authFailureThreshold {
		config.Logger.Warn("[auth_failed] managed account authentication failed",
			"account", a.AccountID, "consecutive_failures", failures, "threshold", authFailureThreshold, "reason", reason)
		return false
	}
	config.Logger.Warn("[auth_failed] account classified as authentication anomaly, removing from pool",
		"account", a.AccountID, "consecutive_failures", failures, "reason", reason)
	r.persistAuthFailed(a, reason)
	return true
}

// NoteLoginSuccess 在账号成功登录（刷新 Token）后调用：清零该账号的连续鉴权
// 失败计数，并在账号此前被判定为鉴权异常时解除该状态，使其重新参与调度。
func (r *Resolver) NoteLoginSuccess(identifier string) {
	identifier = strings.TrimSpace(identifier)
	if r == nil || identifier == "" {
		return
	}
	r.mu.Lock()
	delete(r.authFailures, identifier)
	r.mu.Unlock()
	if r.Store == nil {
		return
	}
	if acc, ok := r.Store.FindAccount(identifier); !ok || !acc.AuthFailed {
		return
	}
	cleared := false
	if err := r.Store.Update(func(c *config.Config) error {
		for i := range c.Accounts {
			if c.Accounts[i].Identifier() != identifier {
				continue
			}
			if !c.Accounts[i].AuthFailed {
				return nil
			}
			c.Accounts[i].AuthFailed = false
			c.Accounts[i].Disabled = false
			c.Accounts[i].DisabledReason = ""
			cleared = true
			break
		}
		if cleared {
			if c.ElasticPool.Enabled {
				account.ReconcileElasticPool(c)
			}
			// 账号重新启用后同步重算 device_id 绑定，与 persistAuthFailed 解除
			// 绑定的动作对称，避免绑定计数与实际状态不一致。
			config.ReconcileDeviceIDBindings(c)
		}
		return nil
	}); err != nil {
		config.Logger.Error("[auth_failed] failed to clear auth_failed state", "account", identifier, "error", err)
		return
	}
	if !cleared {
		return
	}
	if r.Pool != nil {
		r.Pool.Reset()
	}
	r.notifyPoolChanged()
	config.Logger.Info("[auth_failed] account re-enabled after successful login", "account", identifier)
}

// persistAuthFailed 把账号标记为鉴权异常：与封号一致，禁用账号并在同一个事务
// 内触发弹性号池补位，让休眠账号立刻顶上，全程无需人工干预。
func (r *Resolver) persistAuthFailed(a *RequestAuth, reason string) {
	if r == nil || r.Store == nil || a == nil {
		return
	}
	identifier := a.AccountID
	if strings.TrimSpace(reason) == "" {
		reason = DefaultAuthFailedReason
	}
	if err := r.Store.Update(func(c *config.Config) error {
		for i := range c.Accounts {
			if c.Accounts[i].Identifier() != identifier {
				continue
			}
			c.Accounts[i].AuthFailed = true
			c.Accounts[i].Disabled = true
			c.Accounts[i].DisabledReason = reason
			break
		}
		if c.ElasticPool.Enabled {
			account.ReconcileElasticPool(c)
		}
		// 被判定异常的账号不再持有 device_id 绑定，重新启用后重新分配。
		config.ReconcileDeviceIDBindings(c)
		return nil
	}); err != nil {
		config.Logger.Error("[auth_failed] failed to persist auth_failed state", "account", identifier, "error", err)
		return
	}
	a.Account.AuthFailed = true
	a.Account.Disabled = true
	a.Account.DisabledReason = reason
	if r.Pool != nil {
		r.Pool.Reset()
	}
	r.notifyPoolChanged()
}

func (r *Resolver) noteAuthFailure(accountID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.authFailures == nil {
		r.authFailures = map[string]int{}
	}
	r.authFailures[accountID]++
	return r.authFailures[accountID]
}

// ForgetAccount 清理已删除账号的连续鉴权失败计数，避免内存中残留无主条目。
// 由账号删除链路通过 client 的可选接口调用。
func (r *Resolver) ForgetAccount(identifier string) {
	if r == nil {
		return
	}
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return
	}
	r.mu.Lock()
	delete(r.authFailures, identifier)
	r.mu.Unlock()
}

// markAccountUsed 记录本次请求成功调度（拿到可用 Token）过的账号。
func (a *RequestAuth) markAccountUsed() {
	if a == nil || a.AccountID == "" {
		return
	}
	if a.authUsedAccounts == nil {
		a.authUsedAccounts = map[string]bool{}
	}
	a.authUsedAccounts[a.AccountID] = true
}

// markAuthFailureForRequest 记录本次请求内发生过鉴权失败的账号，供请求结束时
// 区分"本次失败账号"与"本次正常使用过的账号"。
func (a *RequestAuth) markAuthFailureForRequest() {
	if a == nil || a.AccountID == "" {
		return
	}
	if a.authFailedAccounts == nil {
		a.authFailedAccounts = map[string]bool{}
	}
	a.authFailedAccounts[a.AccountID] = true
}

// AuthRefreshAlreadySucceeded 报告当前账号在本次请求内是否已经成功强制重新
// 登录过。用于区分"刷新失败"与"刷新成功但新 Token 仍被上游拒绝"：只有后者才
// 说明账号本身已不可用，应计入账号级鉴权失败。
func (a *RequestAuth) AuthRefreshAlreadySucceeded() bool {
	if a == nil || a.AccountID == "" {
		return false
	}
	return a.authRefreshState[a.AccountID] == authRefreshSucceeded
}

// settleAuthFailures 在请求结束（Release）时结算连续鉴权失败计数：本次请求中
// 成功调度并正常使用过、且没有发生鉴权失败的账号，其历史失败计数清零。这样
// authFailureThreshold 统计的就是"相邻请求"上的连续失败，而不是累计失败——
// 账号早先偶发失败后正常工作很久，不会因一次新失败被误判为鉴权异常。
func (r *Resolver) settleAuthFailures(a *RequestAuth) {
	if r == nil || a == nil || len(a.authUsedAccounts) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id := range a.authUsedAccounts {
		if a.authFailedAccounts[id] {
			continue
		}
		delete(r.authFailures, id)
	}
}

func (r *Resolver) notifyPoolChanged() {
	if r == nil || r.OnAccountPoolChanged == nil {
		return
	}
	r.OnAccountPoolChanged()
}

// authFailedReasonFromLoginError 从登录错误里提取给管理员看的说明。
func authFailedReasonFromLoginError(err error) string {
	if err == nil {
		return DefaultAuthFailedReason
	}
	return "账号鉴权持续失败：" + err.Error()
}

// MarkAuthFailure 上报一次账号级鉴权失败，见 Resolver.MarkAuthFailure。
// 返回 true 表示账号已被判定为鉴权异常并移出号池。
func (a *RequestAuth) MarkAuthFailure(reason string) bool {
	if a == nil || a.resolver == nil {
		return false
	}
	return a.resolver.MarkAuthFailure(a, reason)
}

// RefreshTokenOnce 在本次请求内最多为该账号强制重新登录一次，见
// Resolver.RefreshTokenOnce。
func (a *RequestAuth) RefreshTokenOnce(ctx context.Context) bool {
	if a == nil || a.resolver == nil {
		return false
	}
	return a.resolver.RefreshTokenOnce(ctx, a)
}
