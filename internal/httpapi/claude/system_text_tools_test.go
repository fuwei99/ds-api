package claude

import (
	"testing"

	"ds2api/internal/config"
	"ds2api/internal/promptcompat"
)

const claudeToolSpecHead = "工具调用格式规范"

// Claude 的工具目录只能来自顶层 `tools` 数组：Anthropic 的 `system` 是独立字段，
// 原始 body 的 `messages` 里不会出现 system 轮，所以路由判定
// promptcompat.RequestBodyHasTools（扫原始 body）必然把「没有 tools 数组」的 Claude
// 请求判为无工具请求。
//
// 共享组装看到的却是转换后的消息（顶层 system 已经变成一条 system 轮）。如果出口不
// 声明「工具只来自顶层数组」，system 文本里恰好出现 "tool" 字样就会命中
// MessagesDeclareToolsInSystemText，从而注入 EPSE 格式规范：路由认为无工具、prompt
// 却要求模型输出工具调用。又因为这类请求 ToolNames 为空，Claude 流式运行时的
// bufferToolContent 为 false（见 stream_runtime_core.go），模型照做输出的 EPSE 块
// 不会被筛分，会直接漏进可见正文。
//
// 这组测试锁定该分支对 Claude 关闭、对顶层 tools 数组仍然照常注入。
func TestNormalizeClaudeRequestSkipsFormatSpecForSystemTextTools(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{}`)
	store := config.LoadStore()
	req := map[string]any{
		"model":  "claude-sonnet-4-5",
		"system": "You are Claude Code, Anthropic's official CLI tool.",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
	}

	norm, err := normalizeClaudeRequest(store, req)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if containsStr(norm.Standard.FinalPrompt, claudeToolSpecHead) || containsStr(norm.Standard.FinalPrompt, "<|EPSE|") {
		t.Fatalf("a Claude request without a tools array must not get the tool-call format spec, got=%q", norm.Standard.FinalPrompt)
	}
	if containsStr(norm.Standard.FinalPrompt, claudeReminderHead) {
		t.Fatalf("no tool prompt means no trailing reminder either, got=%q", norm.Standard.FinalPrompt)
	}
	if len(norm.Standard.ToolNames) != 0 {
		t.Fatalf("expected no tool names, got=%#v", norm.Standard.ToolNames)
	}
	if !norm.Standard.SystemTextToolsDisabled {
		t.Fatal("expected the Claude adapter to declare that its tools only arrive in the top-level array")
	}
	// 只是不注入工具规范，system 文本本身照常渲染。
	if !containsStr(norm.Standard.FinalPrompt, "official CLI tool") {
		t.Fatalf("the system prompt must still be rendered, got=%q", norm.Standard.FinalPrompt)
	}
}

// 同一形态的另一种写法：system 文本放在 messages 里的 system 轮（Claude 适配层允许，
// 部分客户端这样发）。判定口径与顶层 `system` 一致——出口形态决定了它不可能是工具目录。
func TestNormalizeClaudeRequestSkipsFormatSpecForSystemMessageTools(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{}`)
	store := config.LoadStore()
	req := map[string]any{
		"model": "claude-sonnet-4-5",
		"messages": []any{
			map[string]any{"role": "system", "content": "Available tools: read_file, write_file"},
			map[string]any{"role": "user", "content": "hello"},
		},
	}

	norm, err := normalizeClaudeRequest(store, req)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if containsStr(norm.Standard.FinalPrompt, claudeToolSpecHead) || containsStr(norm.Standard.FinalPrompt, "<|EPSE|") {
		t.Fatalf("a Claude request without a tools array must not get the tool-call format spec, got=%q", norm.Standard.FinalPrompt)
	}
	if !containsStr(norm.Standard.FinalPrompt, "Available tools: read_file, write_file") {
		t.Fatalf("the system turn must still be rendered, got=%q", norm.Standard.FinalPrompt)
	}
}

// 关闭的是「system 文本声明工具」这一条推断，不是整个工具注入：带顶层 tools 数组的
// Claude 请求照旧拿到工具提示词与格式规范。
func TestNormalizeClaudeRequestKeepsToolInjectionForTopLevelTools(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{}`)
	store := config.LoadStore()
	req := claudeToolsRequest()
	req["system"] = "You are Claude Code, Anthropic's official CLI tool."

	norm, err := normalizeClaudeRequest(store, req)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if !containsStr(norm.Standard.FinalPrompt, "You have access to these tools") {
		t.Fatalf("expected the tool prompt for a top-level tools array, got=%q", norm.Standard.FinalPrompt)
	}
	if !containsStr(norm.Standard.FinalPrompt, claudeToolSpecHead) {
		t.Fatalf("expected the tool-call format spec for a top-level tools array, got=%q", norm.Standard.FinalPrompt)
	}
	if len(norm.Standard.ToolNames) == 0 {
		t.Fatalf("expected tool names, got=%#v", norm.Standard.ToolNames)
	}
}

// 「强制禁用工具调用」的请求什么都不注入：工具定义已在入口处被清除，工具策略钉成
// none，system 文本里的关键词也不能把任何工具内容拉回来。
func TestNormalizeClaudeRequestForceDisabledToolsInjectNothing(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{}`)
	store := config.LoadStore()
	req := claudeToolsRequest()
	req["system"] = "You are Claude Code, Anthropic's official CLI tool."
	promptcompat.DisableToolsForRequest(req)

	norm, err := normalizeClaudeRequest(store, req)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if !norm.Standard.ToolsDisabled {
		t.Fatal("expected the force-disabled state on the standard request")
	}
	if !norm.Standard.ToolChoice.IsNone() {
		t.Fatalf("expected tool_choice none for a force-disabled request, got=%#v", norm.Standard.ToolChoice)
	}
	for _, needle := range []string{claudeToolSpecHead, "<|EPSE|", "You have access to these tools", claudeReminderHead} {
		if containsStr(norm.Standard.FinalPrompt, needle) {
			t.Fatalf("force-disabled request must not inject %q, got=%q", needle, norm.Standard.FinalPrompt)
		}
	}
	if len(norm.Standard.ToolNames) != 0 {
		t.Fatalf("expected no tool names, got=%#v", norm.Standard.ToolNames)
	}
}
