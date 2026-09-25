package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ds2api/internal/account"
	"ds2api/internal/auth"
	"ds2api/internal/config"
	"ds2api/internal/promptcompat"
	"ds2api/internal/toolcall"
)

const markerUnderTest = "Q7ZK3M"

// markerAuthStub is the direct-token auth stub variant that carries a
// caller-specific tool-call marker, so the Vercel prepare response can be
// asserted without a full resolver.
type markerAuthStub struct{}

func (markerAuthStub) Determine(_ *http.Request) (*auth.RequestAuth, error) {
	return &auth.RequestAuth{
		UseConfigToken: false,
		DeepSeekToken:  "direct-token",
		CallerID:       "caller:test",
		TriedAccounts:  map[string]bool{},
		ToolMarker:     markerUnderTest,
	}, nil
}

func (markerAuthStub) DetermineCaller(_ *http.Request) (*auth.RequestAuth, error) {
	return (markerAuthStub{}).Determine(nil)
}

func (markerAuthStub) ForceDisableToolsForRequest(_ *http.Request) bool { return false }

func (markerAuthStub) Release(_ *auth.RequestAuth) {}

func (markerAuthStub) SetAccountMutedUntil(_ *auth.RequestAuth, _ float64) {}

func (markerAuthStub) SetAccountBanned(_ *auth.RequestAuth, _ string) {}

func markerTools() []any {
	return []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "search",
				"description": "search docs",
				"parameters":  map[string]any{"type": "object"},
			},
		},
	}
}

// TestHandleVercelStreamPrepareReturnsToolMarker covers the Node-mirror handoff:
// the Vercel prepare response must carry the caller-specific marker so the Node
// stream can normalize the model output back to EPSE before its own sieve runs,
// and the final prompt handed over must already be in marker form.
func TestHandleVercelStreamPrepareReturnsToolMarker(t *testing.T) {
	t.Setenv("VERCEL", "1")
	t.Setenv("DS2API_VERCEL_INTERNAL_SECRET", "stream-secret")

	h := &Handler{
		Store: mockOpenAIConfig{},
		Auth:  markerAuthStub{},
		DS:    &inlineUploadDSStub{},
	}

	reqBody, _ := json.Marshal(map[string]any{
		"model":    "deepseek-v4.1-flash",
		"messages": []any{map[string]any{"role": "user", "content": "search docs"}},
		"tools":    markerTools(),
		"stream":   true,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions?__stream_prepare=1", strings.NewReader(string(reqBody)))
	req.Header.Set("Authorization", "Bearer direct-token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ds2-Internal-Token", "stream-secret")
	rec := httptest.NewRecorder()

	h.handleVercelStreamPrepare(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if body["tool_marker"] != markerUnderTest {
		t.Fatalf("expected tool_marker=%q, got %#v", markerUnderTest, body["tool_marker"])
	}
	finalPrompt, _ := body["final_prompt"].(string)
	if strings.Contains(finalPrompt, toolcall.EPSEKeyword) {
		t.Fatalf("final prompt still contains the canonical keyword: %q", finalPrompt)
	}
	if !strings.Contains(finalPrompt, "<|"+markerUnderTest+"|tool_calls>") {
		t.Fatalf("final prompt missing the marker shell: %q", finalPrompt)
	}
	payload, _ := body["payload"].(map[string]any)
	if promptText, _ := payload["prompt"].(string); strings.Contains(promptText, toolcall.EPSEKeyword) {
		t.Fatalf("completion payload prompt still contains the canonical keyword: %q", promptText)
	}
}

// TestHandleVercelStreamSwitchReturnsToolMarkerAndReuploadsMarkerForm covers the
// account-switch mirror: the switched payload's HISTORY.txt is re-rendered with
// the marker while the response keeps reporting the same marker.
func TestHandleVercelStreamSwitchReturnsToolMarkerAndReuploadsMarkerForm(t *testing.T) {
	t.Setenv("VERCEL", "1")
	t.Setenv("DS2API_VERCEL_INTERNAL_SECRET", "stream-secret")
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["managed-key"],
		"accounts":[
			{"email":"acc1@test.com","password":"pwd"},
			{"email":"acc2@test.com","password":"pwd"}
		]
	}`)
	store := config.LoadStore()
	resolver := auth.NewResolver(store, account.NewPool(store), func(_ context.Context, acc config.Account) (string, error) {
		return "token-" + acc.Identifier(), nil
	})
	authReq := httptest.NewRequest(http.MethodPost, "/", nil)
	authReq.Header.Set("Authorization", "Bearer managed-key")
	a, err := resolver.Determine(authReq)
	if err != nil {
		t.Fatalf("determine failed: %v", err)
	}
	defer resolver.Release(a)
	a.ToolMarker = markerUnderTest

	ds := &inlineUploadDSStub{}
	h := &Handler{
		Store: mockOpenAIConfig{},
		Auth:  resolver,
		DS:    ds,
	}
	stdReq := promptcompat.StandardRequest{
		RequestedModel:          "deepseek-v4.1-flash",
		ResolvedModel:           "deepseek-v4.1-flash",
		ResponseModel:           "deepseek-v4.1-flash",
		FinalPrompt:             "继续会话 " + markerToolBlock(markerUnderTest),
		CurrentInputFileApplied: true,
		CurrentInputFileID:      "file-old",
		CurrentToolsFileID:      "file-old-tools",
		HistoryText:             "# HISTORY.txt\n\n" + markerToolBlock(toolcall.EPSEKeyword) + "\n",
		RefFileIDs:              []string{"file-old", "file-old-tools"},
		ToolMarker:              markerUnderTest,
	}
	leaseID := h.holdStreamLease(a, stdReq, "")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions?__stream_switch=1", strings.NewReader(`{"lease_id":"`+leaseID+`"}`))
	req.Header.Set("X-Ds2-Internal-Token", "stream-secret")
	rec := httptest.NewRecorder()

	h.handleVercelStreamSwitch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if body["tool_marker"] != markerUnderTest {
		t.Fatalf("expected tool_marker=%q, got %#v", markerUnderTest, body["tool_marker"])
	}
	if len(ds.uploadCalls) == 0 {
		t.Fatal("expected the current input file to be re-uploaded")
	}
	uploaded := string(ds.uploadCalls[0].Data)
	if strings.Contains(uploaded, toolcall.EPSEKeyword) {
		t.Fatalf("re-uploaded HISTORY.txt still contains the canonical keyword: %q", uploaded)
	}
	if !strings.Contains(uploaded, "<|"+markerUnderTest+"|tool_calls>") {
		t.Fatalf("re-uploaded HISTORY.txt missing the marker shell: %q", uploaded)
	}
}

func markerToolBlock(marker string) string {
	return "<|" + marker + "|tool_calls>\n  <|" + marker + "|invoke name=\"search\">\n    <|" + marker + "|parameter name=\"query\">docs</|" + marker + "|parameter>\n  </|" + marker + "|invoke>\n</|" + marker + "|tool_calls>"
}
