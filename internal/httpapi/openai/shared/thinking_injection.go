package shared

import (
	"ds2api/internal/promptcompat"
	"ds2api/internal/toolcall"
)

func ApplyThinkingInjection(store ConfigReader, stdReq promptcompat.StandardRequest) promptcompat.StandardRequest {
	if store == nil || !store.ThinkingInjectionEnabled() || !stdReq.Thinking {
		return stdReq
	}
	messages, changed := promptcompat.AppendThinkingInjectionPromptToLatestUser(stdReq.Messages, store.ThinkingInjectionPrompt())
	if !changed {
		return stdReq
	}
	finalPrompt, toolNames := promptcompat.BuildOpenAIPrompt(messages, stdReq.ToolsRaw, "", stdReq.ToolChoice, stdReq.Thinking, stdReq.ToolReminderConfig(), stdReq.ToolMarker)
	if len(toolNames) == 0 && len(stdReq.ToolNames) > 0 {
		toolNames = stdReq.ToolNames
	}
	stdReq.Messages = messages
	// The prompt is rebuilt from the messages, so the renderers emit the
	// canonical EPSE keyword again: the caller-specific marker has to be
	// re-applied here or this rebuild would silently drop it (the same
	// invariant the request normalize path enforces).
	stdReq.FinalPrompt = toolcall.ApplyToolMarker(finalPrompt, stdReq.ToolMarker)
	stdReq.ToolNames = toolNames
	return stdReq
}
