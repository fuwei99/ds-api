package responsehistory

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"ds2api/internal/auth"
	"ds2api/internal/promptcompat"
	"ds2api/internal/usagestats"
)

func TestProgressPersistDueCoalescesFrequentUpdates(t *testing.T) {
	base := time.Unix(100, 0)
	if progressPersistDue(base.Add(500*time.Millisecond), base) {
		t.Fatal("expected frequent progress update to be coalesced")
	}
	if !progressPersistDue(base.Add(time.Second), base) {
		t.Fatal("expected progress update after one second")
	}
}

func TestProgressPersistDueAllowsInitialUpdate(t *testing.T) {
	if !progressPersistDue(time.Unix(100, 0), time.Time{}) {
		t.Fatal("expected initial progress update")
	}
}

func TestStartRecordsUsageWithoutHistoryStore(t *testing.T) {
	usageStore := usagestats.New(filepath.Join(t.TempDir(), "usage.json"))
	defer usageStore.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	session := Start(StartParams{
		Usage:   usageStore,
		Request: req,
		Auth:    &auth.RequestAuth{CallerID: "caller-a"},
		Surface: "claude.messages",
		Standard: promptcompat.StandardRequest{
			ResponseModel: "deepseek-v4-pro",
		},
	})
	if session == nil {
		t.Fatal("expected usage-only session")
	}
	session.Success(http.StatusOK, "", "ok", "stop", map[string]any{"input_tokens": 12, "output_tokens": 4})

	entries := usageStore.Summary()
	if len(entries) != 1 {
		t.Fatalf("unexpected usage entries: %+v", entries)
	}
	entry := entries[0]
	if entry.Calls != 1 || entry.PromptTokens != 12 || entry.CompletionTokens != 4 {
		t.Fatalf("unexpected usage entry: %+v", entry)
	}
	if entry.Model != "deepseek-v4-pro" || entry.CallerID != "caller-a" {
		t.Fatalf("unexpected usage dimensions: %+v", entry)
	}
}
