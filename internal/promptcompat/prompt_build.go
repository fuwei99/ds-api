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

// BuildOpenAIPromptWithoutSystemTextTools builds a prompt for a protocol surface
// whose tool catalog can only arrive as a top-level `tools` array: the
// system-text branch (see MessagesDeclareToolsInSystemText) is skipped, so a
// system message that merely mentions "tool" cannot pull the EPSE format spec
// in.
//
// The branch exists for clients that expose their tool catalog inside a system
// message instead of a `tools` array, and it is only sound when the routing
// decision sees the same thing: RequestBodyHasTools scans the *raw* body, so a
// surface whose system prompt is a top-level field (Anthropic Messages) is
// classified as a no-tools request while the converted messages the builder
// renders do carry a system turn. Left un-scoped the two disagree: the prompt
// demands tool calls while the exit, whose ToolNames stays empty, does not
// sieve them, so an EPSE block emitted in obedience to that spec can surface as
// visible text. A force-disabled request resolves to ToolChoiceNone and never
// reaches either branch.
func BuildOpenAIPromptWithoutSystemTextTools(messagesRaw []any, toolsRaw any, traceID string, toolPolicy ToolChoicePolicy, thinkingEnabled bool, reminder ToolReminderConfig, toolMarker ...string) (string, []string) {
	readGuard := ReadToolResultNeedsCacheGuard(messagesRaw)
	return buildOpenAIPromptWithReadGuard(messagesRaw, toolsRaw, traceID, toolPolicy, thinkingEnabled, true, readGuard, false, reminder, toolMarker...)
}

func BuildOpenAIPromptWithToolInstructionsOnly(messagesRaw []any, toolsRaw any, traceID string, toolPolicy ToolChoicePolicy, thinkingEnabled bool, reminder ToolReminderConfig, toolMarker ...string) (string, []string) {
	return buildOpenAIPrompt(messagesRaw, toolsRaw, traceID, toolPolicy, thinkingEnabled, false, reminder, toolMarker...)
}

func BuildOpenAIPromptWithToolInstructionsOnlyWithReadGuard(messagesRaw []any, toolsRaw any, traceID string, toolPolicy ToolChoicePolicy, thinkingEnabled bool, includeReadCacheGuard bool, reminder ToolReminderConfig, toolMarker ...string) (string, []string) {
	return buildOpenAIPromptWithReadGuard(messagesRaw, toolsRaw, traceID, toolPolicy, thinkingEnabled, false, includeReadCacheGuard, true, reminder, toolMarker...)
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
	return buildOpenAIPromptWithReadGuard(messagesRaw, toolsRaw, traceID, toolPolicy, thinkingEnabled, includeToolDescriptions, readGuard, true, reminder, toolMarker...)
}

// buildOpenAIPromptWithReadGuard assembles the prompt. allowSystemTextTools
// selects whether a tool catalog declared in system text may be inferred (see
// BuildOpenAIPromptWithoutSystemTextTools for the surface that must not).
func buildOpenAIPromptWithReadGuard(messagesRaw []any, toolsRaw any, traceID string, toolPolicy ToolChoicePolicy, thinkingEnabled bool, includeToolDescriptions bool, includeReadCacheGuard bool, allowSystemTextTools bool, reminder ToolReminderConfig, toolMarker ...string) (string, []string) {
	messages := NormalizeOpenAIMessagesForPrompt(messagesRaw, traceID, toolMarker...)
	toolNames := []string{}
	if tools, ok := toolsRaw.([]any); ok && len(tools) > 0 {
		if includeToolDescriptions {
			messages, toolNames = injectToolPrompt(messages, tools, toolPolicy, includeReadCacheGuard, reminder)
		} else {
			messages, toolNames = injectToolPromptInstructionsOnly(messages, tools, toolPolicy, includeReadCacheGuard, reminder)
		}
	} else if allowSystemTextTools && !toolPolicy.IsNone() && MessagesDeclareToolsInSystemText(messagesRaw) {
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

// RenderPromptText renders standard messages into prompt text without injecting
// anything: it is the prompt-visible form of a message list, which callers need
// for input-token accounting. It exists so the rendering of standard messages
// stays in one place — a surface that renders messages itself would drift from
// what the model is actually sent.
func RenderPromptText(messages []any, thinkingEnabled bool) string {
	return prompt.MessagesPrepareWithThinking(NormalizeOpenAIMessagesForPrompt(messages, ""), thinkingEnabled)
}
