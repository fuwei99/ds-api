package claude

import (
	"strings"
	"testing"

	"ds2api/internal/config"
)

func claudeToolsRequest() map[string]any {
	return map[string]any{
		"model": "claude-sonnet-4-5",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
		"tools": []any{map[string]any{"name": "search", "description": "Search"}},
	}
}

// TestNormalizeClaudeRequestBottomFormatInjectionDisabled 验证「底部格式提示注入」
// 关闭后 Claude 路径不再追加末尾复述（工具提示词本身仍注入），并且开关状态被
// 带到 StandardRequest 上供下游重建路径沿用。
func TestNormalizeClaudeRequestBottomFormatInjectionDisabled(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{"bottom_format_injection":{"enabled":false}}`)
	store := config.LoadStore()

	norm, err := normalizeClaudeRequest(store, claudeToolsRequest())
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if !norm.Standard.ToolReminderDisabled {
		t.Fatal("expected the disabled switch to be carried on the standard request")
	}
	if containsStr(norm.Standard.FinalPrompt, claudeReminderHead) {
		t.Fatalf("disabled injection must not append the reminder, got=%q", norm.Standard.FinalPrompt)
	}
	if !containsStr(norm.Standard.FinalPrompt, "You have access to these tools") {
		t.Fatalf("tool prompt must still be injected, got=%q", norm.Standard.FinalPrompt)
	}
}

// TestNormalizeClaudeRequestBottomFormatInjectionCustomPrompt 验证自定义提示词
// 覆盖内置提醒，且位置仍紧贴结尾的 [Assistant] 标记。
func TestNormalizeClaudeRequestBottomFormatInjectionCustomPrompt(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{"bottom_format_injection":{"enabled":true,"prompt":"自定义底部格式提醒"}}`)
	store := config.LoadStore()

	norm, err := normalizeClaudeRequest(store, claudeToolsRequest())
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if norm.Standard.ToolReminderDisabled {
		t.Fatal("expected the switch to stay enabled")
	}
	if norm.Standard.ToolReminderPrompt != "自定义底部格式提醒" {
		t.Fatalf("custom prompt=%q want 自定义底部格式提醒", norm.Standard.ToolReminderPrompt)
	}
	prompt := norm.Standard.FinalPrompt
	if !strings.HasSuffix(prompt, "自定义底部格式提醒[Assistant]:") {
		t.Fatalf("custom reminder must stay immediately before the [Assistant] marker, got=%q", prompt)
	}
	if containsStr(prompt, claudeReminderHead) {
		t.Fatalf("custom reminder must replace the built-in one, got=%q", prompt)
	}
}
