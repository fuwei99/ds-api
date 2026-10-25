package client

import (
	"bufio"
	"strings"
	"testing"
)

func scanString(t *testing.T, body string) segmentScanResult {
	t.Helper()
	out, err := scanSegmentHead(bufio.NewReader(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("scanSegmentHead returned error: %v", err)
	}
	return out
}

// TestScanSegmentHeadStopsAtResponseMessageID 正常情况下拿到 response_message_id
// 就结束首轮扫描，并沿途记录内容标记。
func TestScanSegmentHeadStopsAtResponseMessageID(t *testing.T) {
	body := strings.Join([]string{
		`data: {"request_message_id":1}`,
		`data: {"p":"response/content","v":"hi"}`,
		`data: {"response_message_id":2}`,
		`data: {"p":"response/content","v":"never read"}`,
	}, "\n") + "\n"

	got := scanString(t, body)
	if got.responseMessageID != 2 {
		t.Fatalf("response_message_id = %d, want 2", got.responseMessageID)
	}
	if got.requestMessageID != 1 {
		t.Fatalf("request_message_id = %d, want 1", got.requestMessageID)
	}
	if !got.hadContent {
		t.Fatal("expected hadContent=true")
	}
	if got.streamEnded {
		t.Fatal("expected streamEnded=false when stopping at response_message_id")
	}
	if got.upstreamError != "" {
		t.Fatalf("unexpected upstream error %q", got.upstreamError)
	}
}

// TestScanSegmentHeadCapturesInputExceedsLimit 回归：上游以普通 data: 行下发
// 的 hint 错误（超长输入）必须被首轮扫描捕获，而不是被当成"无内容的正常流"。
// 这是 "invalid message id" 报错的真实来源之一：原实现丢弃该错误后，返回了一个
// 不可信的 message id，直到下一段才以完全无关的文案失败。
func TestScanSegmentHeadCapturesInputExceedsLimit(t *testing.T) {
	// 错误行先于 response_message_id 到达（真实空流场景的顺序）。
	body := strings.Join([]string{
		`data: {"request_message_id":1}`,
		`data: {"type":"error","content":"内容超长，请删减后再试","finish_reason":"input_exceeds_limit"}`,
		`data: {"response_message_id":2}`,
	}, "\n") + "\n"

	got := scanString(t, body)
	if got.upstreamError != "内容超长，请删减后再试" {
		t.Fatalf("upstreamError = %q, want 内容超长，请删减后再试", got.upstreamError)
	}
	if got.hadContent {
		t.Fatal("expected hadContent=false for an error-only stream")
	}
}

// TestScanSegmentHeadCapturesContentFilter 内容过滤同样必须终止本段。
func TestScanSegmentHeadCapturesContentFilter(t *testing.T) {
	body := `data: {"code":"content_filter"}` + "\n"
	got := scanString(t, body)
	if !got.contentFilter {
		t.Fatal("expected contentFilter=true")
	}
	if got.responseMessageID != 0 {
		t.Fatalf("expected no trustworthy message id, got %d", got.responseMessageID)
	}
}

// TestScanSegmentHeadEmptyStreamMarksEnded 空流（EOF 前无任何内容）必须标记
// streamEnded 且 hadContent=false，让调用方拒绝把 message id 用作 parent。
func TestScanSegmentHeadEmptyStreamMarksEnded(t *testing.T) {
	body := strings.Join([]string{
		`data: {"request_message_id":1}`,
		`data: {"p":"response/status","v":"INCOMPLETE"}`,
	}, "\n") + "\n"

	got := scanString(t, body)
	if !got.streamEnded {
		t.Fatal("expected streamEnded=true on EOF")
	}
	if got.hadContent {
		t.Fatal("expected hadContent=false")
	}
	if got.responseMessageID != 0 {
		t.Fatalf("expected no message id, got %d", got.responseMessageID)
	}
}

// TestScanSegmentHeadCountsThinkingAsContent thinking 增量也算本段有产出：
// 只要上游开始推理，消息就已在会话中有效建立，可以 stop_stream 后续写。
func TestScanSegmentHeadCountsThinkingAsContent(t *testing.T) {
	body := strings.Join([]string{
		`data: {"p":"response/thinking_content","v":"think"}`,
		`data: {"response_message_id":7}`,
	}, "\n") + "\n"

	got := scanString(t, body)
	if !got.hadContent {
		t.Fatal("expected thinking increments to count as content")
	}
	if got.responseMessageID != 7 {
		t.Fatalf("response_message_id = %d, want 7", got.responseMessageID)
	}
}

// TestScanSegmentHeadPreviewKeepsRawJSONError 非 SSE 的裸 JSON 错误体要保留在
// preview 里，供上层解析出 biz_msg。
func TestScanSegmentHeadPreviewKeepsRawJSONError(t *testing.T) {
	body := `{"code":0,"msg":"","data":{"biz_code":26,"biz_msg":"invalid message id","biz_data":null}}` + "\n"
	got := scanString(t, body)
	if !strings.Contains(got.preview, "invalid message id") {
		t.Fatalf("preview lost the raw JSON error: %q", got.preview)
	}
	if got.responseMessageID != 0 {
		t.Fatalf("expected no message id, got %d", got.responseMessageID)
	}
}

func TestSegmentLineSignal(t *testing.T) {
	cases := []struct {
		name       string
		line       string
		hasContent bool
		errMsg     string
		filtered   bool
	}{
		{"content", `data: {"p":"response/content","v":"hi"}`, true, "", false},
		{"thinking", `data: {"p":"response/thinking_content","v":"t"}`, true, "", false},
		{"done", "data: [DONE]", false, "", false},
		{"finished", `data: {"p":"response/status","v":"FINISHED"}`, false, "", false},
		{"hint error", `data: {"type":"error","content":"内容超长"}`, false, "内容超长", false},
		{"content filter", `data: {"code":"content_filter"}`, false, "", true},
		{"non data line", "event: close", false, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hasContent, errMsg, filtered := segmentLineSignal([]byte(c.line))
			if hasContent != c.hasContent || errMsg != c.errMsg || filtered != c.filtered {
				t.Fatalf("segmentLineSignal(%q) = (%v, %q, %v), want (%v, %q, %v)",
					c.line, hasContent, errMsg, filtered, c.hasContent, c.errMsg, c.filtered)
			}
		})
	}
}

func TestSegmentDrainErrorsKeepsFirstMessage(t *testing.T) {
	d := &segmentDrainErrors{}
	if d.get() != "" {
		t.Fatal("expected empty initial message")
	}
	d.record("")
	if d.get() != "" {
		t.Fatal("empty record must not be stored")
	}
	d.record("first")
	d.record("second")
	if d.get() != "first" {
		t.Fatalf("get() = %q, want first", d.get())
	}
}
