package chat

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"ds2api/internal/auth"
	"ds2api/internal/completionruntime"
	"ds2api/internal/config"
	dsprotocol "ds2api/internal/deepseek/protocol"
	"ds2api/internal/httpapi/openai/history"
	"ds2api/internal/promptcompat"
	"ds2api/internal/util"

	"github.com/google/uuid"
)

func (h *Handler) handleVercelStreamPrepare(w http.ResponseWriter, r *http.Request) {
	if !config.IsVercel() {
		http.NotFound(w, r)
		return
	}
	h.sweepExpiredStreamLeases()
	internalSecret := vercelInternalSecret()
	internalToken := strings.TrimSpace(r.Header.Get("X-Ds2-Internal-Token"))
	if internalSecret == "" || subtle.ConstantTimeCompare([]byte(internalToken), []byte(internalSecret)) != 1 {
		writeOpenAIError(w, http.StatusUnauthorized, "unauthorized internal request")
		return
	}

	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid json")
		return
	}
	// 与非流式链路一致：是否注入工具提示词与号池路由均按“body 是否携带工具
	// 定义”决定，写入上下文供 Determine 读取（保持 Vercel 流式镜像与主链路行为一致）。
	// 按 Key 的开关同样经 promptcompat.ApplyCallerFlags 生效，两条链路不会分叉。
	r = r.WithContext(auth.WithToolsPresent(r.Context(), promptcompat.ApplyCallerFlags(h.Auth, r, req)))

	a, err := h.Auth.Determine(r)
	if err != nil {
		status := http.StatusUnauthorized
		if auth.IsNoIdleAccountError(err) {
			status = http.StatusTooManyRequests
		}
		writeOpenAIError(w, status, err.Error())
		return
	}
	leased := false
	defer func() {
		if !leased {
			h.Auth.Release(a)
		}
	}()
	r = r.WithContext(auth.WithAuth(r.Context(), a))

	// 图片处理分三步且顺序固定，与非流式主链路一致：先判定（此时图片块还在，
	// 上传要用），再在两轮内联上传之后剥离图片块（图片改经 ref_file_ids 送达），
	// 最后在标准化之后还原客户端请求的模型名。
	imageRoute := promptcompat.PlanImageRoute(req, h.Store)
	if err := h.preprocessInlineFileInputs(r.Context(), a, req); err != nil {
		writeOpenAIInlineFileError(w, err)
		return
	}
	if err := h.preprocessInlineTextFilesForExpert(r.Context(), a, req); err != nil {
		writeOpenAIInlineFileError(w, err)
		return
	}
	imageRoute.StripImages(req)
	if !util.ToBool(req["stream"]) {
		writeOpenAIError(w, http.StatusBadRequest, "stream must be true")
		return
	}
	stdReq, err := promptcompat.NormalizeOpenAIChatRequest(h.Store, req, requestTraceID(r), a.ToolMarker)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, err.Error())
		return
	}
	imageRoute.RestoreModelName(&stdReq)
	if !stdReq.Stream {
		writeOpenAIError(w, http.StatusBadRequest, "stream must be true")
		return
	}
	stdReq, err = h.applyCurrentInputFile(r.Context(), a, stdReq)
	if err != nil {
		status, message := mapCurrentInputFileError(err)
		writeOpenAIError(w, status, message)
		return
	}

	sessionID, powHeader, payload, outErr := completionruntime.PrepareCompletionPayload(r.Context(), h.DS, a, stdReq, completionruntime.Options{
		CurrentInputFile:    h.Store,
		ExpertPromptSegment: h.Store,
	}, 3)
	if outErr != nil {
		writeOpenAIError(w, outErr.Status, outErr.Message)
		return
	}
	if strings.TrimSpace(a.DeepSeekToken) == "" {
		writeOpenAIError(w, http.StatusUnauthorized, "Invalid token. If this should be a DS2API key, add it to config.keys first.")
		return
	}

	leaseID := h.holdStreamLease(a, stdReq, sessionID)
	if leaseID == "" {
		writeOpenAIError(w, http.StatusInternalServerError, "failed to create stream lease")
		return
	}
	leased = true
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":       sessionID,
		"lease_id":         leaseID,
		"model":            stdReq.ResponseModel,
		"final_prompt":     stdReq.FinalPrompt,
		"thinking_enabled": stdReq.Thinking,
		"search_enabled":   stdReq.Search,
		"tool_names":       stdReq.ToolNames,
		// The Node stream mirror performs the tool sieve itself, so it needs the
		// caller-specific marker to normalize the model's output back to EPSE
		// before parsing (the Go path does the same inside toolstream.State).
		"tool_marker":    stdReq.ToolMarker,
		"deepseek_token": a.DeepSeekToken,
		"pow_header":     powHeader,
		"payload":        payload,
		"base_headers":   dsprotocol.BaseHeadersFor(a.Account.Locale),
	})
}

func (h *Handler) handleVercelStreamRelease(w http.ResponseWriter, r *http.Request) {
	if !config.IsVercel() {
		http.NotFound(w, r)
		return
	}
	h.sweepExpiredStreamLeases()
	internalSecret := vercelInternalSecret()
	internalToken := strings.TrimSpace(r.Header.Get("X-Ds2-Internal-Token"))
	if internalSecret == "" || subtle.ConstantTimeCompare([]byte(internalToken), []byte(internalSecret)) != 1 {
		writeOpenAIError(w, http.StatusUnauthorized, "unauthorized internal request")
		return
	}

	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid json")
		return
	}
	leaseID, _ := req["lease_id"].(string)
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		writeOpenAIError(w, http.StatusBadRequest, "lease_id is required")
		return
	}
	lease, ok := h.releaseStreamLease(leaseID)
	if !ok {
		writeOpenAIError(w, http.StatusNotFound, "stream lease not found")
		return
	}
	if h.Auth != nil && lease.Auth != nil {
		defer h.Auth.Release(lease.Auth)
	}
	if lease.Auth != nil {
		h.autoDeleteRemoteSession(r.Context(), lease.Auth, lease.SessionID)
	}
	h.recordVercelStreamCompletion(r, lease.Auth, lease.Standard, req)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// recordVercelStreamCompletion 在 Vercel Node 流式路径收尾（release）时
// 把最终结果回填到聊天历史并记录 Token 用量。Node 侧会在 release 请求里
// 携带 finish_reason/usage/thinking/content/error。
func (h *Handler) recordVercelStreamCompletion(r *http.Request, a *auth.RequestAuth, stdReq promptcompat.StandardRequest, req map[string]any) {
	if a == nil {
		return
	}
	finishReason := strings.TrimSpace(asString(req["finish_reason"]))
	if finishReason == "" {
		finishReason = "stop"
	}
	thinking := asString(req["thinking"])
	content := asString(req["content"])
	errorMessage := strings.TrimSpace(asString(req["error"]))
	usage, _ := req["usage"].(map[string]any)

	// 流尚未建立就失败（例如上游连接失败）的 release 不产生任何有意义的
	// 记录，跳过以免写入空的历史条目。
	if usage == nil && thinking == "" && content == "" && errorMessage == "" {
		return
	}

	session := startChatHistoryWithUsage(h.ChatHistory, h.Usage, r, a, stdReq)
	if session == nil {
		if usage != nil && h.Usage != nil {
			h.Usage.Record(recordModelForUsage(stdReq), strings.TrimSpace(a.CallerID), usage, time.Now())
		}
		return
	}
	if errorMessage != "" {
		session.error(http.StatusInternalServerError, errorMessage, finishReason, thinking, content)
		return
	}
	if finishReason == "stopped" {
		session.stopped(thinking, content, finishReason)
		return
	}
	session.success(http.StatusOK, thinking, content, finishReason, usage)
}

func (h *Handler) handleVercelStreamPow(w http.ResponseWriter, r *http.Request) {
	if !config.IsVercel() {
		http.NotFound(w, r)
		return
	}
	internalSecret := vercelInternalSecret()
	internalToken := strings.TrimSpace(r.Header.Get("X-Ds2-Internal-Token"))
	if internalSecret == "" || subtle.ConstantTimeCompare([]byte(internalToken), []byte(internalSecret)) != 1 {
		writeOpenAIError(w, http.StatusUnauthorized, "unauthorized internal request")
		return
	}

	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid json")
		return
	}
	leaseID, _ := req["lease_id"].(string)
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		writeOpenAIError(w, http.StatusBadRequest, "lease_id is required")
		return
	}
	leaseAuth := h.lookupStreamLeaseAuth(leaseID)
	if leaseAuth == nil {
		writeOpenAIError(w, http.StatusNotFound, "stream lease not found or expired")
		return
	}
	powHeader, err := h.DS.GetPow(r.Context(), leaseAuth, 3)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "Failed to get PoW.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pow_header": powHeader,
	})
}

func (h *Handler) handleVercelStreamSwitch(w http.ResponseWriter, r *http.Request) {
	if !config.IsVercel() {
		http.NotFound(w, r)
		return
	}
	h.sweepExpiredStreamLeases()
	internalSecret := vercelInternalSecret()
	internalToken := strings.TrimSpace(r.Header.Get("X-Ds2-Internal-Token"))
	if internalSecret == "" || subtle.ConstantTimeCompare([]byte(internalToken), []byte(internalSecret)) != 1 {
		writeOpenAIError(w, http.StatusUnauthorized, "unauthorized internal request")
		return
	}

	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid json")
		return
	}
	leaseID, _ := req["lease_id"].(string)
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		writeOpenAIError(w, http.StatusBadRequest, "lease_id is required")
		return
	}
	lease, ok := h.lookupStreamLease(leaseID)
	if !ok || lease.Auth == nil {
		writeOpenAIError(w, http.StatusNotFound, "stream lease not found or expired")
		return
	}
	a := lease.Auth
	disable, _ := req["disable"].(bool)
	mutedUntil, _ := req["mute_until"].(float64)
	banned, _ := req["banned"].(bool)
	bannedReason, _ := req["banned_reason"].(string)
	authFailed, _ := req["auth_failed"].(bool)
	if mutedUntil > 0 && a.UseConfigToken {
		h.Auth.SetAccountMutedUntil(a, mutedUntil)
	}
	if banned && a.UseConfigToken {
		if strings.TrimSpace(bannedReason) == "" {
			bannedReason = "账户已被停用"
		}
		h.Auth.SetAccountBanned(a, bannedReason)
	}
	// Node 镜像在上游以 401 拒绝本账号时带上 auth_failed 标记：恢复顺序（先为同一
	// 账号强制刷新 Token，再换号）由 auth 层共享，与 client 层的上游调用点和
	// completionruntime 同源，见 auth.RequestAuth.RecoverAuthFailure。
	//
	// 只有"本请求内已成功刷新过、但新 Token 仍被上游拒绝"才计入账号级鉴权失败，
	// 该判定也在共享步骤内；刷新本身失败的原因（登录被上游拒绝 / 网络抖动）由
	// auth.Resolver 在 RefreshTokenErr 内按错误类型判定。
	//
	// RecoverAuthFailure 内部已经完成"刷新失败则换号"的完整尝试，返回
	// AuthRecoveryNone 即表示刷新与换号都不成，调用方只按失败路径处理：这里不能
	// 再调一次 SwitchAccount，否则会重复 Release 当前账号（引用计数多减一次，可能
	// 把并发请求仍在使用的账号名额提前放回号池）。非鉴权失败（空输出 / 禁言 /
	// 封号）没有可刷新的凭据问题，仍由 SwitchAccount 换一次号。
	authRecovered := false
	if authFailed && a.UseConfigToken {
		authRecovered = a.RecoverAuthFailure(r.Context()) != auth.AuthRecoveryNone
	} else if !authFailed && a.UseConfigToken {
		authRecovered = a.SwitchAccount(r.Context())
	}
	if !authRecovered {
		if banned {
			writeOpenAIErrorWithCode(w, http.StatusForbidden, "Account is banned by upstream.", "account_banned")
		} else if mutedUntil > 0 {
			writeOpenAIErrorWithCode(w, http.StatusForbidden, "Account is muted by upstream.", "account_muted")
		} else if authFailed {
			writeOpenAIErrorWithCode(w, http.StatusUnauthorized, "Account token is invalid. Please re-login the account in admin.", "account_unauthorized")
		} else if disable {
			writeOpenAIErrorWithCode(w, http.StatusServiceUnavailable, "Upstream service is unavailable and returned no output.", "upstream_unavailable")
		} else {
			writeOpenAIErrorWithCode(w, http.StatusTooManyRequests, "Upstream account hit a rate limit and returned reasoning without visible output.", "upstream_empty_output")
		}
		return
	}

	stdReq := lease.Standard
	var err error
	if stdReq.CurrentInputFileApplied {
		stdReq, err = (history.Service{Store: h.Store, DS: h.DS}).ReuploadAppliedCurrentInputFile(r.Context(), a, stdReq)
		if err != nil {
			status, message := mapCurrentInputFileError(err)
			writeOpenAIError(w, status, message)
			return
		}
	}
	sessionID, powHeader, payload, outErr := completionruntime.PrepareCompletionPayload(r.Context(), h.DS, a, stdReq, completionruntime.Options{
		CurrentInputFile:    h.Store,
		ExpertPromptSegment: h.Store,
	}, 3)
	if outErr != nil {
		writeOpenAIError(w, outErr.Status, outErr.Message)
		return
	}
	if strings.TrimSpace(a.DeepSeekToken) == "" {
		writeOpenAIError(w, http.StatusUnauthorized, "Account token is invalid. Please re-login the account in admin.")
		return
	}
	h.updateStreamLeaseState(leaseID, stdReq, sessionID)
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":       sessionID,
		"lease_id":         leaseID,
		"model":            stdReq.ResponseModel,
		"final_prompt":     stdReq.FinalPrompt,
		"thinking_enabled": stdReq.Thinking,
		"search_enabled":   stdReq.Search,
		"tool_names":       stdReq.ToolNames,
		"tool_marker":      stdReq.ToolMarker,
		"deepseek_token":   a.DeepSeekToken,
		"pow_header":       powHeader,
		"payload":          payload,
		"base_headers":     dsprotocol.BaseHeadersFor(a.Account.Locale),
	})
}

func isVercelStreamPrepareRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.TrimSpace(r.URL.Query().Get("__stream_prepare")) == "1"
}

func isVercelStreamReleaseRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.TrimSpace(r.URL.Query().Get("__stream_release")) == "1"
}

func isVercelStreamPowRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.TrimSpace(r.URL.Query().Get("__stream_pow")) == "1"
}

func isVercelStreamSwitchRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.TrimSpace(r.URL.Query().Get("__stream_switch")) == "1"
}

func vercelInternalSecret() string {
	if v := strings.TrimSpace(os.Getenv("DS2API_VERCEL_INTERNAL_SECRET")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("DS2API_ADMIN_KEY")); v != "" {
		return v
	}
	return "admin"
}

func (h *Handler) holdStreamLease(a *auth.RequestAuth, stdReq promptcompat.StandardRequest, sessionID string) string {
	if a == nil {
		return ""
	}
	now := time.Now()
	ttl := streamLeaseTTL()
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}

	h.leaseMu.Lock()
	expired := h.popExpiredLeasesLocked(now)
	if h.streamLeases == nil {
		h.streamLeases = make(map[string]streamLease)
	}
	leaseID := newLeaseID()
	h.streamLeases[leaseID] = streamLease{
		Auth:      a,
		Standard:  stdReq,
		SessionID: sessionID,
		ExpiresAt: now.Add(ttl),
	}
	h.leaseMu.Unlock()
	h.releaseExpiredAuths(expired)
	return leaseID
}

func (h *Handler) lookupStreamLease(leaseID string) (streamLease, bool) {
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		return streamLease{}, false
	}
	h.leaseMu.Lock()
	lease, ok := h.streamLeases[leaseID]
	h.leaseMu.Unlock()
	if !ok || time.Now().After(lease.ExpiresAt) {
		return streamLease{}, false
	}
	return lease, true
}

func (h *Handler) lookupStreamLeaseAuth(leaseID string) *auth.RequestAuth {
	lease, ok := h.lookupStreamLease(leaseID)
	if !ok {
		return nil
	}
	return lease.Auth
}

func (h *Handler) updateStreamLeaseState(leaseID string, stdReq promptcompat.StandardRequest, sessionID string) {
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		return
	}
	h.leaseMu.Lock()
	defer h.leaseMu.Unlock()
	lease, ok := h.streamLeases[leaseID]
	if !ok {
		return
	}
	lease.Standard = stdReq
	lease.SessionID = sessionID
	h.streamLeases[leaseID] = lease
}

func (h *Handler) releaseStreamLease(leaseID string) (streamLease, bool) {
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		return streamLease{}, false
	}

	h.leaseMu.Lock()
	expired := h.popExpiredLeasesLocked(time.Now())
	lease, ok := h.streamLeases[leaseID]
	if ok {
		delete(h.streamLeases, leaseID)
	}
	h.leaseMu.Unlock()
	h.releaseExpiredAuths(expired)

	if !ok {
		return streamLease{}, false
	}
	return lease, true
}

func (h *Handler) popExpiredLeasesLocked(now time.Time) []*auth.RequestAuth {
	if len(h.streamLeases) == 0 {
		return nil
	}
	expired := make([]*auth.RequestAuth, 0)
	for leaseID, lease := range h.streamLeases {
		if now.After(lease.ExpiresAt) {
			delete(h.streamLeases, leaseID)
			expired = append(expired, lease.Auth)
		}
	}
	return expired
}

func (h *Handler) releaseExpiredAuths(expired []*auth.RequestAuth) {
	if h.Auth == nil || len(expired) == 0 {
		return
	}
	for _, a := range expired {
		h.Auth.Release(a)
	}
}

func (h *Handler) sweepExpiredStreamLeases() {
	h.leaseMu.Lock()
	expired := h.popExpiredLeasesLocked(time.Now())
	h.leaseMu.Unlock()
	h.releaseExpiredAuths(expired)
}

func streamLeaseTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv("DS2API_VERCEL_STREAM_LEASE_TTL_SECONDS"))
	if raw == "" {
		return 15 * time.Minute
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return 15 * time.Minute
	}
	return time.Duration(seconds) * time.Second
}

func newLeaseID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}
