package completionruntime

import (
	"context"
	"path/filepath"
	"testing"

	"ds2api/internal/auth"
	"ds2api/internal/chathistory"
)

func newRepairHistoryStore(t *testing.T) *chathistory.Store {
	t.Helper()
	store := chathistory.New(filepath.Join(t.TempDir(), "chat_history.json"))
	if !store.Enabled() {
		t.Fatalf("expected chat history store to be enabled by default")
	}
	return store
}

func repairEntries(t *testing.T, store *chathistory.Store) []chathistory.SummaryEntry {
	t.Helper()
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	out := make([]chathistory.SummaryEntry, 0, len(snapshot.Items))
	for _, item := range snapshot.Items {
		if item.Surface == toolCallRepairSurface {
			out = append(out, item)
		}
	}
	return out
}

// TestToolCallRepairRecordsSuccessInResponseHistory asserts the proactive repair
// sub-request shows up in 响应记录 as its own "toolcall.repair" entry.
func TestToolCallRepairRecordsSuccessInResponseHistory(t *testing.T) {
	store := newRepairHistoryStore(t)
	ds := &repairFakeCaller{}
	invoke := NewToolCallRepairInvoker(ds, &auth.RequestAuth{CallerID: "caller-1", AccountID: "acct-1"}, store)
	if invoke == nil {
		t.Fatal("expected non-nil invoker")
	}
	if _, err := invoke(context.Background(), "some repair prompt"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items := repairEntries(t, store)
	if len(items) != 1 {
		t.Fatalf("expected 1 toolcall.repair record, got %d", len(items))
	}
	entry, err := store.Get(items[0].ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if entry.Status != "success" {
		t.Fatalf("expected success status, got %q", entry.Status)
	}
	if entry.Model != toolCallRepairModel {
		t.Fatalf("expected model %q, got %q", toolCallRepairModel, entry.Model)
	}
	if entry.CallerID != "caller-1" || entry.AccountID != "acct-1" {
		t.Fatalf("expected caller/account to be recorded, got %q/%q", entry.CallerID, entry.AccountID)
	}
	if entry.FinalPrompt != "some repair prompt" {
		t.Fatalf("expected repair prompt recorded, got %q", entry.FinalPrompt)
	}
	if entry.Content == "" {
		t.Fatalf("expected repair output recorded")
	}
	if entry.CompletedAt == 0 {
		t.Fatalf("expected record to be completed")
	}
}

// TestToolCallRepairRecordsUpstreamErrorInResponseHistory asserts a failed
// repair pass is still recorded, with the error surfaced.
func TestToolCallRepairRecordsUpstreamErrorInResponseHistory(t *testing.T) {
	store := newRepairHistoryStore(t)
	failing := &repairFailingCaller{repairFakeCaller: &repairFakeCaller{}}
	invoke := NewToolCallRepairInvoker(failing, &auth.RequestAuth{}, store)
	if _, err := invoke(context.Background(), "prompt"); err == nil {
		t.Fatal("expected upstream error")
	}

	items := repairEntries(t, store)
	if len(items) != 1 {
		t.Fatalf("expected 1 toolcall.repair record, got %d", len(items))
	}
	entry, err := store.Get(items[0].ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if entry.Status != "error" {
		t.Fatalf("expected error status, got %q", entry.Status)
	}
	if entry.Error == "" {
		t.Fatalf("expected error message recorded")
	}
}

// TestToolCallRepairWithoutHistoryStoreStillRepairs asserts recording is best
// effort: a nil store must not break the repair pass.
func TestToolCallRepairWithoutHistoryStoreStillRepairs(t *testing.T) {
	ds := &repairFakeCaller{}
	invoke := NewToolCallRepairInvoker(ds, &auth.RequestAuth{}, nil)
	out, err := invoke(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatalf("expected repair output")
	}
}
