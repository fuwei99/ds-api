package chat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ds2api/internal/auth"
	"ds2api/internal/config"
)

// forceDisableToolsAuthStub 模拟一个开启了「强制禁用工具调用」的 API Key。
type forceDisableToolsAuthStub struct{}

func (forceDisableToolsAuthStub) Determine(_ *http.Request) (*auth.RequestAuth, error) {
	return &auth.RequestAuth{
		UseConfigToken: false,
		DeepSeekToken:  "direct-token",
		CallerID:       "caller:test",
		TriedAccounts:  map[string]bool{},
	}, nil
}

func (forceDisableToolsAuthStub) DetermineCaller(_ *http.Request) (*auth.RequestAuth, error) {
	return (&forceDisableToolsAuthStub{}).Determine(nil)
}

func (forceDisableToolsAuthStub) ForceDisableToolsForRequest(_ *http.Request) bool { return true }

func (forceDisableToolsAuthStub) Release(_ *auth.RequestAuth) {}

func (forceDisableToolsAuthStub) SetAccountMutedUntil(_ *auth.RequestAuth, _ float64) {}

func (forceDisableToolsAuthStub) SetAccountBanned(_ *auth.RequestAuth, _ string) {}

// TestChatCompletionsForceDisableToolsStripsToolDefinitions 验证入口开关开启后，
// 请求里的结构化 tools 被强制清除、下游 prompt 里没有任何工具提示词。
func TestChatCompletionsForceDisableToolsStripsToolDefinitions(t *testing.T) {
	historyStore := newTestChatHistoryStore(t)
	ds := &inlineUploadDSStub{
		completionResp: makeOpenAISSEHTTPResponse(
			`data: {"p":"response/content","v":"plain answer"}`,
			`data: [DONE]`,
		),
	}
	h := &Handler{
		Store:       mockOpenAIConfig{aliases: config.DefaultModelAliases()},
		Auth:        forceDisableToolsAuthStub{},
		DS:          ds,
		ChatHistory: historyStore,
	}

	reqBody := `{
		"model":"deepseek-v4.1-flash",
		"messages":[{"role":"user","content":"hello there"}],
		"tools":[{"type":"function","function":{"name":"web_search","description":"Search the web","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}}],
		"tool_choice":"required",
		"stream":false
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer blocked-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	payload := ds.completionReq
	if payload == nil {
		t.Fatal("expected completion payload to be captured")
	}
	prompt, _ := payload["prompt"].(string)
	if prompt == "" {
		t.Fatalf("expected non-empty prompt, payload=%#v", payload)
	}
	if strings.Contains(prompt, "You have access to these tools") {
		t.Fatalf("expected no tool descriptions in downstream prompt, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "EPSE") {
		t.Fatalf("expected no tool-call format spec in downstream prompt, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "web_search") {
		t.Fatalf("expected stripped tool name to be absent from downstream prompt, got:\n%s", prompt)
	}
}

// TestChatCompletionsForceDisableToolsCoversSystemKeywordScan 验证入口开关同样
// 覆盖「无 tools 数组、system 文本声明工具」的形态。
func TestChatCompletionsForceDisableToolsCoversSystemKeywordScan(t *testing.T) {
	historyStore := newTestChatHistoryStore(t)
	ds := &inlineUploadDSStub{
		completionResp: makeOpenAISSEHTTPResponse(
			`data: {"p":"response/content","v":"plain answer"}`,
			`data: [DONE]`,
		),
	}
	h := &Handler{
		Store:       mockOpenAIConfig{aliases: config.DefaultModelAliases()},
		Auth:        forceDisableToolsAuthStub{},
		DS:          ds,
		ChatHistory: historyStore,
	}

	reqBody := `{
		"model":"deepseek-v4.1-flash",
		"messages":[
			{"role":"system","content":"Available tools: workspace_read_file, workspace_write_file"},
			{"role":"user","content":"read the file"}
		],
		"stream":false
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer blocked-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	payload := ds.completionReq
	if payload == nil {
		t.Fatal("expected completion payload to be captured")
	}
	prompt, _ := payload["prompt"].(string)
	if strings.Contains(prompt, "EPSE") {
		t.Fatalf("expected no tool-call format spec for system-declared tools, got:\n%s", prompt)
	}
}
