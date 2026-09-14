package client

import (
	"bufio"
	"context"
	dsprotocol "ds2api/internal/deepseek/protocol"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ds2api/internal/auth"
	"ds2api/internal/banreport"
	"ds2api/internal/config"
)

const (
	segmentContentWaitTimeout = 30 * time.Second
	// segmentDrainTimeout 是 stop 之后等待上游流真正关闭（drain 读到 EOF）
	// 的上限，对齐 deepseek2api 的 15 秒；超时直接判本段失败。
	segmentDrainTimeout = 15 * time.Second
	// segmentSettleDelay 是 stop 确认后、发送下一段前的固定等待。
	// 暂时停用（segmentSettleDelayEnabled=false）：drain 已改为等待流真正
	// 关闭，固定 settle 不再承担落库等待职责；代码保留，需要时改回 true。
	segmentSettleDelay        = 1 * time.Second
	segmentSettleDelayEnabled = false
)

func (c *Client) StopStream(ctx context.Context, a *auth.RequestAuth, sessionID string, messageID int) error {
	if strings.TrimSpace(sessionID) == "" || messageID <= 0 {
		return errors.New("missing stop_stream identifiers")
	}
	clients := c.requestClientsForAuth(ctx, a)
	headers := c.authHeaders(a.DeepSeekToken, a.Account.Locale)
	payload := map[string]any{
		"chat_session_id": sessionID,
		"message_id":      messageID,
	}
	applySessionReferer(headers, payload)
	resp, status, err := c.postJSONWithStatus(ctx, clients.regular, clients.fallback, dsprotocol.DeepSeekStopStreamURL, headers, payload)
	if err != nil {
		config.Logger.Warn("[stop_stream] request error", "session_id", sessionID, "message_id", messageID, "account", a.AccountID, "error", err)
		return err
	}
	code, bizCode, msg, bizMsg := extractResponseStatus(resp)
	if status != http.StatusOK || code != 0 || bizCode != 0 {
		config.Logger.Warn("[stop_stream] non-success response", "session_id", sessionID, "message_id", messageID, "account", a.AccountID, "status", status, "code", code, "biz_code", bizCode, "msg", msg, "biz_msg", bizMsg, "resp", fmt.Sprintf("%v", resp))
		return fmt.Errorf("stop_stream failed: status=%d code=%d biz_code=%d msg=%s biz_msg=%s", status, code, bizCode, msg, bizMsg)
	}
	config.Logger.Info("[stop_stream] ok", "session_id", sessionID, "message_id", messageID, "account", a.AccountID)
	return nil
}

func (c *Client) FireCompletionAndStop(ctx context.Context, a *auth.RequestAuth, payload map[string]any, powResp string) (int, error) {
	sessionID, _ := payload["chat_session_id"].(string)
	clients := c.requestClientsForAuth(ctx, a)
	headers := c.authHeaders(a.DeepSeekToken, a.Account.Locale)
	headers["x-ds-pow-response"] = powResp
	applySessionReferer(headers, payload)
	captureSession := c.capture.Start("deepseek_completion", dsprotocol.DeepSeekCompletionURL, a.AccountID, payload)
	resp, err := c.streamPostOnce(ctx, clients.stream, dsprotocol.DeepSeekCompletionURL, headers, payload)
	if err != nil {
		config.Logger.Warn("[fire_completion_and_stop] completion request failed", "session_id", sessionID, "account", a.AccountID, "error", err)
		return 0, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			config.Logger.Warn("[fire_completion_and_stop] response body close failed", "error", err)
		}
	}()
	if captureSession != nil {
		resp.Body = captureSession.WrapBody(resp.Body, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		config.Logger.Warn("[fire_completion_and_stop] completion returned non-200", "session_id", sessionID, "account", a.AccountID, "status", resp.StatusCode)
		return 0, fmt.Errorf("completion returned HTTP %d", resp.StatusCode)
	}

	newBody, muted, muteUntil, err := detectMutedCompletion(resp.Body)
	if err != nil {
		config.Logger.Warn("[fire_completion_and_stop] mute detection failed", "session_id", sessionID, "account", a.AccountID, "error", err)
		return 0, err
	}
	if muted {
		config.Logger.Warn("[fire_completion_and_stop] account muted", "session_id", sessionID, "account", a.AccountID)
		c.persistMutedUntil(a.AccountID, muteUntil, banreport.SourceStopStream)
		return 0, &RequestFailure{Op: "completion", Kind: FailureMuted, Message: "user is muted"}
	}
	if newBody != nil {
		resp.Body = newBody
	}

	responseMessageID := 0
	requestMessageID := 0
	hadContent := false
	streamEnded := false
	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	scan, scanErr := scanSegmentHead(reader)
	if scanErr != nil {
		config.Logger.Warn("[fire_completion_and_stop] SSE scan error", "session_id", sessionID, "account", a.AccountID, "error", scanErr)
		return 0, scanErr
	}
	responseMessageID = scan.responseMessageID
	requestMessageID = scan.requestMessageID
	hadContent = scan.hadContent
	streamEnded = scan.streamEnded
	previewStr := scan.preview

	// 上游在流内明确报错（例如超长输入的 input_exceeds_limit）或触发内容过滤：
	// 本段没有有效落库的消息，即便已收到 response_message_id 也不可作为下一段
	// 的 parent_message_id，必须立刻带着真实原因失败。
	if scan.upstreamError != "" {
		config.Logger.Warn("[fire_completion_and_stop] upstream stream error", "session_id", sessionID, "account", a.AccountID, "parent_message_id", payload["parent_message_id"], "response_message_id", responseMessageID, "error", scan.upstreamError)
		return 0, fmt.Errorf("segment rejected by upstream: %s", scan.upstreamError)
	}
	if scan.contentFilter {
		config.Logger.Warn("[fire_completion_and_stop] upstream content filter", "session_id", sessionID, "account", a.AccountID, "parent_message_id", payload["parent_message_id"], "response_message_id", responseMessageID)
		return 0, errors.New("segment rejected by upstream content filter")
	}

	if responseMessageID <= 0 {
		if strings.HasPrefix(strings.TrimSpace(previewStr), "{") {
			var errResp map[string]any
			if json.Unmarshal([]byte(previewStr), &errResp) == nil {
				code := intFrom(errResp["code"])
				msg, _ := errResp["msg"].(string)
				if msg == "" {
					if data, _ := errResp["data"].(map[string]any); data != nil {
						msg, _ = data["biz_msg"].(string)
					}
				}
				if code != 0 || msg != "" {
					config.Logger.Warn("[fire_completion_and_stop] upstream JSON error", "session_id", sessionID, "account", a.AccountID, "parent_message_id", payload["parent_message_id"], "code", code, "msg", msg, "raw", previewStr)
					return 0, fmt.Errorf("completion upstream error: code=%d msg=%s", code, msg)
				}
			}
		}
		config.Logger.Warn("[fire_completion_and_stop] response_message_id not received before stream ended", "session_id", sessionID, "account", a.AccountID, "parent_message_id", payload["parent_message_id"], "request_message_id", requestMessageID, "sse_preview", previewStr)
		return 0, errors.New("response_message_id not received before stream ended")
	}
	config.Logger.Info("[fire_completion_and_stop] captured ids", "session_id", sessionID, "account", a.AccountID, "request_message_id", requestMessageID, "response_message_id", responseMessageID, "had_content", hadContent)

	if streamEnded && !hadContent {
		// 上游在产出任何内容前就结束了这一段的流。此时消息并未有效落库，
		// 它返回的 response_message_id 是不可信的：继续用它作为下一段的
		// parent_message_id 会让上游回 biz_code=26 "invalid message id"，
		// 报错信息与真实原因完全脱节。这里直接失败，把问题定位在出错的这一段。
		config.Logger.Warn("[fire_completion_and_stop] stream ended before any content, message id is not trustworthy", "session_id", sessionID, "account", a.AccountID, "request_message_id", requestMessageID, "response_message_id", responseMessageID, "sse_preview", previewStr)
		return 0, fmt.Errorf("segment produced no content before stream ended (session=%s, response_message_id=%d)", sessionID, responseMessageID)
	}

	contentCh := make(chan struct{}, 1)
	drainDone := make(chan struct{})
	drainErrs := &segmentDrainErrors{}
	go func() {
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				hasContent, errMsg, filtered := segmentLineSignal(line)
				if hasContent {
					select {
					case contentCh <- struct{}{}:
					default:
					}
				}
				if errMsg != "" {
					drainErrs.record(errMsg)
				}
				if filtered {
					drainErrs.record("content filtered by upstream")
				}
			}
			if err != nil {
				if err != io.EOF {
					config.Logger.Warn("[fire_completion_and_stop] drain stream error", "session_id", sessionID, "account", a.AccountID, "error", err)
				}
				break
			}
		}
		close(drainDone)
	}()

	if !hadContent {
		select {
		case <-contentCh:
			hadContent = true
		case <-drainDone:
			// 同上：无内容即结束意味着 message id 不可信，不能继续链式续写。
			reason := drainErrs.get()
			config.Logger.Warn("[fire_completion_and_stop] stream ended before any content, message id is not trustworthy", "session_id", sessionID, "account", a.AccountID, "request_message_id", requestMessageID, "response_message_id", responseMessageID, "upstream_error", reason)
			if reason != "" {
				return 0, fmt.Errorf("segment rejected by upstream: %s", reason)
			}
			return 0, fmt.Errorf("segment produced no content before stream ended (session=%s, response_message_id=%d)", sessionID, responseMessageID)
		case <-time.After(segmentContentWaitTimeout):
			config.Logger.Warn("[fire_completion_and_stop] content wait timed out, aborting segment", "session_id", sessionID, "account", a.AccountID, "request_message_id", requestMessageID, "response_message_id", responseMessageID, "wait", segmentContentWaitTimeout)
			return 0, fmt.Errorf("segment content wait timed out after %s (session=%s, response_message_id=%d)", segmentContentWaitTimeout, sessionID, responseMessageID)
		case <-ctx.Done():
			config.Logger.Warn("[fire_completion_and_stop] context cancelled while waiting for content", "session_id", sessionID, "account", a.AccountID, "error", ctx.Err())
			return responseMessageID, ctx.Err()
		}
	}

	stopCalledAt := time.Now()
	config.Logger.Info("[fire_completion_and_stop] content detected, calling stop_stream", "session_id", sessionID, "account", a.AccountID, "request_message_id", requestMessageID, "response_message_id", responseMessageID)
	if err := c.StopStream(ctx, a, sessionID, responseMessageID); err != nil {
		config.Logger.Warn("[fire_completion_and_stop] stop_stream failed", "session_id", sessionID, "message_id", responseMessageID, "error", err)
		return 0, err
	}

	// After stop, wait for the stopped stream to actually close (drain reads to
	// EOF), matching deepseek2api: a stream that does not close in time means the
	// previous message may not be committed yet — fail the segment instead of
	// force-closing the body and letting the next segment hit "message still wip".
	select {
	case <-drainDone:
		config.Logger.Info("[fire_completion_and_stop] drain completed", "session_id", sessionID, "account", a.AccountID, "response_message_id", responseMessageID)
	case <-time.After(segmentDrainTimeout):
		config.Logger.Warn("[fire_completion_and_stop] stopped stream did not close in time", "session_id", sessionID, "account", a.AccountID, "response_message_id", responseMessageID, "timeout", segmentDrainTimeout)
		return 0, fmt.Errorf("stopped stream did not close in time after %s (session=%s, response_message_id=%d)", segmentDrainTimeout, sessionID, responseMessageID)
	case <-ctx.Done():
		config.Logger.Warn("[fire_completion_and_stop] context cancelled while draining stopped stream", "session_id", sessionID, "account", a.AccountID, "error", ctx.Err())
		return responseMessageID, ctx.Err()
	}

	// Brief settle after upstream close confirmation to let the session
	// finish committing the previous message before sending the next segment.
	// Temporarily disabled: the drain wait above now covers stream closure.
	if segmentSettleDelayEnabled {
		select {
		case <-time.After(segmentSettleDelay):
		case <-ctx.Done():
			config.Logger.Warn("[fire_completion_and_stop] context cancelled during settle delay", "session_id", sessionID, "account", a.AccountID, "error", ctx.Err())
			return responseMessageID, ctx.Err()
		}
	}

	config.Logger.Info("[fire_completion_and_stop] segment sent and stopped", "session_id", sessionID, "response_message_id", responseMessageID, "request_message_id", requestMessageID, "drain_after_stop", time.Since(stopCalledAt), "had_content", hadContent, "account", a.AccountID)
	return responseMessageID, nil
}

// lineHasContent 判定一行 SSE 是否携带可见/思考内容。
// 语义由 segmentLineSignal 单点定义，首段扫描与后台 drain 共用同一规则。
func lineHasContent(line []byte) bool {
	hasContent, _, _ := segmentLineSignal(line)
	return hasContent
}

func extractResponseMessageID(chunk map[string]any, out *int) {
	if chunk == nil || out == nil {
		return
	}
	if id := intFrom(chunk["response_message_id"]); id > 0 {
		*out = id
	}
	if v, ok := chunk["v"].(map[string]any); ok {
		if response, _ := v["response"].(map[string]any); response != nil {
			if id := intFrom(response["message_id"]); id > 0 {
				*out = id
			}
		}
	}
	if message, _ := chunk["message"].(map[string]any); message != nil {
		if response, _ := message["response"].(map[string]any); response != nil {
			if id := intFrom(response["message_id"]); id > 0 {
				*out = id
			}
		}
	}
}
