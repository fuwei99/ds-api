package completionruntime

import (
	"strings"
	"time"

	"ds2api/internal/auth"
	"ds2api/internal/chathistory"
	"ds2api/internal/config"
)

// repairHistoryRecord tracks the response-history entry opened for a single
// proactive tool-call repair sub-request. The repair pass fires its own DeepSeek
// completion on a throwaway session, so it needs its own record: it is not part
// of the main request's turn and would otherwise leave no trace in 响应记录.
//
// It is intentionally a thin wrapper over chathistory.Store rather than
// responsehistory.Session: the repair invoker has no *http.Request, and the
// record is opened and finalized within one function call, so the session-level
// progress/missing-entry recovery machinery is not needed.
type repairHistoryRecord struct {
	store     *chathistory.Store
	entryID   string
	startedAt time.Time
}

// startRepairHistory opens a "toolcall.repair" record for one repair
// completion. It returns nil when history is unavailable or disabled, so all
// call sites can treat recording as best effort.
func startRepairHistory(store *chathistory.Store, a *auth.RequestAuth, prompt string) *repairHistoryRecord {
	if store == nil || a == nil || !store.Enabled() {
		return nil
	}
	entry, err := store.Start(chathistory.StartParams{
		CallerID:  strings.TrimSpace(a.CallerID),
		UserAgent: strings.TrimSpace(a.UserAgent),
		AccountID: strings.TrimSpace(a.AccountID),
		Surface:   toolCallRepairSurface,
		Model:     toolCallRepairModel,
		Stream:    false,
		UserInput: strings.TrimSpace(prompt),
		Messages: []chathistory.Message{{
			Role:    "user",
			Content: strings.TrimSpace(prompt),
		}},
		FinalPrompt: prompt,
	})
	if entry.ID == "" {
		if err != nil {
			config.Logger.Warn("[toolcall_repair] history start failed", "error", err)
		}
		return nil
	}
	if err != nil {
		config.Logger.Warn("[toolcall_repair] history start persisted in memory after write failure", "error", err)
	}
	return &repairHistoryRecord{store: store, entryID: entry.ID, startedAt: time.Now()}
}

// success finalizes the record with the raw repair output.
func (r *repairHistoryRecord) success(content string) {
	r.finish(chathistory.UpdateParams{
		Status:       "success",
		Content:      content,
		StatusCode:   200,
		ElapsedMs:    r.elapsedMs(),
		FinishReason: "stop",
		Completed:    true,
	})
}

// failure finalizes the record with the reason the repair pass did not produce
// usable output (upstream error, non-200, or the 10s timeout).
func (r *repairHistoryRecord) failure(statusCode int, err error) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	r.finish(chathistory.UpdateParams{
		Status:       "error",
		Error:        message,
		StatusCode:   statusCode,
		ElapsedMs:    r.elapsedMs(),
		FinishReason: "error",
		Completed:    true,
	})
}

func (r *repairHistoryRecord) elapsedMs() int64 {
	if r == nil {
		return 0
	}
	return time.Since(r.startedAt).Milliseconds()
}

func (r *repairHistoryRecord) finish(params chathistory.UpdateParams) {
	if r == nil || r.store == nil || r.entryID == "" {
		return
	}
	if _, err := r.store.Update(r.entryID, params); err != nil {
		config.Logger.Warn("[toolcall_repair] history update failed", "error", err)
	}
}
