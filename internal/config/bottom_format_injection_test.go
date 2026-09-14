package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBottomFormatInjectionDefaultsToEnabled 验证「底部格式提示注入」与其它行为
// 开关相反：未配置(nil)时默认开启，只有显式写入 false 才关闭。
func TestBottomFormatInjectionDefaultsToEnabled(t *testing.T) {
	store := &Store{cfg: Config{}}
	if !store.BottomFormatInjectionEnabled() {
		t.Fatal("expected bottom format injection enabled by default")
	}
	if got := store.BottomFormatInjectionPrompt(); got != "" {
		t.Fatalf("default prompt=%q want empty", got)
	}

	disabled := false
	store.cfg.BottomFormatInjection.Enabled = &disabled
	if store.BottomFormatInjectionEnabled() {
		t.Fatal("expected bottom format injection disabled by explicit config")
	}

	enabled := true
	store.cfg.BottomFormatInjection = BottomFormatInjectionConfig{Enabled: &enabled, Prompt: "  自定义提醒  "}
	if !store.BottomFormatInjectionEnabled() {
		t.Fatal("expected bottom format injection enabled by explicit config")
	}
	if got := store.BottomFormatInjectionPrompt(); got != "自定义提醒" {
		t.Fatalf("prompt=%q want trimmed custom prompt", got)
	}
}

// TestBottomFormatInjectionCodecRoundTrip 验证手写 codec 的 Marshal / Unmarshal /
// Clone 三处都带上了新字段。显式 false 必须落盘，否则「关闭」会在重启后悄悄
// 回到默认开启；解析后的值也不能掉进 AdditionalFields。
func TestBottomFormatInjectionCodecRoundTrip(t *testing.T) {
	disabled := false
	cfg := Config{BottomFormatInjection: BottomFormatInjectionConfig{Enabled: &disabled}}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(raw), `"bottom_format_injection"`) {
		t.Fatalf("explicit false must be persisted, got: %s", raw)
	}
	if !strings.Contains(string(raw), `"enabled":false`) {
		t.Fatalf("explicit false must be serialized as enabled:false, got: %s", raw)
	}

	var back Config
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if back.BottomFormatInjection.Enabled == nil || *back.BottomFormatInjection.Enabled {
		t.Fatalf("enabled=%v want explicit false", back.BottomFormatInjection.Enabled)
	}
	if _, ok := back.AdditionalFields["bottom_format_injection"]; ok {
		t.Fatal("bottom_format_injection must not fall into AdditionalFields")
	}

	// 自定义提示词同样要落盘并解析回来。
	promptCfg := Config{BottomFormatInjection: BottomFormatInjectionConfig{Prompt: "自定义提醒"}}
	promptRaw, err := json.Marshal(promptCfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(promptRaw), `"prompt":"自定义提醒"`) {
		t.Fatalf("custom prompt must be persisted, got: %s", promptRaw)
	}
	var promptBack Config
	if err := json.Unmarshal(promptRaw, &promptBack); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if promptBack.BottomFormatInjection.Prompt != "自定义提醒" {
		t.Fatalf("prompt=%q want 自定义提醒", promptBack.BottomFormatInjection.Prompt)
	}
}

// TestBottomFormatInjectionCloneDeepCopies 验证 Clone 深拷贝 Enabled 指针：
// 否则调用方改动快照会污染 Store 内部状态。
func TestBottomFormatInjectionCloneDeepCopies(t *testing.T) {
	disabled := false
	cfg := Config{BottomFormatInjection: BottomFormatInjectionConfig{Enabled: &disabled, Prompt: "自定义"}}

	clone := cfg.Clone()
	if clone.BottomFormatInjection.Enabled == nil || *clone.BottomFormatInjection.Enabled {
		t.Fatalf("clone enabled=%v want explicit false", clone.BottomFormatInjection.Enabled)
	}
	if clone.BottomFormatInjection.Prompt != "自定义" {
		t.Fatalf("clone prompt=%q want 自定义", clone.BottomFormatInjection.Prompt)
	}
	*clone.BottomFormatInjection.Enabled = true
	if *cfg.BottomFormatInjection.Enabled {
		t.Fatal("clone must deep-copy the Enabled pointer")
	}
}

// TestBottomFormatInjectionUpdatePersists 验证 Store.Update 会持久化并热更新
// 该开关（关闭 -> 重新开启）。
func TestBottomFormatInjectionUpdatePersists(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{}`)
	store := LoadStore()
	if !store.BottomFormatInjectionEnabled() {
		t.Fatal("expected enabled by default")
	}

	if err := store.Update(func(c *Config) error {
		disabled := false
		c.BottomFormatInjection.Enabled = &disabled
		c.BottomFormatInjection.Prompt = "自定义提醒"
		return nil
	}); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if store.BottomFormatInjectionEnabled() {
		t.Fatal("expected disabled after update")
	}
	if got := store.BottomFormatInjectionPrompt(); got != "自定义提醒" {
		t.Fatalf("prompt=%q want 自定义提醒", got)
	}

	if err := store.Update(func(c *Config) error {
		enabled := true
		c.BottomFormatInjection.Enabled = &enabled
		return nil
	}); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if !store.BottomFormatInjectionEnabled() {
		t.Fatal("expected re-enabled after update")
	}
}
