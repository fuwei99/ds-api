package gemini

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"ds2api/internal/account"
	"ds2api/internal/auth"
	"ds2api/internal/config"
	dsclient "ds2api/internal/deepseek/client"
	"ds2api/internal/toolcall"
	"ds2api/internal/util"
)

// geminiMarkerDS is the upstream stub for the marker coverage tests: it captures
// the payload and answers with a tool call rendered in the caller's marker form,
// exactly as the upstream model does when the prompt used the per-API-key
// marker.
type geminiMarkerDS struct {
	payload  map[string]any
	toolCall string
}

func (d *geminiMarkerDS) CreateSession(context.Context, *auth.RequestAuth, int) (string, error) {
	return "session-id", nil
}

func (d *geminiMarkerDS) GetPow(context.Context, *auth.RequestAuth, int) (string, error) {
	return "pow", nil
}

func (d *geminiMarkerDS) UploadFile(context.Context, *auth.RequestAuth, dsclient.UploadFileRequest, int) (*dsclient.UploadFileResult, error) {
	return &dsclient.UploadFileResult{ID: "file-id"}, nil
}

func (d *geminiMarkerDS) CallCompletion(_ context.Context, _ *auth.RequestAuth, payload map[string]any, _ string, _ int) (*http.Response, error) {
	d.payload = payload
	encoded, err := json.Marshal(d.toolCall)
	if err != nil {
		return nil, err
	}
	body := "data: " + `{"p":"response/content","v":` + string(encoded) + `}` + "\n\ndata: [DONE]\n\n"
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func (d *geminiMarkerDS) StopStream(context.Context, *auth.RequestAuth, string, int) error {
	return nil
}

func (d *geminiMarkerDS) FireCompletionAndStop(context.Context, *auth.RequestAuth, map[string]any, string) (int, error) {
	return 0, nil
}

func newGeminiMarkerResolver(t *testing.T) (*config.Store, *auth.Resolver) {
	t.Helper()
	t.Setenv("DS2API_CONFIG_JSON", `{"keys":[],"accounts":[]}`)
	store := config.LoadStore()
	pool := account.NewPool(store)
	resolver := auth.NewResolver(store, pool, func(_ context.Context, _ config.Account) (string, error) {
		return "unused", nil
	})
	return store, resolver
}

// geminiMarkerForToken mirrors the production derivation (auth.callerTokenID +
// toolcall.DeriveToolMarker).
func geminiMarkerForToken(store *config.Store, token string) string {
	sum := sha256.Sum256([]byte(token))
	callerID := "caller:" + hex.EncodeToString(sum[:8])
	return toolcall.DeriveToolMarker(store.ToolMarkerSecret(), callerID)
}

func geminiMarkerToolCallText(marker string) string {
	return "<|" + marker + "|tool_calls>\n" +
		"  <|" + marker + "|invoke name=\"read_file\">\n" +
		"    <|" + marker + "|parameter name=\"path\"><![CDATA[README.MD]]></|" + marker + "|parameter>\n" +
		"  </|" + marker + "|invoke>\n" +
		"</|" + marker + "|tool_calls>"
}

func geminiMarkerRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:generateContent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "direct-token")
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("model", "gemini-2.5-flash")
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}

// TestGeminiAppliesPerKeyToolMarker covers the Gemini surface end to end: the
// upstream prompt must carry the caller-specific marker instead of the shared
// EPSE keyword, the model's marker-form tool call must be normalized back to
// EPSE and parsed into a functionCall, and the reported prompt token count must
// be the one counted on the prompt actually sent (the same usage口径 the OpenAI
// exits get inside their normalizers).
func TestGeminiAppliesPerKeyToolMarker(t *testing.T) {
	t.Setenv("DS2API_TOOL_MARKER_SECRET", "gemini-marker-secret")
	store, resolver := newGeminiMarkerResolver(t)
	expected := geminiMarkerForToken(store, "direct-token")
	if expected == "" || expected == toolcall.EPSEKeyword {
		t.Fatalf("expected a randomized marker, got %q", expected)
	}

	ds := &geminiMarkerDS{toolCall: geminiMarkerToolCallText(expected)}
	h := &Handler{Store: store, Auth: resolver, DS: ds}

	body := `{"contents":[{"role":"user","parts":[{"text":"read it"}]}],"tools":[{"functionDeclarations":[{"name":"read_file","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}]}]}`
	rec := httptest.NewRecorder()
	h.GenerateContent(rec, geminiMarkerRequest(t, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if ds.payload == nil {
		t.Fatal("expected an upstream payload")
	}
	prompt, _ := ds.payload["prompt"].(string)
	if !strings.Contains(prompt, "<|"+expected+"|tool_calls>") {
		t.Fatalf("upstream prompt must carry the caller marker %q: %q", expected, prompt)
	}
	if strings.Contains(prompt, toolcall.EPSEKeyword) {
		t.Fatalf("upstream prompt leaked the canonical keyword: %q", prompt)
	}
	if strings.Contains(rec.Body.String(), expected) {
		t.Fatalf("caller marker leaked into the client response: %s", rec.Body.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode gemini response: %v", err)
	}
	if !strings.Contains(rec.Body.String(), `"functionCall"`) || !strings.Contains(rec.Body.String(), "read_file") {
		t.Fatalf("marker-form tool call was not parsed into a functionCall: %s", rec.Body.String())
	}
	usage, _ := decoded["usageMetadata"].(map[string]any)
	gotTokens, _ := usage["promptTokenCount"].(float64)
	wantTokens := util.CountPromptTokens(prompt, "gemini-2.5-flash")
	if int(gotTokens) != wantTokens {
		t.Fatalf("promptTokenCount = %v, want %d (counted from the prompt actually sent)", gotTokens, wantTokens)
	}
}
