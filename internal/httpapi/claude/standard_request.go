package claude

import (
	"fmt"
	"strings"

	"ds2api/internal/config"
	"ds2api/internal/prompt"
	"ds2api/internal/promptcompat"
	"ds2api/internal/util"
)

type claudeNormalizedRequest struct {
	Standard           promptcompat.StandardRequest
	NormalizedMessages []any
}

func normalizeClaudeRequest(store ConfigReader, req map[string]any) (claudeNormalizedRequest, error) {
	model, _ := req["model"].(string)
	messagesRaw, _ := req["messages"].([]any)
	if strings.TrimSpace(model) == "" || len(messagesRaw) == 0 {
		return claudeNormalizedRequest{}, fmt.Errorf("request must include 'model' and 'messages'")
	}
	if _, ok := req["max_tokens"]; !ok {
		req["max_tokens"] = 8192
	}
	normalizedMessages := normalizeClaudeMessages(messagesRaw)
	payload := cloneMap(req)
	payload["messages"] = normalizedMessages
	// API Key 上的「强制禁用工具调用」：工具定义已在入口处被清除，这里只把
	// 状态带到标准请求上，供 current_input_file 等共享链路判断。
	toolsDisabled := promptcompat.ToolsForceDisabledInRequest(req)
	toolPolicy := promptcompat.DefaultToolChoicePolicy()
	if toolsDisabled {
		// API Key 上的「强制禁用工具调用」：工具策略钉成 none，与 OpenAI Chat /
		// Responses、Gemini 保持一致，保证 StandardRequest.ToolChoice 不变量成立
		// （ToolsDisabled 为 true 时 ToolChoice 必为 ToolChoiceNone）。
		toolPolicy = promptcompat.ToolChoicePolicy{Mode: promptcompat.ToolChoiceNone}
	}
	toolsRequested, _ := req["tools"].([]any)
	reminder := promptcompat.ResolveToolReminder(store)
	payload["messages"] = injectClaudeToolPrompt(payload, normalizedMessages, toolsRequested, reminder)

	dsPayload := convertClaudeToDeepSeek(payload, store)
	dsModel, _ := dsPayload["model"].(string)
	defaultThinkingEnabled, searchEnabled, ok := config.GetModelConfig(dsModel)
	if !ok {
		searchEnabled = false
	}
	thinkingEnabled := util.ResolveThinkingEnabled(req, defaultThinkingEnabled)
	if config.IsNoThinkingModel(dsModel) {
		thinkingEnabled = false
	}
	finalPrompt := prompt.MessagesPrepareWithThinking(toMessageMaps(dsPayload["messages"]), thinkingEnabled)
	toolNames := extractClaudeToolNames(toolsRequested)
	if len(toolNames) == 0 && len(toolsRequested) > 0 {
		toolNames = []string{"__any_tool__"}
	}

	return claudeNormalizedRequest{
		Standard: promptcompat.StandardRequest{
			Surface:              "anthropic_messages",
			RequestedModel:       strings.TrimSpace(model),
			ResolvedModel:        dsModel,
			ResponseModel:        strings.TrimSpace(model),
			Messages:             normalizedMessages,
			PromptTokenText:      finalPrompt,
			ToolsRaw:             toolsRequested,
			FinalPrompt:          finalPrompt,
			ToolNames:            toolNames,
			ToolChoice:           toolPolicy,
			ToolsDisabled:        toolsDisabled,
			Stream:               util.ToBool(req["stream"]),
			Thinking:             thinkingEnabled,
			Search:               searchEnabled,
			ToolReminderDisabled: !reminder.Enabled,
			ToolReminderPrompt:   reminder.Prompt,
		},
		NormalizedMessages: normalizedMessages,
	}, nil
}

func injectClaudeToolPrompt(payload map[string]any, normalizedMessages []any, tools []any, reminder promptcompat.ToolReminderConfig) []any {
	if len(tools) == 0 {
		return normalizedMessages
	}
	toolPrompt := strings.TrimSpace(buildClaudeToolPrompt(tools))
	if toolPrompt == "" {
		return normalizedMessages
	}

	// Prefer top-level Anthropic-style system prompt when available.
	if systemText, ok := payload["system"].(string); ok && strings.TrimSpace(systemText) != "" {
		payload["system"] = mergeSystemPrompt(systemText, toolPrompt)
		return appendClaudeToolCallFormatReminder(normalizedMessages, reminder)
	}

	messages := cloneAnySlice(normalizedMessages)
	for i := range messages {
		msg, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if !strings.EqualFold(strings.TrimSpace(role), "system") {
			continue
		}
		copied := cloneMap(msg)
		copied["content"] = mergeSystemPrompt(strings.TrimSpace(fmt.Sprintf("%v", copied["content"])), toolPrompt)
		messages[i] = copied
		return appendClaudeToolCallFormatReminder(messages, reminder)
	}

	messages = append([]any{map[string]any{"role": "system", "content": toolPrompt}}, messages...)
	return appendClaudeToolCallFormatReminder(messages, reminder)
}

// appendClaudeToolCallFormatReminder appends the short tool-call format
// reminder as the last system turn so it renders immediately before the
// trailing `[Assistant]:` marker, matching the OpenAI/Gemini prompt path.
// When the sequence already ends with an assistant turn the block is inserted
// before it, keeping the trailing-assistant continuation shape intact.
//
// The block is controlled by the 「底部格式提示注入」behavior setting: when the
// setting is off nothing is appended, and a non-empty custom prompt replaces
// the built-in text.
func appendClaudeToolCallFormatReminder(messages []any, reminder promptcompat.ToolReminderConfig) []any {
	text := reminder.ReminderText()
	if text == "" {
		return messages
	}
	insertAt := len(messages)
	if n := len(messages); n > 0 {
		if msg, ok := messages[n-1].(map[string]any); ok {
			role, _ := msg["role"].(string)
			if strings.EqualFold(strings.TrimSpace(role), "assistant") {
				insertAt = n - 1
			}
		}
	}
	out := make([]any, 0, len(messages)+1)
	out = append(out, messages[:insertAt]...)
	out = append(out, map[string]any{"role": "system", "content": text})
	out = append(out, messages[insertAt:]...)
	return out
}

func mergeSystemPrompt(base, extra string) string {
	base = strings.TrimSpace(base)
	extra = strings.TrimSpace(extra)
	switch {
	case base == "":
		return extra
	case extra == "":
		return base
	default:
		return base + "\n\n" + extra
	}
}

func cloneAnySlice(in []any) []any {
	if len(in) == 0 {
		return nil
	}
	out := make([]any, len(in))
	copy(out, in)
	return out
}
