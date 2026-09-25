package completionruntime

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"ds2api/internal/assistantturn"
	"ds2api/internal/auth"
	"ds2api/internal/chathistory"
	"ds2api/internal/config"
	dsclient "ds2api/internal/deepseek/client"
	"ds2api/internal/httpapi/openai/history"
	"ds2api/internal/httpapi/openai/shared"
	"ds2api/internal/promptcompat"
	"ds2api/internal/sse"
	"ds2api/internal/toolcall"
)

type DeepSeekCaller interface {
	CreateSession(ctx context.Context, a *auth.RequestAuth, maxAttempts int) (string, error)
	GetPow(ctx context.Context, a *auth.RequestAuth, maxAttempts int) (string, error)
	UploadFile(ctx context.Context, a *auth.RequestAuth, req dsclient.UploadFileRequest, maxAttempts int) (*dsclient.UploadFileResult, error)
	CallCompletion(ctx context.Context, a *auth.RequestAuth, payload map[string]any, powResp string, maxAttempts int) (*http.Response, error)
	StopStream(ctx context.Context, a *auth.RequestAuth, sessionID string, messageID int) error
	FireCompletionAndStop(ctx context.Context, a *auth.RequestAuth, payload map[string]any, powResp string) (int, error)
}

type Options struct {
	StripReferenceMarkers bool
	MaxAttempts           int
	RetryEnabled          bool
	RetryMaxAttempts      int
	CurrentInputFile      history.CurrentInputConfigReader
	ExpertPromptSegment   ExpertPromptSegmentConfigReader
	// ToolCallRepairEnabled turns on the phase-3 finalize-only LLM tool-call
	// repair pass. When enabled, the runtime builds a repair invoker bound to
	// the request's account (expert mode, thinking off, new session, 10s) and
	// hands it to the finalize turn builder so residual bad tool-call code can
	// be repaired. It is never wired into the streaming sieve.
	ToolCallRepairEnabled bool
	// ChatHistory is the response-history store used to record the proactive
	// tool-call repair sub-request as its own "toolcall.repair" entry. Optional:
	// when nil the repair pass runs unrecorded.
	ChatHistory *chathistory.Store
	// toolCallRepair / toolCallRepairCtx are populated internally by the
	// Execute* entrypoints (where ds and auth are available) and threaded into
	// the finalize turn build options.
	toolCallRepair    toolcall.ToolCallRepairInvoker
	toolCallRepairCtx context.Context
}

type NonStreamResult struct {
	SessionID string
	Payload   map[string]any
	Turn      assistantturn.Turn
	Attempts  int
}

type StartResult struct {
	SessionID string
	Payload   map[string]any
	Pow       string
	Response  *http.Response
	Request   promptcompat.StandardRequest
}

func StartCompletion(ctx context.Context, ds DeepSeekCaller, a *auth.RequestAuth, stdReq promptcompat.StandardRequest, opts Options) (StartResult, *assistantturn.OutputError) {
	start, outErr := startCompletionForRequest(ctx, ds, a, stdReq, opts)
	// 弹性号池/多账号场景下，上游封号（禁言/停用）会让当前账号在开跑阶段直接
	// 失败。此时立即切换到下一个可调度账号重试本次请求，而不是把错误透传给
	// 客户端；被禁言账号的补位在客户端持久化 mute_until 时已同步完成，因此
	// 全程无需人工干预。上游返回的临时 5xx 响应同样在此升级为可重试错误，
	// 否则流式/非流式开跑阶段会把它原样透传给客户端。
	outErr = promoteTransientStartResponse(&start, a, outErr)
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	attempted := false
	for outErr != nil && canRetryOnAlternateAccount(ctx, a, outErr, true, &attempted) {
		config.Logger.Info("[completion_runtime_account_switch_retry] retrying start after account failure",
			"surface", stdReq.Surface, "account", a.AccountID, "status", outErr.Status, "error_code", outErr.Code)
		start, outErr = startStandardCompletionOnAlternateAccount(ctx, ds, a, stdReq, opts, maxAttempts)
		outErr = promoteTransientStartResponse(&start, a, outErr)
	}
	return start, outErr
}

// startCompletionForRequest 是 StartCompletion 的纯执行部分：按是否需要分段
// 分派到分段或单次启动，不做账号切换重试。
func startCompletionForRequest(ctx context.Context, ds DeepSeekCaller, a *auth.RequestAuth, stdReq promptcompat.StandardRequest, opts Options) (StartResult, *assistantturn.OutputError) {
	if segments := shouldSegmentExpertPrompt(stdReq, opts); segments != nil {
		return StartCompletionWithSegments(ctx, ds, a, stdReq, opts, segments)
	}
	return startCompletionOnce(ctx, ds, a, stdReq, opts)
}

// PrepareCompletionPayload builds the session-aware completion payload for a
// request without calling the completion endpoint itself. Oversized expert
// prompts are split into segments first (all but the last are sent via
// FireCompletionAndStop), so the caller can stream the returned final payload
// directly. This is the payload-side equivalent of StartCompletion for
// surfaces that stream upstream responses themselves (e.g. the Vercel Node
// stream layer). On success the caller must stream the returned payload with
// the returned PoW on the same account and session.
//
// 与非流式链路一致：账号级鉴权失败（401）先刷新 Token 再换号重试，
// 避免把错误直接透传给客户端（Vercel 流式镜像同样受弹性号池保护）。
func PrepareCompletionPayload(ctx context.Context, ds DeepSeekCaller, a *auth.RequestAuth, stdReq promptcompat.StandardRequest, opts Options, maxAttempts int) (sessionID, pow string, payload map[string]any, outErr *assistantturn.OutputError) {
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	var attempted bool
	sessionID, pow, payload, outErr = prepareCompletionPayloadOnce(ctx, ds, a, stdReq, opts, maxAttempts, false)
	for outErr != nil && canRetryOnAlternateAccount(ctx, a, outErr, true, &attempted) {
		config.Logger.Info("[completion_runtime_account_switch_retry] retrying payload prepare after account failure",
			"surface", stdReq.Surface, "account", a.AccountID, "status", outErr.Status, "error_code", outErr.Code)
		// 换号后当前输入文件必须为新账号重新上传（file_id 是账号级的）。
		sessionID, pow, payload, outErr = prepareCompletionPayloadOnce(ctx, ds, a, stdReq, opts, maxAttempts, true)
	}
	return sessionID, pow, payload, outErr
}

func prepareCompletionPayloadOnce(ctx context.Context, ds DeepSeekCaller, a *auth.RequestAuth, stdReq promptcompat.StandardRequest, opts Options, maxAttempts int, retry bool) (sessionID, pow string, payload map[string]any, outErr *assistantturn.OutputError) {
	var prepErr *assistantturn.OutputError
	if retry {
		stdReq, prepErr = reuploadCurrentInputFileForAccount(ctx, ds, a, stdReq, opts)
	} else {
		stdReq, prepErr = prepareCurrentInputFile(ctx, ds, a, stdReq, opts)
	}
	if prepErr != nil {
		return "", "", nil, prepErr
	}
	var err error
	sessionID, err = ds.CreateSession(ctx, a, maxAttempts)
	if err != nil {
		return "", "", nil, sessionOutputError(a, err)
	}
	if segments := shouldSegmentExpertPrompt(stdReq, opts); len(segments) > 1 {
		finalPow, finalPayload, segErr := fireSegmentPayloads(ctx, ds, a, stdReq, sessionID, segments, maxAttempts)
		if segErr != nil {
			return sessionID, "", nil, segErr
		}
		return sessionID, finalPow, finalPayload, nil
	}
	pow, err = ds.GetPow(ctx, a, maxAttempts)
	if err != nil {
		return sessionID, "", nil, powOutputError(a, err)
	}
	return sessionID, pow, stdReq.CompletionPayload(sessionID), nil
}

func startCompletionOnce(ctx context.Context, ds DeepSeekCaller, a *auth.RequestAuth, stdReq promptcompat.StandardRequest, opts Options) (StartResult, *assistantturn.OutputError) {
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	var prepErr *assistantturn.OutputError
	stdReq, prepErr = prepareCurrentInputFile(ctx, ds, a, stdReq, opts)
	if prepErr != nil {
		return StartResult{Request: stdReq}, prepErr
	}
	sessionID, err := ds.CreateSession(ctx, a, maxAttempts)
	if err != nil {
		return StartResult{Request: stdReq}, sessionOutputError(a, err)
	}
	pow, err := ds.GetPow(ctx, a, maxAttempts)
	if err != nil {
		return StartResult{SessionID: sessionID, Request: stdReq}, powOutputError(a, err)
	}
	payload := stdReq.CompletionPayload(sessionID)
	resp, err := ds.CallCompletion(ctx, a, payload, pow, maxAttempts)
	if err != nil {
		return StartResult{SessionID: sessionID, Payload: payload, Pow: pow, Request: stdReq}, completionCallOutputError(ctx, err)
	}
	return StartResult{SessionID: sessionID, Payload: payload, Pow: pow, Response: resp, Request: stdReq}, nil
}

func prepareCurrentInputFile(ctx context.Context, ds DeepSeekCaller, a *auth.RequestAuth, stdReq promptcompat.StandardRequest, opts Options) (promptcompat.StandardRequest, *assistantturn.OutputError) {
	if opts.CurrentInputFile == nil || stdReq.CurrentInputFileApplied {
		return stdReq, nil
	}
	out, err := (history.Service{Store: opts.CurrentInputFile, DS: ds}).ApplyCurrentInputFile(ctx, a, stdReq)
	if err != nil {
		return out, currentInputFileOutputError(err)
	}
	return out, nil
}

// currentInputFileOutputError 把当前输入文件（上传/解析）失败映射为标准输出错误。
// 上传链路同样是账号级操作：Token 被上游拒绝时要带上 account_unauthorized 错误码，
// 让共享重试逻辑先刷新 Token 再换号，而不是把 401 直接透传给客户端。
func currentInputFileOutputError(err error) *assistantturn.OutputError {
	status, message := history.MapError(err)
	code := "error"
	switch {
	case dsclient.IsMutedError(err):
		code = "account_muted"
	case dsclient.IsManagedUnauthorizedError(err):
		code = OutputCodeAccountUnauthorized
	}
	return &assistantturn.OutputError{Status: status, Message: message, Code: code}
}

func ExecuteNonStreamWithRetry(ctx context.Context, ds DeepSeekCaller, a *auth.RequestAuth, stdReq promptcompat.StandardRequest, opts Options) (NonStreamResult, *assistantturn.OutputError) {
	opts = withToolCallRepair(ctx, ds, a, opts)
	start, startErr := StartCompletion(ctx, ds, a, stdReq, opts)
	if startErr != nil {
		return NonStreamResult{SessionID: start.SessionID, Payload: start.Payload}, startErr
	}
	return ExecuteNonStreamStartedWithRetry(ctx, ds, a, start, opts)
}

// withToolCallRepair lazily builds the phase-3 repair invoker (bound to the
// request account) and scopes it to ctx, but only when repair is enabled and
// not already configured. The repair pass is finalize-only; the streaming sieve
// must never receive an invoker (plan §5.1).
func withToolCallRepair(ctx context.Context, ds DeepSeekCaller, a *auth.RequestAuth, opts Options) Options {
	if !opts.ToolCallRepairEnabled || opts.toolCallRepair != nil {
		return opts
	}
	opts.toolCallRepair = NewToolCallRepairInvoker(ds, a, opts.ChatHistory)
	opts.toolCallRepairCtx = ctx
	return opts
}

func ExecuteNonStreamStartedWithRetry(ctx context.Context, ds DeepSeekCaller, a *auth.RequestAuth, start StartResult, opts Options) (NonStreamResult, *assistantturn.OutputError) {
	opts = withToolCallRepair(ctx, ds, a, opts)
	stdReq := start.Request
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	sessionID := start.SessionID
	payload := start.Payload
	pow := start.Pow

	attempts := 0
	accountSwitchAttempted := false
	currentResp := start.Response
	usagePrompt := stdReq.PromptTokenText
	accumulatedThinking := ""
	accumulatedRawThinking := ""
	accumulatedToolDetectionThinking := ""
	for {
		turn, outErr := collectAttempt(currentResp, stdReq, usagePrompt, opts)
		if outErr != nil {
			if canRetryOnAlternateAccount(ctx, a, outErr, opts.RetryEnabled, &accountSwitchAttempted) {
				switched, switchErr := startStandardCompletionOnAlternateAccount(ctx, ds, a, stdReq, opts, maxAttempts)
				if switchErr != nil {
					return NonStreamResult{SessionID: sessionID, Payload: payload, Attempts: attempts}, switchErr
				}
				if switched.Response != nil {
					config.Logger.Info("[completion_runtime_account_switch_retry] retrying after 429", "surface", stdReq.Surface, "stream", false, "account", a.AccountID)
					sessionID = switched.SessionID
					payload = switched.Payload
					pow = switched.Pow
					currentResp = switched.Response
					usagePrompt = stdReq.PromptTokenText
					accumulatedThinking = ""
					accumulatedRawThinking = ""
					accumulatedToolDetectionThinking = ""
					continue
				}
			}
			return NonStreamResult{SessionID: sessionID, Payload: payload, Attempts: attempts}, outErr
		}
		accumulatedThinking += sse.TrimContinuationOverlap(accumulatedThinking, turn.Thinking)
		accumulatedRawThinking += sse.TrimContinuationOverlap(accumulatedRawThinking, turn.RawThinking)
		accumulatedToolDetectionThinking += sse.TrimContinuationOverlap(accumulatedToolDetectionThinking, turn.DetectionThinking)
		turn.Thinking = accumulatedThinking
		turn.RawThinking = accumulatedRawThinking
		turn.DetectionThinking = accumulatedToolDetectionThinking
		turn = assistantturn.BuildTurnFromCollected(sse.CollectResult{
			Text:                  turn.RawText,
			Thinking:              turn.RawThinking,
			ToolDetectionThinking: turn.DetectionThinking,
			ContentFilter:         turn.ContentFilter,
			CitationLinks:         turn.CitationLinks,
			ResponseMessageID:     turn.ResponseMessageID,
		}, buildOptions(stdReq, usagePrompt, opts))

		retryMax := opts.RetryMaxAttempts
		if retryMax <= 0 {
			retryMax = shared.EmptyOutputRetryMaxAttempts()
		}
		if !opts.RetryEnabled || !assistantturn.ShouldRetryEmptyOutput(turn, attempts, retryMax) {
			if canRetryOnAlternateAccount(ctx, a, turn.Error, opts.RetryEnabled, &accountSwitchAttempted) {
				switched, switchErr := startStandardCompletionOnAlternateAccount(ctx, ds, a, stdReq, opts, maxAttempts)
				if switchErr != nil {
					return NonStreamResult{SessionID: sessionID, Payload: payload, Turn: turn, Attempts: attempts}, switchErr
				}
				if switched.Response != nil {
					config.Logger.Info("[completion_runtime_account_switch_retry] retrying after 429", "surface", stdReq.Surface, "stream", false, "account", a.AccountID)
					sessionID = switched.SessionID
					payload = switched.Payload
					pow = switched.Pow
					currentResp = switched.Response
					usagePrompt = stdReq.PromptTokenText
					accumulatedThinking = ""
					accumulatedRawThinking = ""
					accumulatedToolDetectionThinking = ""
					continue
				}
			}
			return NonStreamResult{SessionID: sessionID, Payload: payload, Turn: turn, Attempts: attempts}, turn.Error
		}

		attempts++
		parentMessageID := retryParentMessageID(turn.ResponseMessageID, payload)
		config.Logger.Info("[completion_runtime_empty_retry] attempting synthetic retry", "surface", stdReq.Surface, "stream", false, "retry_attempt", attempts, "parent_message_id", parentMessageID)
		retryPow, powErr := ds.GetPow(ctx, a, maxAttempts)
		if powErr != nil {
			config.Logger.Warn("[completion_runtime_empty_retry] retry PoW fetch failed, falling back to original PoW", "surface", stdReq.Surface, "retry_attempt", attempts, "error", powErr)
			retryPow = pow
		}
		retryPayload := shared.ClonePayloadForEmptyOutputRetry(payload, parentMessageID)
		nextResp, err := ds.CallCompletion(ctx, a, retryPayload, retryPow, maxAttempts)
		if err != nil {
			callErr := completionCallOutputError(ctx, err)
			if canRetryOnAlternateAccount(ctx, a, callErr, opts.RetryEnabled, &accountSwitchAttempted) {
				switched, switchErr := startStandardCompletionOnAlternateAccount(ctx, ds, a, stdReq, opts, maxAttempts)
				if switchErr != nil {
					return NonStreamResult{SessionID: sessionID, Payload: payload, Turn: turn, Attempts: attempts}, switchErr
				}
				if switched.Response != nil {
					config.Logger.Info("[completion_runtime_account_switch_retry] retrying after retry request failure", "surface", stdReq.Surface, "stream", false, "account", a.AccountID, "error_code", callErr.Code)
					sessionID = switched.SessionID
					payload = switched.Payload
					pow = switched.Pow
					currentResp = switched.Response
					usagePrompt = stdReq.PromptTokenText
					accumulatedThinking = ""
					accumulatedRawThinking = ""
					accumulatedToolDetectionThinking = ""
					continue
				}
			}
			return NonStreamResult{SessionID: sessionID, Payload: payload, Turn: turn, Attempts: attempts}, callErr
		}
		payload = retryPayload
		usagePrompt = shared.UsagePromptWithEmptyOutputRetry(usagePrompt, attempts)
		currentResp = nextResp
	}
}

func retryParentMessageID(observed int, payload map[string]any) int {
	if observed > 0 {
		return observed
	}
	return payloadParentMessageID(payload)
}

func payloadParentMessageID(payload map[string]any) int {
	if payload == nil {
		return 0
	}
	switch v := payload["parent_message_id"].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	default:
		return 0
	}
}

func canRetryOnAlternateAccount(ctx context.Context, a *auth.RequestAuth, outErr *assistantturn.OutputError, retryEnabled bool, attempted *bool) bool {
	if outErr == nil || !retryEnabled || a == nil || !a.UseConfigToken {
		return false
	}
	if isAccountMuted(outErr) {
		return a.SwitchAccount(ctx)
	}
	if isUpstreamUnavailable(outErr) {
		return a.SwitchAccount(ctx)
	}
	if isRetryableUpstreamError(outErr) {
		return a.SwitchAccount(ctx)
	}
	if isManagedUnauthorized(outErr) {
		return retryManagedAuthFailure(ctx, a)
	}
	if outErr.Status != http.StatusTooManyRequests {
		return false
	}
	if attempted == nil || *attempted {
		return false
	}
	*attempted = true
	return a.SwitchAccount(ctx)
}

// retryManagedAuthFailure 处理"托管账号的 Token 被上游拒绝"（401）的情况。
//
// 弹性号池场景下这类错误不应直接透传给客户端：先为当前账号强制重新登录一次
// （同一请求内每账号一次），拿到新 Token 就用同一账号继续跑；刷新不成功就换到
// 下一个可调度账号，从而保证请求不中断。两者都不可用时返回 false，由调用方
// 决定是否终止；账号级的异常判定由 auth.Resolver 在重新登录被拒时完成。
func retryManagedAuthFailure(ctx context.Context, a *auth.RequestAuth) bool {
	if a.RefreshTokenOnce(ctx) {
		return true
	}
	return a.SwitchAccount(ctx)
}

// isManagedUnauthorized 判断错误是否表示托管账号的 Token 被上游拒绝。
// 只认显式的 account_unauthorized 错误码：所有托管账号的 401 都会在构造输出
// 错误时归一化为该错误码（见 authOutputError / sessionOutputError /
// powOutputError / managedUnauthorizedResponseError）。若再用 HTTP 401 兜底，
// 就会把网络抖动一类的失败也当成账号级鉴权失败，触发无谓的强制重新登录与换号。
func isManagedUnauthorized(outErr *assistantturn.OutputError) bool {
	return outErr != nil && outErr.Code == OutputCodeAccountUnauthorized
}

func isUpstreamUnavailable(outErr *assistantturn.OutputError) bool {
	return outErr != nil && outErr.Code == "upstream_unavailable"
}

func isAccountMuted(outErr *assistantturn.OutputError) bool {
	return outErr != nil && outErr.Code == "account_muted"
}

func startStandardCompletionOnAlternateAccount(ctx context.Context, ds DeepSeekCaller, a *auth.RequestAuth, stdReq promptcompat.StandardRequest, opts Options, maxAttempts int) (StartResult, *assistantturn.OutputError) {
	if segments := shouldSegmentExpertPrompt(stdReq, opts); segments != nil {
		return StartCompletionWithSegments(ctx, ds, a, stdReq, opts, segments)
	}
	var prepErr *assistantturn.OutputError
	stdReq, prepErr = reuploadCurrentInputFileForAccount(ctx, ds, a, stdReq, opts)
	if prepErr != nil {
		return StartResult{Request: stdReq}, prepErr
	}
	sessionID, err := ds.CreateSession(ctx, a, maxAttempts)
	if err != nil {
		return StartResult{}, sessionOutputError(a, err)
	}
	pow, err := ds.GetPow(ctx, a, maxAttempts)
	if err != nil {
		return StartResult{SessionID: sessionID}, powOutputError(a, err)
	}
	payload := stdReq.CompletionPayload(sessionID)
	resp, err := ds.CallCompletion(ctx, a, payload, pow, maxAttempts)
	if err != nil {
		return StartResult{SessionID: sessionID, Payload: payload, Pow: pow}, completionCallOutputError(ctx, err)
	}
	return StartResult{SessionID: sessionID, Payload: payload, Pow: pow, Response: resp, Request: stdReq}, nil
}

func reuploadCurrentInputFileForAccount(ctx context.Context, ds DeepSeekCaller, a *auth.RequestAuth, stdReq promptcompat.StandardRequest, opts Options) (promptcompat.StandardRequest, *assistantturn.OutputError) {
	if opts.CurrentInputFile == nil || !stdReq.CurrentInputFileApplied {
		return stdReq, nil
	}
	out, err := (history.Service{Store: opts.CurrentInputFile, DS: ds}).ReuploadAppliedCurrentInputFile(ctx, a, stdReq)
	if err != nil {
		return out, currentInputFileOutputError(err)
	}
	return out, nil
}

func collectAttempt(resp *http.Response, stdReq promptcompat.StandardRequest, usagePrompt string, opts Options) (assistantturn.Turn, *assistantturn.OutputError) {
	defer func() {
		if err := resp.Body.Close(); err != nil {
			config.Logger.Warn("[completion_runtime] response body close failed", "surface", stdReq.Surface, "error", err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		if captchaBody := tryDetectCaptchaFromBody(body); captchaBody != "" {
			config.Logger.Warn("[completion_runtime] captcha challenge detected, mapping to 429 for account switch", "surface", stdReq.Surface, "detail", captchaBody)
			return assistantturn.Turn{}, &assistantturn.OutputError{Status: http.StatusTooManyRequests, Message: "Captcha challenge detected, account may be rate-limited.", Code: "captcha_required"}
		}
		if transientErr := transientUpstreamError(resp.StatusCode, message); transientErr != nil {
			return assistantturn.Turn{}, transientErr
		}
		return assistantturn.Turn{}, &assistantturn.OutputError{Status: resp.StatusCode, Message: message, Code: "error"}
	}
	result := sse.CollectStream(resp, stdReq.Thinking, false)
	// collectAttempt only accumulates raw text/thinking across retries; the
	// terminal turn is rebuilt once from turn.RawText by the retry loop (see
	// ExecuteNonStreamStartedWithRetry). Running LLM repair here would repair
	// the same output twice (H1) — one repair session/completion is created in
	// this build and another in the terminal build, doubling cost, latency and
	// leaked sessions. Build without the repair invoker so repair runs only in
	// the single terminal build.
	return assistantturn.BuildTurnFromCollected(result, buildOptionsWithoutRepair(stdReq, usagePrompt, opts)), nil
}

func buildOptions(stdReq promptcompat.StandardRequest, prompt string, opts Options) assistantturn.BuildOptions {
	built := buildOptionsWithoutRepair(stdReq, prompt, opts)
	built.ToolCallRepair = opts.toolCallRepair
	built.ToolCallRepairCtx = opts.toolCallRepairCtx
	return built
}

// buildOptionsWithoutRepair mirrors buildOptions but never attaches the LLM
// repair invoker. Intermediate builds (per-attempt collection) must use this so
// a single upstream output triggers at most one LLM repair pass.
func buildOptionsWithoutRepair(stdReq promptcompat.StandardRequest, prompt string, opts Options) assistantturn.BuildOptions {
	return assistantturn.BuildOptions{
		Model:                 stdReq.ResponseModel,
		Prompt:                prompt,
		RefFileTokens:         stdReq.RefFileTokens,
		SearchEnabled:         stdReq.Search,
		StripReferenceMarkers: opts.StripReferenceMarkers,
		ToolNames:             stdReq.ToolNames,
		ToolsRaw:              stdReq.ToolsRaw,
		ToolChoice:            stdReq.ToolChoice,
		ToolMarker:            stdReq.ToolMarker,
	}
}

// OutputCodeAccountUnauthorized 标记"托管账号的 Token 被上游拒绝"。
// 与通用的 "error" 区分开，好让共享重试逻辑知道这是一次账号级鉴权失败，
// 可以刷新 Token / 换号重试，而不是直接把错误透传给客户端。
const OutputCodeAccountUnauthorized = "account_unauthorized"

func authOutputError(a *auth.RequestAuth) *assistantturn.OutputError {
	if a != nil && a.UseConfigToken {
		return &assistantturn.OutputError{Status: http.StatusUnauthorized, Message: "Account token is invalid. Please re-login the account in admin.", Code: OutputCodeAccountUnauthorized}
	}
	return &assistantturn.OutputError{Status: http.StatusUnauthorized, Message: DirectTokenErrorMessage, Code: "error"}
}

// sessionOutputError 把 CreateSession 失败映射为标准输出错误。只有上游明确以
// 鉴权失败拒绝（托管账号的 FailureManagedUnauthorized、直传 token 的
// FailureDirectUnauthorized）才标记 account_unauthorized / 直传 token 错误，
// 让共享重试逻辑刷新 Token 或换号；网络抖动等非鉴权失败归类为可换号重试的
// upstream_unavailable，避免把健康账号误判为鉴权异常。
func sessionOutputError(a *auth.RequestAuth, err error) *assistantturn.OutputError {
	if dsclient.IsManagedUnauthorizedError(err) || dsclient.IsDirectUnauthorizedError(err) {
		return authOutputError(a)
	}
	if a != nil && !a.UseConfigToken {
		return authOutputError(a)
	}
	return &assistantturn.OutputError{Status: http.StatusBadGateway, Message: "Failed to create session.", Code: "upstream_unavailable"}
}

const DirectTokenErrorMessage = "Invalid token. If this should be a DS2API key, add it to config.keys first."

// powOutputError 把 GetPow 失败映射为输出错误。只有上游明确以鉴权失败拒绝托管
// 账号时才带 account_unauthorized 错误码，让共享重试逻辑先刷新 Token 再换号；
// 网络抖动等非鉴权失败归类为可换号重试的 upstream_unavailable，避免把健康账号
// 误判为鉴权异常（isManagedUnauthorized 只认 account_unauthorized 错误码）。
// 直传 token 模式保持原有 401 语义。
func powOutputError(a *auth.RequestAuth, err error) *assistantturn.OutputError {
	const message = "Failed to get PoW (invalid token or unknown error)."
	if a == nil || !a.UseConfigToken {
		return &assistantturn.OutputError{Status: http.StatusUnauthorized, Message: message, Code: "error"}
	}
	if dsclient.IsManagedUnauthorizedError(err) {
		return &assistantturn.OutputError{Status: http.StatusUnauthorized, Message: message, Code: OutputCodeAccountUnauthorized}
	}
	return &assistantturn.OutputError{Status: http.StatusBadGateway, Message: message, Code: "upstream_unavailable"}
}

func IsDirectTokenAuthError(outErr *assistantturn.OutputError) bool {
	return outErr != nil && outErr.Status == http.StatusUnauthorized && outErr.Message == DirectTokenErrorMessage
}

func Errorf(status int, format string, args ...any) *assistantturn.OutputError {
	return &assistantturn.OutputError{Status: status, Message: fmt.Sprintf(format, args...), Code: "error"}
}
