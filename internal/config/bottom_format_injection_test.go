package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBottomFormatInjectionDefaultsToEnabled 验证「底部格式提示注入」与其它行为
// 开关相反：未配置时默认开启，只有显式写入 false 才关闭。
func TestBottomFormatInjectionDefaultsToEnabled(t *testing.T) {
	store := &Store{cfg: Config{}}
	if !store.BottomFormatInjectionEnabled() {
		t.Fatal("expected bottom format injection enabled by default")
	}
	if got := store.BottomFormatInjectionPrompt(); got != "" {
		t.Fatalf("default prompt=%q want empty", got)
	}

	store.cfg.SetSetting("bottom_format_injection", "enabled", false)
	if store.BottomFormatInjectionEnabled() {
		t.Fatal("expected bottom format injection disabled by explicit config")
	}

	store.cfg.SetSetting("bottom_format_injection", "enabled", true)
	store.cfg.SetSetting("bottom_format_injection", "prompt", "  自定义提醒  ")
	if !store.BottomFormatInjectionEnabled() {
		t.Fatal("expected bottom format injection enabled by explicit config")
	}
	if got := store.BottomFormatInjectionPrompt(); got != "自定义提醒" {
		t.Fatalf("prompt=%q want trimmed custom prompt", got)
	}
}

// TestBottomFormatInjectionCodecRoundTrip 验证注册表驱动的 codec 在 Marshal /
// Unmarshal / Clone 三处都带上了这些字段。显式 false 必须落盘，否则「关闭」会在
// 重启后悄悄回到默认开启；解析后的值也不能掉进 AdditionalFields。
func TestBottomFormatInjectionCodecRoundTrip(t *testing.T) {
	cfg := Config{}
	cfg.SetSetting("bottom_format_injection", "enabled", false)

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
	if got := back.Settings["bottom_format_injection"]["enabled"]; got != false {
		t.Fatalf("enabled=%v want explicit false", got)
	}
	if _, ok := back.AdditionalFields["bottom_format_injection"]; ok {
		t.Fatal("bottom_format_injection must not fall into AdditionalFields")
	}

	// 自定义提示词同样要落盘并解析回来。
	promptCfg := Config{}
	promptCfg.SetSetting("bottom_format_injection", "prompt", "自定义提醒")
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
	if got := promptBack.Settings["bottom_format_injection"]["prompt"]; got != "自定义提醒" {
		t.Fatalf("prompt=%q want 自定义提醒", got)
	}
}

// TestBottomFormatInjectionCloneDeepCopies 验证 Clone 深拷贝设置存储：
// 否则调用方改动快照会污染 Store 内部状态。
func TestBottomFormatInjectionCloneDeepCopies(t *testing.T) {
	cfg := Config{}
	cfg.SetSetting("bottom_format_injection", "enabled", false)
	cfg.SetSetting("bottom_format_injection", "prompt", "自定义")

	clone := cfg.Clone()
	if got := clone.Settings["bottom_format_injection"]["enabled"]; got != false {
		t.Fatalf("clone enabled=%v want explicit false", got)
	}
	if got := clone.Settings["bottom_format_injection"]["prompt"]; got != "自定义" {
		t.Fatalf("clone prompt=%q want 自定义", got)
	}
	clone.Settings["bottom_format_injection"]["enabled"] = true
	if cfg.Settings["bottom_format_injection"]["enabled"] != false {
		t.Fatal("clone must deep-copy the settings store")
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
		c.SetSetting("bottom_format_injection", "enabled", false)
		c.SetSetting("bottom_format_injection", "prompt", "自定义提醒")
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
		c.SetSetting("bottom_format_injection", "enabled", true)
		return nil
	}); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if !store.BottomFormatInjectionEnabled() {
		t.Fatal("expected re-enabled after update")
	}
}
