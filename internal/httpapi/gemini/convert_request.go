package gemini

import (
	"fmt"
	"strings"

	"ds2api/internal/config"
	"ds2api/internal/promptcompat"
	"ds2api/internal/util"
)

//nolint:unused // kept for native Gemini adapter route compatibility.
func normalizeGeminiRequest(store ConfigReader, routeModel string, req map[string]any, stream bool) (promptcompat.StandardRequest, error) {
	requestedModel := strings.TrimSpace(routeModel)
	if requestedModel == "" {
		return promptcompat.StandardRequest{}, fmt.Errorf("model is required in request path")
	}

	resolvedModel, ok := config.ResolveModel(store, requestedModel)
	if !ok {
		return promptcompat.StandardRequest{}, fmt.Errorf("model %q is not available", requestedModel)
	}
	defaultThinkingEnabled, searchEnabled, _ := config.GetModelConfig(resolvedModel)
	thinkingEnabled := util.ResolveThinkingEnabled(req, defaultThinkingEnabled)
	if config.IsNoThinkingModel(resolvedModel) {
		thinkingEnabled = false
	}

	messagesRaw := geminiMessagesFromRequest(req)
	if len(messagesRaw) == 0 {
		return promptcompat.StandardRequest{}, fmt.Errorf("request must include non-empty contents")
	}

	toolsDisabled := promptcompat.ToolsForceDisabledInRequest(req)
	toolsRaw := convertGeminiTools(req["tools"])
	toolPolicy := promptcompat.DefaultToolChoicePolicy()
	if toolsDisabled {
		// API Key 上的「强制禁用工具调用」：请求侧工具定义已被清除，这里再
		// 把工具策略钉成 none，使工具提示词与 EPSE 格式规范都不注入。
		toolsRaw = nil
		toolPolicy = promptcompat.ToolChoicePolicy{Mode: promptcompat.ToolChoiceNone}
	}
	reminder := promptcompat.ResolveToolReminder(store)
	finalPrompt, toolNames := promptcompat.BuildOpenAIPrompt(messagesRaw, toolsRaw, "", toolPolicy, thinkingEnabled, reminder)
	if len(toolNames) == 0 && len(toolsRaw) > 0 {
		toolNames = []string{"__any_tool__"}
	}
	passThrough := collectGeminiPassThrough(req)

	return promptcompat.StandardRequest{
		Surface:              "google_gemini",
		RequestedModel:       requestedModel,
		ResolvedModel:        resolvedModel,
		ResponseModel:        requestedModel,
		Messages:             messagesRaw,
		PromptTokenText:      finalPrompt,
		ToolsRaw:             toolsRaw,
		FinalPrompt:          finalPrompt,
		ToolNames:            toolNames,
		ToolChoice:           toolPolicy,
		ToolsDisabled:        toolsDisabled,
		Stream:               stream,
		Thinking:             thinkingEnabled,
		Search:               searchEnabled,
		PassThrough:          passThrough,
		ToolReminderDisabled: !reminder.Enabled,
		ToolReminderPrompt:   reminder.Prompt,
	}, nil
}
