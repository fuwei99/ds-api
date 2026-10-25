package promptcompat

import (
	"strings"
	"testing"

	"ds2api/internal/toolcall"
)

const markerUnderTest = "Q7ZK3M"

func markerToolMarkupBlock(marker string) string {
	return "<|" + marker + "|tool_calls>\n" +
		"  <|" + marker + "|invoke name=\"read_file\">\n" +
		"    <|" + marker + "|parameter name=\"path\">README.MD</|" + marker + "|parameter>\n" +
		"  </|" + marker + "|invoke>\n" +
		"</|" + marker + "|tool_calls>"
}

// TestNormalizeOpenAIChatRequestRendersMarkerAtPromptBoundary 验证出口会把规范与
// 示例里的 EPSE 渲染成调用者专属标识，且提示词里不再出现共享关键字。
func TestNormalizeOpenAIChatRequestRendersMarkerAtPromptBoundary(t *testing.T) {
	store := &fakeRouteConfigReader{}
	req := map[string]any{
		"model":    "deepseek-v4.1-flash",
		"messages": []any{map[string]any{"role": "user", "content": "read it"}},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{
				"name":        "read_file",
				"description": "Read a file",
				"parameters":  map[string]any{"type": "object"},
			}},
		},
	}

	stdReq, err := NormalizeOpenAIChatRequest(store, req, "", markerUnderTest)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if stdReq.ToolMarker != markerUnderTest {
		t.Fatalf("marker not carried on the standard request: %q", stdReq.ToolMarker)
	}
	if strings.Contains(stdReq.FinalPrompt, toolcall.EPSEKeyword) {
		t.Fatalf("prompt still contains the canonical keyword: %q", stdReq.FinalPrompt)
	}
	if !strings.Contains(stdReq.FinalPrompt, "<|"+markerUnderTest+"|tool_calls>") {
		t.Fatalf("prompt missing the marker shell: %q", stdReq.FinalPrompt)
	}
	if !strings.Contains(stdReq.FinalPrompt, "<|"+markerUnderTest+"|parameter name=\"path\">") {
		t.Fatalf("prompt examples were not rendered with the marker: %q", stdReq.FinalPrompt)
	}
}

// TestNormalizeOpenAIChatRequestWithoutMarkerKeepsEPSE guards the legacy path:
// callers that pass no marker must still get the canonical prompt.
func TestNormalizeOpenAIChatRequestWithoutMarkerKeepsEPSE(t *testing.T) {
	store := &fakeRouteConfigReader{}
	req := map[string]any{
		"model":    "deepseek-v4.1-flash",
		"messages": []any{map[string]any{"role": "user", "content": "read it"}},
	}
	stdReq, err := NormalizeOpenAIChatRequest(store, req, "")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if stdReq.ToolMarker != "" {
		t.Fatalf("expected no marker, got %q", stdReq.ToolMarker)
	}
}

// TestNormalizeOpenAIMessagesRecognizesMarkerHistory 验证历史里以标识形态出现的
// assistant 工具块仍会被识别、解析并重渲染为规范外壳（P1-a）。重渲染本身仍用
// EPSE，标识由出口统一替换。
func TestNormalizeOpenAIMessagesRecognizesMarkerHistory(t *testing.T) {
	raw := []any{
		map[string]any{"role": "user", "content": "read it"},
		map[string]any{"role": "assistant", "content": markerToolMarkupBlock(markerUnderTest)},
		map[string]any{"role": "user", "content": "again"},
	}

	normalized := NormalizeOpenAIMessagesForPrompt(raw, "", markerUnderTest)
	if len(normalized) != 3 {
		t.Fatalf("unexpected message count: %#v", normalized)
	}
	content, _ := normalized[1]["content"].(string)
	if !strings.Contains(content, "CDATA[README.MD]") {
		t.Fatalf("marker-form history block was not re-rendered: %q", content)
	}
	if !strings.Contains(content, "<|EPSE|tool_calls>") {
		t.Fatalf("expected the canonical shell before the boundary rewrite: %q", content)
	}
	if strings.Contains(content, "<|"+markerUnderTest) {
		t.Fatalf("marker must not survive into the internal render: %q", content)
	}
}

// TestNormalizeOpenAIMessagesMarkerHistoryMatchesCanonicalHistory 验证同一份历史
// 无论以标识形态还是规范形态出现，渲染结果一致——这是「同一请求内 prompt 用 M、
// 归档历史为 EPSE」这条不变量得以成立的前提。
func TestNormalizeOpenAIMessagesMarkerHistoryMatchesCanonicalHistory(t *testing.T) {
	canonical := []any{
		map[string]any{"role": "assistant", "content": markerToolMarkupBlock(toolcall.EPSEKeyword)},
	}
	markerForm := []any{
		map[string]any{"role": "assistant", "content": markerToolMarkupBlock(markerUnderTest)},
	}

	want, _ := NormalizeOpenAIMessagesForPrompt(canonical, "")[0]["content"].(string)
	got, _ := NormalizeOpenAIMessagesForPrompt(markerForm, "", markerUnderTest)[0]["content"].(string)
	if got != want {
		t.Fatalf("marker-form history rendered differently:\n got %q\nwant %q", got, want)
	}
}
