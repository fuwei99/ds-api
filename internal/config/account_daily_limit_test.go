package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// 24h 速率限制（runtime.account_daily_limit）的出厂状态是"关闭且不填数值"：
// 设置接口必须把未填的上限报成 null（前端渲染成空输入框），而不是一个用户没
// 输入过的 0；只打开开关而不填数值时也不生效。
func TestAccountDailyLimitDefaultsToOffAndUnset(t *testing.T) {
	store := &Store{cfg: Config{}}
	if store.RuntimeAccountDailyLimitEnabled() {
		t.Fatal("the 24h rate limit must default to off")
	}
	if got := store.RuntimeAccountDailyLimitMaxPerDay(); got != 0 {
		t.Fatalf("an unset daily limit must read as 0 (unset), got %d", got)
	}

	values := store.SectionValues("runtime")
	group, ok := values["account_daily_limit"].(map[string]any)
	if !ok {
		t.Fatalf("account_daily_limit must be reported as a declared group: %#v", values["account_daily_limit"])
	}
	if group["enabled"] != false {
		t.Fatalf("the switch must be reported as off, got %#v", group["enabled"])
	}
	if value, present := group["max_per_day"]; !present || value != nil {
		t.Fatalf("an unfilled value must be reported as null so the form shows an empty box, got %#v", group["max_per_day"])
	}
}

// 开关打开但不带数值的写入是合法的：数值保持未填，功能不生效——它不能被当成
// 上限 0 把全部账号立刻打成不可用。
func TestAccountDailyLimitEnabledWithoutValueStaysUnset(t *testing.T) {
	cfg := Config{}
	updates := SectionUpdates(map[string]any{
		"runtime": map[string]any{"account_daily_limit": map[string]any{"enabled": true}},
	})
	if err := ApplySections(&cfg, updates); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if cfg.Runtime.AccountDailyLimit.Enabled == nil || !*cfg.Runtime.AccountDailyLimit.Enabled {
		t.Fatalf("expected the switch to be applied, got %+v", cfg.Runtime.AccountDailyLimit)
	}
	if cfg.Runtime.AccountDailyLimit.MaxPerDay != 0 {
		t.Fatalf("a value the request did not carry must stay unset, got %d", cfg.Runtime.AccountDailyLimit.MaxPerDay)
	}
	if err := ValidateRuntimeConfig(cfg.Runtime); err != nil {
		t.Fatalf("an enabled limit without a value must be a legal state: %v", err)
	}
}

// 填了数值之后：读取侧报出该数值，写入侧做范围校验。
func TestAccountDailyLimitValueRoundTripsAndIsRangeChecked(t *testing.T) {
	cfg := Config{}
	updates := SectionUpdates(map[string]any{
		"runtime": map[string]any{"account_daily_limit": map[string]any{"enabled": true, "max_per_day": 250}},
	})
	if err := ApplySections(&cfg, updates); err != nil {
		t.Fatalf("apply: %v", err)
	}
	store := &Store{cfg: cfg}
	if !store.RuntimeAccountDailyLimitEnabled() {
		t.Fatal("expected the switch to be on")
	}
	if got := store.RuntimeAccountDailyLimitMaxPerDay(); got != 250 {
		t.Fatalf("expected the filled value to be read back, got %d", got)
	}
	group, _ := store.SectionValues("runtime")["account_daily_limit"].(map[string]any)
	if group["max_per_day"] != 250 {
		t.Fatalf("a filled value must be reported as a number, got %#v", group["max_per_day"])
	}

	for _, bad := range []int{-1, MaxAccountDailyLimitMaxPerDay + 1} {
		err := ApplySections(&Config{}, SectionUpdates(map[string]any{
			"runtime": map[string]any{"account_daily_limit": map[string]any{"max_per_day": bad}},
		}))
		if err == nil || !strings.Contains(err.Error(), "runtime.account_daily_limit.max_per_day") {
			t.Fatalf("value %d must be rejected with an error naming the field, got %v", bad, err)
		}
	}
	if err := ValidateAccountDailyLimitConfig(AccountDailyLimitConfig{MaxPerDay: -1}); err == nil {
		t.Fatal("a negative value must be rejected on load too")
	}

	// 0 表示"清除该数值"：写入后读取侧回到未填状态，而不是一个上限 0。
	cleared := Config{}
	updates = SectionUpdates(map[string]any{
		"runtime": map[string]any{"account_daily_limit": map[string]any{"max_per_day": 0}},
	})
	if err := ApplySections(&cleared, updates); err != nil {
		t.Fatalf("clearing the value with 0 must be accepted: %v", err)
	}
	if got := (&Store{cfg: cleared}).RuntimeAccountDailyLimitMaxPerDay(); got != 0 {
		t.Fatalf("expected the cleared value to read as unset, got %d", got)
	}
}

// runtimeConfigIsSet / Clone 必须覆盖新字段：只配了 24h 限额的实例不能在保存时
// 丢掉它，快照也不能与 Store 共享 *bool。
func TestAccountDailyLimitIsEncodedAndDeepCopied(t *testing.T) {
	enabled := true
	cfg := Config{Runtime: RuntimeConfig{AccountDailyLimit: AccountDailyLimitConfig{Enabled: &enabled, MaxPerDay: 3}}}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"account_daily_limit"`) {
		t.Fatalf("a set 24h limit must survive marshal, got %s", raw)
	}

	clone := cfg.Clone()
	if clone.Runtime.AccountDailyLimit.Enabled == nil || !*clone.Runtime.AccountDailyLimit.Enabled {
		t.Fatalf("expected the clone to keep the switch, got %+v", clone.Runtime.AccountDailyLimit)
	}
	*clone.Runtime.AccountDailyLimit.Enabled = false
	if !*cfg.Runtime.AccountDailyLimit.Enabled {
		t.Fatal("the clone must not share the enabled pointer with the original")
	}
}

// 未配置的实例不写这一段（配置文件形态与手写字段时一致）。
func TestAccountDailyLimitOmittedWhenUnset(t *testing.T) {
	raw, err := json.Marshal(Config{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "account_daily_limit") {
		t.Fatalf("an unset 24h limit must stay out of the file, got %s", raw)
	}
}
