package gemini

import (
	"context"
	"strings"
	"testing"

	"ds2api/internal/auth"
	"ds2api/internal/promptcompat"
)

// testGeminiThinkingConfig 只用于「思考注入」接线测试：current-input-file 关闭，
// 以便断言聚焦在注入本身（真实接线顺序是注入 → current-input-file）。
type testGeminiThinkingConfig struct {
	enabled bool
}

func (testGeminiThinkingConfig) ModelAliases() map[string]string  { return nil }
func (testGeminiThinkingConfig) CurrentInputFileEnabled() bool    { return false }
func (testGeminiThinkingConfig) CurrentInputFileMinChars() int    { return 0 }
func (testGeminiThinkingConfig) ExpertPromptSegmentEnabled() bool { return false }
func (testGeminiThinkingConfig) ExpertPromptSegmentMaxChars() int { return 120000 }
func (c testGeminiThinkingConfig) ThinkingInjectionEnabled() bool { return c.enabled }
func (testGeminiThinkingConfig) ThinkingInjectionPrompt() string  { return "" }

// 「思考注入」是全局设置，但此前只接在 OpenAI 两个出口上：打开它，Gemini 的
// prompt 完全没有变化。这两条测试锁定 Gemini 出口已经接上。
func TestGeminiAppliesThinkingInjectionToLatestUserTurn(t *testing.T) {
	h := &Handler{
		Store: testGeminiThinkingConfig{enabled: true},
		DS:    &testGeminiDS{},
	}
	stdReq := promptcompat.StandardRequest{
		Surface:     "google_gemini",
		Messages:    []any{map[string]any{"role": "user", "content": "hello"}},
		Thinking:    true,
		FinalPrompt: "[User]:hello\n[Assistant]:",
	}

	out, err := h.applyCurrentInputFile(context.Background(), &auth.RequestAuth{DeepSeekToken: "direct-token"}, stdReq)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if !strings.Contains(out.FinalPrompt, promptcompat.ThinkingInjectionMarker) {
		t.Fatalf("expected the thinking injection in the rebuilt prompt, got %q", out.FinalPrompt)
	}
	content, _ := out.Messages[0].(map[string]any)["content"].(string)
	if !strings.Contains(content, promptcompat.ThinkingInjectionMarker) {
		t.Fatalf("expected the latest user turn to carry the injection, got %#v", out.Messages)
	}
}

func TestGeminiSkipsThinkingInjectionWhenTheSettingIsOff(t *testing.T) {
	h := &Handler{
		Store: testGeminiThinkingConfig{},
		DS:    &testGeminiDS{},
	}
	stdReq := promptcompat.StandardRequest{
		Surface:     "google_gemini",
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
