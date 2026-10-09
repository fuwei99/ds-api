package claude

import (
	"fmt"
	"strings"

	"ds2api/internal/config"
	"ds2api/internal/promptcompat"
	"ds2api/internal/util"
)

// claudeNormalizedRequest is the Claude adapter's translation result: the
// standard request every shared step consumes. Claude contributes the message
// translation (Anthropic content blocks to standard messages); prompt assembly
// stays in promptcompat.
type claudeNormalizedRequest struct {
	Standard promptcompat.StandardRequest
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
	// 适配层只做协议翻译：把 Anthropic 的 content blocks 摊成标准消息
	// （content 纯文本 / reasoning_content / tool_calls），工具提示词、EPSE 格式
	// 规范、底部格式提示与最终 prompt 全部由 promptcompat 的共享组装产出。
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
	dsMessages, _ := dsPayload["messages"].([]any)
	// Claude 的工具目录只能来自顶层 `tools` 数组：Anthropic 的 `system` 是独立
	// 字段，原始 body 的 `messages` 里不会出现 system 轮，因此路由判定
	// （promptcompat.RequestBodyHasTools 扫原始 body）把「没有 tools 数组」的请求
	// 判为无工具请求。共享组装看的是转换后的 dsMessages（顶层 system 已变成一条
	// system 轮），必须显式声明该形态，否则 system 文本里恰好出现 "tool" 字样就会
	// 注入 EPSE 格式规范：prompt 要求模型输出工具调用，而这里 ToolNames 为空，
	// Claude 流式运行时的工具筛分是关闭的，模型照做的输出会漏进可见正文。
	finalPrompt, toolNames := promptcompat.BuildOpenAIPromptWithoutSystemTextTools(dsMessages, toolsRequested, "", toolPolicy, thinkingEnabled, reminder)
	if len(toolNames) == 0 && len(toolsRequested) > 0 {
		toolNames = []string{"__any_tool__"}
	}

	return claudeNormalizedRequest{
		Standard: promptcompat.StandardRequest{
			Surface:        "anthropic_messages",
			RequestedModel: strings.TrimSpace(model),
			ResolvedModel:  dsModel,
			ResponseModel:  strings.TrimSpace(model),
			// 不变量：Messages 就是 FinalPrompt 渲染所依据的那份列表。dsMessages 是
			// normalizedMessages 加上由顶层 `system` 翻译出的 system 轮——只存
			// normalizedMessages 会让共享重建步骤（思考注入、current input file
			// 续写）静默丢掉调用方的 system prompt。
			Messages:                dsMessages,
			PromptTokenText:         finalPrompt,
			ToolsRaw:                toolsRequested,
			FinalPrompt:             finalPrompt,
			ToolNames:               toolNames,
			ToolChoice:              toolPolicy,
			ToolsDisabled:           toolsDisabled,
			SystemTextToolsDisabled: true,
			Stream:                  util.ToBool(req["stream"]),
			Thinking:                thinkingEnabled,
			Search:                  searchEnabled,
			ToolReminderDisabled:    !reminder.Enabled,
			ToolReminderPrompt:      reminder.Prompt,
		},
	}, nil
}
