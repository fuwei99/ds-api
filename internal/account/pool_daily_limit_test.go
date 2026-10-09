package account

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ds2api/internal/config"
)

// newDailyLimitPool 构造一个带 24h 速率限制的号池：计数文件落在 t.TempDir() 下，
// 因此测试永远不会碰到真实的 data/account_daily_limit.json。
// dailyLimitJSON 是 runtime.account_daily_limit 的 JSON 片段（例如
// `{"enabled":true,"max_per_day":2}` 或 `{"enabled":true}` 表示开关打开但没填数值）。
func newDailyLimitPool(t *testing.T, dailyLimitJSON string, accounts ...string) (*Pool, *config.Store, string) {
	t.Helper()
	accountsJSON := ""
	for i, acc := range accounts {
		if i > 0 {
			accountsJSON += ","
		}
		accountsJSON += fmt.Sprintf(`{"email":%q,"token":"tok-%s"}`, acc, acc)
	}
	cfgJSON := fmt.Sprintf(`{
		"keys":["test-key"],
		"accounts":[%s],
		"runtime":{
			"account_max_inflight":10,
			"account_max_queue":10,
			"account_daily_limit":%s
		}
	}`, accountsJSON, dailyLimitJSON)

	path := filepath.Join(t.TempDir(), "account_daily_limit.json")
	t.Setenv("DS2API_ACCOUNT_DAILY_LIMIT_PATH", path)
	t.Setenv("DS2API_CONFIG_JSON", cfgJSON)
	store := config.LoadStore()
	return NewPool(store), store, path
}

func writeDailyLimitStateFile(t *testing.T, path, date string, counts map[string]int) {
	t.Helper()
	body, err := json.Marshal(dailyLimitState{Date: date, Counts: counts})
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write state file: %v", err)
	}
}

func readDailyLimitStateFile(t *testing.T, path string) dailyLimitState {
	t.Helper()
	state, err := readDailyLimitState(path)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	return state
}

// acquireOnce 取一次号并立即归还（调用方只关心"能不能取到"）。
func acquireOnce(t *testing.T, pool *Pool) bool {
	t.Helper()
	acc, ok := pool.Acquire("", nil, nil)
	if ok {
		pool.Release(acc.Identifier())
	}
	return ok
}

func TestAccountDailyLimitDisabledByDefaultDoesNotLimitOrPersist(t *testing.T) {
	pool, _, path := newDailyLimitPool(t, `{"enabled":false,"max_per_day":1}`, "acc1@example.com")

	for i := 0; i < 3; i++ {
		if !acquireOnce(t, pool) {
			t.Fatalf("acquire %d failed with the limit disabled", i+1)
		}
	}
	if pool.IsAccountDailyLimited("acc1@example.com") {
		t.Fatal("account must not be daily limited while the feature is off")
	}
	if pool.DailyLimitExhausted("", nil) {
		t.Fatal("DailyLimitExhausted must be false while the feature is off")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a disabled feature must not create a counter file, stat err = %v", err)
	}
}

// 开关打开但没填数值（默认状态）：不生效，也不能把上限当成 0 把账号全打成不可用。
func TestAccountDailyLimitEnabledWithoutValueDoesNotLimit(t *testing.T) {
	pool, _, path := newDailyLimitPool(t, `{"enabled":true}`, "acc1@example.com")

	for i := 0; i < 3; i++ {
		if !acquireOnce(t, pool) {
			t.Fatalf("acquire %d failed while no daily value is filled in", i+1)
		}
	}
	if pool.IsAccountDailyLimited("acc1@example.com") {
		t.Fatal("an enabled limit without a value must not mark accounts unavailable")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("an enabled-but-unset limit must not create a counter file, stat err = %v", err)
	}
}

func TestAccountDailyLimitBlocksAfterMaxRequests(t *testing.T) {
	pool, _, path := newDailyLimitPool(t, `{"enabled":true,"max_per_day":2}`, "acc1@example.com")

	for i := 0; i < 2; i++ {
		if !acquireOnce(t, pool) {
			t.Fatalf("acquire %d must succeed: the Nth request is still served", i+1)
		}
		if i == 0 && pool.IsAccountDailyLimited("acc1@example.com") {
			t.Fatal("account must stay available while it still has quota left")
		}
	}
	if !pool.IsAccountDailyLimited("acc1@example.com") {
		t.Fatal("expected the account to be daily limited once its quota is used up")
	}
	if acquireOnce(t, pool) {
		t.Fatal("the request after the limit must not be served")
	}
	if !pool.DailyLimitExhausted("", nil) {
		t.Fatal("expected DailyLimitExhausted once every candidate is at the limit")
	}

	// 限额用尽后不能排队：等到的是跨天重置，所以取号必须立刻失败。
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if _, ok := pool.AcquireWait(ctx, "", nil, nil); ok {
		t.Fatal("expected AcquireWait to fail once every account is at the daily limit")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("AcquireWait waited %s; it must fail immediately instead of queueing", elapsed)
	}

	if counts := readDailyLimitStateFile(t, path).Counts; counts["acc1@example.com"] != 2 {
		t.Fatalf("expected 2 persisted requests, got %v", counts)
	}
}

func TestAccountDailyLimitTargetAccountFailsWhileOthersServe(t *testing.T) {
	pool, _, _ := newDailyLimitPool(t, `{"enabled":true,"max_per_day":1}`, "acc1@example.com", "acc2@example.com")

	if !acquireOnce(t, pool) {
		t.Fatal("expected the first request to be served")
	}
	limited := "acc1@example.com"
	if !pool.IsAccountDailyLimited(limited) {
		t.Fatalf("%s should be the account that hit the limit", limited)
	}
	if pool.DailyLimitExhausted("", nil) {
		t.Fatal("another account is still under its limit, so the pool is not exhausted")
	}
	// 指定账号模式：被点名的账号已达上限 → 取不到，且失败原因归因于额度。
	if _, ok := pool.Acquire(limited, nil, nil); ok {
		t.Fatal("expected the limited target account to be unavailable")
	}
	if !pool.DailyLimitExhausted(limited, nil) {
		t.Fatal("expected DailyLimitExhausted for the limited target account")
	}
	// 另一个账号仍可服务：队列过滤把两者区分开。
	if pool.IsAccountDailyLimited("acc2@example.com") {
		t.Fatal("the second account must still be under its limit")
	}
}

func TestAccountDailyLimitSurvivesAccountQueueRebuild(t *testing.T) {
	pool, _, _ := newDailyLimitPool(t, `{"enabled":true,"max_per_day":1}`, "acc1@example.com")

	if !acquireOnce(t, pool) {
		t.Fatal("expected the first request to be served")
	}
	// 重建队列（改账号 / 弹性号池补位走的就是这条路）不是新的一天：额度不能重新发。
	pool.Reset()
	if !pool.IsAccountDailyLimited("acc1@example.com") {
		t.Fatal("queue rebuild must keep the day's usage")
	}
	if acquireOnce(t, pool) {
		t.Fatal("a queue rebuild must not hand out a fresh daily quota")
	}
}

func TestAccountDailyLimitFlushesTrailingCountWithoutAnotherRequest(t *testing.T) {
	pool, _, path := newDailyLimitPool(t, `{"enabled":true,"max_per_day":10}`, "acc1@example.com")
	pool.SetDailyLimitPersistInterval(30 * time.Millisecond)

	if !acquireOnce(t, pool) {
		t.Fatal("expected the first request to be served")
	}
	// 节流窗口内的这一笔不立即写盘，但补偿写盘必须把最新计数带走——否则一轮流量
	// 结束后的最后一笔会一直停在旧值上，重启就少算。
	if !acquireOnce(t, pool) {
		t.Fatal("expected the second request to be served")
	}
	// 等补偿写盘落地后再读一次：轮询读取会和写盘的原子重命名抢文件句柄（Windows 上
	// 表现为间歇失败），而这里要断言的是"最终写下了什么"。
	time.Sleep(250 * time.Millisecond)
	if got := readDailyLimitStateFile(t, path).Counts["acc1@example.com"]; got != 2 {
		t.Fatalf("trailing flush did not persist the last count, got %d", got)
	}
}

func TestAccountDailyLimitPersistsAcrossRestartOnSameDay(t *testing.T) {
	pool, _, path := newDailyLimitPool(t, `{"enabled":true,"max_per_day":2}`, "acc1@example.com")

	if !acquireOnce(t, pool) {
		t.Fatal("expected the first request to be served")
	}

	// 新进程：同一份计数文件、同一天 → 沿用当天已用次数（上限 2，还能再来一次）。
	restarted := NewPool(config.LoadStore())
	if !acquireOnce(t, restarted) {
		t.Fatal("the day's remaining quota must survive a restart")
	}
	if acquireOnce(t, restarted) {
		t.Fatal("the restarted pool must still enforce the same limit")
	}
	state := readDailyLimitStateFile(t, path)
	if state.Date != dailyLimitDate(time.Now()) {
		t.Fatalf("state date = %q, want today", state.Date)
	}
	if state.Counts["acc1@example.com"] != 2 {
		t.Fatalf("expected the restarted pool to persist 2 requests, got %v", state.Counts)
	}
}

func TestAccountDailyLimitResetsWhenStartupFindsAnotherDay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "account_daily_limit.json")
	yesterday := time.Now().In(dailyLimitZone).AddDate(0, 0, -1).Format("2006-01-02")
	if yesterday == dailyLimitDate(time.Now()) {
		t.Fatal("test fixture must write a date other than today")
	}
	writeDailyLimitStateFile(t, path, yesterday, map[string]int{"acc1@example.com": 5})

	t.Setenv("DS2API_ACCOUNT_DAILY_LIMIT_PATH", path)
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["test-key"],
		"accounts":[{"email":"acc1@example.com","token":"tok-acc1"}],
		"runtime":{"account_max_inflight":10,"account_max_queue":10,
			"account_daily_limit":{"enabled":true,"max_per_day":1}}
	}`)
	pool := NewPool(config.LoadStore())

	if pool.IsAccountDailyLimited("acc1@example.com") {
		t.Fatal("a counter from another day must not limit the account")
	}
	if !acquireOnce(t, pool) {
		t.Fatal("the new day must start with a fresh quota")
	}
	// 启动检查把翻页后的空账写回，下一行才不会被当成缺文件。
	state := readDailyLimitStateFile(t, path)
	if state.Date != dailyLimitDate(time.Now()) {
		t.Fatalf("startup reset must rewrite the state date, got %q", state.Date)
	}
	if state.Counts["acc1@example.com"] != 1 {
		t.Fatalf("expected the new day's count, got %v", state.Counts)
	}
}

func TestAccountDailyLimitRollsOverWhileRunning(t *testing.T) {
	pool, _, _ := newDailyLimitPool(t, `{"enabled":true,"max_per_day":1}`, "acc1@example.com")

	if !acquireOnce(t, pool) {
		t.Fatal("expected the first request to be served")
	}
	// 进程跨过午夜：计数属于昨天，读取侧按日期判定翻页，账号重新可用。
	pool.mu.Lock()
	pool.dailyDate = time.Now().In(dailyLimitZone).AddDate(0, 0, -1).Format("2006-01-02")
	pool.mu.Unlock()

	if pool.IsAccountDailyLimited("acc1@example.com") {
		t.Fatal("counters from a previous day must be rolled over")
	}
	if !acquireOnce(t, pool) {
		t.Fatal("a new day must restore the quota without a restart")
	}
}

func TestAccountDailyLimitSyncRaisesLimitAndRecoversAccounts(t *testing.T) {
	pool, store, _ := newDailyLimitPool(t, `{"enabled":true,"max_per_day":1}`, "acc1@example.com")

	if !acquireOnce(t, pool) {
		t.Fatal("expected the first request to be served")
	}
	if !pool.IsAccountDailyLimited("acc1@example.com") {
		t.Fatal("expected the account to be limited")
	}
	if err := store.Update(func(c *config.Config) error {
		c.Runtime.AccountDailyLimit.MaxPerDay = 3
		return nil
	}); err != nil {
		t.Fatalf("update config: %v", err)
	}
	pool.SyncDailyLimits()

	if pool.IsAccountDailyLimited("acc1@example.com") {
		t.Fatal("raising the limit must recover the account without a restart")
	}
	if !acquireOnce(t, pool) {
		t.Fatal("expected the raised limit to allow another request")
	}
}

func TestAccountDailyLimitSyncDisablesAndForgetsCounters(t *testing.T) {
	pool, store, _ := newDailyLimitPool(t, `{"enabled":true,"max_per_day":1}`, "acc1@example.com")

	if !acquireOnce(t, pool) {
		t.Fatal("expected the first request to be served")
	}
	if err := store.Update(func(c *config.Config) error {
		c.Runtime.AccountDailyLimit.Enabled = nil
		return nil
	}); err != nil {
		t.Fatalf("update config: %v", err)
	}
	pool.SyncDailyLimits()

	if pool.IsAccountDailyLimited("acc1@example.com") {
		t.Fatal("disabling the limit must clear its counters")
	}
	if !acquireOnce(t, pool) {
		t.Fatal("expected requests to be served once the limit is off")
	}
}

func TestAccountDailyLimitStatusReportsLimitedAccounts(t *testing.T) {
	pool, _, _ := newDailyLimitPool(t, `{"enabled":true,"max_per_day":1}`, "acc1@example.com")

	if !acquireOnce(t, pool) {
		t.Fatal("expected the first request to be served")
	}
	status := pool.Status()
	limited, _ := status["daily_limited_accounts"].([]string)
	if len(limited) != 1 || limited[0] != "acc1@example.com" {
		t.Fatalf("expected the limited account in the status payload, got %v", status["daily_limited_accounts"])
	}
	if status["daily_limit_max_per_day"] != 1 {
		t.Fatalf("expected the configured limit in the status payload, got %v", status["daily_limit_max_per_day"])
	}
	if status["daily_limit_date"] != dailyLimitDate(time.Now()) {
		t.Fatalf("expected today's date in the status payload, got %v", status["daily_limit_date"])
	}
}
