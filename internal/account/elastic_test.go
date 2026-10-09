package account

import (
	"strings"
	"testing"
	"time"

	"ds2api/internal/config"
)

func makeElasticAccount(email string, disabled bool, mutedUntil float64) config.Account {
	return config.Account{
		Email:      email,
		Token:      "token-" + email,
		Disabled:   disabled,
		MutedUntil: mutedUntil,
	}
}

func futureMuteUntil() float64 {
	return float64(time.Now().Unix() + 3600)
}

func TestReconcileElasticPoolDisabledNoOp(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			makeElasticAccount("a@x.com", false, 0),
			makeElasticAccount("b@x.com", true, 0),
		},
		ElasticPool: config.ElasticPoolConfig{Enabled: false, GlobalCount: 1},
	}
	ReconcileElasticPool(cfg)
	if cfg.Accounts[0].Disabled {
		t.Error("account 0 should remain enabled when elastic pool is off")
	}
	if !cfg.Accounts[1].Disabled {
		t.Error("account 1 should remain disabled when elastic pool is off")
	}
}

func TestReconcileElasticPoolGlobalTopN(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			makeElasticAccount("a@x.com", false, 0),
			makeElasticAccount("b@x.com", false, 0),
			makeElasticAccount("c@x.com", false, 0),
			makeElasticAccount("d@x.com", false, 0),
		},
		ElasticPool: config.ElasticPoolConfig{Enabled: true, PerPool: false, GlobalCount: 2},
	}
	ReconcileElasticPool(cfg)
	if cfg.Accounts[0].Disabled {
		t.Error("account 0 (a) should be enabled")
	}
	if cfg.Accounts[1].Disabled {
		t.Error("account 1 (b) should be enabled")
	}
	if !cfg.Accounts[2].Disabled {
		t.Error("account 2 (c) should be disabled")
	}
	if !cfg.Accounts[3].Disabled {
		t.Error("account 3 (d) should be disabled")
	}
}

func TestReconcileElasticPoolMutedDisabledNotCounted(t *testing.T) {
	mute := futureMuteUntil()
	cfg := &config.Config{
		Accounts: []config.Account{
			makeElasticAccount("a@x.com", false, mute),
			makeElasticAccount("b@x.com", false, 0),
			makeElasticAccount("c@x.com", false, 0),
			makeElasticAccount("d@x.com", false, 0),
		},
		ElasticPool: config.ElasticPoolConfig{Enabled: true, PerPool: false, GlobalCount: 2},
	}
	ReconcileElasticPool(cfg)
	if !cfg.Accounts[0].Disabled {
		t.Error("muted account 0 (a) should be disabled")
	}
	if cfg.Accounts[1].Disabled {
		t.Error("account 1 (b) should be enabled to fill the slot")
	}
	if cfg.Accounts[2].Disabled {
		t.Error("account 2 (c) should be enabled to fill the slot")
	}
	if !cfg.Accounts[3].Disabled {
		t.Error("account 3 (d) should be disabled")
	}
}

func TestReconcileElasticPoolBannedDisabledNotCounted(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			{Email: "a@x.com", Token: "ta", Banned: true, Disabled: true, DisabledReason: "账户已被停用"},
			{Email: "b@x.com", Token: "tb"},
			{Email: "c@x.com", Token: "tc"},
			{Email: "d@x.com", Token: "td"},
		},
		ElasticPool: config.ElasticPoolConfig{Enabled: true, PerPool: false, GlobalCount: 2},
	}
	ReconcileElasticPool(cfg)
	if !cfg.Accounts[0].Disabled {
		t.Error("banned account 0 (a) should stay disabled")
	}
	if cfg.Accounts[1].Disabled {
		t.Error("account 1 (b) should be enabled to fill the slot")
	}
	if cfg.Accounts[2].Disabled {
		t.Error("account 2 (c) should be enabled to fill the slot")
	}
	if !cfg.Accounts[3].Disabled {
		t.Error("account 3 (d) should be disabled")
	}
}

func TestReconcileElasticPoolBannedHighPrioritySkipped(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			{Email: "a@x.com", Token: "ta", Priority: 100, Banned: true, Disabled: true},
			{Email: "b@x.com", Token: "tb"},
			{Email: "c@x.com", Token: "tc"},
		},
		ElasticPool: config.ElasticPoolConfig{Enabled: true, PerPool: false, GlobalCount: 2},
	}
	ReconcileElasticPool(cfg)
	if !cfg.Accounts[0].Disabled {
		t.Error("banned high-priority account 0 (a) should stay disabled")
	}
	if cfg.Accounts[1].Disabled || cfg.Accounts[2].Disabled {
		t.Error("healthy accounts b and c should fill both slots")
	}
}

func TestReconcileElasticPoolBanTriggersBackfillFromFront(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			makeElasticAccount("a@x.com", false, 0),
			makeElasticAccount("b@x.com", false, 0),
			makeElasticAccount("c@x.com", false, 0),
		},
		ElasticPool: config.ElasticPoolConfig{Enabled: true, PerPool: false, GlobalCount: 2},
	}
	ReconcileElasticPool(cfg)
	if cfg.Accounts[2].Disabled == false {
		t.Fatal("account 2 (c) should be disabled initially")
	}
	cfg.Accounts[0].MutedUntil = futureMuteUntil()
	ReconcileElasticPool(cfg)
	if !cfg.Accounts[0].Disabled {
		t.Error("banned account 0 (a) should be disabled")
	}
	if cfg.Accounts[1].Disabled {
		t.Error("account 1 (b) should remain enabled")
	}
	if cfg.Accounts[2].Disabled {
		t.Error("account 2 (c) should be backfilled to enabled")
	}
}

func TestReconcileElasticPoolFrontRecoversAfterBanExpires(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			makeElasticAccount("a@x.com", false, 0),
			makeElasticAccount("b@x.com", false, 0),
			makeElasticAccount("c@x.com", true, 0),
		},
		ElasticPool: config.ElasticPoolConfig{Enabled: true, PerPool: false, GlobalCount: 2},
	}
	cfg.Accounts[0].MutedUntil = futureMuteUntil()
	ReconcileElasticPool(cfg)
	if !cfg.Accounts[0].Disabled {
		t.Fatal("banned account 0 should be disabled after first reconcile")
	}
	if cfg.Accounts[2].Disabled {
		t.Fatal("account 2 should be backfilled after first reconcile")
	}
	cfg.Accounts[0].MutedUntil = float64(time.Now().Unix() - 1)
	ReconcileElasticPool(cfg)
	if cfg.Accounts[0].Disabled {
		t.Error("account 0 (a) should be re-enabled after ban expires (front priority)")
	}
	if cfg.Accounts[1].Disabled {
		t.Error("account 1 (b) should remain enabled")
	}
	if !cfg.Accounts[2].Disabled {
		t.Error("account 2 (c) should be disabled again after front recovers")
	}
}

func TestReconcileElasticPoolPerPool(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			{Email: "d1@x.com", Token: "t1", PoolType: "default"},
			{Email: "d2@x.com", Token: "t2", PoolType: "default"},
			{Email: "d3@x.com", Token: "t3", PoolType: "default"},
			{Email: "n1@x.com", Token: "t4", PoolType: "no_tools"},
			{Email: "n2@x.com", Token: "t5", PoolType: "no_tools"},
		},
		ElasticPool: config.ElasticPoolConfig{
			Enabled:        true,
			PerPool:        true,
			DefaultCount:   2,
			NoToolsCount:   1,
			ToolsOnlyCount: 1,
		},
	}
	ReconcileElasticPool(cfg)
	if cfg.Accounts[0].Disabled {
		t.Error("default account 0 should be enabled")
	}
	if cfg.Accounts[1].Disabled {
		t.Error("default account 1 should be enabled")
	}
	if !cfg.Accounts[2].Disabled {
		t.Error("default account 2 should be disabled")
	}
	if cfg.Accounts[3].Disabled {
		t.Error("no_tools account 0 should be enabled")
	}
	if !cfg.Accounts[4].Disabled {
		t.Error("no_tools account 1 should be disabled")
	}
}

func TestReconcileElasticPoolDefaultCountZeroDisablesAll(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			makeElasticAccount("a@x.com", false, 0),
			makeElasticAccount("b@x.com", false, 0),
			makeElasticAccount("c@x.com", false, 0),
		},
		ElasticPool: config.ElasticPoolConfig{Enabled: true, PerPool: false, GlobalCount: 0},
	}
	ReconcileElasticPool(cfg)
	for i, acc := range cfg.Accounts {
		if !acc.Disabled {
			t.Errorf("account %d should be disabled when global_count=0", i)
		}
	}
}

func TestReconcileElasticPoolPrefersHigherPriority(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			{Email: "a@x.com", Token: "ta"},
			{Email: "b@x.com", Token: "tb", Priority: 5},
			{Email: "c@x.com", Token: "tc", Priority: 10},
			{Email: "d@x.com", Token: "td"},
		},
		ElasticPool: config.ElasticPoolConfig{Enabled: true, PerPool: false, GlobalCount: 2},
	}
	ReconcileElasticPool(cfg)
	if cfg.Accounts[2].Disabled {
		t.Error("highest priority account c (10) should be enabled")
	}
	if cfg.Accounts[1].Disabled {
		t.Error("second highest priority account b (5) should be enabled")
	}
	if !cfg.Accounts[0].Disabled {
		t.Error("default priority account a (0) should be disabled")
	}
	if !cfg.Accounts[3].Disabled {
		t.Error("default priority account d (0) should be disabled")
	}
}

func TestReconcileElasticPoolNegativePriorityRanksLast(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			{Email: "a@x.com", Token: "ta"},
			{Email: "b@x.com", Token: "tb", Priority: -10},
			{Email: "c@x.com", Token: "tc"},
		},
		ElasticPool: config.ElasticPoolConfig{Enabled: true, PerPool: false, GlobalCount: 2},
	}
	ReconcileElasticPool(cfg)
	if cfg.Accounts[0].Disabled || cfg.Accounts[2].Disabled {
		t.Error("default priority accounts a and c should be enabled ahead of the negative one")
	}
	if !cfg.Accounts[1].Disabled {
		t.Error("negative priority account b (-10) should be disabled")
	}
}

func TestReconcileElasticPoolEqualPriorityKeepsOriginalOrder(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			{Email: "a@x.com", Token: "ta"},
			{Email: "b@x.com", Token: "tb", Priority: 7},
			{Email: "c@x.com", Token: "tc", Priority: 7},
			{Email: "d@x.com", Token: "td", Priority: 7},
		},
		ElasticPool: config.ElasticPoolConfig{Enabled: true, PerPool: false, GlobalCount: 2},
	}
	ReconcileElasticPool(cfg)
	if !cfg.Accounts[0].Disabled {
		t.Error("lower priority account a (0) should be disabled")
	}
	if cfg.Accounts[1].Disabled || cfg.Accounts[2].Disabled {
		t.Error("equal-priority accounts should be enabled in original order (b then c)")
	}
	if !cfg.Accounts[3].Disabled {
		t.Error("account d should be disabled once the count is exhausted")
	}
}

func TestReconcileElasticPoolPriorityScopedPerPoolType(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			{Email: "d1@x.com", Token: "t1", PoolType: "default"},
			{Email: "d2@x.com", Token: "t2", PoolType: "default", Priority: 3},
			{Email: "n1@x.com", Token: "t3", PoolType: "no_tools", Priority: 99},
		},
		ElasticPool: config.ElasticPoolConfig{
			Enabled:      true,
			PerPool:      true,
			DefaultCount: 1,
			NoToolsCount: 1,
		},
	}
	ReconcileElasticPool(cfg)
	if !cfg.Accounts[0].Disabled {
		t.Error("default account d1 should be disabled behind higher-priority d2")
	}
	if cfg.Accounts[1].Disabled {
		t.Error("default account d2 (priority 3) should be enabled")
	}
	if cfg.Accounts[2].Disabled {
		t.Error("no_tools account n1 should be enabled regardless of default pool priority")
	}
}

func TestReconcileElasticPoolAuthFailedDisabledNotCounted(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			{Email: "a@x.com", Token: "ta", AuthFailed: true, Disabled: true, DisabledReason: "账号鉴权持续失败"},
			{Email: "b@x.com", Token: "tb"},
			{Email: "c@x.com", Token: "tc"},
			{Email: "d@x.com", Token: "td"},
		},
		ElasticPool: config.ElasticPoolConfig{Enabled: true, PerPool: false, GlobalCount: 2},
	}
	ReconcileElasticPool(cfg)
	if !cfg.Accounts[0].Disabled {
		t.Error("auth-failed account 0 (a) should stay disabled")
	}
	if cfg.Accounts[1].Disabled {
		t.Error("account 1 (b) should be enabled to fill the slot")
	}
	if cfg.Accounts[2].Disabled {
		t.Error("account 2 (c) should be enabled to fill the slot")
	}
	if !cfg.Accounts[3].Disabled {
		t.Error("account 3 (d) should be disabled")
	}
}

func TestReconcileElasticPoolAuthFailedBackfillsStandby(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			{Email: "a@x.com", Token: "ta"},
			{Email: "b@x.com", Token: "tb"},
			{Email: "c@x.com", Token: "tc"},
		},
		ElasticPool: config.ElasticPoolConfig{Enabled: true, PerPool: false, GlobalCount: 2},
	}
	ReconcileElasticPool(cfg)
	if !cfg.Accounts[2].Disabled {
		t.Fatal("account 2 (c) should start disabled")
	}
	// 账号 a 被判定为鉴权异常：让出名额，休眠账号 c 自动补位。
	cfg.Accounts[0].AuthFailed = true
	ReconcileElasticPool(cfg)
	if !cfg.Accounts[0].Disabled {
		t.Error("auth-failed account 0 (a) should be disabled")
	}
	if cfg.Accounts[1].Disabled {
		t.Error("account 1 (b) should remain enabled")
	}
	if cfg.Accounts[2].Disabled {
		t.Error("account 2 (c) should be backfilled to enabled")
	}
}

func TestDisableAllAccounts(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			makeElasticAccount("a@x.com", true, 0),
			makeElasticAccount("b@x.com", true, 0),
		},
	}
	DisableAllAccounts(cfg)
	for i, acc := range cfg.Accounts {
		if acc.Disabled {
			t.Errorf("account %d should be enabled after DisableAllAccounts", i)
		}
	}
}

// 弹性号池改小每批启用数时，正在生成回复（busy）的账号必须保持启用：
// 把它置为 Disabled 会让代理桥解除其托管绑定、重写 mihomo 运行时配置并全量
// 重启进程，正在流式输出的回复会被直接掐断。
func TestReconcileElasticPoolWithBusyKeepsGeneratingAccount(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			{Email: "a@x.com", Token: "ta"},
			{Email: "b@x.com", Token: "tb"},
			{Email: "c@x.com", Token: "tc"},
		},
		ElasticPool: config.ElasticPoolConfig{Enabled: true, PerPool: false, GlobalCount: 1},
	}
	ReconcileElasticPool(cfg)
	if !cfg.Accounts[2].Disabled {
		t.Fatal("account 2 (c) should start disabled with count=1")
	}

	// c 正有一条回复在生成：本次重算必须放过它（即使它超出启用的前 N 个）。
	busy := map[string]bool{"c@x.com": true}
	ReconcileElasticPoolWithBusy(cfg, busy)
	if cfg.Accounts[0].Disabled {
		t.Error("account 0 (a) should stay enabled")
	}
	if cfg.Accounts[2].Disabled {
		t.Error("generating account 2 (c) must not be disabled by the count change")
	}
	if !cfg.Accounts[1].Disabled {
		t.Error("account 1 (b) should give up its slot to the generating account")
	}

	// 回复结束后下一次重算按新数量收敛：c 回到休眠。
	ReconcileElasticPoolWithBusy(cfg, nil)
	if !cfg.Accounts[2].Disabled {
		t.Error("account 2 (c) should sleep once it stops generating")
	}
	if cfg.Accounts[0].Disabled {
		t.Error("account 0 (a) should remain enabled")
	}
}

// busy 保护同时覆盖 device_id 绑定：正在生成回复的账号不能被解除绑定，
// 否则它的 device_id 可能被立刻分配给另一个账号，两个账号会共用同一设备号。
func TestReconcileElasticPoolWithBusyKeepsDeviceIDBinding(t *testing.T) {
	deviceID := strings.Repeat("A", 86) + "=="
	cfg := &config.Config{
		Accounts: []config.Account{
			{Email: "a@x.com", Token: "ta"},
			{Email: "b@x.com", Token: "tb", DeviceID: deviceID, DeviceIDType: config.DeviceIDTypeManual},
		},
		DeviceIDPool: config.DeviceIDPoolConfig{Items: []config.DeviceIDPoolItem{{ID: deviceID, Bound: 1}}},
		Runtime:      config.RuntimeConfig{DeviceIDMode: config.DeviceIDTypeManual},
		ElasticPool:  config.ElasticPoolConfig{Enabled: true, PerPool: false, GlobalCount: 1},
	}

	ReconcileElasticPoolWithBusy(cfg, map[string]bool{"b@x.com": true})
	if cfg.Accounts[1].Disabled {
		t.Fatal("generating account b must stay enabled")
	}
	if cfg.Accounts[1].DeviceID != deviceID {
		t.Errorf("generating account b must keep its device_id, got %q", cfg.Accounts[1].DeviceID)
	}

	// 没有在途请求时，b 超出启用数，绑定随之解除。
	ReconcileElasticPoolWithBusy(cfg, nil)
	if !cfg.Accounts[1].Disabled {
		t.Fatal("account b should sleep once idle")
	}
	if cfg.Accounts[1].DeviceID != "" {
		t.Errorf("idle disabled account should release its device_id, got %q", cfg.Accounts[1].DeviceID)
	}
}

// 硬状态不受 busy 保护：账号刚被封禁/禁言/判为鉴权异常时必须立刻让出名额。
func TestReconcileElasticPoolWithBusyStillDisablesHardState(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			{Email: "a@x.com", Token: "ta"},
			{Email: "b@x.com", Token: "tb", Banned: true},
		},
		ElasticPool: config.ElasticPoolConfig{Enabled: true, PerPool: false, GlobalCount: 2},
	}
	ReconcileElasticPoolWithBusy(cfg, map[string]bool{"b@x.com": true})
	if !cfg.Accounts[1].Disabled {
		t.Error("banned account must be disabled even while a request is in flight")
	}
}
