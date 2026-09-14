package promptcompat

import (
	"strings"
	"testing"

	"ds2api/internal/config"
)

// TestDisableToolsForRequestStripsToolDefinitions 验证「强制禁用工具调用」会
// 把请求里所有工具定义字段整体删除。
func TestDisableToolsForRequestStripsToolDefinitions(t *testing.T) {
	req := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
		},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "search"}},
		},
		"tool_choice":   "auto",
		"functions":     []any{map[string]any{"name": "legacy"}},
		"function_call": "auto",
	}

	DisableToolsForRequest(req)

	for _, field := range toolDefinitionFields {
		if _, ok := req[field]; ok {
			t.Fatalf("expected tool definition field %q to be removed, req=%#v", field, req)
		}
	}
	if !ToolsForceDisabledInRequest(req) {
		t.Fatal("expected the request to be marked as force-disabled")
	}
}

// TestDisableToolsForRequestNilIsNoop 验证 nil 请求不会 panic。
func TestDisableToolsForRequestNilIsNoop(t *testing.T) {
	DisableToolsForRequest(nil)
	if ToolsForceDisabledInRequest(nil) {
		t.Fatal("expected nil request to report tools as not disabled")
	}
	if ToolsForceDisabledInRequest(map[string]any{}) {
		t.Fatal("expected empty request to report tools as not disabled")
	}
}

// TestRequestBodyHasToolsHonoursForceDisable 验证强制禁用会同时覆盖
// 结构化 tools 判定与 system 文本关键词判定。
func TestRequestBodyHasToolsHonoursForceDisable(t *testing.T) {
	structured := map[string]any{
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "search"}},
		},
	}
	if !RequestBodyHasTools(structured) {
		t.Fatal("precondition: structured tools request should be detected as a tools request")
	}
	DisableToolsForRequest(structured)
	if RequestBodyHasTools(structured) {
		t.Fatal("expected structured tools to stop counting after force-disable")
	}

	// system 关键词判定（无 body tools 数组，如 RikkaHub workspace 应用）同样要覆盖。
	systemDeclared := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "Available tools: workspace_read_file, workspace_write_file"},
			map[string]any{"role": "user", "content": "read the file"},
		},
	}
	if !RequestBodyHasTools(systemDeclared) {
		t.Fatal("precondition: system-declared tools should be detected as a tools request")
	}
	DisableToolsForRequest(systemDeclared)
	if RequestBodyHasTools(systemDeclared) {
		t.Fatal("expected system-declared tools to stop counting after force-disable")
	}
}

// TestDisableToolsForRequestSkipsOpenAIChatToolInjection 验证强制禁用后
// OpenAI Chat 标准化不再注入任何工具提示词，且工具字段全空。
func TestDisableToolsForRequestSkipsOpenAIChatToolInjection(t *testing.T) {
	store := &fakeRouteConfigReader{aliases: config.DefaultModelAliases()}
	messages := []any{
		map[string]any{"role": "system", "content": "Available tools: workspace_read_file"},
		map[string]any{"role": "user", "content": "read the file"},
	}

	// 基线：普通请求（含 system 文本声明工具）会注入 EPSE 格式规范。
	baseline, err := NormalizeOpenAIChatRequest(store, map[string]any{
		"model":    "deepseek-v4.1-flash",
		"messages": messages,
	}, "")
	if err != nil {
		t.Fatalf("baseline normalize failed: %v", err)
	}
	if !strings.Contains(baseline.FinalPrompt, "EPSE") {
		t.Fatalf("precondition: baseline prompt should carry the tool-call format spec, got:\n%s", baseline.FinalPrompt)
	}
	if baseline.ToolsDisabled {
		t.Fatal("baseline request must not be flagged as tools-disabled")
	}

	req := map[string]any{
		"model":    "deepseek-v4.1-flash",
		"messages": messages,
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "workspace_read_file"}},
		},
	}
	DisableToolsForRequest(req)
	std, err := NormalizeOpenAIChatRequest(store, req, "")
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if !std.ToolsDisabled {
		t.Fatal("expected StandardRequest.ToolsDisabled to be set")
	}
	if std.ToolsRaw != nil {
		t.Fatalf("expected ToolsRaw to be nil, got %#v", std.ToolsRaw)
	}
	if len(std.ToolNames) != 0 {
		t.Fatalf("expected empty ToolNames, got %#v", std.ToolNames)
	}
	if !std.ToolChoice.IsNone() {
		t.Fatalf("expected ToolChoice none, got %q", std.ToolChoice.Mode)
	}
	if strings.Contains(std.FinalPrompt, "EPSE") {
		t.Fatalf("expected no tool-call format spec in prompt, got:\n%s", std.FinalPrompt)
	}
	if strings.Contains(std.FinalPrompt, "You have access to these tools") {
		t.Fatalf("expected no tool descriptions in prompt, got:\n%s", std.FinalPrompt)
	}
}

// TestDisableToolsForRequestSkipsOpenAIResponsesToolInjection 验证 Responses
// 标准化同样在强制禁用后跳过注入，并且不会因为残留 tool_choice 报错。
func TestDisableToolsForRequestSkipsOpenAIResponsesToolInjection(t *testing.T) {
	store := &fakeRouteConfigReader{aliases: config.DefaultModelAliases()}
	req := map[string]any{
		"model": "deepseek-v4.1-flash",
		"input": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "search"}},
		},
		"tool_choice": "required",
	}
	DisableToolsForRequest(req)

	std, err := NormalizeOpenAIResponsesRequest(store, req, "")
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if !std.ToolsDisabled {
		t.Fatal("expected StandardRequest.ToolsDisabled to be set")
	}
	if std.ToolsRaw != nil || len(std.ToolNames) != 0 {
		t.Fatalf("expected no tools to survive, got raw=%#v names=%#v", std.ToolsRaw, std.ToolNames)
	}
	if !std.ToolChoice.IsNone() {
		t.Fatalf("expected ToolChoice none, got %q", std.ToolChoice.Mode)
	}
	if strings.Contains(std.FinalPrompt, "EPSE") {
		t.Fatalf("expected no tool-call format spec in prompt, got:\n%s", std.FinalPrompt)
	}
}

// TestBuildOpenAIPromptNonePolicySkipsSystemTextSpec 验证 tool_choice=none
// 时 system 文本声明工具也不再触发格式规范注入。
func TestBuildOpenAIPromptNonePolicySkipsSystemTextSpec(t *testing.T) {
	messages := []any{
		map[string]any{"role": "system", "content": "Available tools: read_file"},
		map[string]any{"role": "user", "content": "hi"},
	}
	prompt, names := BuildOpenAIPrompt(messages, nil, "", ToolChoicePolicy{Mode: ToolChoiceNone}, false, DefaultToolReminderConfig())
	if strings.Contains(prompt, "EPSE") {
		t.Fatalf("expected no format spec under ToolChoiceNone, got:\n%s", prompt)
	}
	if len(names) != 0 {
		t.Fatalf("expected no tool names, got %#v", names)
	}
}
