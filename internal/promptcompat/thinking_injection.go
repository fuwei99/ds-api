package promptcompat

import (
	"strings"

	"ds2api/internal/config"
	"ds2api/internal/toolcall"
)

const (
	ThinkingInjectionMarker        = "text: You are a helpful software engineer assistant."
	DefaultThinkingInjectionPrompt = ThinkingInjectionMarker + "\n" +
		"description: Bootstrap with shell/read, then expose the full Standard tool catalog after the first durable tool call.\n" +
		`**when you thought, start with "we need..."**`
)

// ThinkingInjectionReader 由配置存储实现，提供「思考注入」设置。与
// ToolReminderReader 一样，它以窄接口的形式在请求路径上按需断言：真实实现只有
// config.Store，各协议包里的轻量 ConfigReader 桩无需实现它（未实现即退化为
// 「不注入」，与历史行为一致）。
type ThinkingInjectionReader interface {
	ThinkingInjectionEnabled() bool
	ThinkingInjectionPrompt() string
}

// 真实配置存储必须实现该能力，否则「思考注入」会在所有出口上静默失效。
var _ ThinkingInjectionReader = (*config.Store)(nil)

// ApplyThinkingInjection applies the 「思考注入」 setting to a normalized request:
// when the setting is on and this request asked for thinking, it appends the
// injection prompt to the latest user turn and rebuilds the prompt from the
// rewritten messages.
//
// Every protocol surface calls it once, before the current-input-file step, so
// the setting behaves the same on OpenAI Chat / Responses, Gemini and Claude
// instead of being wired into some surfaces only.
func ApplyThinkingInjection(store any, stdReq StandardRequest) StandardRequest {
	reader, ok := store.(ThinkingInjectionReader)
	if !ok || !reader.ThinkingInjectionEnabled() || !stdReq.Thinking {
		return stdReq
	}
	messages, changed := AppendThinkingInjectionPromptToLatestUser(stdReq.Messages, reader.ThinkingInjectionPrompt())
	if !changed {
		return stdReq
	}
	// The rebuild replays stdReq.Messages through the same surface-aware builder
	// the adapter used (StandardRequest.BuildPromptFromMessages), so an exit that
	// opted out of the system-text tool branch does not get it back here — and,
	// because Messages is the list FinalPrompt was rendered from, no turn can go
	// missing on the way through.
	finalPrompt, toolNames := stdReq.BuildPromptFromMessages(messages)
	if len(toolNames) == 0 && len(stdReq.ToolNames) > 0 {
		toolNames = stdReq.ToolNames
	}
	stdReq.Messages = messages
	// The prompt is rebuilt from the messages, so the renderers emit the
	// canonical EPSE keyword again: the caller-specific marker has to be
	// re-applied here or this rebuild would silently drop it (the same
	// invariant the request normalize path enforces).
	stdReq.FinalPrompt = toolcall.ApplyToolMarker(finalPrompt, stdReq.ToolMarker)
	// PromptTokenText is the text the usage reports count, and every exit sets it
	// to the prompt it renders. Leaving it at the pre-injection value would make
	// the reported prompt_tokens / input_tokens describe a prompt that was never
	// sent — the injection is exactly the difference.
	stdReq.PromptTokenText = stdReq.FinalPrompt
	stdReq.ToolNames = toolNames
	return stdReq
}

func AppendThinkingInjectionToLatestUser(messages []any) ([]any, bool) {
	return AppendThinkingInjectionPromptToLatestUser(messages, "")
}

func AppendThinkingInjectionPromptToLatestUser(messages []any, injectionPrompt string) ([]any, bool) {
	if len(messages) == 0 {
		return messages, false
	}
	injectionPrompt = strings.TrimSpace(injectionPrompt)
	if injectionPrompt == "" {
		injectionPrompt = DefaultThinkingInjectionPrompt
	}
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}
		if strings.ToLower(strings.TrimSpace(asString(msg["role"]))) != "user" {
			continue
		}
		content := msg["content"]
		normalizedContent := NormalizeOpenAIContentForPrompt(content)
		if strings.Contains(normalizedContent, ThinkingInjectionMarker) || strings.Contains(normalizedContent, injectionPrompt) {
			return messages, false
		}
		updatedContent := appendThinkingInjectionToContent(content, injectionPrompt)
		out := append([]any(nil), messages...)
		cloned := make(map[string]any, len(msg))
		for k, v := range msg {
			cloned[k] = v
		}
		cloned["content"] = updatedContent
		out[i] = cloned
		return out, true
	}
	return messages, false
}

func appendThinkingInjectionToContent(content any, injectionPrompt string) any {
	switch x := content.(type) {
	case string:
		return appendTextBlock(x, injectionPrompt)
	case []any:
		out := append([]any(nil), x...)
		out = append(out, map[string]any{
			"type": "text",
			"text": injectionPrompt,
		})
		return out
	default:
		text := NormalizeOpenAIContentForPrompt(content)
		return appendTextBlock(text, injectionPrompt)
	}
}

func appendTextBlock(base, addition string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return addition
	}
	return base + "\n\n" + addition
}
