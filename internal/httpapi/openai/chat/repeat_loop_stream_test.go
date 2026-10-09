package chat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ds2api/internal/promptcompat"
	"ds2api/internal/sse"
)

// A degenerate upstream that repeats protocol closing tags forever must be cut
// short: the stream still finishes as a normal 200 completion (finish chunk +
// [DONE]), the content produced before the loop is preserved, and chunks that
// arrive after the loop are never consumed. The observed loop alternates between
// two different closing shells, so the fixture does too.
func TestConsumeChatStreamAttemptCutsRepeatLoopAndFinishesNormally(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	streamRuntime := newChatStreamRuntime(
		rec,
		http.NewResponseController(rec),
		true,
		"cid-repeat-loop",
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

	lines := []string{`data: {"response_message_id":91,"p":"response/content","v":"visible answer"}`}
	loopTags := []string{`</invoke>`, `</parameter>`}
	for i := 0; i < sse.DefaultRepeatLoopThreshold+2; i++ {
		lines = append(lines, `data: {"p":"response/content","v":"`+loopTags[i%len(loopTags)]+`\n"}`)
	}
	lines = append(lines,
		`data: {"p":"response/content","v":"never consumed tail"}`,
		`data: [DONE]`,
	)
	resp := makeOpenAISSEHTTPResponse(lines...)

	h := &Handler{}
	res := h.consumeChatStreamAttempt(req, resp, streamRuntime, "text", false, nil, false)
	if !res.Terminal || res.Retryable || res.ResumeContinue {
		t.Fatalf("expected repeat loop to finish terminally without retry, got result=%+v", res)
	}
	if streamRuntime.finalErrorCode != "" {
		t.Fatalf("repeat loop must not produce an error response, got code=%q message=%q", streamRuntime.finalErrorCode, streamRuntime.finalErrorMessage)
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
		t.Fatalf("repeat loop must not be reported as an interruption, body=%q", body)
	}
	if got := streamRuntime.accumulator.RawText.String(); got == "" {
		t.Fatalf("content before the loop must be preserved")
	}
	if got := streamRuntime.finalText; got == "" {
		t.Fatalf("expected the pre-loop visible text to survive finalize, body=%q", body)
	}
	if got := streamRuntime.accumulator.RawText.String(); strings.Contains(got, "never consumed tail") {
		t.Fatalf("upstream chunks after the loop must not be consumed, raw=%q", got)
	}
}
