package chat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ds2api/internal/promptcompat"
)

// A model that hallucinates the `[Tool]:` prompt role marker must be cut short:
// the stream still finishes as a normal 200 completion (finish chunk + [DONE]),
// the text produced before the marker is preserved, and both the marker and
// everything after it is dropped — including chunks that arrive later.
func TestConsumeChatStreamAttemptCutsHallucinatedToolRoleMarker(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	streamRuntime := newChatStreamRuntime(
		rec,
		http.NewResponseController(rec),
		true,
		"cid-tool-role-marker",
		time.Now().Unix(),
		"deepseek-v4.1-flash",
		"prompt",
		false,
		false,
		true,
		nil,
		nil,
		promptcompat.DefaultToolChoicePolicy(),
		false,
		false,
	)

	lines := []string{
		`data: {"response_message_id":92,"p":"response/content","v":"命令已执行。"}`,
		`data: {"p":"response/content","v":"\n[Tool]:\n输出：ok\n[Assistant]: 继续"}`,
		`data: {"p":"response/content","v":"never consumed tail"}`,
		`data: [DONE]`,
	}
	resp := makeOpenAISSEHTTPResponse(lines...)

	h := &Handler{}
	res := h.consumeChatStreamAttempt(req, resp, streamRuntime, "text", false, nil, false)
	if !res.Terminal || res.Retryable || res.ResumeContinue {
		t.Fatalf("expected the role marker cut to finish terminally without retry, got result=%+v", res)
	}
	if streamRuntime.finalErrorCode != "" {
		t.Fatalf("role marker cut must not produce an error response, got code=%q message=%q", streamRuntime.finalErrorCode, streamRuntime.finalErrorMessage)
	}
	if got, want := streamRuntime.finalFinishReason, "stop"; got != want {
		t.Fatalf("finish reason = %q, want %q", got, want)
	}

	body := rec.Body.String()
	frames, done := parseSSEDataFrames(t, body)
	if !done {
		t.Fatalf("expected [DONE] terminator, body=%q", body)
	}
	if len(frames) == 0 {
		t.Fatalf("expected content and finish frames, body=%q", body)
	}
	if containsStreamErrorCode(body, "upstream_interrupted") {
		t.Fatalf("role marker cut must not be reported as an interruption, body=%q", body)
	}
	raw := streamRuntime.accumulator.RawText.String()
	if !strings.Contains(raw, "命令已执行。") {
		t.Fatalf("content before the marker must be preserved, raw=%q", raw)
	}
	if strings.Contains(raw, "never consumed tail") {
		t.Fatalf("upstream chunks after the marker must not be consumed, raw=%q", raw)
	}
	if strings.Contains(raw, "[Tool]:") {
		t.Fatalf("the hallucinated marker must be dropped, raw=%q", raw)
	}
	if strings.Contains(raw, "输出：ok") {
		t.Fatalf("the invented tool result must be dropped, raw=%q", raw)
	}
	if got := streamRuntime.finalText; strings.Contains(got, "[Tool]:") {
		t.Fatalf("final text must not carry the marker, finalText=%q", got)
	}
}

// The marker is normally split across SSE deltas; no fragment of it may reach
// the client, and the text before it must still be delivered.
func TestConsumeChatStreamAttemptCutsToolRoleMarkerSplitAcrossChunks(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	streamRuntime := newChatStreamRuntime(
		rec,
		http.NewResponseController(rec),
		true,
		"cid-tool-role-marker-split",
		time.Now().Unix(),
		"deepseek-v4.1-flash",
		"prompt",
		false,
		false,
		true,
		nil,
		nil,
		promptcompat.DefaultToolChoicePolicy(),
		false,
		false,
	)

	resp := makeOpenAISSEHTTPResponse(
		`data: {"p":"response/content","v":"好的，"}`,
		`data: {"p":"response/content","v":"[To"}`,
		`data: {"p":"response/content","v":"ol"}`,
		`data: {"p":"response/content","v":"]:编造内容"}`,
		`data: [DONE]`,
	)

	h := &Handler{}
	res := h.consumeChatStreamAttempt(req, resp, streamRuntime, "text", false, nil, false)
	if !res.Terminal || res.Retryable {
		t.Fatalf("expected the split marker cut to finish terminally, got result=%+v", res)
	}

	body := rec.Body.String()
	frames, done := parseSSEDataFrames(t, body)
	if !done {
		t.Fatalf("expected [DONE] terminator, body=%q", body)
	}
	if len(frames) == 0 {
		t.Fatalf("expected content and finish frames, body=%q", body)
	}
	if strings.Contains(body, "[Tool") || strings.Contains(body, "[To\\u") {
		t.Fatalf("no fragment of the marker may reach the client, body=%q", body)
	}
	if strings.Contains(body, "编造内容") {
		t.Fatalf("text after the marker must not reach the client, body=%q", body)
	}
	if raw := streamRuntime.accumulator.RawText.String(); raw != "好的，" {
		t.Fatalf("raw text = %q, want %q", raw, "好的，")
	}
}
