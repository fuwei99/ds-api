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

// TestGeminiStreamRuntimeWithMarkerNormalizesToolCalls 验证 Gemini 原生流式直连模式下，
// runtime.toolMarker 正确接收 a.ToolMarker，并在 finalize 阶段将模型返回的专属
// Marker 归一化回 EPSE 并解析为 Gemini 的 functionCall candidate。
func TestGeminiStreamRuntimeWithMarkerNormalizesToolCalls(t *testing.T) {
	const marker = "Q7ZK3M"
	toolCallText := "<|" + marker + "|tool_calls>\n" +
		"  <|" + marker + "|invoke name=\"read_file\">\n" +
		"    <|" + marker + "|parameter name=\"path\">README.MD</|" + marker + "|parameter>\n" +
		"  </|" + marker + "|invoke>\n" +
		"</|" + marker + "|tool_calls>"

	line1, _ := json.Marshal(map[string]any{
		"p": "response/content",
		"v": toolCallText,
	})
	line2, _ := json.Marshal(map[string]any{
		"p": "response/status",
		"v": "FINISHED",
	})
	sseBody := "data: " + string(line1) + "\n\ndata: " + string(line2) + "\n\ndata: [DONE]\n\n"

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(sseBody)),
	}

	h := &Handler{
		Store: testGeminiConfig{},
		DS:    &testGeminiDS{},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:streamGenerateContent", nil)
	a := &auth.RequestAuth{
		CallerID:   "caller:test",
		ToolMarker: marker,
	}
	stdReq := promptcompat.StandardRequest{
		ResponseModel:   "gemini-2.5-flash",
		PromptTokenText: "prompt",
		ToolMarker:      marker,
		ToolNames:       []string{"read_file"},
	}

	toolsRaw := []any{
		map[string]any{
			"function": map[string]any{
				"name": "read_file",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	h.handleStreamGenerateContentWithRetry(
		w, r, a, resp, map[string]any{}, "",
		stdReq, "gemini-2.5-flash", "prompt",
		false, false, []string{"read_file"}, toolsRaw, nil,
	)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	frames := extractGeminiSSEFrames(t, w.Body.String())
	if len(frames) == 0 {
		t.Fatalf("expected SSE frames, got none: %s", w.Body.String())
	}

	foundFunctionCall := false
	for _, frame := range frames {
		parts := geminiPartsFromFrame(frame)
		for _, part := range parts {
			if fc, ok := part["functionCall"].(map[string]any); ok {
				foundFunctionCall = true
				if fc["name"] != "read_file" {
					t.Fatalf("unexpected functionCall name: %#v", fc)
				}
				args, _ := fc["args"].(map[string]any)
				if args["path"] != "README.MD" {
					t.Fatalf("unexpected functionCall args: %#v", args)
				}
			}
		}
	}

	if !foundFunctionCall {
		t.Fatalf("expected functionCall part in streamed candidates, body=%s", w.Body.String())
	}
}
