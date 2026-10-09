package gemini

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ds2api/internal/auth"
	"ds2api/internal/promptcompat"
)

// system 文本声明工具（无顶层 tools 数组）这条分支按出口收窄，本次只对 Claude 关闭：
// Gemini 保持原样。这条测试与 internal/httpapi/claude/system_text_tools_test.go 成对，
// 锁定的是「收窄是按出口」而不是把分支整个删掉——RikkaHub 一类把工具目录写进 system
// 文本的客户端仍然由该分支服务。
//
// 已知边界（有意留在这里）：Gemini 的原始 body 用 `contents` / `systemInstruction`，
// `RequestBodyHasTools` 扫原始 body 时看不到 system 轮，因此「路由判无工具、组装却注入
// 格式规范」的错配在路由侧仍然存在——这类请求会走到无工具号池。prompt 侧的错配已由
// 流式筛分闸门补上：既然 prompt 要求模型按 EPSE 输出工具调用，出口就必须筛分，否则
// 模型照做的输出会作为可见正文漏给客户端（见下方流式测试，以及 OpenAI 出口为同一
// 原因无条件开启筛分的 empty_retry_runtime.go）。
func TestNormalizeGeminiRequestKeepsFormatSpecForSystemInstructionTools(t *testing.T) {
	req := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []any{map[string]any{"text": "You are a helpful agent with access to these tools."}},
		},
		"contents": []any{
			map[string]any{
				"role":  "user",
				"parts": []any{map[string]any{"text": "hello"}},
			},
		},
	}

	out, err := normalizeGeminiRequest(testGeminiConfig{}, "gemini-2.5-pro", req, false)
	if err != nil {
		t.Fatalf("normalizeGeminiRequest error: %v", err)
	}
	if !strings.Contains(out.FinalPrompt, "工具调用格式规范") || !strings.Contains(out.FinalPrompt, "<|EPSE|tool_calls>") {
		t.Fatalf("expected the format spec for system-declared tools, got=%q", out.FinalPrompt)
	}
	if !strings.Contains(out.FinalPrompt, "access to these tools") {
		t.Fatalf("expected the system instruction to stay rendered, got=%q", out.FinalPrompt)
	}
	if out.SystemTextToolsDisabled {
		t.Fatal("Gemini must keep the system-text tools branch")
	}
	if len(out.ToolNames) != 0 {
		t.Fatalf("expected no extracted tool names, got=%#v", out.ToolNames)
	}
}

// 流式路径上的另一半：system 文本声明工具的请求里 ToolNames 为空，而 prompt 已经
// 要求模型按 EPSE 输出工具调用。此时若正文不筛分，模型照做的 EPSE 块会作为可见正文
// 透传给客户端——正是 OpenAI 出口在 empty_retry_runtime.go 里记录的同一个坑。这条
// 测试锁定 Gemini 的筛分闸门跟随 prompt：这类请求同样被筛分，EPSE 块解析成
// functionCall candidate，正文里不再出现标记。
func TestGeminiStreamSievesToolCallsDeclaredInSystemInstruction(t *testing.T) {
	toolCallText := "<|EPSE|tool_calls>\n" +
		"  <|EPSE|invoke name=\"read_file\">\n" +
		"    <|EPSE|parameter name=\"path\">README.MD</|EPSE|parameter>\n" +
		"  </|EPSE|invoke>\n" +
		"</|EPSE|tool_calls>"
	line1, _ := json.Marshal(map[string]any{"p": "response/content", "v": toolCallText})
	line2, _ := json.Marshal(map[string]any{"p": "response/status", "v": "FINISHED"})
	sseBody := "data: " + string(line1) + "\n\ndata: " + string(line2) + "\n\ndata: [DONE]\n\n"
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(sseBody)),
	}

	h := &Handler{Store: testGeminiConfig{}, DS: &testGeminiDS{}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-pro:streamGenerateContent", nil)
	a := &auth.RequestAuth{CallerID: "caller:test"}

	// 工具目录只在 system 文本里：没有 tools 数组，也没有顶层工具名。
	stdReq := promptcompat.StandardRequest{
		ResponseModel:   "gemini-2.5-pro",
		PromptTokenText: "prompt",
		Messages: []any{
			map[string]any{"role": "system", "content": "You are a helpful agent with access to these tools."},
			map[string]any{"role": "user", "content": "read the readme"},
		},
	}
	if !stdReq.SystemTextToolsApply() {
		t.Fatal("precondition: the system-text tool branch must apply to this request")
	}

	h.handleStreamGenerateContentWithRetry(
		w, r, a, resp, map[string]any{}, "",
		stdReq, "gemini-2.5-pro", "prompt",
		false, false, stdReq.ToolNames, stdReq.ToolsRaw, nil,
	)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	foundFunctionCall := false
	for _, frame := range extractGeminiSSEFrames(t, w.Body.String()) {
		for _, part := range geminiPartsFromFrame(frame) {
			if text, ok := part["text"].(string); ok && strings.Contains(text, "<|EPSE|") {
				t.Fatalf("tool-call markup leaked into the visible text: %q (body=%s)", text, w.Body.String())
			}
			if fc, ok := part["functionCall"].(map[string]any); ok {
				foundFunctionCall = true
				if fc["name"] != "read_file" {
					t.Fatalf("unexpected functionCall name: %#v", fc)
				}
			}
		}
	}
	if !foundFunctionCall {
		t.Fatalf("expected the EPSE block to be sieved into a functionCall part, body=%s", w.Body.String())
	}
}
