package promptcompat

import (
	"strings"
	"testing"
)

type fakeToolReminderStore struct {
	enabled bool
	prompt  string
}

func (f fakeToolReminderStore) BottomFormatInjectionEnabled() bool  { return f.enabled }
func (f fakeToolReminderStore) BottomFormatInjectionPrompt() string { return f.prompt }

// TestToolReminderConfigReminderText 覆盖提醒文本的三种解析结果：关闭 -> 空
// （不注入）、未自定义 -> 内置提醒、自定义 -> 原样（去除首尾空白）覆盖。
func TestToolReminderConfigReminderText(t *testing.T) {
	if got := (ToolReminderConfig{Enabled: false}).ReminderText(); got != "" {
		t.Fatalf("disabled reminder must be empty, got %q", got)
	}
	if got := (ToolReminderConfig{Enabled: false, Prompt: "自定义"}).ReminderText(); got != "" {
		t.Fatalf("disabled reminder must ignore the custom prompt, got %q", got)
	}
	if got := DefaultToolReminderConfig().ReminderText(); got != DefaultBottomFormatInjectionPrompt {
		t.Fatalf("default reminder must be the built-in prompt, got %q", got)
	}
	if got := (ToolReminderConfig{Enabled: true, Prompt: "  自定义提醒  "}).ReminderText(); got != "自定义提醒" {
		t.Fatalf("custom reminder must override the built-in prompt, got %q", got)
	}
	if got := (ToolReminderConfig{Enabled: true, Prompt: "   "}).ReminderText(); got != DefaultBottomFormatInjectionPrompt {
		t.Fatalf("blank custom prompt must fall back to the built-in prompt, got %q", got)
	}
	if !strings.Contains(DefaultBottomFormatInjectionPrompt, toolCallReminderHead) {
		t.Fatalf("built-in prompt must keep the EPSE skeleton, got %q", DefaultBottomFormatInjectionPrompt)
	}
}

// TestResolveToolReminder 验证配置读取的两种形态：实现 ToolReminderReader 的存储
// 按配置解析；未实现的轻量存储退化为内置行为（默认开启 + 内置提醒）。
func TestResolveToolReminder(t *testing.T) {
	got := ResolveToolReminder(fakeToolReminderStore{enabled: false, prompt: "自定义"})
	if got.Enabled || got.Prompt != "自定义" {
		t.Fatalf("resolved=%+v want disabled with custom prompt", got)
	}

	got = ResolveToolReminder(fakeToolReminderStore{enabled: true})
	if !got.Enabled || got.Prompt != "" {
		t.Fatalf("resolved=%+v want enabled without custom prompt", got)
	}

	for _, store := range []any{nil, struct{}{}} {
		got = ResolveToolReminder(store)
		if got != DefaultToolReminderConfig() {
			t.Fatalf("store %#v must fall back to the built-in behavior, got %+v", store, got)
		}
	}
}

// TestBuildOpenAIPromptReminderToggle 验证「底部格式提示注入」开关在 OpenAI
// prompt 组装层的生效方式：关闭后不注入，自定义后按自定义文本注入。
func TestBuildOpenAIPromptReminderToggle(t *testing.T) {
	messages := []any{
		map[string]any{"role": "system", "content": "You may use tools."},
		map[string]any{"role": "user", "content": "请调用工具"},
	}
	tools := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "search",
				"description": "search docs",
				"parameters":  map[string]any{"type": "object"},
			},
		},
	}

	finalPrompt, _ := buildOpenAIFinalPrompt(messages, tools, "", false, ToolReminderConfig{Enabled: false})
	if strings.Contains(finalPrompt, toolCallReminderHead) {
		t.Fatalf("disabled bottom format injection must not append the reminder, got: %q", finalPrompt)
	}
	// 关闭的只是末尾复述；工具格式规范本身仍要注入。
	if !strings.Contains(finalPrompt, "工具调用格式规范") {
		t.Fatalf("the format spec must still be injected when the reminder is off, got: %q", finalPrompt)
	}

	customPrompt := "自定义底部格式提醒"
	finalPrompt, _ = buildOpenAIFinalPrompt(messages, tools, "", false, ToolReminderConfig{Enabled: true, Prompt: customPrompt})
	if !strings.Contains(finalPrompt, customPrompt) {
		t.Fatalf("custom reminder must be injected, got: %q", finalPrompt)
	}
	if strings.Contains(finalPrompt, toolCallReminderHead) {
		t.Fatalf("custom reminder must replace the built-in one, got: %q", finalPrompt)
	}
	if !strings.HasSuffix(finalPrompt, customPrompt+"[Assistant]:") {
		t.Fatalf("custom reminder must stay immediately before the [Assistant] marker, got: %q", finalPrompt)
	}
}

// TestBuildOpenAIPromptForcedFormatSpecReminderToggle 验证 input_file 续写路径
// （强制注入格式规范的分支）同样受开关控制。
func TestBuildOpenAIPromptForcedFormatSpecReminderToggle(t *testing.T) {
	messages := []any{
		map[string]any{"role": "system", "content": "继续会话 使用工具时请参照说明与格式要求，仅使用所列出的工具"},
		map[string]any{"role": "user", "content": "read the file"},
	}

	finalPrompt, _ := BuildOpenAIPromptWithForcedFormatSpec(messages, "", DefaultToolChoicePolicy(), false, ToolReminderConfig{Enabled: false})
	if strings.Contains(finalPrompt, toolCallReminderHead) {
		t.Fatalf("disabled bottom format injection must not append the reminder, got: %q", finalPrompt)
	}
	if !strings.Contains(finalPrompt, "工具调用格式规范") {
		t.Fatalf("the forced format spec must still be injected, got: %q", finalPrompt)
	}
}
