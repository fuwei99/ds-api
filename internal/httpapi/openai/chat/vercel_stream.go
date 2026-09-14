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
	// 「强制禁用工具调用」同样在 Vercel 流式镜像上生效，避免两条链路行为分叉。
	if h.Auth.ForceDisableToolsForRequest(r) {
		promptcompat.DisableToolsForRequest(req)
	}
	r = r.WithContext(auth.WithToolsPresent(r.Context(), promptcompat.RequestBodyHasTools(req)))

	a, err := h.Auth.Determine(r)
	if err != nil {
		status := http.StatusUnauthorized
		if err == auth.ErrNoAccount {
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

	originalModel, rerouted := promptcompat.MaybeAutoRouteVision(req, h.Store)
	if err := h.preprocessInlineFileInputs(r.Context(), a, req); err != nil {
		writeOpenAIInlineFileError(w, err)
		return
	}
	if err := h.preprocessInlineTextFilesForExpert(r.Context(), a, req); err != nil {
		writeOpenAIInlineFileError(w, err)
		return
	}
	if rerouted {
		promptcompat.StripImageBlocksFromRequest(req)
	}
	if !util.ToBool(req["stream"]) {
		writeOpenAIError(w, http.StatusBadRequest, "stream must be true")
		return
	}
	stdReq, err := promptcompat.NormalizeOpenAIChatRequest(h.Store, req, requestTraceID(r), a.ToolMarker)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if rerouted && originalModel != "" {
		stdReq.RequestedModel = originalModel
		stdReq.ResponseModel = originalModel
	}
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
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
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
	if mutedUntil > 0 && a.UseConfigToken {
		h.Auth.SetAccountMutedUntil(a, mutedUntil)
	}
	if banned && a.UseConfigToken {
		if strings.TrimSpace(bannedReason) == "" {
			bannedReason = "账户已被停用"
		}
		h.Auth.SetAccountBanned(a, bannedReason)
	}
	if !a.UseConfigToken || !a.SwitchAccount(r.Context()) {
		if banned {
			writeOpenAIErrorWithCode(w, http.StatusForbidden, "Account is banned by upstream.", "account_banned")
		} else if mutedUntil > 0 {
			writeOpenAIErrorWithCode(w, http.StatusForbidden, "Account is muted by upstream.", "account_muted")
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
