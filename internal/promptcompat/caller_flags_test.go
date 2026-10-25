package promptcompat

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"ds2api/internal/config"
)

// callerFlagStub is a CredentialFlagReader test double: it answers from a set of
// flag names and records which names it was asked about, so a test can pin both
// the effect of a flag and the fact that the shared step looked it up under its
// registered name.
type callerFlagStub struct {
	on    map[string]bool
	asked []string
}

func (s *callerFlagStub) CredentialFlagForRequest(_ *http.Request, flag string) bool {
	s.asked = append(s.asked, flag)
	return s.on[flag]
}

func toolsRequestBody() map[string]any {
	return map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
		},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "search"}},
		},
	}
}

func plainRequestBody() map[string]any {
	return map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
		},
	}
}

// TestApplyCallerFlagsReportsBodyTools 验证没有开关生效时，共享步骤只做判定：
// 带工具定义的 body 判为含工具，普通 body 判为不含。
func TestApplyCallerFlagsReportsBodyTools(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	if !ApplyCallerFlags(&callerFlagStub{}, req, toolsRequestBody()) {
		t.Fatal("expected a body carrying tools to report tools present")
	}
	if ApplyCallerFlags(&callerFlagStub{}, req, plainRequestBody()) {
		t.Fatal("expected a plain body to report no tools")
	}
}

// TestApplyCallerFlagsStripsToolsWhenForceDisableToolsIsOn 验证开关生效时，
// 共享步骤先改写 body 再判定，因此结构化 tools 与 system 文本关键词两种形态
// 都判为不含工具——顺序是这条规则的契约。
func TestApplyCallerFlagsStripsToolsWhenForceDisableToolsIsOn(t *testing.T) {
	stub := &callerFlagStub{on: map[string]bool{config.CredentialFlagForceDisableTools: true}}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	structured := toolsRequestBody()
	if ApplyCallerFlags(stub, req, structured) {
		t.Fatal("expected a force-disabled request to stop counting as a tools request")
	}
	if _, ok := structured["tools"]; ok {
		t.Fatalf("expected tool definitions to be stripped, body=%#v", structured)
	}
	if !ToolsForceDisabledInRequest(structured) {
		t.Fatal("expected the force-disable marker on the rewritten body")
	}

	systemDeclared := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "Available tools: workspace_read_file"},
			map[string]any{"role": "user", "content": "read the file"},
		},
	}
	if ApplyCallerFlags(stub, req, systemDeclared) {
		t.Fatal("expected system-declared tools to stop counting as well")
	}

	// 开关必须按注册名读取：换一个名字读到的开关不该影响工具判定。断言"每个被读
	// 到的名字都注册过"而不是钉死某一个名字，这样新增一个开关只需改注册表与动作
	// 分支，不必回来改这里。
	registered := map[string]bool{}
	for _, name := range config.CredentialFlagNames() {
		registered[name] = true
	}
	if len(stub.asked) == 0 {
		t.Fatal("expected the shared step to read a credential flag")
	}
	sawForceDisable := false
	for _, name := range stub.asked {
		if !registered[name] {
			t.Fatalf("shared step read unregistered flag %q; every flag is read under a name from config.CredentialFlagNames()", name)
		}
		if name == config.CredentialFlagForceDisableTools {
			sawForceDisable = true
		}
	}
	if !sawForceDisable {
		t.Fatalf("shared step never read %q, so this case would pass without the flag being consulted at all", config.CredentialFlagForceDisableTools)
	}
}

// TestApplyCallerFlagsIgnoresOtherFlags 验证一个只对别的开关返回 true 的读取方
// 不会误触发工具清除。
func TestApplyCallerFlagsIgnoresOtherFlags(t *testing.T) {
	stub := &callerFlagStub{on: map[string]bool{"some_other_flag": true}}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	body := toolsRequestBody()
	if !ApplyCallerFlags(stub, req, body) {
		t.Fatal("expected tools to still count while only an unrelated flag is on")
	}
	if _, ok := body["tools"]; !ok {
		t.Fatalf("expected tool definitions to be kept, body=%#v", body)
	}
}

// TestApplyCallerFlagsWithoutReader 验证没有读取方时共享步骤仍只做判定且不 panic，
// 这对应直连 token 等没有托管 Key 的调用方。
func TestApplyCallerFlagsWithoutReader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	if !ApplyCallerFlags(nil, req, toolsRequestBody()) {
		t.Fatal("expected tools present without a flag reader")
	}
	if ApplyCallerFlags(nil, req, plainRequestBody()) {
		t.Fatal("expected no tools without a flag reader")
	}
}
