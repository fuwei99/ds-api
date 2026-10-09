package claude

import (
	"context"
	"strings"
	"testing"

	"ds2api/internal/auth"
	"ds2api/internal/config"
	"ds2api/internal/promptcompat"
)

// 「思考注入」是全局设置，但此前只接在 OpenAI 两个出口上：打开它，Claude 与
// Gemini 的 prompt 完全没有变化。这两条测试锁定 Claude 出口已经接上，注入落在
// 最后一条 user 消息上，并且发生在 current-input-file 之前（与 OpenAI 同位置）。
//
// 消息列表里刻意放了一条 system 轮：重建是按 StandardRequest.Messages 重放整份
// 输入的，任何一轮（包括 system）都不允许在重建时消失。
func TestClaudeAppliesThinkingInjectionToLatestUserTurn(t *testing.T) {
	h := &Handler{
		Store: claudeHistoryConfig{thinkingInjection: true},
		DS:    &claudeCurrentInputDS{},
	}
	stdReq := promptcompat.StandardRequest{
		Surface: "anthropic_messages",
		Messages: []any{
			map[string]any{"role": "system", "content": "BASE-SYSTEM"},
			map[string]any{"role": "user", "content": "hello"},
		},
		Thinking:    true,
		FinalPrompt: "[System]:BASE-SYSTEM\n[User]:hello\n[Assistant]:",
	}

	out, err := h.applyCurrentInputFile(context.Background(), &auth.RequestAuth{DeepSeekToken: "direct-token"}, stdReq)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if !strings.Contains(out.FinalPrompt, promptcompat.ThinkingInjectionMarker) {
		t.Fatalf("expected the thinking injection in the rebuilt prompt, got %q", out.FinalPrompt)
	}
	if !strings.Contains(out.FinalPrompt, "BASE-SYSTEM") {
		t.Fatalf("the rebuild must keep every turn of Messages, got %q", out.FinalPrompt)
	}
	content, _ := out.Messages[1].(map[string]any)["content"].(string)
	if !strings.Contains(content, promptcompat.ThinkingInjectionMarker) {
		t.Fatalf("expected the latest user turn to carry the injection, got %#v", out.Messages)
	}
}

func TestClaudeSkipsThinkingInjectionWhenTheSettingIsOff(t *testing.T) {
	h := &Handler{
		Store: claudeHistoryConfig{},
		DS:    &claudeCurrentInputDS{},
	}
	stdReq := promptcompat.StandardRequest{
		Surface:     "anthropic_messages",
		Messages:    []any{map[string]any{"role": "user", "content": "hello"}},
		Thinking:    true,
		FinalPrompt: "[User]:hello\n[Assistant]:",
	}

	out, err := h.applyCurrentInputFile(context.Background(), &auth.RequestAuth{DeepSeekToken: "direct-token"}, stdReq)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if strings.Contains(out.FinalPrompt, promptcompat.ThinkingInjectionMarker) {
		t.Fatalf("expected no injection while the setting is off, got %q", out.FinalPrompt)
	}
}

// TestClaudeThinkingInjectionKeepsTopLevelSystemPrompt 是回归测试：Anthropic 的
// `system` 是顶层字段而不是消息，而共享的 ApplyThinkingInjection 按
// StandardRequest.Messages 重建整份 prompt。适配层一旦把「不含 system 轮的中间
// 列表」存进 Messages，开启思考注入的 Claude 请求就会整段丢掉调用方的 system
// prompt——旧路径（Claude 自带组装、从不按 Messages 重建）不会，所以这个缺陷只在
// 并入共享路径之后出现。
//
// 测试必须走真实适配器 normalizeClaudeRequest：手工构造的 StandardRequest 里
// Messages 天然就是最终列表，覆盖不到「适配层存错列表」这一层。
func TestClaudeThinkingInjectionKeepsTopLevelSystemPrompt(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{"thinking_injection":{"enabled":true}}`)
	store := config.LoadStore()
	req := map[string]any{
		"model":  "claude-sonnet-4-5",
		"system": "SYSTEM-RULE: always answer in haiku.",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
	}

	norm, err := normalizeClaudeRequest(store, req)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	// 适配器不变量：Messages 就是 FinalPrompt 渲染所依据的那份列表，由顶层
	// system 翻译出的 system 轮必须已经在里面。
	if !messagesContainText(norm.Standard.Messages, "SYSTEM-RULE") {
		t.Fatalf("expected the translated system turn in StandardRequest.Messages, got %#v", norm.Standard.Messages)
	}
	if !containsStr(norm.Standard.FinalPrompt, "SYSTEM-RULE") {
		t.Fatalf("expected the system prompt in the normalized final prompt, got=%q", norm.Standard.FinalPrompt)
	}

	out := promptcompat.ApplyThinkingInjection(store, norm.Standard)
	if !containsStr(out.FinalPrompt, promptcompat.ThinkingInjectionMarker) {
		t.Fatalf("expected the thinking injection in the rebuilt prompt, got=%q", out.FinalPrompt)
	}
	if !containsStr(out.FinalPrompt, "SYSTEM-RULE") {
		t.Fatalf("thinking injection must not drop the caller's top-level system prompt, got=%q", out.FinalPrompt)
	}
	if !messagesContainText(out.Messages, "SYSTEM-RULE") {
		t.Fatalf("expected the system turn to survive the rebuild, got %#v", out.Messages)
	}
	// 不变量本身：Messages 渲染出来仍必须带着 system 轮。
	if rendered := promptcompat.RenderPromptText(out.Messages, out.Thinking); !containsStr(rendered, "SYSTEM-RULE") {
		t.Fatalf("Messages must stay the source the prompt is rendered from, got=%q", rendered)
	}
}

// TestClaudeSystemPromptIsPartOfUploadedHistory 锁定并入共享路径后的第二个修复：
// system 轮现在也在 Messages 里，因此 current-input-file 的 HISTORY.txt 转录会带上
// 调用方的 system prompt（此前 Claude 的转录把它整段丢掉）。
func TestClaudeSystemPromptIsPartOfUploadedHistory(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{}`)
	store := config.LoadStore()
	req := map[string]any{
		"model":  "claude-sonnet-4-5",
		"system": "SYSTEM-RULE: always answer in haiku.",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
	}

	norm, err := normalizeClaudeRequest(store, req)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}

	transcript := promptcompat.BuildOpenAICurrentInputContextTranscript(norm.Standard.Messages, norm.Standard.ToolMarker)
	if !containsStr(transcript, "SYSTEM-RULE") {
		t.Fatalf("expected the system prompt in the uploaded history transcript, got=%q", transcript)
	}
	if !containsStr(transcript, "hello") {
		t.Fatalf("expected the user turn in the uploaded history transcript, got=%q", transcript)
	}
}

func messagesContainText(messages []any, needle string) bool {
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if containsStr(promptcompat.NormalizeOpenAIContentForPrompt(msg["content"]), needle) {
			return true
		}
	}
	return false
}
