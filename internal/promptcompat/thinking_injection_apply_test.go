package promptcompat

import (
	"strings"
	"testing"
)

// thinkingInjectionStoreStub implements ThinkingInjectionReader, which is what
// the real config.Store provides on the request path.
type thinkingInjectionStoreStub struct {
	enabled bool
	prompt  string
}

func (s thinkingInjectionStoreStub) ThinkingInjectionEnabled() bool { return s.enabled }
func (s thinkingInjectionStoreStub) ThinkingInjectionPrompt() string {
	return s.prompt
}

func thinkingInjectionRequest() StandardRequest {
	return StandardRequest{
		Surface:    "test_surface",
		Messages:   []any{map[string]any{"role": "user", "content": "hello"}},
		Thinking:   true,
		ToolsRaw:   []any{map[string]any{"name": "search", "description": "Search the web"}},
		ToolChoice: DefaultToolChoicePolicy(),
		ToolNames:  []string{"search"},
	}
}

// TestApplyThinkingInjectionRewritesMessagesAndPrompt 验证设置开启且本次请求启用
// 思考时，注入落在最后一条 user 消息上，prompt 由改写后的消息重建（因此仍然带着
// 工具提示词），并且返回的 ToolNames 不丢。
func TestApplyThinkingInjectionRewritesMessagesAndPrompt(t *testing.T) {
	out := ApplyThinkingInjection(thinkingInjectionStoreStub{enabled: true}, thinkingInjectionRequest())

	if len(out.Messages) != 1 {
		t.Fatalf("expected the message list to stay one turn, got %#v", out.Messages)
	}
	content, _ := out.Messages[0].(map[string]any)["content"].(string)
	if !strings.Contains(content, ThinkingInjectionMarker) {
		t.Fatalf("expected the latest user turn to carry the injection, got %q", content)
	}
	if !strings.Contains(out.FinalPrompt, ThinkingInjectionMarker) {
		t.Fatalf("expected the rebuilt prompt to carry the injection, got %q", out.FinalPrompt)
	}
	if !strings.Contains(out.FinalPrompt, "You have access to these tools:") {
		t.Fatalf("expected the rebuild to keep the tool prompt, got %q", out.FinalPrompt)
	}
	if len(out.ToolNames) == 0 {
		t.Fatalf("expected tool names to survive the rebuild, got %#v", out.ToolNames)
	}
}

// TestApplyThinkingInjectionRebuildsPromptTokenText 验证重建后的 prompt 也是
// token 统计所依据的那份文本：usage 上报的 prompt_tokens / input_tokens 由
// PromptTokenText 算出，留在注入之前的值会让上报的用量描述的是一份从未发出的
// prompt。
func TestApplyThinkingInjectionRebuildsPromptTokenText(t *testing.T) {
	req := thinkingInjectionRequest()
	req.PromptTokenText = "before-injection"

	out := ApplyThinkingInjection(thinkingInjectionStoreStub{enabled: true}, req)

	if out.PromptTokenText != out.FinalPrompt {
		t.Fatalf("PromptTokenText must describe the prompt that is sent:\n token text: %q\n final prompt: %q",
			out.PromptTokenText, out.FinalPrompt)
	}
	if !strings.Contains(out.PromptTokenText, ThinkingInjectionMarker) {
		t.Fatalf("expected the counted text to carry the injection, got %q", out.PromptTokenText)
	}
}

// TestApplyThinkingInjectionReappliesToolMarker 验证重建后的 prompt 重新套用了
// 调用者专属标识：重建会重新输出规范形态 EPSE，漏掉这一步会静默丢标识。
func TestApplyThinkingInjectionReappliesToolMarker(t *testing.T) {
	req := thinkingInjectionRequest()
	req.ToolMarker = "Q7ZK3M"

	out := ApplyThinkingInjection(thinkingInjectionStoreStub{enabled: true}, req)

	if strings.Contains(out.FinalPrompt, "<|EPSE|") {
		t.Fatalf("expected the canonical keyword to be rewritten to the caller marker, got %q", out.FinalPrompt)
	}
	if !strings.Contains(out.FinalPrompt, "<|Q7ZK3M|tool_calls>") {
		t.Fatalf("expected the caller marker in the rebuilt prompt, got %q", out.FinalPrompt)
	}
}

// TestApplyThinkingInjectionIsGated 验证三种不注入的情形：设置关闭、本次请求未
// 启用思考、读取方没有提供该能力（轻量 ConfigReader 桩）。
func TestApplyThinkingInjectionIsGated(t *testing.T) {
	off := thinkingInjectionRequest()
	out := ApplyThinkingInjection(thinkingInjectionStoreStub{enabled: false}, off)
	if out.FinalPrompt != off.FinalPrompt || strings.Contains(out.FinalPrompt, ThinkingInjectionMarker) {
		t.Fatalf("expected no injection while the setting is off, got %q", out.FinalPrompt)
	}

	noThinking := thinkingInjectionRequest()
	noThinking.Thinking = false
	out = ApplyThinkingInjection(thinkingInjectionStoreStub{enabled: true}, noThinking)
	if strings.Contains(out.FinalPrompt, ThinkingInjectionMarker) {
		t.Fatalf("expected no injection for a request without thinking, got %q", out.FinalPrompt)
	}

	out = ApplyThinkingInjection(struct{}{}, thinkingInjectionRequest())
	if strings.Contains(out.FinalPrompt, ThinkingInjectionMarker) {
		t.Fatalf("expected a reader without the capability to degrade to no injection, got %q", out.FinalPrompt)
	}
}

// TestApplyThinkingInjectionSkipsDuplicate 验证同一请求重复应用不会叠加注入。
func TestApplyThinkingInjectionSkipsDuplicate(t *testing.T) {
	store := thinkingInjectionStoreStub{enabled: true}
	once := ApplyThinkingInjection(store, thinkingInjectionRequest())
	twice := ApplyThinkingInjection(store, once)

	if strings.Count(twice.FinalPrompt, ThinkingInjectionMarker) != 1 {
		t.Fatalf("expected exactly one injection after applying twice, got %q", twice.FinalPrompt)
	}
}

// 重建必须沿用请求上记录的出口差异：声明「工具只能来自顶层 tools 数组」的出口，
// 在思考注入重建之后依然不能因为 system 文本里出现 "tool" 字样而拿到 EPSE 格式
// 规范。对照组是同形态但未声明该差异的出口——它照旧走 system 文本分支，说明这条
// 测试锁定的是「按出口收窄」而不是把分支整个删掉。
func TestApplyThinkingInjectionKeepsSurfaceSystemTextToolsOptOut(t *testing.T) {
	req := StandardRequest{
		Surface: "anthropic_messages",
		Messages: []any{
			map[string]any{"role": "system", "content": "You are Claude Code, Anthropic's official CLI tool."},
			map[string]any{"role": "user", "content": "hello"},
		},
		Thinking:                true,
		ToolChoice:              DefaultToolChoicePolicy(),
		SystemTextToolsDisabled: true,
	}

	out := ApplyThinkingInjection(thinkingInjectionStoreStub{enabled: true}, req)
	if !strings.Contains(out.FinalPrompt, ThinkingInjectionMarker) {
		t.Fatalf("expected the injection in the rebuilt prompt, got %q", out.FinalPrompt)
	}
	if strings.Contains(out.FinalPrompt, "工具调用格式规范") || strings.Contains(out.FinalPrompt, "<|EPSE|") {
		t.Fatalf("the surface opt-out must survive the rebuild, got %q", out.FinalPrompt)
	}
	if !strings.Contains(out.FinalPrompt, "official CLI tool") {
		t.Fatalf("the rebuild must keep the system turn, got %q", out.FinalPrompt)
	}

	other := req
	other.SystemTextToolsDisabled = false
	out = ApplyThinkingInjection(thinkingInjectionStoreStub{enabled: true}, other)
	if !strings.Contains(out.FinalPrompt, "工具调用格式规范") {
		t.Fatalf("expected the system-text branch to stay on for other surfaces, got %q", out.FinalPrompt)
	}
}
