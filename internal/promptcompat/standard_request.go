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
	Stream                  bool
	Thinking                bool
	Search                  bool
	RefFileIDs              []string
	RefFileTokens           int
	PassThrough             map[string]any
	// FileTagSuffix 是在响应末尾追加的 <||file:name:email:id||> 标签文本。
	// 由 <||fileid:True||> 控制标签触发：当 force_upload 上传成功后，
	// chat handler 把新文件 ID 和实际账号 email 拼成标签存入此处，
	// 流式 / 非流式响应在收尾时将其追加到输出内容末尾。
	FileTagSuffix string
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
