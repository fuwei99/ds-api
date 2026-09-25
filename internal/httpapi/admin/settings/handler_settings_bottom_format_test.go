package settings

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"ds2api/internal/config"
)

// TestParseSettingsUpdateRequestBottomFormatInjection 验证新段的解析：显式 false
// 必须落成非 nil 指针（默认开启的开关只有显式 false 才有意义），prompt 去空白。
func TestParseSettingsUpdateRequestBottomFormatInjection(t *testing.T) {
	_, _, _, _, _, _, _, bottom, _, _, _, err := parseSettingsUpdateRequest(map[string]any{
		"bottom_format_injection": map[string]any{
			"enabled": false,
			"prompt":  "  自定义提醒  ",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bottom == nil {
		t.Fatal("expected bottom_format_injection section to be parsed")
	}
	if bottom.Enabled == nil || *bottom.Enabled {
		t.Fatalf("enabled=%v want explicit false", bottom.Enabled)
	}
	if bottom.Prompt != "自定义提醒" {
		t.Fatalf("prompt=%q want trimmed 自定义提醒", bottom.Prompt)
	}
}

// TestUpdateSettingsBottomFormatInjectionPartialUpdate 验证部分字段更新：只传
// enabled 时不清空已保存的自定义提示词，只传 prompt 时也不把开关翻回默认开启。
func TestUpdateSettingsBottomFormatInjectionPartialUpdate(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{}`)
	store := config.LoadStore()
	if !store.BottomFormatInjectionEnabled() {
		t.Fatal("expected bottom format injection enabled by default")
	}
	handler := &Handler{Store: store}

	// 先存一份自定义提示词。
	putSettingsBody(t, handler, map[string]any{
		"bottom_format_injection": map[string]any{"prompt": "自定义提醒"},
	})
	if got := store.BottomFormatInjectionPrompt(); got != "自定义提醒" {
		t.Fatalf("prompt=%q want 自定义提醒", got)
	}
	if !store.BottomFormatInjectionEnabled() {
		t.Fatal("a prompt-only update must not disable the injection")
	}

	// 只传 enabled=false：自定义提示词必须保留。
	putSettingsBody(t, handler, map[string]any{
		"bottom_format_injection": map[string]any{"enabled": false},
	})
	if store.BottomFormatInjectionEnabled() {
		t.Fatal("expected injection disabled after enabled-only update")
	}
	if got := store.BottomFormatInjectionPrompt(); got != "自定义提醒" {
		t.Fatalf("enabled-only update must keep the custom prompt, got %q", got)
	}

	// 清空提示词后回退内置默认（读取侧返回空串，注入侧回退内置提醒）。
	putSettingsBody(t, handler, map[string]any{
		"bottom_format_injection": map[string]any{"prompt": ""},
	})
	if got := store.BottomFormatInjectionPrompt(); got != "" {
		t.Fatalf("cleared prompt=%q want empty", got)
	}

	// 重新开启。
	putSettingsBody(t, handler, map[string]any{
		"bottom_format_injection": map[string]any{"enabled": true},
	})
	if !store.BottomFormatInjectionEnabled() {
		t.Fatal("expected injection re-enabled")
	}
}

func putSettingsBody(t *testing.T, handler *Handler, body map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/admin/settings", bytes.NewReader(raw))
	handler.updateSettings(rec, req)
	if rec.Code != 200 {
		t.Fatalf("update settings status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestGetSettingsExposesBottomFormatInjection 验证读取接口暴露默认开启的开关
// 以及用于预填输入框的 default_prompt。
func TestGetSettingsExposesBottomFormatInjection(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{}`)
	store := config.LoadStore()
	handler := &Handler{Store: store}

	rec := httptest.NewRecorder()
	handler.getSettings(rec, httptest.NewRequest("GET", "/admin/settings", nil))
	if rec.Code != 200 {
		t.Fatalf("get settings status=%d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	section, ok := body["bottom_format_injection"].(map[string]any)
	if !ok {
		t.Fatalf("bottom_format_injection section missing: %s", rec.Body.String())
	}
	if section["enabled"] != true {
		t.Fatalf("enabled=%v want true by default", section["enabled"])
	}
	if section["prompt"] != "" {
		t.Fatalf("prompt=%v want empty when unset", section["prompt"])
	}
	defaultPrompt, _ := section["default_prompt"].(string)
	if defaultPrompt == "" {
		t.Fatal("default_prompt must be exposed so the WebUI can prefill the input box")
	}
}
