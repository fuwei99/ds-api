package promptcompat

import (
	"ds2api/internal/prompt"
)

func buildOpenAIFinalPrompt(messagesRaw []any, toolsRaw any, traceID string, thinkingEnabled bool, reminder ToolReminderConfig, toolMarker ...string) (string, []string) {
	return BuildOpenAIPrompt(messagesRaw, toolsRaw, traceID, DefaultToolChoicePolicy(), thinkingEnabled, reminder, toolMarker...)
}

func BuildOpenAIPrompt(messagesRaw []any, toolsRaw any, traceID string, toolPolicy ToolChoicePolicy, thinkingEnabled bool, reminder ToolReminderConfig, toolMarker ...string) (string, []string) {
	return buildOpenAIPrompt(messagesRaw, toolsRaw, traceID, toolPolicy, thinkingEnabled, true, reminder, toolMarker...)
}

func BuildOpenAIPromptWithToolInstructionsOnly(messagesRaw []any, toolsRaw any, traceID string, toolPolicy ToolChoicePolicy, thinkingEnabled bool, reminder ToolReminderConfig, toolMarker ...string) (string, []string) {
	return buildOpenAIPrompt(messagesRaw, toolsRaw, traceID, toolPolicy, thinkingEnabled, false, reminder, toolMarker...)
}

func BuildOpenAIPromptWithToolInstructionsOnlyWithReadGuard(messagesRaw []any, toolsRaw any, traceID string, toolPolicy ToolChoicePolicy, thinkingEnabled bool, includeReadCacheGuard bool, reminder ToolReminderConfig, toolMarker ...string) (string, []string) {
	return buildOpenAIPromptWithReadGuard(messagesRaw, toolsRaw, traceID, toolPolicy, thinkingEnabled, false, includeReadCacheGuard, reminder, toolMarker...)
}

// BuildOpenAIPromptWithForcedFormatSpec builds a prompt and always appends the
// tool-name-independent EPSE tool-call format spec, regardless of whether a
// top-level `tools` array is present. It is used by the current-input-file
// continuation path: when the original request exposed its tool catalog inside
// a system message (no `tools` array), the catalog is uploaded as HISTORY.txt
// context while the live continuation prompt still needs the format spec so the
// model keeps emitting tool calls in the <|EPSE|tool_calls> format.
//
// The optional toolMarker is the caller-specific marker, threaded through so
// history that carries the marker form is still recognized as tool markup. The
// returned prompt stays in EPSE form; the caller re-applies the marker.
func BuildOpenAIPromptWithForcedFormatSpec(messagesRaw []any, traceID string, toolPolicy ToolChoicePolicy, thinkingEnabled bool, reminder ToolReminderConfig, toolMarker ...string) (string, []string) {
	messages := NormalizeOpenAIMessagesForPrompt(messagesRaw, traceID, toolMarker...)
	messages, toolNames := injectToolCallFormatSpecOnly(messages, toolPolicy, reminder)
	return prompt.MessagesPrepareWithThinking(messages, thinkingEnabled), toolNames
}

func buildOpenAIPrompt(messagesRaw []any, toolsRaw any, traceID string, toolPolicy ToolChoicePolicy, thinkingEnabled bool, includeToolDescriptions bool, reminder ToolReminderConfig, toolMarker ...string) (string, []string) {
	readGuard := ReadToolResultNeedsCacheGuard(messagesRaw)
	return buildOpenAIPromptWithReadGuard(messagesRaw, toolsRaw, traceID, toolPolicy, thinkingEnabled, includeToolDescriptions, readGuard, reminder, toolMarker...)
}

func buildOpenAIPromptWithReadGuard(messagesRaw []any, toolsRaw any, traceID string, toolPolicy ToolChoicePolicy, thinkingEnabled bool, includeToolDescriptions bool, includeReadCacheGuard bool, reminder ToolReminderConfig, toolMarker ...string) (string, []string) {
	messages := NormalizeOpenAIMessagesForPrompt(messagesRaw, traceID, toolMarker...)
	toolNames := []string{}
	if tools, ok := toolsRaw.([]any); ok && len(tools) > 0 {
		if includeToolDescriptions {
			messages, toolNames = injectToolPrompt(messages, tools, toolPolicy, includeReadCacheGuard, reminder)
		} else {
			messages, toolNames = injectToolPromptInstructionsOnly(messages, tools, toolPolicy, includeReadCacheGuard, reminder)
		}
	} else if !toolPolicy.IsNone() && MessagesDeclareToolsInSystemText(messagesRaw) {
		// The caller exposed its tool catalog inside a system message rather
		// than a top-level `tools` array (e.g. the RikkaHub workspace app), so
		// there is no schema to extract. Inject the tool-name-independent
		// EPSE format spec so the model still knows how to emit tool calls.
		// A force-disabled request resolves to ToolChoiceNone and never reaches
		// this branch, so its system text cannot pull tool instructions back in.
		messages, toolNames = injectToolCallFormatSpecOnly(messages, toolPolicy, reminder)
	}
	return prompt.MessagesPrepareWithThinking(messages, thinkingEnabled), toolNames
}

// BuildOpenAIPromptForAdapter exposes the OpenAI-compatible prompt building flow so
// other protocol adapters (for example Gemini) can reuse the same tool/history
// normalization logic and remain behavior-compatible with chat/completions.
func BuildOpenAIPromptForAdapter(messagesRaw []any, toolsRaw any, traceID string, thinkingEnabled bool, reminder ToolReminderConfig, toolMarker ...string) (string, []string) {
	return buildOpenAIFinalPrompt(messagesRaw, toolsRaw, traceID, thinkingEnabled, reminder, toolMarker...)
}
