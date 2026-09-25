package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"ds2api/internal/account"
	"ds2api/internal/banreport"
	"ds2api/internal/config"
	"ds2api/internal/toolcall"
)

type ctxKey string

const authCtxKey ctxKey = "auth_context"

const toolsPresentCtxKey ctxKey = "tools_present"

var (
	ErrUnauthorized = errors.New("unauthorized: missing auth token")
	ErrNoAccount    = errors.New("no accounts configured or all accounts are busy")
)

type RequestAuth struct {
	UseConfigToken bool
	DeepSeekToken  string
	CallerID       string
	UserAgent      string
	AccountID      string
	TargetAccount  string
	Account        config.Account
	TriedAccounts  map[string]bool
	// ToolsPresent 表示本次请求是否携带工具定义（请求 body 的 tools 非空）。
	// 它决定账号池调度：含工具请求走默认/仅工具池，无工具请求走无工具池。
	// 账号切换重试时沿用该标志，保证切换后仍遵守号池约束。
	ToolsPresent bool
	// ToolMarker 是本次调用者专属的工具调用标识（由服务端密钥派生，按 Key 稳定）。
	// 提示词渲染输出该标识，模型输出在进入解析器前会被归一化回 EPSE。
	ToolMarker string
	// authRefreshState 记录本次请求内每个账号的强制重新登录结果（成功/失败），
	// 避免同一账号被反复刷新 Token 造成死循环；换号后新账号拥有独立的刷新额度。
	authRefreshState map[string]string
	// authUsedAccounts 记录本次请求内成功调度（拿到可用 Token）过的账号；
	// authFailedAccounts 记录本次请求内发生过鉴权失败的账号。二者供请求结束时
	// 结算连续鉴权失败计数（见 Resolver.settleAuthFailures）。
	authUsedAccounts   map[string]bool
	authFailedAccounts map[string]bool
	resolver           *Resolver
}

type LoginFunc func(ctx context.Context, acc config.Account) (string, error)
type PostLoginFunc func(ctx context.Context, a *RequestAuth)

type Resolver struct {
	Store     *config.Store
	Pool      *account.Pool
	Login     LoginFunc
	PostLogin PostLoginFunc
	// OnAccountPoolChanged 在弹性号池重算后触发（若非 nil），供 mihomo 代理桥
	// 立即给新启用的补位账号分配节点。与 client 侧的 SetAccountPoolChanged 同义。
	OnAccountPoolChanged func()

	mu               sync.Mutex
	tokenRefreshedAt map[string]time.Time
	authFailures     map[string]int
}

func NewResolver(store *config.Store, pool *account.Pool, login LoginFunc) *Resolver {
	return &Resolver{
		Store:            store,
		Pool:             pool,
		Login:            login,
		tokenRefreshedAt: map[string]time.Time{},
		authFailures:     map[string]int{},
	}
}

func (r *Resolver) Determine(req *http.Request) (*RequestAuth, error) {
	callerKey := extractCallerToken(req)
	if callerKey == "" {
		return nil, ErrUnauthorized
	}
	callerID := callerTokenID(callerKey)
	callerUA := callerUserAgent(req)
	ctx := req.Context()
	if !r.Store.HasAPIKey(callerKey) {
		return &RequestAuth{
			UseConfigToken: false,
			DeepSeekToken:  callerKey,
			CallerID:       callerID,
			UserAgent:      callerUA,
			ToolMarker:     toolMarkerFor(r, callerID),
			resolver:       r,
			TriedAccounts:  map[string]bool{},
		}, nil
	}
	target := strings.TrimSpace(req.Header.Get("X-Ds2-Target-Account"))
	// 号池选择按“本次请求 body 是否携带工具定义”路由。含工具请求走默认/仅
	// 工具池，无工具请求走无工具池；该标志由各协议适配器在标准化前写入请求
	// 上下文（见 WithToolsPresent）。
	toolsPresent := toolsPresentFromContext(ctx)
	a, err := r.acquireManagedRequestAuth(ctx, callerID, target, toolsPresent)
	if err != nil {
		return nil, err
	}
	a.UserAgent = callerUA
	return a, nil
}

func (r *Resolver) acquireManagedRequestAuth(ctx context.Context, callerID, target string, toolsPresent bool) (*RequestAuth, error) {
	tried := map[string]bool{}
	var lastEnsureErr error
	filter := account.AccountFilter(func(acc config.Account) bool {
		return acc.MatchesPoolType(toolsPresent)
	})
	for {
		if target == "" && len(tried) >= r.Pool.SchedulableCount() {
			if lastEnsureErr != nil {
				return nil, lastEnsureErr
			}
			return nil, ErrNoAccount
		}
		acc, ok := r.Pool.AcquireWait(ctx, target, tried, filter)
		if !ok {
			if lastEnsureErr != nil {
				return nil, lastEnsureErr
			}
			return nil, ErrNoAccount
		}

		a := &RequestAuth{
			UseConfigToken: true,
			CallerID:       callerID,
			AccountID:      acc.Identifier(),
			TargetAccount:  target,
			Account:        acc,
			TriedAccounts:  tried,
			ToolsPresent:   toolsPresent,
			ToolMarker:     toolMarkerFor(r, callerID),
			resolver:       r,
		}

		if err := r.ensureManagedToken(ctx, a); err != nil {
			lastEnsureErr = err
			tried[a.AccountID] = true
			r.Pool.Release(a.AccountID)
			// 上游明确拒绝登录（密码错误/账号被注销）：这是账号级异常，计入
			// 连续鉴权失败；达到阈值后账号被移出号池，弹性号池随即补位。
			if errors.Is(err, ErrLoginRejected) {
				r.MarkAuthFailure(a, authFailedReasonFromLoginError(err))
			}
			// device_id 号池为空是全局性问题，与具体账号无关：直接失败，
			// 不必把全部账号都试一遍。
			if target != "" || errors.Is(err, config.ErrDeviceIDPoolEmpty) {
				return nil, err
			}
			continue
		}
		a.markAccountUsed()
		return a, nil
	}
}

// DetermineCaller resolves caller identity without acquiring any pooled account.
// Use this for local-cache lookup routes that only need tenant isolation.
func (r *Resolver) DetermineCaller(req *http.Request) (*RequestAuth, error) {
	callerKey := extractCallerToken(req)
	if callerKey == "" {
		return nil, ErrUnauthorized
	}
	callerID := callerTokenID(callerKey)
	a := &RequestAuth{
		UseConfigToken: false,
		CallerID:       callerID,
		UserAgent:      callerUserAgent(req),
		ToolMarker:     toolMarkerFor(r, callerID),
		resolver:       r,
		TriedAccounts:  map[string]bool{},
	}
	if r == nil || r.Store == nil || !r.Store.HasAPIKey(callerKey) {
		a.DeepSeekToken = callerKey
	}
	return a, nil
}

func WithAuth(ctx context.Context, a *RequestAuth) context.Context {
	return context.WithValue(ctx, authCtxKey, a)
}

func FromContext(ctx context.Context) (*RequestAuth, bool) {
	v := ctx.Value(authCtxKey)
	a, ok := v.(*RequestAuth)
	return a, ok
}

// ForceDisableToolsForRequest reports whether the caller behind req uses a
// managed API key with the per-key "force disable tool calling" switch on.
// Direct-token callers and missing/unknown keys keep the default (off) behavior,
// so only keys explicitly configured in the store can opt in.
func (r *Resolver) ForceDisableToolsForRequest(req *http.Request) bool {
	if req == nil {
		return false
	}
	callerKey := extractCallerToken(req)
	if callerKey == "" {
		return false
	}
	if r == nil || r.Store == nil || !r.Store.HasAPIKey(callerKey) {
		return false
	}
	return r.Store.APIKeyForceDisableTools(callerKey)
}

// WithToolsPresent records whether the current request body carries a non-empty
// tool definition. Protocol adapters set this (after normalizing their own
// request shape onto the shared `tools` field) so that Determine can route the
// request to the matching account pool without re-parsing the body. It also
// governs whether tool-call prompt injection happens for the request.
func WithToolsPresent(ctx context.Context, present bool) context.Context {
	return context.WithValue(ctx, toolsPresentCtxKey, present)
}

// toolsPresentFromContext returns the tools-present flag stored by
// WithToolsPresent. When unset (e.g. routes that do not inspect the body such
// as embeddings or file uploads) it defaults to false, routing to the
// no-tools/default pool.
func toolsPresentFromContext(ctx context.Context) bool {
	v := ctx.Value(toolsPresentCtxKey)
	b, _ := v.(bool)
	return b
}

func (r *Resolver) loginAndPersist(ctx context.Context, a *RequestAuth) error {
	token, err := r.Login(ctx, a.Account)
	if err != nil {
		return err
	}
	a.Account.Token = token
	a.DeepSeekToken = token
	r.markTokenRefreshedNow(a.AccountID)
	if err := r.Store.UpdateAccountToken(a.AccountID, token); err != nil {
		return err
	}
	if r.PostLogin != nil {
		r.PostLogin(ctx, a)
	}
	return nil
}

func (r *Resolver) RefreshToken(ctx context.Context, a *RequestAuth) bool {
	return r.RefreshTokenErr(ctx, a) == nil
}

func (r *Resolver) MarkTokenInvalid(a *RequestAuth) {
	if !a.UseConfigToken || a.AccountID == "" {
		return
	}
	a.Account.Token = ""
	a.DeepSeekToken = ""
	r.clearTokenRefreshMark(a.AccountID)
	_ = r.Store.UpdateAccountToken(a.AccountID, "")
}

// SetAccountMutedUntil 持久化账号禁言到期时间。
// 与 DisableAccount 不同，这只是临时禁用，到期后号池会自动恢复调度。
// 当弹性号池开启时，封号后立即在同一个事务内触发 ReconcileElasticPool
// 补位：被封账号让出名额，按原始顺序从后面的休眠账号中启用一个补上。
func (r *Resolver) SetAccountMutedUntil(a *RequestAuth, muteUntil float64) {
	if !a.UseConfigToken || a.AccountID == "" || muteUntil <= 0 {
		return
	}
	identifier := a.AccountID
	if err := r.Store.Update(func(c *config.Config) error {
		for i := range c.Accounts {
			if c.Accounts[i].Identifier() != identifier {
				continue
			}
			c.Accounts[i].MutedUntil = muteUntil
			break
		}
		if c.ElasticPool.Enabled {
			account.ReconcileElasticPool(c)
		}
		// 被禁言的账号不再持有 device_id 绑定，重新启用后重新分配。
		config.ReconcileDeviceIDBindings(c)
		return nil
	}); err != nil {
		config.Logger.Error("[muted_account] failed to persist muted_until", "account", identifier, "error", err)
		return
	}
	a.Account.MutedUntil = muteUntil
	if r.Pool != nil {
		r.Pool.Reset()
	}
	config.Logger.Info("[muted_account] account muted until", "account", identifier, "mute_until", muteUntil)
	banreport.Report(r.Store, banreport.Event{
		Type:      banreport.KindMuted,
		Account:   identifier,
		MuteUntil: muteUntil,
		Source:    banreport.SourceVercelStream,
	})
}

// SetAccountBanned 将账号标记为被上游停用（USER_IS_BANNED）。
// 手动启用或启用全部账号不会解除该状态；只有成功刷新 token 后才会在 Login 中清除。
func (r *Resolver) SetAccountBanned(a *RequestAuth, reason string) {
	if !a.UseConfigToken || a.AccountID == "" {
		return
	}
	if strings.TrimSpace(reason) == "" {
		reason = "账户已被停用"
	}
	identifier := a.AccountID
	if err := r.Store.Update(func(c *config.Config) error {
		for i := range c.Accounts {
			if c.Accounts[i].Identifier() != identifier {
				continue
			}
			c.Accounts[i].Banned = true
			c.Accounts[i].Disabled = true
			c.Accounts[i].DisabledReason = reason
			break
		}
		if c.ElasticPool.Enabled {
			account.ReconcileElasticPool(c)
		}
		// 被封禁的账号不再持有 device_id 绑定。
		config.ReconcileDeviceIDBindings(c)
		return nil
	}); err != nil {
		config.Logger.Error("[banned_account] failed to persist banned state", "account", identifier, "error", err)
		return
	}
	a.Account.Banned = true
	a.Account.Disabled = true
	a.Account.DisabledReason = reason
	if r.Pool != nil {
		r.Pool.Reset()
	}
	config.Logger.Warn("[banned_account] account disabled after USER_IS_BANNED", "account", identifier, "reason", reason)
	banreport.Report(r.Store, banreport.Event{
		Type:    banreport.KindBanned,
		Account: identifier,
		Source:  banreport.SourceVercelStream,
	})
}

// SwitchAccount 把当前请求切到下一个可调度账号，并确保新账号持有可用 Token。
// 逐个尝试，登录被上游明确拒绝（ErrLoginRejected）的账号同样计入连续鉴权失败，
// 达到阈值后会被移出号池；全部账号都不可用时返回 false。
func (r *Resolver) SwitchAccount(ctx context.Context, a *RequestAuth) bool {
	if a == nil || !a.UseConfigToken {
		return false
	}
	if strings.TrimSpace(a.TargetAccount) != "" {
		return false
	}
	if a.TriedAccounts == nil {
		a.TriedAccounts = map[string]bool{}
	}
	if a.AccountID != "" {
		a.TriedAccounts[a.AccountID] = true
		r.Pool.Release(a.AccountID)
	}
	filter := account.AccountFilter(func(acc config.Account) bool {
		return acc.MatchesPoolType(a.ToolsPresent)
	})
	for {
		acc, ok := r.Pool.Acquire("", a.TriedAccounts, filter)
		if !ok {
			return false
		}
		a.Account = acc
		a.AccountID = acc.Identifier()
		if err := r.ensureManagedToken(ctx, a); err != nil {
			a.TriedAccounts[a.AccountID] = true
			r.Pool.Release(a.AccountID)
			if errors.Is(err, ErrLoginRejected) {
				r.MarkAuthFailure(a, authFailedReasonFromLoginError(err))
			}
			continue
		}
		a.markAccountUsed()
		return true
	}
}

func (a *RequestAuth) SwitchAccount(ctx context.Context) bool {
	if a == nil || a.resolver == nil {
		return false
	}
	return a.resolver.SwitchAccount(ctx, a)
}

func (r *Resolver) Release(a *RequestAuth) {
	if a == nil || !a.UseConfigToken || a.AccountID == "" {
		return
	}
	r.settleAuthFailures(a)
	r.Pool.Release(a.AccountID)
}

func extractCallerToken(req *http.Request) string {
	authHeader := strings.TrimSpace(req.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		token := strings.TrimSpace(authHeader[7:])
		if token != "" {
			return token
		}
	}
	if key := strings.TrimSpace(req.Header.Get("x-api-key")); key != "" {
		return key
	}
	// Gemini/Google clients commonly send API key via x-goog-api-key.
	if key := strings.TrimSpace(req.Header.Get("x-goog-api-key")); key != "" {
		return key
	}
	// Gemini AI Studio compatibility: allow query key fallback only when no
	// header-based credential is present.
	if key := strings.TrimSpace(req.URL.Query().Get("key")); key != "" {
		return key
	}
	return strings.TrimSpace(req.URL.Query().Get("api_key"))
}

func callerTokenID(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return "caller:" + hex.EncodeToString(sum[:8])
}

// toolMarkerFor derives the per-caller tool-call marker from the server marker
// secret. It degrades gracefully when the store is unavailable (e.g. lightweight
// local-cache routes) by falling back to the built-in derivation salt.
func toolMarkerFor(r *Resolver, callerID string) string {
	if r == nil || r.Store == nil {
		return toolcall.DeriveToolMarker("", callerID)
	}
	return toolcall.DeriveToolMarker(r.Store.ToolMarkerSecret(), callerID)
}

func callerUserAgent(req *http.Request) string {
	if req == nil {
		return ""
	}
	return strings.TrimSpace(req.Header.Get("User-Agent"))
}

func (r *Resolver) ensureManagedToken(ctx context.Context, a *RequestAuth) error {
	if strings.TrimSpace(a.Account.Token) == "" {
		return r.loginAndPersist(ctx, a)
	}
	if r.shouldForceRefresh(a.AccountID) {
		if err := r.loginAndPersist(ctx, a); err != nil {
			return err
		}
		return nil
	}
	a.DeepSeekToken = a.Account.Token
	return nil
}

func (r *Resolver) shouldForceRefresh(accountID string) bool {
	if r == nil || r.Store == nil {
		return false
	}
	if strings.TrimSpace(accountID) == "" {
		return false
	}
	intervalHours := r.Store.RuntimeTokenRefreshIntervalHours()
	if intervalHours <= 0 {
		return false
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	last, ok := r.tokenRefreshedAt[accountID]
	if !ok || last.IsZero() {
		r.tokenRefreshedAt[accountID] = now
		return false
	}
	return now.Sub(last) >= time.Duration(intervalHours)*time.Hour
}

func (r *Resolver) markTokenRefreshedNow(accountID string) {
	if strings.TrimSpace(accountID) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokenRefreshedAt[accountID] = time.Now()
}

func (r *Resolver) clearTokenRefreshMark(accountID string) {
	if strings.TrimSpace(accountID) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tokenRefreshedAt, accountID)
}
