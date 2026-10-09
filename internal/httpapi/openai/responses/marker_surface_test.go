package responses

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

	"ds2api/internal/auth"
	"ds2api/internal/config"
	dsclient "ds2api/internal/deepseek/client"
	"ds2api/internal/toolcall"
)

// responsesMarkerDS is the upstream stub for the marker coverage tests: it
// captures the payload and answers with a tool call rendered in the caller's
// marker form, exactly as the upstream model does when the prompt used the
// per-API-key marker.
type responsesMarkerDS struct {
	payload  map[string]any
	toolCall string
}

func (d *responsesMarkerDS) CreateSession(context.Context, *auth.RequestAuth, int) (string, error) {
	return "session-id", nil
}

func (d *responsesMarkerDS) GetPow(context.Context, *auth.RequestAuth, int) (string, error) {
	return "pow", nil
}

func (d *responsesMarkerDS) UploadFile(context.Context, *auth.RequestAuth, dsclient.UploadFileRequest, int) (*dsclient.UploadFileResult, error) {
	return &dsclient.UploadFileResult{ID: "file-id"}, nil
}

func (d *responsesMarkerDS) CallCompletion(_ context.Context, _ *auth.RequestAuth, payload map[string]any, _ string, _ int) (*http.Response, error) {
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

func (d *responsesMarkerDS) StopStream(context.Context, *auth.RequestAuth, string, int) error {
	return nil
}

func (d *responsesMarkerDS) FireCompletionAndStop(context.Context, *auth.RequestAuth, map[string]any, string) (int, error) {
	return 0, nil
}

func (d *responsesMarkerDS) DeleteSessionForToken(context.Context, string, string) (*dsclient.DeleteSessionResult, error) {
	return &dsclient.DeleteSessionResult{Success: true}, nil
}

func (d *responsesMarkerDS) DeleteAllSessionsForToken(context.Context, string) error { return nil }

func (d *responsesMarkerDS) DeleteSession(context.Context, *auth.RequestAuth, string, int) (*dsclient.DeleteSessionResult, error) {
	return &dsclient.DeleteSessionResult{Success: true}, nil
}

// markerForCallerToken mirrors the production derivation (auth.callerTokenID +
// toolcall.DeriveToolMarker) so the test asserts the real per-API-key marker
// rather than a hard-coded one.
func markerForCallerToken(store *config.Store, token string) string {
	sum := sha256.Sum256([]byte(token))
	callerID := "caller:" + hex.EncodeToString(sum[:8])
	return toolcall.DeriveToolMarker(store.ToolMarkerSecret(), callerID)
}

func markerToolCallText(marker string) string {
	return "<|" + marker + "|tool_calls>\n" +
		"  <|" + marker + "|invoke name=\"read_file\">\n" +
		"    <|" + marker + "|parameter name=\"path\"><![CDATA[README.MD]]></|" + marker + "|parameter>\n" +
		"  </|" + marker + "|invoke>\n" +
		"</|" + marker + "|tool_calls>"
}

func responsesMarkerRequestBody() string {
	return `{"model":"deepseek-v4.1-flash","stream":false,"input":"read it","tools":[{"type":"function","function":{"name":"read_file","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}}]}`
}

// TestResponsesAppliesPerKeyToolMarker pins the Responses surface against the
// OpenAI Chat coverage that already exists (see internal/httpapi/openai/marker_rebuild_test.go
// and the chat Vercel marker tests): the upstream prompt must carry the
// caller-specific marker instead of the shared EPSE keyword, and the model's
// marker-form tool call must be normalized back to EPSE and parsed into a
// structured function_call for the client.
func TestResponsesAppliesPerKeyToolMarker(t *testing.T) {
	t.Setenv("DS2API_TOOL_MARKER_SECRET", "responses-marker-secret")
	store, resolver := newDirectTokenResolver(t)
	expected := markerForCallerToken(store, "direct-token")
	if expected == "" || expected == toolcall.EPSEKeyword {
		t.Fatalf("expected a randomized marker, got %q", expected)
	}

	ds := &responsesMarkerDS{toolCall: markerToolCallText(expected)}
	h := &Handler{Store: store, Auth: resolver, DS: ds}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(responsesMarkerRequestBody()))
	req.Header.Set("Authorization", "Bearer direct-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Responses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if ds.payload == nil {
		t.Fatal("expected an upstream payload")
	}
	prompt, _ := ds.payload["prompt"].(string)
	if !strings.Contains(prompt, "<|"+expected+"|tool_calls>") {
		t.Fatalf("upstream prompt must carry the caller marker %q", expected)
	}
	if strings.Contains(prompt, toolcall.EPSEKeyword) {
		t.Fatalf("upstream prompt leaked the canonical keyword: %q", prompt)
	}
	if !strings.Contains(rec.Body.String(), `"function_call"`) || !strings.Contains(rec.Body.String(), "read_file") {
		t.Fatalf("marker-form tool call was not parsed into a function_call: %s", rec.Body.String())
	}
}

// TestResponsesMarkerIsPerKeyStableAndDistinct locks the two properties the
// randomization promises: the same API key always maps to the same marker
// (multi-turn prompt/history consistency), and different keys map to different
// markers (no cross-caller fingerprint).
func TestResponsesMarkerIsPerKeyStableAndDistinct(t *testing.T) {
	t.Setenv("DS2API_TOOL_MARKER_SECRET", "responses-marker-secret")
	store, _ := newDirectTokenResolver(t)

	first := markerForCallerToken(store, "direct-token")
	again := markerForCallerToken(store, "direct-token")
	other := markerForCallerToken(store, "another-direct-token")

	if first != again {
		t.Fatalf("marker must be stable per key: %q vs %q", first, again)
	}
	if first == other {
		t.Fatalf("different keys must derive different markers: %q", first)
	}
}

// TestResponsesMarkerSecretFromEnv pins the deployment contract: the env secret
// (the only way read-only/Vercel deployments can keep markers stable) actually
// drives the derivation.
func TestResponsesMarkerSecretFromEnv(t *testing.T) {
	t.Setenv("DS2API_TOOL_MARKER_SECRET", "one-secret")
	store, _ := newDirectTokenResolver(t)
	fromEnv := markerForCallerToken(store, "direct-token")

	t.Setenv("DS2API_TOOL_MARKER_SECRET", "another-secret")
	if got := markerForCallerToken(store, "direct-token"); got == fromEnv {
		t.Fatalf("expected the env secret to change the derived marker, both %q", got)
	}
}
