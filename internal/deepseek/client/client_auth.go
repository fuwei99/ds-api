package client

import (
	"context"
	dsprotocol "ds2api/internal/deepseek/protocol"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"ds2api/internal/account"
	"ds2api/internal/auth"
	"ds2api/internal/banreport"
	"ds2api/internal/config"
)

// Login 使用账号密码登录并返回 DeepSeek token。
//
// manual 模式下若上游返回 RISK_DEVICE_DETECTED（该 device_id 已被风控标记），
// 会把该 id 从号池移除、给原绑定账号换绑，并无感换用新 id 重试；只有号池里
// 最后一个 id 也失效时才向上游调用方报错。
//
// 循环必然终止：每轮都验证失败的 id 确实已从号池移除，因此最多循环
// 「进入时号池大小」次。
func (c *Client) Login(ctx context.Context, acc config.Account) (string, error) {
	for {
		deviceID, deviceType, err := c.ensureAccountDeviceID(ctx, acc)
		if err != nil {
			return "", err
		}
		acc.DeviceID = deviceID
		acc.DeviceIDType = deviceType
		token, err := c.loginWithDeviceID(ctx, acc, deviceID)
		if err == nil {
			return token, nil
		}
		if !c.shouldRotateDeviceID(deviceID, err) {
			return "", err
		}
		if !c.InvalidateDeviceID(deviceID) {
			return "", errAllDeviceIDsInvalid
		}
		if c.poolHasDeviceID(deviceID) {
			// 持久化失败导致 id 没能真正移出号池：继续重试只会拿同一个 id
			// 反复撞风控，如实把原始错误上报。
			config.Logger.Error("[device_id] invalidation did not take effect", "device_id", deviceID)
			return "", err
		}
		acc.DeviceID = ""
		acc.DeviceIDType = ""
	}
}

// shouldRotateDeviceID 判断本次登录失败是否应当换号重试。
// 仅在 manual 模式下、且失败的 device_id 确实来自号池时才换号；
// real 模式生成的 device_id 不参与号池，保持原样上报。
func (c *Client) shouldRotateDeviceID(deviceID string, err error) bool {
	if !isRiskDeviceDetected(err) {
		return false
	}
	if c.deviceIDMode() != config.DeviceIDTypeManual {
		return false
	}
	return c.poolHasDeviceID(deviceID)
}

// isRiskDeviceDetected 判断登录错误是否表示 device_id 被上游风控识别。
func isRiskDeviceDetected(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToUpper(err.Error()), "RISK_DEVICE_DETECTED")
}

// loginWithDeviceID 用指定的 device_id 执行一次登录。
func (c *Client) loginWithDeviceID(ctx context.Context, acc config.Account, deviceID string) (string, error) {
	// Start the new session with a clean jar: replaying cookies issued against
	// the previous token alongside a freshly minted one is incoherent.
	c.cookies.forget(acc.Identifier())
	clients := c.requestClientsForAccount(acc)
	payload := map[string]any{
		"email":     "",
		"mobile":    "",
		"password":  strings.TrimSpace(acc.Password),
		"area_code": "",
		"device_id": deviceID,
		"os":        "web",
	}
	if email := strings.TrimSpace(acc.Email); email != "" {
		payload["email"] = email
	} else if mobile := strings.TrimSpace(acc.Mobile); mobile != "" {
		loginMobile, areaCode := normalizeMobileForLogin(mobile)
		payload["mobile"] = loginMobile
		payload["area_code"] = areaCode
	} else {
		return "", errors.New("missing email/mobile")
	}
	resp, err := c.postJSON(ctx, clients.regular, clients.fallback, dsprotocol.DeepSeekLoginURL, dsprotocol.LoginHeaders(acc.Locale), payload)
	if err != nil {
		return "", err
	}
	code := intFrom(resp["code"])
	if code != 0 {
		return "", loginRejectedError("login failed: %v", resp["msg"])
	}
	data, _ := resp["data"].(map[string]any)
	bizCode := intFrom(data["biz_code"])
	bizMsg, _ := data["biz_msg"].(string)
	if bizCode != 0 {
		if isUserBannedResponse(bizCode, bizMsg) {
			c.persistBannedAccount(acc.Identifier(), "账户已被停用", banreport.SourceLogin)
			return "", fmt.Errorf("login failed: USER_IS_BANNED")
		}
		return "", loginRejectedError("login failed: %v", bizMsg)
	}
	bizData, _ := data["biz_data"].(map[string]any)
	user, _ := bizData["user"].(map[string]any)
	token, _ := user["token"].(string)
	if strings.TrimSpace(token) == "" {
		return "", loginRejectedError("missing login token")
	}
	if chat, _ := user["chat"].(map[string]any); chat != nil {
		if isMuted, _ := chat["is_muted"].(float64); isMuted == 1 {
			muteUntil, _ := chat["mute_until"].(float64)
			c.persistMutedUntil(acc.Identifier(), muteUntil, banreport.SourceLogin)
			config.Logger.Warn("[login] account is muted", "account", acc.Identifier(), "mute_until", muteUntil)
		}
	}
	if acc.Banned {
		c.clearBannedAccount(acc.Identifier())
	}
	// 登录成功说明账号鉴权可用：清零连续鉴权失败计数，并解除此前的鉴权异常
	// 判定，让账号重新回到调度队列。
	c.noteLoginSuccess(acc.Identifier())
	ssoID, _ := user["id"].(string)
	loginAuth := &auth.RequestAuth{
		UseConfigToken: true,
		DeepSeekToken:  token,
		AccountID:      acc.Identifier(),
		Account:        acc,
	}
	auth.WithAuth(ctx, loginAuth)
	c.reportClientSettingsAfterLogin(ctx, loginAuth, ssoID)
	if err := c.DisableTrainingAllowed(ctx, loginAuth); err != nil {
		config.Logger.Warn("[disable_training] failed after login", "account", acc.Identifier(), "error", err)
	}
	return token, nil
}

func (c *Client) reportClientSettingsAfterLogin(ctx context.Context, a *auth.RequestAuth, ssoID string) {
	if c == nil || a == nil || strings.TrimSpace(a.DeepSeekToken) == "" || strings.TrimSpace(a.Account.DeviceID) == "" {
		return
	}
	if err := c.ReportClientSettings(ctx, a, ssoID); err != nil {
		config.Logger.Warn("[client_settings] report after login failed", "account", a.AccountID, "error", err)
	}
}

// persistMutedUntil 持久化账号禁言到期时间到配置。
// muteUntil 为 DeepSeek 返回的 Unix 时间戳（秒，可能含小数）。
// 当弹性号池开启时，在同一个事务内触发 ReconcileElasticPool 补位。
// source 标记检测点，用于禁言上报（见 internal/banreport）。
func (c *Client) persistMutedUntil(identifier string, muteUntil float64, source string) {
	if c == nil || c.Store == nil || strings.TrimSpace(identifier) == "" {
		return
	}
	if muteUntil <= 0 {
		// 上游未下发到期时间戳时的兜底禁言时长（10分钟）
		muteUntil = float64(time.Now().Add(10 * time.Minute).Unix())
	}
	if err := c.Store.Update(func(cfg *config.Config) error {
		for i := range cfg.Accounts {
			if cfg.Accounts[i].Identifier() != identifier {
				continue
			}
			cfg.Accounts[i].MutedUntil = muteUntil
			break
		}
		if cfg.ElasticPool.Enabled {
			account.ReconcileElasticPool(cfg)
		}
		// 被禁言的账号不再持有 device_id 绑定，下次重新启用后重新分配。
		config.ReconcileDeviceIDBindings(cfg)
		return nil
	}); err != nil {
		config.Logger.Error("[muted_account] failed to persist muted_until", "account", identifier, "error", err)
		return
	}
	if c.Auth != nil && c.Auth.Pool != nil {
		c.Auth.Pool.Reset()
	}
	// 弹性号池可能借机启用补位账号，通知桥立即按已有测速结果分配节点。
	c.notifyAccountPoolChanged()
	// 在持久化之后上报：此时 enabled_count 已经扣除了本次被禁言的账号。
	banreport.Report(c.Store, banreport.Event{
		Type:      banreport.KindMuted,
		Account:   identifier,
		MuteUntil: muteUntil,
		Source:    source,
	})
}

// isUserBannedResponse 判断登录响应是否表示账号被上游停用（USER_IS_BANNED）。
func isUserBannedResponse(bizCode int, bizMsg string) bool {
	msg := strings.ToLower(strings.TrimSpace(bizMsg))
	return strings.Contains(msg, "user_is_banned") || bizCode == 10
}

// persistBannedAccount 持久化账号因上游 USER_IS_BANNED 而被停用的状态。
// 与禁言不同：禁言有 MutedUntil 可自动恢复，而被上游停用需等待
// 下次成功刷新 token 后才会解除。
// source 标记检测点，用于封号上报（见 internal/banreport）。
func (c *Client) persistBannedAccount(identifier string, reason string, source string) {
	if c == nil || c.Store == nil || strings.TrimSpace(identifier) == "" || strings.TrimSpace(reason) == "" {
		return
	}
	if err := c.Store.Update(func(cfg *config.Config) error {
		for i := range cfg.Accounts {
			if cfg.Accounts[i].Identifier() != identifier {
				continue
			}
			cfg.Accounts[i].Banned = true
			cfg.Accounts[i].Disabled = true
			cfg.Accounts[i].DisabledReason = reason
			break
		}
		if cfg.ElasticPool.Enabled {
			account.ReconcileElasticPool(cfg)
		}
		// 被封禁的账号不再持有 device_id 绑定。
		config.ReconcileDeviceIDBindings(cfg)
		return nil
	}); err != nil {
		config.Logger.Error("[banned_account] failed to persist banned state", "account", identifier, "error", err)
		return
	}
	if c.Auth != nil && c.Auth.Pool != nil {
		c.Auth.Pool.Reset()
	}
	c.notifyAccountPoolChanged()
	config.Logger.Warn("[banned_account] account disabled after USER_IS_BANNED", "account", identifier, "reason", reason)
	// 在持久化之后上报：此时 enabled_count 已经扣除了本次被封号的账号。
	banreport.Report(c.Store, banreport.Event{
		Type:    banreport.KindBanned,
		Account: identifier,
		Source:  source,
	})
}

// clearBannedAccount 在成功刷新 token 后解除上游停用状态。
func (c *Client) clearBannedAccount(identifier string) {
	if c == nil || c.Store == nil || strings.TrimSpace(identifier) == "" {
		return
	}
	if err := c.Store.Update(func(cfg *config.Config) error {
		for i := range cfg.Accounts {
			if cfg.Accounts[i].Identifier() != identifier {
				continue
			}
			if !cfg.Accounts[i].Banned {
				return nil
			}
			cfg.Accounts[i].Banned = false
			cfg.Accounts[i].Disabled = false
			cfg.Accounts[i].DisabledReason = ""
			break
		}
		if cfg.ElasticPool.Enabled {
			account.ReconcileElasticPool(cfg)
		}
		return nil
	}); err != nil {
		config.Logger.Error("[banned_account] failed to clear banned state", "account", identifier, "error", err)
		return
	}
	if c.Auth != nil && c.Auth.Pool != nil {
		c.Auth.Pool.Reset()
	}
	c.notifyAccountPoolChanged()
	config.Logger.Info("[banned_account] account re-enabled after successful token refresh", "account", identifier)
}

// captchaCooldown is how long an account is benched after a captcha challenge.
// A challenge means risk control has already flagged this account; continuing
// to drive traffic through it is the fastest way to escalate to a mute.
// Override with DS2API_CAPTCHA_COOLDOWN_MINUTES.
const defaultCaptchaCooldownMinutes = 30

func captchaCooldownDuration() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("DS2API_CAPTCHA_COOLDOWN_MINUTES")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			return time.Duration(n) * time.Minute
		}
	}
	return defaultCaptchaCooldownMinutes * time.Minute
}

// coolDownAfterCaptcha benches the account and reports whether it was applied.
func (c *Client) coolDownAfterCaptcha(a *auth.RequestAuth, op string) {
	if c == nil || a == nil || !a.UseConfigToken {
		return
	}
	identifier := strings.TrimSpace(a.AccountID)
	if identifier == "" {
		return
	}
	cooldown := captchaCooldownDuration()
	if cooldown <= 0 {
		return
	}
	until := float64(time.Now().Add(cooldown).Unix())
	c.persistCooldownUntil(identifier, until)
	a.Account.CooldownUntil = until
	config.Logger.Warn("[captcha] account cooled down after challenge",
		"op", op, "account", identifier, "cooldown_minutes", int(cooldown.Minutes()), "until", until)
}

// persistCooldownUntil stores the cooldown deadline, mirroring how a mute is
// persisted so an elastic pool can promote a standby account in its place.
func (c *Client) persistCooldownUntil(identifier string, until float64) {
	if c == nil || c.Store == nil || strings.TrimSpace(identifier) == "" || until <= 0 {
		return
	}
	if err := c.Store.Update(func(cfg *config.Config) error {
		for i := range cfg.Accounts {
			if cfg.Accounts[i].Identifier() != identifier {
				continue
			}
			cfg.Accounts[i].CooldownUntil = until
			break
		}
		if cfg.ElasticPool.Enabled {
			account.ReconcileElasticPool(cfg)
		}
		return nil
	}); err != nil {
		config.Logger.Error("[captcha] failed to persist cooldown_until", "account", identifier, "error", err)
		return
	}
	if c.Auth != nil && c.Auth.Pool != nil {
		c.Auth.Pool.Reset()
	}
	// 弹性号池可能借机启用补位账号，通知桥立即按已有测速结果分配节点。
	c.notifyAccountPoolChanged()
}

func (c *Client) CreateSession(ctx context.Context, a *auth.RequestAuth, maxAttempts int) (string, error) {
	if maxAttempts <= 0 {
		maxAttempts = c.maxRetries
	}
	clients := c.requestClientsForAuth(ctx, a)
	attempts := 0
	refreshed := false
	lastFailureKind := FailureUnknown
	lastFailureMessage := ""
	for attempts < maxAttempts {
		headers := c.authHeaders(a.DeepSeekToken, a.Account.Locale)
		resp, status, err := c.postJSONWithStatus(ctx, clients.regular, clients.fallback, dsprotocol.DeepSeekCreateSessionURL, headers, map[string]any{})
		if err != nil {
			config.Logger.Warn("[create_session] request error", "error", err, "account", a.AccountID)
			attempts++
			continue
		}
		code, bizCode, msg, bizMsg := extractResponseStatus(resp)
		if status == http.StatusOK && code == 0 && bizCode == 0 {
			sessionID := extractCreateSessionID(resp)
			if sessionID != "" {
				return sessionID, nil
			}
		}
		if ch := DetectCaptchaChallenge(resp); ch != nil {
			config.Logger.Warn("[create_session] captcha challenge detected", "account", a.AccountID, "instruction", ch.Instruction, "image_url", ch.ImageURL, "rid", ch.Rid)
			c.coolDownAfterCaptcha(a, "create_session")
			if a.UseConfigToken && c.Auth.SwitchAccount(ctx, a) {
				refreshed = false
				attempts++
				continue
			}
			return "", &RequestFailure{Op: "create session", Kind: FailureCaptchaRequired, Message: failureMessage(msg, bizMsg, "captcha challenge required")}
		}
		config.Logger.Warn("[create_session] failed", "status", status, "code", code, "biz_code", bizCode, "msg", msg, "biz_msg", bizMsg, "use_config_token", a.UseConfigToken, "account", a.AccountID)
		lastFailureMessage = failureMessage(msg, bizMsg, "create session failed")
		if isTokenInvalid(status, code, bizCode, msg, bizMsg) || isAuthIndicativeBizFailure(msg, bizMsg) {
			lastFailureKind = authFailureKind(a.UseConfigToken)
		} else {
			lastFailureKind = FailureUnknown
		}
		if a.UseConfigToken {
			if shouldAttemptRefresh(status, code, bizCode, msg, bizMsg) && c.handleManagedAuthFailure(ctx, a, &refreshed) {
				continue
			}
			if c.Auth.SwitchAccount(ctx, a) {
				refreshed = false
				attempts++
				continue
			}
		}
		attempts++
	}
	if lastFailureKind != FailureUnknown {
		return "", &RequestFailure{Op: "create session", Kind: lastFailureKind, Message: lastFailureMessage}
	}
	return "", errors.New("create session failed")
}

func (c *Client) GetPow(ctx context.Context, a *auth.RequestAuth, maxAttempts int) (string, error) {
	return c.GetPowForTarget(ctx, a, dsprotocol.DeepSeekCompletionTargetPath, maxAttempts)
}

func (c *Client) GetPowForTarget(ctx context.Context, a *auth.RequestAuth, targetPath string, maxAttempts int) (string, error) {
	if maxAttempts <= 0 {
		maxAttempts = c.maxRetries
	}
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		targetPath = dsprotocol.DeepSeekCompletionTargetPath
	}
	// PoW challenge 缓存：命中后台预取回填的新鲜 challenge 时直接本地求解，
	// 省一次 create_pow_challenge 往返。条目取出即消费，随后再预取回填。
	if a != nil && strings.TrimSpace(a.AccountID) != "" {
		if challenge, ok := c.powCache.get(a.AccountID, targetPath); ok {
			answer, solveErr := ComputePow(ctx, challenge)
			if solveErr == nil {
				header, buildErr := BuildPowHeader(challenge, answer)
				if buildErr == nil {
					c.prefetchPowChallenge(a, targetPath)
					return header, nil
				}
				config.Logger.Warn("[get_pow] build cached pow header failed", "account", a.AccountID, "target_path", targetPath, "error", buildErr)
			} else {
				config.Logger.Warn("[get_pow] cached challenge solve failed", "account", a.AccountID, "target_path", targetPath, "error", solveErr)
			}
		}
	}
	clients := c.requestClientsForAuth(ctx, a)
	attempts := 0
	refreshed := false
	lastFailureKind := FailureUnknown
	lastFailureMessage := ""
	for attempts < maxAttempts {
		headers := c.authHeaders(a.DeepSeekToken, a.Account.Locale)
		resp, status, err := c.postJSONWithStatus(ctx, clients.regular, clients.fallback, dsprotocol.DeepSeekCreatePowURL, headers, map[string]any{"target_path": targetPath})
		if err != nil {
			config.Logger.Warn("[get_pow] request error", "error", err, "account", a.AccountID, "target_path", targetPath)
			lastFailureKind = FailureUnknown
			lastFailureMessage = err.Error()
			attempts++
			continue
		}
		code, bizCode, msg, bizMsg := extractResponseStatus(resp)
		if status == http.StatusOK && code == 0 && bizCode == 0 {
			data, _ := resp["data"].(map[string]any)
			bizData, _ := data["biz_data"].(map[string]any)
			challenge, _ := bizData["challenge"].(map[string]any)
			answer, err := ComputePow(ctx, challenge)
			if err != nil {
				attempts++
				continue
			}
			header, err := BuildPowHeader(challenge, answer)
			if err != nil {
				config.Logger.Warn("[get_pow] build pow header failed", "account", a.AccountID, "target_path", targetPath, "error", err)
				attempts++
				continue
			}
			c.prefetchPowChallenge(a, targetPath)
			return header, nil
		}
		if ch := DetectCaptchaChallenge(resp); ch != nil {
			config.Logger.Warn("[get_pow] captcha challenge detected", "account", a.AccountID, "target_path", targetPath, "instruction", ch.Instruction, "image_url", ch.ImageURL, "rid", ch.Rid)
			c.coolDownAfterCaptcha(a, "get_pow")
			lastFailureKind = FailureCaptchaRequired
			lastFailureMessage = failureMessage(msg, bizMsg, "captcha challenge required")
			if a.UseConfigToken && c.Auth.SwitchAccount(ctx, a) {
				refreshed = false
				attempts++
				continue
			}
			attempts++
			continue
		}
		config.Logger.Warn("[get_pow] failed", "status", status, "code", code, "biz_code", bizCode, "msg", msg, "biz_msg", bizMsg, "use_config_token", a.UseConfigToken, "account", a.AccountID, "target_path", targetPath)
		lastFailureMessage = failureMessage(msg, bizMsg, "get pow failed")
		if isTokenInvalid(status, code, bizCode, msg, bizMsg) || isAuthIndicativeBizFailure(msg, bizMsg) {
			lastFailureKind = authFailureKind(a.UseConfigToken)
		} else {
			lastFailureKind = FailureUnknown
		}
		if a.UseConfigToken {
			if shouldAttemptRefresh(status, code, bizCode, msg, bizMsg) && c.handleManagedAuthFailure(ctx, a, &refreshed) {
				continue
			}
			if c.Auth.SwitchAccount(ctx, a) {
				refreshed = false
				attempts++
				continue
			}
		}
		attempts++
	}
	if lastFailureKind != FailureUnknown {
		return "", &RequestFailure{Op: "get pow", Kind: lastFailureKind, Message: lastFailureMessage}
	}
	return "", errors.New("get pow failed")
}

func (c *Client) authHeaders(token string, locale string) map[string]string {
	headers := dsprotocol.BaseHeadersFor(locale)
	headers["authorization"] = "Bearer " + token
	return headers
}

// localeFromContext 尝试从上下文中提取账号 locale，供只有 token 的直通接口使用。
func localeFromContext(ctx context.Context) string {
	if a, ok := auth.FromContext(ctx); ok {
		return strings.TrimSpace(a.Account.Locale)
	}
	return ""
}

func isTokenInvalid(status int, code int, bizCode int, msg string, bizMsg string) bool {
	msg = strings.ToLower(strings.TrimSpace(msg) + " " + strings.TrimSpace(bizMsg))
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return true
	}
	if code == 40001 || code == 40002 || code == 40003 || bizCode == 40001 || bizCode == 40002 || bizCode == 40003 {
		return true
	}
	return strings.Contains(msg, "token") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "expired") ||
		strings.Contains(msg, "not login") ||
		strings.Contains(msg, "login required") ||
		strings.Contains(msg, "invalid jwt")
}

func shouldAttemptRefresh(status int, code int, bizCode int, msg string, bizMsg string) bool {
	if isTokenInvalid(status, code, bizCode, msg, bizMsg) {
		return true
	}
	// Some DeepSeek failures come back as HTTP 200/code=0 but with non-zero biz_code.
	// Only attempt refresh when these biz failures still look auth-related.
	return status == http.StatusOK &&
		code == 0 &&
		bizCode != 0 &&
		isAuthIndicativeBizFailure(msg, bizMsg)
}

func isAuthIndicativeBizFailure(msg string, bizMsg string) bool {
	combined := strings.ToLower(strings.TrimSpace(msg) + " " + strings.TrimSpace(bizMsg))
	authKeywords := []string{
		"auth",
		"authorization",
		"credential",
		"expired",
		"invalid jwt",
		"jwt",
		"login",
		"not login",
		"session expired",
		"token",
		"unauthorized",
		"登录",
		"未登录",
		"认证",
		"凭证",
		"会话过期",
		"令牌",
	}
	for _, keyword := range authKeywords {
		if strings.Contains(combined, keyword) {
			return true
		}
	}
	return false
}

func authFailureKind(useConfigToken bool) FailureKind {
	if useConfigToken {
		return FailureManagedUnauthorized
	}
	return FailureDirectUnauthorized
}

func failureMessage(msg string, bizMsg string, fallback string) string {
	if trimmed := strings.TrimSpace(bizMsg); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(msg); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(fallback)
}

// DeepSeek has returned create-session ids in both biz_data.id and
// biz_data.chat_session.id across observed response variants; accept either.
func extractCreateSessionID(resp map[string]any) string {
	data, _ := resp["data"].(map[string]any)
	bizData, _ := data["biz_data"].(map[string]any)
	if sessionID, _ := bizData["id"].(string); strings.TrimSpace(sessionID) != "" {
		return strings.TrimSpace(sessionID)
	}
	if chatSession, ok := bizData["chat_session"].(map[string]any); ok {
		if sessionID, _ := chatSession["id"].(string); strings.TrimSpace(sessionID) != "" {
			return strings.TrimSpace(sessionID)
		}
	}
	return ""
}

func extractResponseStatus(resp map[string]any) (code int, bizCode int, msg string, bizMsg string) {
	code = intFrom(resp["code"])
	msg, _ = resp["msg"].(string)
	data, _ := resp["data"].(map[string]any)
	bizCode = intFrom(data["biz_code"])
	bizMsg, _ = data["biz_msg"].(string)
	if strings.TrimSpace(bizMsg) == "" {
		if bizData, ok := data["biz_data"].(map[string]any); ok {
			bizMsg, _ = bizData["msg"].(string)
		}
	}
	return code, bizCode, msg, bizMsg
}

func normalizeMobileForLogin(raw string) (mobile string, areaCode string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ""
	}
	hasPlus := strings.HasPrefix(s, "+")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if digits == "" {
		return "", ""
	}
	if (hasPlus || strings.HasPrefix(digits, "86")) && strings.HasPrefix(digits, "86") && len(digits) == 13 {
		return digits[2:], "+86"
	}
	return digits, "+86"
}
