package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"ds2api/internal/account"
	"ds2api/internal/config"
)

// 24h 速率限制（runtime.account_daily_limit）在设置接口上的形状：默认关闭且不填
// 数值——上限报成 null（前端渲染空输入框），而不是一个用户没输入过的 0。
func TestGetSettingsReportsDailyLimitOffAndUnfilled(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"],"accounts":[{"email":"a@example.com","token":"t"}]}`)
	rec := httptest.NewRecorder()
	h.getSettings(rec, httptest.NewRequest(http.MethodGet, "/admin/settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	runtime, _ := body["runtime"].(map[string]any)
	group, ok := runtime["account_daily_limit"].(map[string]any)
	if !ok {
		t.Fatalf("account_daily_limit must be part of the runtime payload: %#v", runtime)
	}
	if group["enabled"] != false {
		t.Fatalf("the 24h limit must be reported as off by default, got %#v", group["enabled"])
	}
	if value, present := group["max_per_day"]; !present || value != nil {
		t.Fatalf("an unfilled limit must be reported as null so the form shows an empty box, got %#v", group["max_per_day"])
	}
}

// 保存设置后号池立即按新值工作（后置动作把新值推给号池，无需重启）。
func TestUpdateSettingsAppliesDailyLimitToThePool(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"],"accounts":[{"email":"a@example.com","token":"t"}]}`)
	t.Setenv("DS2API_ACCOUNT_DAILY_LIMIT_PATH", filepath.Join(t.TempDir(), "account_daily_limit.json"))

	payload, _ := json.Marshal(map[string]any{
		"runtime": map[string]any{
			"account_daily_limit": map[string]any{"enabled": true, "max_per_day": 7},
		},
	})
	rec := httptest.NewRecorder()
	h.updateSettings(rec, httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(payload)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	if !h.Store.RuntimeAccountDailyLimitEnabled() {
		t.Fatal("the switch must be readable through the accessor after the update")
	}
	if got := h.Store.RuntimeAccountDailyLimitMaxPerDay(); got != 7 {
		t.Fatalf("max_per_day=%d want 7", got)
	}
	status := h.Pool.Status()
	if status["daily_limit_max_per_day"] != 7 {
		t.Fatalf("the pool must be pushed the new limit, got %#v", status["daily_limit_max_per_day"])
	}
	today := time.Now().In(time.FixedZone("GMT+8", 8*60*60)).Format("2006-01-02")
	if status["daily_limit_date"] != today {
		t.Fatalf("the pool must count against today, got %#v", status["daily_limit_date"])
	}
}

// 只打开开关而不填数值：保存成功，数值保持未填，号池不因此把账号打成不可用。
func TestUpdateSettingsDailyLimitEnabledWithoutValueKeepsItUnfilled(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{"keys":["k1"],"accounts":[{"email":"a@example.com","token":"t"}]}`)
	t.Setenv("DS2API_ACCOUNT_DAILY_LIMIT_PATH", filepath.Join(t.TempDir(), "account_daily_limit.json"))
	store := config.LoadStore()
	pool := account.NewPool(store)
	h := &Handler{Store: store, Pool: pool}

	payload, _ := json.Marshal(map[string]any{
		"runtime": map[string]any{"account_daily_limit": map[string]any{"enabled": true}},
	})
	rec := httptest.NewRecorder()
	h.updateSettings(rec, httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(payload)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := store.RuntimeAccountDailyLimitMaxPerDay(); got != 0 {
		t.Fatalf("an unfilled value must stay unfilled, got %d", got)
	}
	if pool.IsAccountDailyLimited("a@example.com") {
		t.Fatal("an enabled switch without a value must not mark accounts unavailable")
	}
	if acc, ok := pool.Acquire("", nil, nil); !ok {
		t.Fatal("a request must still be served while no daily value is filled in")
	} else {
		pool.Release(acc.Identifier())
	}
}

// 越界数值被 400 拒绝，字段名与配置文件中的键一致。
func TestUpdateSettingsRejectsBadDailyLimitValue(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	for _, bad := range []int{-1, config.MaxAccountDailyLimitMaxPerDay + 1} {
		payload, _ := json.Marshal(map[string]any{
			"runtime": map[string]any{"account_daily_limit": map[string]any{"max_per_day": bad}},
		})
		rec := httptest.NewRecorder()
		h.updateSettings(rec, httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(payload)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("value %d: expected 400, got %d body=%s", bad, rec.Code, rec.Body.String())
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte("runtime.account_daily_limit.max_per_day")) {
			t.Fatalf("value %d: the error must name the field, got %s", bad, rec.Body.String())
		}
	}
}
