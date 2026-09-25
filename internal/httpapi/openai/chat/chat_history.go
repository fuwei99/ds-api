package chat

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"ds2api/internal/auth"
	"ds2api/internal/chathistory"
	"ds2api/internal/config"
	openaifmt "ds2api/internal/format/openai"
	"ds2api/internal/prompt"
	"ds2api/internal/promptcompat"
	"ds2api/internal/usagestats"
)

type chatHistorySession struct {
	store       *chathistory.Store
	usage       *usagestats.Store
	entryID     string
	startedAt   time.Time
	lastPersist time.Time
	finalPrompt string
	recordModel string
	startParams chathistory.StartParams
	disabled    bool
}

func startChatHistory(store *chathistory.Store, r *http.Request, a *auth.RequestAuth, stdReq promptcompat.StandardRequest) *chatHistorySession {
	return startChatHistoryWithUsage(store, nil, r, a, stdReq)
}

func startChatHistoryWithUsage(store *chathistory.Store, usage *usagestats.Store, r *http.Request, a *auth.RequestAuth, stdReq promptcompat.StandardRequest) *chatHistorySession {
	if r == nil || a == nil || !shouldCaptureChatHistory(r) {
		return nil
	}
	startParams := chathistory.StartParams{
		CallerID:    strings.TrimSpace(a.CallerID),
		UserAgent:   strings.TrimSpace(a.UserAgent),
		AccountID:   strings.TrimSpace(a.AccountID),
		Surface:     "openai.chat_completions",
		Model:       strings.TrimSpace(stdReq.ResponseModel),
		Stream:      stdReq.Stream,
		UserInput:   extractSingleUserInput(stdReq.Messages),
		Messages:    extractAllMessages(stdReq.Messages),
		HistoryText: stdReq.HistoryText,
		FinalPrompt: stdReq.FinalPrompt,
	}
	session := &chatHistorySession{
		store:       store,
		usage:       usage,
		startedAt:   time.Now(),
		lastPersist: time.Now(),
		finalPrompt: stdReq.FinalPrompt,
		recordModel: recordModelForUsage(stdReq),
		startParams: startParams,
	}
	historyEnabled := store != nil && store.Enabled()
	if !historyEnabled {
		if usage == nil {
			return nil
		}
		return session
	}
	entry, err := store.Start(startParams)
	if entry.ID != "" {
		session.entryID = entry.ID
	}
	if err != nil {
		if entry.ID == "" {
			config.Logger.Warn("[chat_history] start failed", "error", err)
			session.store = nil
			if session.usage == nil {
				return nil
			}
			return session
		}
		config.Logger.Warn("[chat_history] start persisted in memory after write failure", "error", err)
	}
	return session
}

func recordModelForUsage(stdReq promptcompat.StandardRequest) string {
	model := strings.TrimSpace(stdReq.ResolvedModel)
	if model == "" {
		model = strings.TrimSpace(stdReq.ResponseModel)
	}
	if model == "" {
		return "unknown"
	}
	return model
}

func shouldCaptureChatHistory(r *http.Request) bool {
	if r == nil {
		return false
	}
	// prepare 是无正文的内部预请求，不采集；release 需要放行——Vercel
	// Node 流式路径在 release 时回填聊天历史并记录 Token 用量。
	if isVercelStreamPrepareRequest(r) {
		return false
	}
	return true
}

func extractSingleUserInput(messages []any) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(asString(msg["role"])))
		if role != "user" {
			continue
		}
		if normalized := strings.TrimSpace(prompt.NormalizeContent(msg["content"])); normalized != "" {
			return normalized
		}
	}
	// The current-input-file continuation path replaces every turn with a
	// single system-role placeholder, so there is no user turn left to label
	// the archived entry. Fall back to the last non-empty turn instead of
	// persisting an empty title.
	return lastNonEmptyMessageContent(messages)
}

func lastNonEmptyMessageContent(messages []any) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}
		if normalized := strings.TrimSpace(prompt.NormalizeContent(msg["content"])); normalized != "" {
			return normalized
		}
	}
	return ""
}

func extractAllMessages(messages []any) []chathistory.Message {
	out := make([]chathistory.Message, 0, len(messages))
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(asString(msg["role"])))
		content := strings.TrimSpace(prompt.NormalizeContent(msg["content"]))
		if role == "" || content == "" {
			continue
		}
		out = append(out, chathistory.Message{
			Role:    role,
			Content: content,
		})
	}
	return out
}

func (s *chatHistorySession) progress(thinking, content string) {
	if s == nil || s.store == nil || s.disabled {
		return
	}
	now := time.Now()
	if now.Sub(s.lastPersist) < 250*time.Millisecond {
		return
	}
	s.lastPersist = now
	s.persistUpdate(chathistory.UpdateParams{
		Status:           "streaming",
		ReasoningContent: thinking,
		Content:          content,
		StatusCode:       http.StatusOK,
		ElapsedMs:        time.Since(s.startedAt).Milliseconds(),
	})
}

func (s *chatHistorySession) success(statusCode int, thinking, content, finishReason string, usage map[string]any) {
	if s == nil {
		return
	}
	s.recordUsage(usage)
	if s.store == nil || s.disabled {
		return
	}
	s.persistUpdate(chathistory.UpdateParams{
		Status:           "success",
		ReasoningContent: thinking,
		Content:          content,
		StatusCode:       statusCode,
		ElapsedMs:        time.Since(s.startedAt).Milliseconds(),
		FinishReason:     finishReason,
		Usage:            usage,
		Completed:        true,
	})
}

func (s *chatHistorySession) error(statusCode int, message, finishReason, thinking, content string) {
	if s == nil || s.store == nil || s.disabled {
		return
	}
	s.persistUpdate(chathistory.UpdateParams{
		Status:           "error",
		ReasoningContent: thinking,
		Content:          content,
		Error:            message,
		StatusCode:       statusCode,
		ElapsedMs:        time.Since(s.startedAt).Milliseconds(),
		FinishReason:     finishReason,
		Completed:        true,
	})
}

func (s *chatHistorySession) stopped(thinking, content, finishReason string) {
	if s == nil {
		return
	}
	usage := openaifmt.BuildChatUsage(s.finalPrompt, thinking, content)
	s.recordUsage(usage)
	if s.store == nil || s.disabled {
		return
	}
	s.persistUpdate(chathistory.UpdateParams{
		Status:           "stopped",
		ReasoningContent: thinking,
		Content:          content,
		StatusCode:       http.StatusOK,
		ElapsedMs:        time.Since(s.startedAt).Milliseconds(),
		FinishReason:     finishReason,
		Usage:            usage,
		Completed:        true,
	})
}

// recordUsage 把一次请求的 Token 用量计入全局账本。即使聊天历史关闭，
// 只要注入了 usage store 就会生效。
func (s *chatHistorySession) recordUsage(usage map[string]any) {
	if s == nil || s.usage == nil {
		return
	}
	s.usage.Record(s.recordModel, s.startParams.CallerID, usage, s.startedAt)
}

func (s *chatHistorySession) discard() {
	if s == nil || s.store == nil || s.disabled {
		return
	}
	if err := s.store.Delete(s.entryID); err != nil {
		if !isChatHistoryMissingError(err) && !errors.Is(err, chathistory.ErrDisabled) {
			config.Logger.Warn("[chat_history] discard failed", "error", err)
		}
	}
}

func historyTextForArchive(raw, visible string) string {
	if strings.TrimSpace(raw) != "" {
		return raw
	}
	return visible
}

func historyThinkingForArchive(raw, detection, visible string) string {
	if strings.TrimSpace(raw) != "" {
		return raw
	}
	if strings.TrimSpace(detection) != "" {
		return detection
	}
	return visible
}

func (s *chatHistorySession) retryMissingEntry() bool {
	if s == nil || s.store == nil || s.disabled {
		return false
	}
	entry, err := s.store.Start(s.startParams)
	if errors.Is(err, chathistory.ErrDisabled) {
		s.disabled = true
		return false
	}
	if entry.ID == "" {
		if err != nil {
			config.Logger.Warn("[chat_history] recreate missing entry failed", "error", err)
		}
		return false
	}
	s.entryID = entry.ID
	if err != nil {
		config.Logger.Warn("[chat_history] recreate missing entry persisted in memory after write failure", "error", err)
	}
	return true
}

func (s *chatHistorySession) persistUpdate(params chathistory.UpdateParams) {
	if s == nil || s.store == nil || s.disabled {
		return
	}
	if _, err := s.store.Update(s.entryID, params); err != nil {
		s.handlePersistError(params, err)
	}
}

func (s *chatHistorySession) handlePersistError(params chathistory.UpdateParams, err error) {
	if err == nil || s == nil {
		return
	}
	if errors.Is(err, chathistory.ErrDisabled) {
		s.disabled = true
		return
	}
	if isChatHistoryMissingError(err) {
		if s.retryMissingEntry() {
			if _, retryErr := s.store.Update(s.entryID, params); retryErr != nil {
				if errors.Is(retryErr, chathistory.ErrDisabled) || isChatHistoryMissingError(retryErr) {
					s.disabled = true
					return
				}
				config.Logger.Warn("[chat_history] retry after missing entry failed", "error", retryErr)
			}
			return
		}
		s.disabled = true
		return
	}
	config.Logger.Warn("[chat_history] update failed", "error", err)
}

func isChatHistoryMissingError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "not found")
}
