package claude

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

	"ds2api/internal/account"
	"ds2api/internal/auth"
	"ds2api/internal/config"
	dsclient "ds2api/internal/deepseek/client"
	"ds2api/internal/toolcall"
	"ds2api/internal/util"
)

// claudeMarkerDS is the upstream stub for the marker coverage tests: it captures
// the payload and answers with a tool call rendered in the caller's marker form.
type claudeMarkerDS struct {
	payload  map[string]any
	toolCall string
}

func (d *claudeMarkerDS) CreateSession(context.Context, *auth.RequestAuth, int) (string, error) {
	return "session-id", nil
}

func (d *claudeMarkerDS) GetPow(context.Context, *auth.RequestAuth, int) (string, error) {
	return "pow", nil
}

func (d *claudeMarkerDS) UploadFile(context.Context, *auth.RequestAuth, dsclient.UploadFileRequest, int) (*dsclient.UploadFileResult, error) {
	return &dsclient.UploadFileResult{ID: "file-id"}, nil
}

func (d *claudeMarkerDS) CallCompletion(_ context.Context, _ *auth.RequestAuth, payload map[string]any, _ string, _ int) (*http.Response, error) {
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

func (d *claudeMarkerDS) StopStream(context.Context, *auth.RequestAuth, string, int) error {
	return nil
}

func (d *claudeMarkerDS) FireCompletionAndStop(context.Context, *auth.RequestAuth, map[string]any, string) (int, error) {
	return 0, nil
}

func newClaudeMarkerResolver(t *testing.T) (*config.Store, *auth.Resolver) {
	t.Helper()
	t.Setenv("DS2API_CONFIG_JSON", `{"keys":[],"accounts":[]}`)
	store := config.LoadStore()
	pool := account.NewPool(store)
	resolver := auth.NewResolver(store, pool, func(_ context.Context, _ config.Account) (string, error) {
		return "unused", nil
	})
	return store, resolver
}

// claudeMarkerForToken mirrors the production derivation (auth.callerTokenID +
// toolcall.DeriveToolMarker).
func claudeMarkerForToken(store *config.Store, token string) string {
	sum := sha256.Sum256([]byte(token))
	callerID := "caller:" + hex.EncodeToString(sum[:8])
	return toolcall.DeriveToolMarker(store.ToolMarkerSecret(), callerID)
}

func claudeMarkerToolCallText(marker string) string {
	return "<|" + marker + "|tool_calls>\n" +
		"  <|" + marker + "|invoke name=\"read_file\">\n" +
		"    <|" + marker + "|parameter name=\"path\"><![CDATA[README.MD]]></|" + marker + "|parameter>\n" +
		"  </|" + marker + "|invoke>\n" +
		"</|" + marker + "|tool_calls>"
}

func claudeMarkerRequestBody(stream bool) string {
	return `{"model":"claude-sonnet-4-6","max_tokens":1024,"stream":` + map[bool]string{true: "true", false: "false"}[stream] +
		`,"messages":[{"role":"user","content":"read it"}],"tools":[{"name":"read_file","description":"Read a file","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}]}`
}

// TestClaudeAppliesPerKeyToolMarker covers the Claude surface end to end: the
// upstream prompt must carry the caller-specific marker instead of the shared
// EPSE keyword, and the model's marker-form tool call must be normalized back to
// EPSE and parsed into a tool_use block for the client.
func TestClaudeAppliesPerKeyToolMarker(t *testing.T) {
	t.Setenv("DS2API_TOOL_MARKER_SECRET", "claude-marker-secret")
	store, resolver := newClaudeMarkerResolver(t)
	expected := claudeMarkerForToken(store, "direct-token")
	if expected == "" || expected == toolcall.EPSEKeyword {
		t.Fatalf("expected a randomized marker, got %q", expected)
	}

	for _, stream := range []bool{false, true} {
		name := "nonstream"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			ds := &claudeMarkerDS{toolCall: claudeMarkerToolCallText(expected)}
			h := &Handler{Store: store, Auth: resolver, DS: ds}

			req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(claudeMarkerRequestBody(stream)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("anthropic-version", "2023-06-01")
			req.Header.Set("Authorization", "Bearer direct-token")
			rec := httptest.NewRecorder()

			h.Messages(rec, req)

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
			if !strings.Contains(rec.Body.String(), "tool_use") || !strings.Contains(rec.Body.String(), "read_file") {
				t.Fatalf("marker-form tool call was not parsed into a tool_use block: %s", rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), expected) {
				t.Fatalf("caller marker leaked into the client response: %s", rec.Body.String())
			}
			if !stream {
				// Usage 必须按实际发送的那份 prompt 计数：出口替换发生在
				// normalize 之后，PromptTokenText 若留在 EPSE 形态，报告的
				// input_tokens 就与上游真正收到的上下文不是同一串文本。
				var decoded map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
					t.Fatalf("decode claude response: %v", err)
				}
				usage, _ := decoded["usage"].(map[string]any)
				gotTokens, _ := usage["input_tokens"].(float64)
				wantTokens := util.CountPromptTokens(prompt, "claude-sonnet-4-6")
				if int(gotTokens) != wantTokens {
					t.Fatalf("input_tokens = %v, want %d (counted from the prompt actually sent)", gotTokens, wantTokens)
				}
			}
		})
	}
}
