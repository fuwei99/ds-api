package banreport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ds2api/internal/config"
)

func newStore(t *testing.T, cfgJSON string) *config.Store {
	t.Helper()
	t.Setenv("DS2API_CONFIG_JSON", cfgJSON)
	store, err := config.LoadStoreWithError()
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	return store
}

func TestBuildPayloadSkipsWhenDisabled(t *testing.T) {
	store := newStore(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@test.com"}],
		"runtime":{"mute_report":{"enabled":false,"url":"http://127.0.0.1:8100"}}
	}`)
	if _, _, ok := BuildPayload(store, Event{Type: KindMuted, Account: "u@test.com"}); ok {
		t.Fatal("expected no report when disabled")
	}
	if _, _, ok := BuildPayload(nil, Event{Type: KindMuted}); ok {
		t.Fatal("expected no report for nil store")
	}
}

func TestBuildPayloadFallsBackToDefaultURL(t *testing.T) {
	store := newStore(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@test.com"}],
		"runtime":{"mute_report":{"enabled":true,"url":"  "}}
	}`)
	_, endpoint, ok := BuildPayload(store, Event{Type: KindMuted, Account: "u@test.com"})
	if !ok {
		t.Fatal("expected report enabled")
	}
	if endpoint != config.DefaultMuteReportURL {
		t.Fatalf("expected default endpoint, got %q", endpoint)
	}
}

func TestBuildPayloadCountsAndPoolType(t *testing.T) {
	muteUntil := time.Now().Add(time.Hour).Unix()
	store := newStore(t, fmt.Sprintf(`{
		"keys":["k1"],
		"accounts":[
			{"email":"muted@test.com","pool_type":"no_tools","muted_until":%d},
			{"email":"banned@test.com","pool_type":"tools_only","banned":true,"disabled":true},
			{"email":"ok1@test.com"},
			{"email":"ok2@test.com","pool_type":"default"}
		],
		"runtime":{"mute_report":{"enabled":true,"url":"http://127.0.0.1:8100"}}
	}`, muteUntil))

	payload, endpoint, ok := BuildPayload(store, Event{
		Type:      KindMuted,
		Account:   "muted@test.com",
		MuteUntil: float64(muteUntil),
		Source:    SourceLogin,
	})
	if !ok {
		t.Fatal("expected report enabled")
	}
	if endpoint != "http://127.0.0.1:8100" {
		t.Fatalf("unexpected endpoint %q", endpoint)
	}
	if payload.Type != KindMuted || payload.TypeLabel != "临时禁言" {
		t.Fatalf("unexpected type payload %+v", payload)
	}
	if payload.PoolType != config.PoolTypeNoTools || payload.PoolTypeLabel != "无工具" {
		t.Fatalf("unexpected pool type payload %+v", payload)
	}
	if payload.EnabledCount != 2 {
		t.Fatalf("expected 2 schedulable accounts, got %d", payload.EnabledCount)
	}
	if payload.TotalCount != 4 {
		t.Fatalf("expected 4 total accounts, got %d", payload.TotalCount)
	}
	if payload.MuteSeconds < 3500 || payload.MuteSeconds > 3600 {
		t.Fatalf("unexpected mute_seconds %d", payload.MuteSeconds)
	}
	if payload.MuteUntil != muteUntil {
		t.Fatalf("unexpected mute_until %d", payload.MuteUntil)
	}
	if payload.Source != SourceLogin {
		t.Fatalf("unexpected source %q", payload.Source)
	}
	if payload.OccurredAt == 0 {
		t.Fatal("expected occurred_at to be set")
	}
}

func TestBuildPayloadBannedHasNoMuteDuration(t *testing.T) {
	store := newStore(t, `{
		"keys":["k1"],
		"accounts":[{"email":"banned@test.com","pool_type":"tools_only","banned":true,"disabled":true}],
		"runtime":{"mute_report":{"enabled":true,"url":"http://127.0.0.1:8100"}}
	}`)
	payload, _, ok := BuildPayload(store, Event{Type: KindBanned, Account: "banned@test.com"})
	if !ok {
		t.Fatal("expected report enabled")
	}
	if payload.Type != KindBanned || payload.TypeLabel != "永久封号" {
		t.Fatalf("unexpected type payload %+v", payload)
	}
	if payload.PoolTypeLabel != "仅工具" {
		t.Fatalf("unexpected pool type label %q", payload.PoolTypeLabel)
	}
	if payload.MuteSeconds != 0 || payload.MuteUntil != 0 {
		t.Fatalf("expected zero mute fields for banned, got %+v", payload)
	}
	if payload.EnabledCount != 0 || payload.TotalCount != 1 {
		t.Fatalf("unexpected counts %+v", payload)
	}
}

func TestPostSendsJSONPayload(t *testing.T) {
	type captured struct {
		method      string
		contentType string
		userAgent   string
		body        Payload
	}
	got := make(chan captured, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body Payload
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		got <- captured{
			method:      r.Method,
			contentType: r.Header.Get("Content-Type"),
			userAgent:   r.Header.Get("User-Agent"),
			body:        body,
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	want := Payload{Type: KindMuted, TypeLabel: "临时禁言", Account: "u@test.com", EnabledCount: 1, TotalCount: 2}
	if err := Post(context.Background(), server.Client(), server.URL, want); err != nil {
		t.Fatalf("post: %v", err)
	}
	select {
	case c := <-got:
		if c.method != http.MethodPost {
			t.Fatalf("unexpected method %q", c.method)
		}
		if c.contentType != "application/json" {
			t.Fatalf("unexpected content type %q", c.contentType)
		}
		if c.userAgent != userAgent {
			t.Fatalf("unexpected user agent %q", c.userAgent)
		}
		if c.body != want {
			t.Fatalf("unexpected body %+v", c.body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for report")
	}
}

func TestPostReturnsErrorOnNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	if err := Post(context.Background(), server.Client(), server.URL, Payload{Type: KindMuted}); err == nil {
		t.Fatal("expected error for non-2xx response")
	}
}

func TestReportDeliversAsynchronously(t *testing.T) {
	done := make(chan Payload, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body Payload
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		done <- body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := newStore(t, fmt.Sprintf(`{
		"keys":["k1"],
		"accounts":[{"email":"u@test.com"}],
		"runtime":{"mute_report":{"enabled":true,"url":%q}}
	}`, server.URL))

	Report(store, Event{Type: KindBanned, Account: "u@test.com", Source: SourceCompletion})

	select {
	case body := <-done:
		if body.Type != KindBanned || body.Account != "u@test.com" || body.Source != SourceCompletion {
			t.Fatalf("unexpected payload %+v", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for async report")
	}
}

func TestSourceUploadBuildPayload(t *testing.T) {
	muteUntil := time.Now().Add(10 * time.Minute).Unix()
	store := newStore(t, fmt.Sprintf(`{
		"keys":["k1"],
		"accounts":[{"email":"u@test.com","muted_until":%d}],
		"runtime":{"mute_report":{"enabled":true,"url":"http://127.0.0.1:8100"}}
	}`, muteUntil))

	payload, _, ok := BuildPayload(store, Event{
		Type:      KindMuted,
		Account:   "u@test.com",
		MuteUntil: float64(muteUntil),
		Source:    SourceUpload,
	})
	if !ok {
		t.Fatal("expected report enabled")
	}
	if payload.Source != SourceUpload {
		t.Fatalf("expected source %q, got %q", SourceUpload, payload.Source)
	}
	if payload.Type != KindMuted {
		t.Fatalf("expected type %q, got %q", KindMuted, payload.Type)
	}
}
