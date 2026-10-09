package chat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ds2api/internal/promptcompat"
)

// A cut belongs to the upstream stream it happened on. When the attempt that hit
// a content rule produced no visible text, the empty-output retry starts a fresh
// upstream response — and that response's content is what the client is waiting
// for. Before the reset, the cut stayed tripped for the rest of the request, so
// the retry's healthy answer was silently discarded and the client ended up with
// an upstream_unavailable error instead of the content.
func TestConsumeChatStreamAttemptResetsTheCutBetweenAttempts(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	streamRuntime := newChatStreamRuntime(
		rec,
		http.NewResponseController(rec),
		true,
		"cid-cut-reset",
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
	h := &Handler{}

	// Round 1: the model's first output is a fabricated `[Tool]:` marker, so the
	// rule fires with no visible text and the attempt is deferred as empty.
	first := makeOpenAISSEHTTPResponse(
		`data: {"response_message_id":77,"p":"response/content","v":"[Tool]:编造的工具结果"}`,
		`data: [DONE]`,
	)
	firstResult := h.consumeChatStreamAttempt(req, first, streamRuntime, "text", false, nil, true)
	if firstResult.Terminal || !firstResult.Retryable {
		t.Fatalf("a cut with no visible text must leave the attempt retryable, got %+v", firstResult)
	}

	// Round 2: a healthy upstream. Its text must reach the client.
	second := makeOpenAISSEHTTPResponse(
		`data: {"response_message_id":78,"p":"response/content","v":"healthy answer"}`,
		`data: [DONE]`,
	)
	secondResult := h.consumeChatStreamAttempt(req, second, streamRuntime, "text", false, nil, false)
	if !secondResult.Terminal {
		t.Fatalf("the healthy round must finish terminally, got %+v", secondResult)
	}
	if got := rec.Body.String(); !strings.Contains(got, "healthy answer") {
		t.Fatalf("the retry round's content must reach the client, body=%q", got)
	}
	if got := streamRuntime.accumulator.RawText.String(); strings.Contains(got, "[Tool]:") {
		t.Fatalf("the cut fragment must stay dropped, raw=%q", got)
	}
}
