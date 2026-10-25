package promptcompat

import "ds2api/internal/config"

type StandardRequest struct {
	Surface        string
	RequestedModel string
	ResolvedModel  string
	ResponseModel  string
	// Messages 是本次请求渲染 FinalPrompt 所依据的标准消息列表。每个出口在标准化
	// 之后都必须把「prompt 真正由之渲染的那一份」放在这里，而不是协议原样、也不是
	// 少了某个协议专属字段的中间形态：凡是改写 Messages 后重建 prompt 的共享步骤
	// （思考注入、current input file 续写）都按这份列表重放整份输入，出口如果存的
	// 是另一份列表，重建出来的 prompt 就会静默丢掉差额——Anthropic 的顶层 `system`
	// 曾经就是这样丢掉的。
	//
	// 它同时是历史归档与 HISTORY.txt 转录的输入（internal/responsehistory、
	// internal/promptcompat/history_transcript.go），因此这条不变量也决定
	// 「本地记录 / 上传上下文」与「实际发给上游的 prompt」是否一致。
	Messages                []any
	HistoryText             string
	PromptTokenText         string
	CurrentInputFileApplied bool
	CurrentInputFileID      string
	CurrentToolsFileID      string
	ToolsRaw                any
	FinalPrompt             string
	ToolNames               []string
	ToolChoice              ToolChoicePolicy
	// ToolsDisabled 表示本次请求命中了 API Key 上的「强制禁用工具调用」开关。
	// 为 true 时 ToolsRaw 必为 nil、ToolNames 为空、ToolChoice 为 ToolChoiceNone，
	// 且工具提示词 / 格式规范 / TOOLS.txt 都不会注入或上传。
	ToolsDisabled bool
	// SystemTextToolsDisabled 表示本出口的请求形态无法在 system 文本里声明工具：
	// 工具目录只能来自顶层 `tools` 数组。Anthropic Messages 就是这种出口——它的
	// `system` 是独立字段，原始 body 的 `messages` 里不会出现 system 轮，于是路由
	// 判定（promptcompat.RequestBodyHasTools，扫原始 body）必然把「没有 tools 数组」
	// 的请求判为无工具，而共享组装看的是转换后的消息（system 已变成一条 system 轮），
	// 两者会给出相反结论。
	//
	// 为 true 时共享组装跳过「system 文本声明工具」分支，否则 system 文本里恰好出现
	// "tool" 字样就会注入 EPSE 格式规范：prompt 要求模型输出工具调用，而该出口因为
	// ToolNames 为空不会筛分工具块，模型照做的输出会漏进可见正文。
	//
	// 零值 false 即历史行为（该分支适用），因此只有明确声明「工具只能来自顶层数组」
	// 的出口需要置 true。
	SystemTextToolsDisabled bool
	Stream                  bool
	Thinking                bool
	Search                  bool
	RefFileIDs              []string
	RefFileTokens           int
	PassThrough             map[string]any
	// ToolMarker 是本次调用者专属的工具调用标识。提示词构建完成后用它把
	// 规范里的 EPSE 渲染成随机标识；下游重建提示词时沿用同一值。
	ToolMarker string
	// ToolReminderDisabled 表示本次请求关闭了「底部格式提示注入」。
	// ToolReminderPrompt 是本次请求的自定义提醒文本，为空表示使用内置提醒。
	// 两个字段都取零值时即历史默认行为（注入内置提醒），因此手工构造的
	// StandardRequest 不会因为漏填而悄悄丢掉末尾复述。
	ToolReminderDisabled bool
	ToolReminderPrompt   string
}

// BuildPromptFromMessages 用改写后的消息列表重建 FinalPrompt，沿用本请求的工具策略、
// 底部格式提示设置与调用者标识。凡是「改写 Messages 后重建 FinalPrompt」的路径都应该
// 走这里：出口差异（例如「system 文本声明工具」分支是否适用）记录在请求上，换成通用
// 构造器重建会让出口已经关掉的分支悄悄复活。
func (r StandardRequest) BuildPromptFromMessages(messages []any) (string, []string) {
	if r.SystemTextToolsDisabled {
		return BuildOpenAIPromptWithoutSystemTextTools(messages, r.ToolsRaw, "", r.ToolChoice, r.Thinking, r.ToolReminderConfig(), r.ToolMarker)
	}
	return BuildOpenAIPrompt(messages, r.ToolsRaw, "", r.ToolChoice, r.Thinking, r.ToolReminderConfig(), r.ToolMarker)
}

// SystemTextToolsApply 报告本请求是否适用「system 文本声明工具」分支：出口允许该
// 形态、请求未被强制禁用工具、没有顶层 `tools` 数组，且某条 system 消息的文本命中了
// 工具关键词。prompt 组装与该分支的续写路径（current input file 的
// BuildOpenAIPromptWithForcedFormatSpec）共用这一个判定，两处不会各判一次而漂移。
func (r StandardRequest) SystemTextToolsApply() bool {
	if r.ToolsDisabled || r.SystemTextToolsDisabled {
		return false
	}
	if tools, ok := r.ToolsRaw.([]any); ok && len(tools) > 0 {
		return false
	}
	return MessagesDeclareToolsInSystemText(r.Messages)
}

// ToolReminderConfig 返回本次请求的「底部格式提示注入」设置，供在已有 prompt
// 之上重建 FinalPrompt 的路径沿用，避免重建把开关状态丢掉。
func (r StandardRequest) ToolReminderConfig() ToolReminderConfig {
	return ToolReminderConfig{Enabled: !r.ToolReminderDisabled, Prompt: r.ToolReminderPrompt}
}

type ToolChoiceMode string

const (
	ToolChoiceAuto     ToolChoiceMode = "auto"
	ToolChoiceNone     ToolChoiceMode = "none"
	ToolChoiceRequired ToolChoiceMode = "required"
	ToolChoiceForced   ToolChoiceMode = "forced"
)

type ToolChoicePolicy struct {
	Mode       ToolChoiceMode
	ForcedName string
	Allowed    map[string]struct{}
}

func DefaultToolChoicePolicy() ToolChoicePolicy {
	return ToolChoicePolicy{Mode: ToolChoiceAuto}
}

func (p ToolChoicePolicy) IsNone() bool {
	return p.Mode == ToolChoiceNone
}

func (p ToolChoicePolicy) IsRequired() bool {
	return p.Mode == ToolChoiceRequired || p.Mode == ToolChoiceForced
}

func (p ToolChoicePolicy) Allows(name string) bool {
	if len(p.Allowed) == 0 {
		return true
	}
	_, ok := p.Allowed[name]
	return ok
}

func (r StandardRequest) CompletionPayload(sessionID string) map[string]any {
	return r.CompletionPayloadWithParentAndPrompt(sessionID, 0, r.FinalPrompt)
}

func (r StandardRequest) CompletionPayloadWithParentAndPrompt(sessionID string, parentMessageID int, prompt string) map[string]any {
	modelID := r.ResolvedModel
	if modelID == "" {
		modelID = r.RequestedModel
	}
	modelType := "default"
	if resolvedType, ok := config.GetModelType(modelID); ok {
		modelType = resolvedType
	}
	refFileIDs := make([]any, 0, len(r.RefFileIDs))
	if modelType != "expert" {
		for _, fileID := range r.RefFileIDs {
			if fileID == "" {
				continue
			}
			refFileIDs = append(refFileIDs, fileID)
		}
	}
	var parent any
	if parentMessageID > 0 {
		parent = parentMessageID
	}
	payload := map[string]any{
		"chat_session_id":   sessionID,
		"parent_message_id": parent,
		"model_type":        modelType,
		"prompt":            prompt,
		"ref_file_ids":      refFileIDs,
		"thinking_enabled":  r.Thinking,
		"search_enabled":    r.Search,
		"action":            nil,
		"preempt":           false,
	}
	for k, v := range r.PassThrough {
		payload[k] = v
	}
	return payload
}
