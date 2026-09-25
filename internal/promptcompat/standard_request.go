package promptcompat

import "ds2api/internal/config"

type StandardRequest struct {
	Surface                 string
	RequestedModel          string
	ResolvedModel           string
	ResponseModel           string
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
	Stream        bool
	Thinking      bool
	Search        bool
	RefFileIDs    []string
	RefFileTokens int
	PassThrough   map[string]any
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
