package history

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"ds2api/internal/auth"
	"ds2api/internal/config"
	dsclient "ds2api/internal/deepseek/client"
	"ds2api/internal/promptcompat"
	"ds2api/internal/toolcall"
)

const (
	currentInputFilename    = promptcompat.CurrentInputContextFilename
	currentToolsFilename    = promptcompat.CurrentToolsContextFilename
	currentInputContentType = "text/plain; charset=utf-8"
	currentInputPurpose     = "assistants"

	currentInputContinuationInstruction = "Continue from the latest state in the attached HISTORY.txt context. Treat it as the current working state and answer the latest user request directly.Do not mention HISTORY.txt in the main text."
)

type CurrentInputConfigReader interface {
	CurrentInputFileEnabled() bool
	CurrentInputFileMinChars() int
}

type CurrentInputUploader interface {
	UploadFile(ctx context.Context, a *auth.RequestAuth, req dsclient.UploadFileRequest, maxAttempts int) (*dsclient.UploadFileResult, error)
}

type Service struct {
	Store CurrentInputConfigReader
	DS    CurrentInputUploader
}

func (s Service) ApplyCurrentInputFile(ctx context.Context, a *auth.RequestAuth, stdReq promptcompat.StandardRequest) (promptcompat.StandardRequest, error) {
	// 经过最新实测，deepseek网页端不再限制单次输入长度。此功能已不再需要。因此默认关闭。
	if stdReq.CurrentInputFileApplied || s.DS == nil || s.Store == nil || a == nil || !s.Store.CurrentInputFileEnabled() {
		return stdReq, nil
	}
	if modelType, ok := config.GetModelType(stdReq.ResolvedModel); ok && modelType == "expert" {
		return stdReq, nil
	}
	if stdReq.ToolMarker == "" && a != nil && a.ToolMarker != "" {
		stdReq.ToolMarker = a.ToolMarker
	}
	threshold := s.Store.CurrentInputFileMinChars()
	// The threshold is measured against the whole finalized prompt (all role
	// markers plus tool instructions), matching the rune-count semantics used
	// by the retired prompt segmentation path. Counting only the latest user
	// turn ignored the bulk of the context, so long multi-turn requests never
	// triggered the split. An empty prompt means there is nothing to split.
	if len([]rune(stdReq.FinalPrompt)) < threshold {
		return stdReq, nil
	}
	// When the newest transcript-visible turn is a user turn, keep it inline in
	// the live prompt and upload only the preceding turns as HISTORY.txt, so the
	// model sees `[System]:继续会话[User]:...[Assistant]:` instead of having to
	// dig the actual request out of the attached file. SplitTrailingUserTurn
	// declines for single-turn requests (nothing would be left to upload), which
	// keeps long first-turn inputs split out of the live prompt as before.
	readGuard := promptcompat.ReadToolResultNeedsCacheGuard(stdReq.Messages)
	historyMessages := stdReq.Messages
	trailingUserText := ""
	if leading, text, ok := promptcompat.SplitTrailingUserTurn(stdReq.Messages); ok {
		historyMessages = leading
		trailingUserText = text
	}
	fileText := promptcompat.BuildOpenAICurrentInputContextTranscript(historyMessages, stdReq.ToolMarker)
	if strings.TrimSpace(fileText) == "" {
		return stdReq, errors.New("current user input file produced empty transcript")
	}
	// Detect the "tools declared in system text" shape (no top-level `tools`
	// array) before the original messages are replaced by the continuation
	// user message. The tool catalog itself moves into HISTORY.txt, but the
	// live continuation prompt still needs the EPSE format spec so the model
	// keeps emitting tool calls in the expected format.
	systemDeclaredTools := false
	if !stdReq.ToolsDisabled {
		if tools, ok := stdReq.ToolsRaw.([]any); !ok || len(tools) == 0 {
			systemDeclaredTools = promptcompat.MessagesDeclareToolsInSystemText(stdReq.Messages)
		}
	}
	fileText = injectCurrentInputFileContinuation(fileText)
	toolsText, _ := promptcompat.BuildOpenAIToolsContextTranscript(stdReq.ToolsRaw, stdReq.ToolChoice)
	modelType := "default"
	if resolvedType, ok := config.GetModelType(stdReq.ResolvedModel); ok {
		modelType = resolvedType
	}
	// The uploaded context is read by the upstream model, so it must carry
	// the same caller-specific tool marker as the live prompt; otherwise a
	// single request would show the upstream both the shared EPSE keyword
	// (in HISTORY.txt) and the per-key marker (in the live prompt).
	// stdReq.HistoryText stores this rendered text so local archives and WebUI
	// history stay synchronized with the actual uploaded payload.
	fileText = toolcall.ApplyToolMarker(fileText, stdReq.ToolMarker)
	if strings.TrimSpace(toolsText) != "" {
		toolsText = toolcall.ApplyToolMarker(toolsText, stdReq.ToolMarker)
	}
	result, err := s.DS.UploadFile(ctx, a, dsclient.UploadFileRequest{
		Filename:    currentInputFilename,
		ContentType: currentInputContentType,
		Purpose:     currentInputPurpose,
		ModelType:   modelType,
		Data:        []byte(fileText),
	}, 3)
	if err != nil {
		return stdReq, fmt.Errorf("upload current user input file: %w", err)
	}
	fileID := strings.TrimSpace(result.ID)
	if fileID == "" {
		return stdReq, errors.New("upload current user input file returned empty file id")
	}

	toolFileID := ""
	if strings.TrimSpace(toolsText) != "" {
		result, err := s.DS.UploadFile(ctx, a, dsclient.UploadFileRequest{
			Filename:    currentToolsFilename,
			ContentType: currentInputContentType,
			Purpose:     currentInputPurpose,
			ModelType:   modelType,
			Data:        []byte(toolsText),
		}, 3)
		if err != nil {
			return stdReq, fmt.Errorf("upload current tools file: %w", err)
		}
		toolFileID = strings.TrimSpace(result.ID)
		if toolFileID == "" {
			return stdReq, errors.New("upload current tools file returned empty file id")
		}
	}

	// The continuation line is injected as a system turn (rendered as
	// `[System]:继续会话`), not as a fake user turn: attributing a synthetic
	// placeholder to the user would misrepresent the request. When the newest
	// turn was a real user turn it follows as an actual user message, so the
	// prompt reads `[System]:继续会话[User]:...[Assistant]:`. Tool descriptions
	// are merged into the leading system turn; the forced format spec keeps its
	// own contract of landing immediately before `[Assistant]:`.
	messages := []any{
		map[string]any{
			"role":    "system",
			"content": currentInputFilePrompt(toolFileID != ""),
		},
	}
	if trailingUserText != "" {
		messages = append(messages, map[string]any{
			"role":    "user",
			"content": trailingUserText,
		})
	}

	stdReq.Messages = messages
	stdReq.HistoryText = fileText
	stdReq.CurrentInputFileApplied = true
	stdReq.CurrentInputFileID = fileID
	stdReq.CurrentToolsFileID = toolFileID
	stdReq.RefFileIDs = prependUniqueRefFileIDs(stdReq.RefFileIDs, fileID, toolFileID)
	if systemDeclaredTools {
		stdReq.FinalPrompt, stdReq.ToolNames = promptcompat.BuildOpenAIPromptWithForcedFormatSpec(messages, "", stdReq.ToolChoice, stdReq.Thinking, stdReq.ToolReminderConfig(), stdReq.ToolMarker)
	} else {
		stdReq.FinalPrompt, stdReq.ToolNames = promptcompat.BuildOpenAIPromptWithToolInstructionsOnlyWithReadGuard(messages, stdReq.ToolsRaw, "", stdReq.ToolChoice, stdReq.Thinking, readGuard, stdReq.ToolReminderConfig(), stdReq.ToolMarker)
	}
	// Both rebuild branches re-render the tool-call format spec from the
	// canonical EPSE keyword, so the caller-specific marker must be re-applied
	// to the live prompt.
	stdReq.FinalPrompt = toolcall.ApplyToolMarker(stdReq.FinalPrompt, stdReq.ToolMarker)
	// Token accounting must reflect the actual downstream context:
	// uploaded context files + the continuation live prompt.
	tokenParts := []string{fileText}
	if strings.TrimSpace(toolsText) != "" {
		tokenParts = append(tokenParts, toolsText)
	}
	tokenParts = append(tokenParts, stdReq.FinalPrompt)
	stdReq.PromptTokenText = strings.Join(tokenParts, "\n")
	return stdReq, nil
}

func (s Service) ReuploadAppliedCurrentInputFile(ctx context.Context, a *auth.RequestAuth, stdReq promptcompat.StandardRequest) (promptcompat.StandardRequest, error) {
	if !stdReq.CurrentInputFileApplied || s.DS == nil || a == nil {
		return stdReq, nil
	}
	if stdReq.ToolMarker == "" && a.ToolMarker != "" {
		stdReq.ToolMarker = a.ToolMarker
	}
	fileText := strings.TrimSpace(stdReq.HistoryText)
	if fileText == "" {
		return stdReq, nil
	}
	modelType := "default"
	if resolvedType, ok := config.GetModelType(stdReq.ResolvedModel); ok {
		modelType = resolvedType
	}
	result, err := s.DS.UploadFile(ctx, a, dsclient.UploadFileRequest{
		Filename:    currentInputFilename,
		ContentType: currentInputContentType,
		Purpose:     currentInputPurpose,
		ModelType:   modelType,
		// Re-upload for an account switch: HistoryText already carries the
		// caller-specific marker, but ApplyToolMarker is idempotent and also
		// guards any legacy canonical EPSE form.
		Data: []byte(toolcall.ApplyToolMarker(stdReq.HistoryText, stdReq.ToolMarker)),
	}, 3)
	if err != nil {
		return stdReq, fmt.Errorf("upload current user input file: %w", err)
	}
	fileID := strings.TrimSpace(result.ID)
	if fileID == "" {
		return stdReq, errors.New("upload current user input file returned empty file id")
	}

	toolsText, _ := promptcompat.BuildOpenAIToolsContextTranscript(stdReq.ToolsRaw, stdReq.ToolChoice)
	toolFileID := ""
	if strings.TrimSpace(toolsText) != "" {
		result, err := s.DS.UploadFile(ctx, a, dsclient.UploadFileRequest{
			Filename:    currentToolsFilename,
			ContentType: currentInputContentType,
			Purpose:     currentInputPurpose,
			ModelType:   modelType,
			Data:        []byte(toolcall.ApplyToolMarker(toolsText, stdReq.ToolMarker)),
		}, 3)
		if err != nil {
			return stdReq, fmt.Errorf("upload current tools file: %w", err)
		}
		toolFileID = strings.TrimSpace(result.ID)
		if toolFileID == "" {
			return stdReq, errors.New("upload current tools file returned empty file id")
		}
	}

	stdReq.RefFileIDs = replaceGeneratedCurrentInputRefs(stdReq.RefFileIDs, stdReq.CurrentInputFileID, stdReq.CurrentToolsFileID, fileID, toolFileID)
	stdReq.CurrentInputFileID = fileID
	stdReq.CurrentToolsFileID = toolFileID
	return stdReq, nil
}

func injectCurrentInputFileContinuation(transcript string) string {
	trimmed := strings.TrimSpace(transcript)
	const summaryMarker = "Prior conversation history and tool progress."
	if idx := strings.Index(trimmed, summaryMarker); idx >= 0 {
		after := idx + len(summaryMarker)
		return trimmed[:after] + "\n" + currentInputContinuationInstruction + trimmed[after:]
	}
	return trimmed + "\n\n" + currentInputContinuationInstruction
}

func currentInputFilePrompt(hasToolsFile bool) string {
	prompt := "继续会话"
	if hasToolsFile {
		prompt += " 使用工具时请参照说明与格式要求，仅使用所列出的工具"
	}
	return prompt
}

func prependUniqueRefFileIDs(existing []string, fileIDs ...string) []string {
	out := make([]string, 0, len(existing)+len(fileIDs))
	seen := map[string]struct{}{}
	for _, fileID := range fileIDs {
		trimmed := strings.TrimSpace(fileID)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		out = append(out, trimmed)
		seen[key] = struct{}{}
	}
	for _, id := range existing {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		out = append(out, trimmed)
		seen[key] = struct{}{}
	}
	return out
}

func replaceGeneratedCurrentInputRefs(existing []string, oldHistoryID, oldToolsID, newHistoryID, newToolsID string) []string {
	filtered := make([]string, 0, len(existing))
	old := map[string]struct{}{}
	for _, id := range []string{oldHistoryID, oldToolsID} {
		trimmed := strings.ToLower(strings.TrimSpace(id))
		if trimmed != "" {
			old[trimmed] = struct{}{}
		}
	}
	for _, id := range existing {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if _, ok := old[strings.ToLower(trimmed)]; ok {
			continue
		}
		filtered = append(filtered, trimmed)
	}
	return prependUniqueRefFileIDs(filtered, newHistoryID, newToolsID)
}
