package mihomo

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ds2api/internal/config"
)

// newAutoBindTestStore 构造含启用/禁用账号、手动代理账号与 3 个节点的内存配置。
// 初始桥未启用（避免 BindAccount/Apply 需要真实 mihomo 二进制），
// 需要自动调度时通过 enableAutoBridge 打开。
func newAutoBindTestStore(t *testing.T) *config.Store {
	t.Helper()
	t.Setenv("DS2API_MIHOMO_DIR", filepath.Join(t.TempDir(), "mihomo"))
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys": ["k1"],
		"accounts": [
			{"email": "a@test.com", "password": "x"},
			{"email": "b@test.com", "password": "x"},
			{"email": "c@test.com", "password": "x", "disabled": true},
			{"email": "d@test.com", "password": "x", "proxy_id": "manual-1"}
		],
		"proxies": [
			{"id": "manual-1", "name": "手动代理", "type": "socks5", "host": "10.0.0.1", "port": 1080}
		],
		"mihomo": {
			"enabled": false,
			"subscriptions": [{
				"id": "sub-1",
				"name": "测试机场",
				"url": "https://example.com/sub",
				"nodes": [
					{"name": "香港 01", "type": "ss", "raw": {"name": "香港 01", "type": "ss", "server": "hk.example.com", "port": 8388, "cipher": "aes-128-gcm", "password": "pw"}},
					{"name": "日本 01", "type": "vmess", "raw": {"name": "日本 01", "type": "vmess", "server": "jp.example.com", "port": 443, "uuid": "u-u-i-d"}},
					{"name": "美国 01", "type": "trojan", "raw": {"name": "美国 01", "type": "trojan", "server": "us.example.com", "port": 443, "password": "pw"}}
				]
			}]
		}
	}`)
	return config.LoadStore()
}

func nodeKeys3() (hk, jp, us string) {
	return config.MihomoNodeKey("sub-1", "香港 01"),
		config.MihomoNodeKey("sub-1", "日本 01"),
		config.MihomoNodeKey("sub-1", "美国 01")
}

func markHealth(m *Manager, key, status string, latency, streak int) {
	m.setNodeHealth(key, NodeHealth{
		Status:     status,
		LatencyMS:  latency,
		FailStreak: streak,
		TestedAt:   time.Now().Unix(),
	})
}

// enableAutoBridge 打开桥与自动调度开关（reconcile 生效前提）。
func enableAutoBridge(t *testing.T, store *config.Store) {
	t.Helper()
	if err := store.Update(func(c *config.Config) error {
		c.Mihomo.Enabled = true
		c.Mihomo.AutoBind = true
		return nil
	}); err != nil {
		t.Fatalf("enable auto bridge failed: %v", err)
	}
}

func accountProxyID(t *testing.T, store *config.Store, identifier string) string {
	t.Helper()
	acc, ok := store.FindAccount(identifier)
	if !ok {
		t.Fatalf("account %s not found", identifier)
	}
	return strings.TrimSpace(acc.ProxyID)
}

// autoBind 直接写入一个"自动调度管理"的托管绑定（等价于一键分配/补位的结果）。
// BindAccount 现在会把账号标记为手动绑定（退出自动调度），所以需要自动调度
// 接管的测试走这个入口，才能构造出"账号由自动调度管理"的初始状态。
func autoBind(t *testing.T, mgr *Manager, store *config.Store, identifier, nodeKey string) {
	t.Helper()
	if err := store.Update(func(c *config.Config) error {
		idx := -1
		for i := range c.Accounts {
			if c.Accounts[i].Email == identifier {
				idx = i
			}
		}
		if idx < 0 {
			return fmt.Errorf("账号不存在: %s", identifier)
		}
		node, ok := c.Mihomo.FindMihomoNode(nodeKey)
		if !ok {
			return fmt.Errorf("节点不存在: %s", nodeKey)
		}
		port := c.Mihomo.AllocateMihomoPort(nodeKey)
		proxyID := config.MihomoManagedProxyID(nodeKey)
		found := false
		for i := range c.Proxies {
			if strings.TrimSpace(c.Proxies[i].ID) == proxyID {
				found = true
			}
		}
		if !found {
			c.Proxies = append(c.Proxies, config.Proxy{
				ID: proxyID, Name: node.Name, Type: "socks5", Host: "127.0.0.1", Port: port,
			})
		}
		c.Accounts[idx].ProxyID = proxyID
		c.Accounts[idx].ManualBinding = false
		return nil
	}); err != nil {
		t.Fatalf("auto bind %s failed: %v", identifier, err)
	}
	// 重建"托管代理 -> 节点"运行态索引，等价于真实绑定入口结束时的 resetPool，
	// 否则真实流量反馈（ReportUpstreamResult）找不到该节点。
	mgr.resetPool()
}

func TestAssignAccountsSkipsHealthFailedNodes(t *testing.T) {
	store := newBindingTestStore(t)
	mgr := NewManager(store, nil)
	hk := config.MihomoNodeKey("sub-1", "香港 01")
	jp := config.MihomoNodeKey("sub-1", "日本 01")
	markHealth(mgr, jp, NodeHealthFail, 0, 1)

	bound, err := mgr.AssignAccounts(context.Background(), []string{jp, hk})
	if err != nil {
		t.Fatalf("assign failed: %v", err)
	}
	if bound != 2 {
		t.Fatalf("expected 2 bound, got %d", bound)
	}
	pa := proxyIDOf(t, store, "a@test.com")
	pb := proxyIDOf(t, store, "b@test.com")
	if pa != config.MihomoManagedProxyID(hk) || pb != config.MihomoManagedProxyID(hk) {
		t.Fatalf("failed node must be skipped: a=%s b=%s", pa, pb)
	}
	if _, ok := store.Snapshot().Mihomo.PortMap[jp]; ok {
		t.Fatal("failed node should not get a port allocation")
	}
}

func TestReconcileFailoverMovesAccountOffDeadNode(t *testing.T) {
	store := newAutoBindTestStore(t)
	mgr := NewManager(store, nil)
	hk, jp, _ := nodeKeys3()

	autoBind(t, mgr, store, "a@test.com", hk)
	autoBind(t, mgr, store, "b@test.com", jp)
	enableAutoBridge(t, store)

	markHealth(mgr, hk, NodeHealthFail, 0, 5) // 连续失败达到阈值
	markHealth(mgr, jp, NodeHealthOK, 120, 0)

	if !mgr.reconcileBindings() {
		t.Fatal("expected reconcile to apply failover")
	}
	us := config.MihomoNodeKey("sub-1", "美国 01")
	if got := accountProxyID(t, store, "a@test.com"); got != config.MihomoManagedProxyID(us) {
		t.Errorf("a@test.com should fail over to 美国 01, got %q", got)
	}
	if got := accountProxyID(t, store, "b@test.com"); got != config.MihomoManagedProxyID(jp) {
		t.Errorf("b@test.com should stay on healthy node, got %q", got)
	}
}

func TestReconcileIgnoresSingleFlap(t *testing.T) {
	store := newAutoBindTestStore(t)
	mgr := NewManager(store, nil)
	hk, jp, _ := nodeKeys3()

	autoBind(t, mgr, store, "a@test.com", hk)
	autoBind(t, mgr, store, "b@test.com", jp)
	enableAutoBridge(t, store)

	// 单次失败未达阈值：不触发故障转移。
	markHealth(mgr, hk, NodeHealthFail, 0, 1)
	if mgr.reconcileBindings() {
		t.Fatal("single flap should not trigger failover")
	}
	if got := accountProxyID(t, store, "a@test.com"); got != config.MihomoManagedProxyID(hk) {
		t.Errorf("binding should be unchanged, got %q", got)
	}
}

func TestReconcileBackfillNewlyEnabledAccount(t *testing.T) {
	store := newAutoBindTestStore(t)
	mgr := NewManager(store, nil)
	hk, jp, _ := nodeKeys3()

	autoBind(t, mgr, store, "a@test.com", hk)
	enableAutoBridge(t, store)
	markHealth(mgr, hk, NodeHealthOK, 80, 0)
	markHealth(mgr, jp, NodeHealthOK, 90, 0)

	// b@test.com 相当于弹性号池刚补位上线的账号：无代理、已启用。
	if !mgr.reconcileBindings() {
		t.Fatal("expected reconcile to backfill unbound account")
	}
	if got := accountProxyID(t, store, "b@test.com"); got != config.MihomoManagedProxyID(jp) {
		t.Errorf("backfill should pick least-loaded healthy node, got %q", got)
	}
	if got := accountProxyID(t, store, "c@test.com"); got != "" {
		t.Errorf("disabled account should not be backfilled, got %q", got)
	}
	if got := accountProxyID(t, store, "d@test.com"); got != "manual-1" {
		t.Errorf("manual proxy overwritten: %q", got)
	}
}

func TestReconcileKeepsBindingsWhenNoHealthyCandidate(t *testing.T) {
	store := newAutoBindTestStore(t)
	mgr := NewManager(store, nil)
	hk, jp, us := nodeKeys3()

	autoBind(t, mgr, store, "a@test.com", hk)
	autoBind(t, mgr, store, "b@test.com", jp)
	enableAutoBridge(t, store)

	markHealth(mgr, hk, NodeHealthFail, 0, 5)
	markHealth(mgr, jp, NodeHealthFail, 0, 5)
	markHealth(mgr, us, NodeHealthFail, 0, 5)
	if mgr.reconcileBindings() {
		t.Fatal("reconcile should be no-op without healthy candidates")
	}
	if got := accountProxyID(t, store, "a@test.com"); got != config.MihomoManagedProxyID(hk) {
		t.Errorf("binding should be kept when no backup available, got %q", got)
	}
}

func TestReconcileRequiresAutoBind(t *testing.T) {
	store := newAutoBindTestStore(t)
	mgr := NewManager(store, nil)
	hk, jp, _ := nodeKeys3()

	autoBind(t, mgr, store, "a@test.com", hk)
	autoBind(t, mgr, store, "b@test.com", jp)
	// 只开桥、不开 auto_bind：不做任何自动调度。
	if err := store.Update(func(c *config.Config) error {
		c.Mihomo.Enabled = true
		return nil
	}); err != nil {
		t.Fatalf("enable bridge failed: %v", err)
	}
	markHealth(mgr, hk, NodeHealthFail, 0, 5)
	if mgr.reconcileBindings() {
		t.Fatal("reconcile must be gated by auto_bind")
	}
}

func TestReconcileSkipsManuallyUnboundAccount(t *testing.T) {
	store := newAutoBindTestStore(t)
	mgr := NewManager(store, nil)
	hk, jp, _ := nodeKeys3()

	// b 绑定到 hk（避免 reconcile 为 b 补位）；a 绑定后显式解绑（NoProxy）。
	autoBind(t, mgr, store, "b@test.com", hk)
	if err := mgr.BindAccount(context.Background(), "a@test.com", hk); err != nil {
		t.Fatalf("bind a failed: %v", err)
	}
	if err := mgr.BindAccount(context.Background(), "a@test.com", ""); err != nil {
		t.Fatalf("unbind a failed: %v", err)
	}
	acc, ok := store.FindAccount("a@test.com")
	if !ok || !acc.NoProxy {
		t.Fatal("manual unbind must set no_proxy")
	}
	enableAutoBridge(t, store)
	markHealth(mgr, jp, NodeHealthOK, 50, 0)

	if mgr.reconcileBindings() {
		t.Fatal("reconcile must not reassign a manually unbound account")
	}
	if got := accountProxyID(t, store, "a@test.com"); got != "" {
		t.Fatalf("manually unbound account must stay unbound, got %q", got)
	}

	// 显式重新绑定节点后，账号重新走代理（no_proxy 清除）。
	if err := mgr.BindAccount(context.Background(), "a@test.com", jp); err != nil {
		t.Fatalf("rebind failed: %v", err)
	}
	acc, _ = store.FindAccount("a@test.com")
	if acc.NoProxy {
		t.Fatal("explicit rebind must clear no_proxy")
	}
}

func TestAssignAccountsSkipsManuallyUnboundAccount(t *testing.T) {
	store := newBindingTestStore(t)
	mgr := NewManager(store, nil)
	hk, jp, _ := nodeKeys3()

	if err := mgr.BindAccount(context.Background(), "b@test.com", hk); err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	if err := mgr.BindAccount(context.Background(), "b@test.com", ""); err != nil {
		t.Fatalf("unbind failed: %v", err)
	}
	markHealth(mgr, jp, NodeHealthOK, 40, 0)

	bound, err := mgr.AssignAccounts(context.Background(), []string{jp})
	if err != nil {
		t.Fatalf("assign failed: %v", err)
	}
	if bound != 1 {
		t.Fatalf("expected 1 bound (manually unbound account skipped), got %d", bound)
	}
	if got := accountProxyID(t, store, "a@test.com"); got != config.MihomoManagedProxyID(jp) {
		t.Fatalf("a@test.com should be assigned, got %q", got)
	}
	if got := accountProxyID(t, store, "b@test.com"); got != "" {
		t.Fatalf("manually unbound account must be skipped by one-click assign, got %q", got)
	}
}

func TestRequestReconcileEnqueuesNonBlocking(t *testing.T) {
	mgr := NewManager(newAutoBindTestStore(t), nil)
	mgr.RequestReconcile()
	mgr.RequestReconcile() // 二次请求应被缓冲（容量 1）去抖，不阻塞
	select {
	case <-mgr.reconcileCh:
	default:
		t.Fatal("expected one reconcile signal after RequestReconcile")
	}
	select {
	case <-mgr.reconcileCh:
		t.Fatal("reconcile channel must be deduped to a single pending signal")
	default:
	}
	// nil manager / 未初始化通道应安全跳过。
	var nilMgr *Manager
	nilMgr.RequestReconcile()
}

func TestListNodesExposesHealth(t *testing.T) {
	store := newAutoBindTestStore(t)
	mgr := NewManager(store, nil)
	hk, _, _ := nodeKeys3()
	markHealth(mgr, hk, NodeHealthOK, 88, 0)

	nodes := mgr.ListNodes()
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(nodes))
	}
	for _, node := range nodes {
		if node["node_key"] == hk {
			if node["health"] != NodeHealthOK || node["latency_ms"] != 88 {
				t.Errorf("health not merged: %v", node)
			}
			return
		}
	}
	t.Fatal("node not found in ListNodes")
}

func TestHealthSummaryCounts(t *testing.T) {
	store := newAutoBindTestStore(t)
	mgr := NewManager(store, nil)
	hk, jp, _ := nodeKeys3()
	markHealth(mgr, hk, NodeHealthOK, 80, 0)
	markHealth(mgr, jp, NodeHealthFail, 0, 2)

	keys := []string{hk, jp, config.MihomoNodeKey("sub-1", "美国 01")}
	summary := mgr.healthSummary(keys)
	if summary["available"] != 1 || summary["dead"] != 1 || summary["unknown"] != 1 {
		t.Errorf("unexpected summary: %v", summary)
	}
}

func TestAssignAccountsSkipsDisabledAccounts(t *testing.T) {
	store := newBindingTestStore(t)
	if err := store.Update(func(c *config.Config) error {
		c.Accounts = append(c.Accounts, config.Account{Email: "c@test.com", Password: "x", Disabled: true})
		return nil
	}); err != nil {
		t.Fatalf("seed disabled account failed: %v", err)
	}
	mgr := NewManager(store, nil)
	hk := config.MihomoNodeKey("sub-1", "香港 01")
	jp := config.MihomoNodeKey("sub-1", "日本 01")

	bound, err := mgr.AssignAccounts(context.Background(), []string{jp, hk})
	if err != nil {
		t.Fatalf("assign failed: %v", err)
	}
	if bound != 2 {
		t.Fatalf("expected 2 bound (enabled only), got %d", bound)
	}
	if got := proxyIDOf(t, store, "a@test.com"); got == "" {
		t.Fatal("enabled account should be bound")
	}
	if got := proxyIDOf(t, store, "c@test.com"); got != "" {
		t.Errorf("disabled account must not be bound, got %q", got)
	}
}

func TestReconcileUnbindsDisabledAccount(t *testing.T) {
	store := newAutoBindTestStore(t)
	mgr := NewManager(store, nil)
	hk, jp, us := nodeKeys3()

	// 绑定两个启用账号 + 一个已禁用账号，模拟禁用账号残留的托管绑定。
	autoBind(t, mgr, store, "a@test.com", hk)
	autoBind(t, mgr, store, "b@test.com", jp)
	autoBind(t, mgr, store, "c@test.com", us)
	enableAutoBridge(t, store)
	markHealth(mgr, jp, NodeHealthOK, 50, 0)

	if !mgr.reconcileBindings() {
		t.Fatal("expected reconcile to release disabled account binding")
	}
	if got := accountProxyID(t, store, "c@test.com"); got != "" {
		t.Errorf("disabled account binding must be released, got %q", got)
	}
	// 释放的节点端口被 GC 回收（该节点不再被任何账号引用）。
	if _, ok := store.Snapshot().Mihomo.PortMap[us]; ok {
		t.Error("released port should be reclaimed")
	}
	// 启用账号的绑定不受影响。
	if accountProxyID(t, store, "a@test.com") == "" || accountProxyID(t, store, "b@test.com") == "" {
		t.Error("enabled accounts must keep their bindings")
	}
}

func TestReconcileFailoverCooldownBlocksRepeatedMove(t *testing.T) {
	store := newAutoBindTestStore(t)
	mgr := NewManager(store, nil)
	hk, jp, us := nodeKeys3()

	autoBind(t, mgr, store, "a@test.com", hk)
	enableAutoBridge(t, store)

	// 第一轮：hk 死掉，a 应安全转移到 jp，并进入换号冷却期。
	markHealth(mgr, hk, NodeHealthFail, 0, 5)
	markHealth(mgr, jp, NodeHealthOK, 50, 0)
	markHealth(mgr, us, NodeHealthOK, 60, 0)
	if !mgr.reconcileBindings() {
		t.Fatal("expected first reconcile to failover")
	}
	if got := accountProxyID(t, store, "a@test.com"); got != config.MihomoManagedProxyID(jp) {
		t.Fatalf("a@test.com should move to 日本 01, got %q", got)
	}
	acc, _ := store.FindAccount("a@test.com")
	if acc.NodeCooldownUntil <= float64(time.Now().Unix()) {
		t.Fatalf("failover must set node cooldown, got %v", acc.NodeCooldownUntil)
	}

	// 冷却期内：jp 也死掉，a 不应再被自动切换（避免反复横跳）。
	markHealth(mgr, jp, NodeHealthFail, 0, 5)
	if mgr.reconcileBindings() {
		t.Fatal("reconcile should not move account during cooldown")
	}
	if got := accountProxyID(t, store, "a@test.com"); got != config.MihomoManagedProxyID(jp) {
		t.Fatalf("account must stay put during cooldown, got %q", got)
	}

	// 冷却到期后：再次发现死节点，应允许转移到新的健康节点。
	if err := store.Update(func(c *config.Config) error {
		for i := range c.Accounts {
			if c.Accounts[i].Email == "a@test.com" {
				c.Accounts[i].NodeCooldownUntil = 0
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("clear cooldown failed: %v", err)
	}
	if !mgr.reconcileBindings() {
		t.Fatal("expected reconcile to failover after cooldown expires")
	}
	if got := accountProxyID(t, store, "a@test.com"); got != config.MihomoManagedProxyID(us) {
		t.Fatalf("a@test.com should move to 美国 01 after cooldown, got %q", got)
	}
}

func TestReportUpstreamFailureMarksNodeAndTriggersReconcile(t *testing.T) {
	store := newAutoBindTestStore(t)
	mgr := NewManager(store, nil)
	hk, jp, _ := nodeKeys3()

	autoBind(t, mgr, store, "a@test.com", hk)
	enableAutoBridge(t, store)
	markHealth(mgr, jp, NodeHealthOK, 50, 0)
	t.Setenv("DS2API_MIHOMO_FAIL_THRESHOLD", "1")

	// 单次真实请求失败即达到阈值：节点应被立即标 fail。
	proxyID := accountProxyID(t, store, "a@test.com")
	mgr.ReportUpstreamResult(proxyID, false)
	if h := mgr.NodeHealthMap()[hk]; h.Status != NodeHealthFail {
		t.Fatalf("node should be marked fail by real traffic, got %+v", h)
	}

	// watcher 消费触发信号后立即调和：账号转移到健康节点，不等下一拍巡检。
	if !mgr.reconcileBindings() {
		t.Fatal("expected immediate reconcile after real failure feedback")
	}
	if got := accountProxyID(t, store, "a@test.com"); got != config.MihomoManagedProxyID(jp) {
		t.Fatalf("account should fail over immediately, got %q", got)
	}
}

func TestReportUpstreamSuccessResetsRealFailFeedback(t *testing.T) {
	store := newAutoBindTestStore(t)
	mgr := NewManager(store, nil)
	hk, _, _ := nodeKeys3()

	if err := mgr.BindAccount(context.Background(), "a@test.com", hk); err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	enableAutoBridge(t, store)
	t.Setenv("DS2API_MIHOMO_FAIL_THRESHOLD", "2")

	proxyID := accountProxyID(t, store, "a@test.com")
	mgr.ReportUpstreamResult(proxyID, false) // streak=1
	mgr.ReportUpstreamResult(proxyID, true)  // 成功清零
	mgr.ReportUpstreamResult(proxyID, false) // streak=1，未达阈值

	if h := mgr.NodeHealthMap()[hk]; h.Status == NodeHealthFail {
		t.Fatalf("successful request must reset real failure feedback, got %+v", h)
	}
}

func TestReportUpstreamIgnoresNonManagedProxy(t *testing.T) {
	store := newAutoBindTestStore(t)
	mgr := NewManager(store, nil)
	t.Setenv("DS2API_MIHOMO_FAIL_THRESHOLD", "1")

	// 手动代理 ID 不属于 mihomo 托管：不得影响任何节点健康。
	mgr.ReportUpstreamResult("manual-1", false)
	for key, h := range mgr.NodeHealthMap() {
		if h.Status == NodeHealthFail {
			t.Fatalf("non-managed proxy must be ignored, node %s got %+v", key, h)
		}
	}
}

func TestSweepTargetsSweepsAllUnboundNodes(t *testing.T) {
	store := newAutoBindTestStore(t)
	snap := store.Snapshot()

	targets := sweepTargets(snap, map[string]NodeHealth{})
	if len(targets) != 3 {
		t.Fatalf("unbound nodes should all be swept, got %d: %v", len(targets), targets)
	}
}

func TestSweepTargetsCapsColdProbeSample(t *testing.T) {
	store := newAutoBindTestStore(t)
	if err := store.Update(func(c *config.Config) error {
		nodes := c.Mihomo.Subscriptions[0].Nodes
		for i := 0; i < 20; i++ {
			name := fmt.Sprintf("冷节点 %02d", i)
			nodes = append(nodes, config.MihomoNode{Name: name, Type: "ss", Raw: map[string]any{
				"name": name, "type": "ss", "server": "cold.example.com", "port": 8388,
			}})
		}
		c.Mihomo.Subscriptions[0].Nodes = nodes
		return nil
	}); err != nil {
		t.Fatalf("seed nodes failed: %v", err)
	}

	targets := sweepTargets(store.Snapshot(), map[string]NodeHealth{})
	if len(targets) != coldProbePerTick {
		t.Fatalf("cold probe should be capped at %d, got %d", coldProbePerTick, len(targets))
	}
}

func TestTestLatencyPopulatesHealthPool(t *testing.T) {
	srv := newFakeController(map[string]int64{"香港 01": 120})
	defer srv.Close()
	port := srv.Listener.Addr().(*net.TCPAddr).Port

	mgr := runningMgr(newDelayTestStore(t, port))
	if _, err := mgr.TestLatency(context.Background()); err != nil {
		t.Fatalf("TestLatency failed: %v", err)
	}
	health := mgr.NodeHealthMap()
	hk := config.MihomoNodeKey("sub-1", "香港 01")
	slow := config.MihomoNodeKey("sub-1", "超时节点")
	if h := health[hk]; h.Status != NodeHealthOK || h.LatencyMS != 120 {
		t.Errorf("healthy node not recorded: %+v", h)
	}
	if h := health[slow]; h.Status != NodeHealthFail {
		t.Errorf("failed node not recorded as fail: %+v", h)
	}
}

func TestTestLatencyPersistsHealthToConfig(t *testing.T) {
	srv := newFakeController(nil)
	defer srv.Close()
	port := srv.Listener.Addr().(*net.TCPAddr).Port

	store := newDelayTestStore(t, port)
	mgr := runningMgr(store)
	if _, err := mgr.TestLatency(context.Background()); err != nil {
		t.Fatalf("TestLatency failed: %v", err)
	}
	// 延迟直接写在订阅节点字段上。
	hk := config.MihomoNodeKey("sub-1", "香港 01")
	slow := config.MihomoNodeKey("sub-1", "超时节点")
	nodes := map[string]config.MihomoNode{}
	for _, sub := range store.Snapshot().Mihomo.Subscriptions {
		for _, node := range sub.Nodes {
			nodes[config.MihomoNodeKey(sub.ID, node.Name)] = node
		}
	}
	if n := nodes[hk]; n.Status != NodeHealthOK || n.LatencyMS != 50 {
		t.Errorf("ok node latency not persisted on node: %+v", n)
	}
	if n := nodes[slow]; n.Status != NodeHealthFail {
		t.Errorf("fail node status not persisted on node: %+v", n)
	}
}

// fakePool 实现 PoolResetter，用于驱动自动调度的让路行为。
type fakePool struct {
	inflight bool
	resets   int
}

func (f *fakePool) Reset()            { f.resets++ }
func (f *fakePool) HasInflight() bool { return f.inflight }

// 有请求正在生成回复时，自动调度整体让路：绑定变更会改写 runtime.yaml 的
// listeners 并让 mihomo 全量重启，把在途流式连接一起掐断。
func TestReconcileDefersWhileRequestInFlight(t *testing.T) {
	store := newAutoBindTestStore(t)
	pool := &fakePool{}
	mgr := NewManager(store, pool)
	hk, jp, _ := nodeKeys3()

	// a 先绑到 hk，只留 b 待补位。
	if err := mgr.BindAccount(context.Background(), "a@test.com", hk); err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	enableAutoBridge(t, store)
	markHealth(mgr, hk, NodeHealthOK, 80, 0)
	markHealth(mgr, jp, NodeHealthOK, 90, 0)

	// 在途期间：整体让路，b 不被补位。
	pool.inflight = true
	if mgr.reconcileBindings() {
		t.Fatal("reconcile must be deferred while a request is generating")
	}
	if got := accountProxyID(t, store, "b@test.com"); got != "" {
		t.Fatalf("no binding change may happen while in flight, got %q", got)
	}

	// 请求结束后（下一轮巡检）恢复自动补位：b 拿到负载最低的 jp。
	pool.inflight = false
	if !mgr.reconcileBindings() {
		t.Fatal("reconcile should resume once the pool is idle")
	}
	if got := accountProxyID(t, store, "b@test.com"); got != config.MihomoManagedProxyID(jp) {
		t.Errorf("backfill should resume after the request finishes, got %q", got)
	}
}

// 手动绑定的账号完全退出自动调度：即使它绑定的节点已判定死亡，也不做故障转移，
// 不换节点、不回收绑定与端口——由请求自己报错（用户显式选择优先于自动容错）。
// 同一节点上由自动调度管理的账号仍然照常转移。
func TestReconcileLeavesManuallyBoundAccountOnDeadNode(t *testing.T) {
	store := newBindingTestStore(t)
	mgr := NewManager(store, nil)
	hk := config.MihomoNodeKey("sub-1", "香港 01")
	jp := config.MihomoNodeKey("sub-1", "日本 01")

	// a、b 先由一键分配（自动调度管理）绑到 hk，随后 a 被用户手动重绑到 hk。
	if _, err := mgr.AssignAccounts(context.Background(), []string{hk}); err != nil {
		t.Fatalf("assign failed: %v", err)
	}
	if err := mgr.BindAccount(context.Background(), "a@test.com", hk); err != nil {
		t.Fatalf("manual bind failed: %v", err)
	}
	enableAutoBridge(t, store)
	markHealth(mgr, jp, NodeHealthOK, 50, 0)
	markHealth(mgr, hk, NodeHealthFail, 0, 5) // hk 连续失败达到阈值

	mgr.reconcileBindings()

	if got := accountProxyID(t, store, "a@test.com"); got != config.MihomoManagedProxyID(hk) {
		t.Fatalf("manually bound account must keep its dead node, got %q", got)
	}
	if got := accountProxyID(t, store, "b@test.com"); got != config.MihomoManagedProxyID(jp) {
		t.Fatalf("auto-managed account should fail over to the healthy node, got %q", got)
	}
	// hk 的端口分配被 a 继续占用（手动绑定的账号不回收端口）。
	if _, ok := store.Snapshot().Mihomo.PortMap[hk]; !ok {
		t.Fatal("manual binding must keep its port allocation")
	}
}

// 手动绑定的账号在停用（弹性号池休眠/封号）期间保留绑定与端口：
// 否则每次休眠/唤醒都会丢掉用户选定的节点，唤醒后回到自动调度。
func TestReconcileKeepsManualBindingOfDisabledAccount(t *testing.T) {
	store := newAutoBindTestStore(t)
	mgr := NewManager(store, nil)
	hk, _, _ := nodeKeys3()

	// c@test.com 在固件里是 disabled：手动绑定后必须原样保留。
	if err := mgr.BindAccount(context.Background(), "c@test.com", hk); err != nil {
		t.Fatalf("manual bind failed: %v", err)
	}
	autoBind(t, mgr, store, "a@test.com", hk)
	autoBind(t, mgr, store, "b@test.com", hk)
	enableAutoBridge(t, store)
	markHealth(mgr, hk, NodeHealthOK, 60, 0)

	if mgr.reconcileBindings() {
		t.Fatal("reconcile must not touch manually bound accounts")
	}
	acc, ok := store.FindAccount("c@test.com")
	if !ok || !acc.ManualBinding {
		t.Fatal("disabled account must keep manual_binding")
	}
	if got := accountProxyID(t, store, "c@test.com"); got != config.MihomoManagedProxyID(hk) {
		t.Fatalf("disabled account must keep its manual binding, got %q", got)
	}
	if _, ok := store.Snapshot().Mihomo.PortMap[hk]; !ok {
		t.Fatal("disabled account must keep its port allocation")
	}
}

// 手动绑定的账号交回自动调度后（等价 mode=auto：清空绑定 + 清除两个标记），
// 重新参与补位：下一轮调和就给它分配健康节点，旧绑定与端口由 GC 回收。
func TestReconcileBackfillsAccountReturnedFromManualBinding(t *testing.T) {
	store := newAutoBindTestStore(t)
	mgr := NewManager(store, nil)
	hk, jp, _ := nodeKeys3()

	if err := mgr.BindAccount(context.Background(), "a@test.com", hk); err != nil {
		t.Fatalf("manual bind failed: %v", err)
	}
	// 交回自动调度：与 PUT /admin/accounts/{identifier}/proxy {"mode":"auto"} 同一效果。
	if err := store.Update(func(c *config.Config) error {
		for i := range c.Accounts {
			if c.Accounts[i].Email != "a@test.com" {
				continue
			}
			c.Accounts[i].ProxyID = ""
			c.Accounts[i].ManualBinding = false
			c.Accounts[i].NoProxy = false
		}
		return nil
	}); err != nil {
		t.Fatalf("return to auto failed: %v", err)
	}
	enableAutoBridge(t, store)
	markHealth(mgr, hk, NodeHealthFail, 0, 5) // 原节点已死：交回自动调度后不该留在上面
	markHealth(mgr, jp, NodeHealthOK, 70, 0)

	if !mgr.reconcileBindings() {
		t.Fatal("expected reconcile to backfill the returned account")
	}
	got := accountProxyID(t, store, "a@test.com")
	if got != config.MihomoManagedProxyID(jp) {
		t.Fatalf("returned account should be assigned a healthy node, got %q", got)
	}
	acc, _ := store.FindAccount("a@test.com")
	if acc.ManualBinding || acc.NoProxy {
		t.Fatalf("returned account must stay auto-schedulable: %+v", acc)
	}
	// 交回自动调度后原绑定的端口分配不再被引用，由 GC 回收：
	// 端口表里只应剩下账号当前真正用着的节点。
	snap := store.Snapshot()
	bindingByProxy := proxyNodeMap(snap)
	bound := map[string]struct{}{}
	for _, account := range snap.Accounts {
		if nodeKey, ok := bindingByProxy[strings.TrimSpace(account.ProxyID)]; ok {
			bound[nodeKey] = struct{}{}
		}
	}
	if len(snap.Mihomo.PortMap) != len(bound) {
		t.Fatalf("stale port allocations not reclaimed: port_map=%v bound=%v", snap.Mihomo.PortMap, bound)
	}
}

// 手动绑定的账号休眠→唤醒：绑定与端口都保留，但休眠期间任何一次别的 Apply
// 写下的运行时监听都不含它（禁用账号不占端口）。唤醒只清 Disabled 标记，
// 配置层面没有变更可做——missingListeners 必须发现运行中的监听落后，
// 否则账号会停在"绑定还在、端口没人监听"的状态，且不会自愈。
func TestMissingListenersReportsWokenManualAccount(t *testing.T) {
	store := newAutoBindTestStore(t)
	mgr := NewManager(store, nil)
	hk, jp, _ := nodeKeys3()

	if err := mgr.BindAccount(context.Background(), "a@test.com", hk); err != nil {
		t.Fatalf("manual bind failed: %v", err)
	}
	autoBind(t, mgr, store, "b@test.com", jp)
	enableAutoBridge(t, store)

	appliedNow := func() []int {
		t.Helper()
		ports := []int{}
		for _, b := range collectActiveBindings(store.Snapshot()) {
			ports = append(ports, b.Port)
		}
		return ports
	}
	setDisabled := func(v bool) {
		t.Helper()
		if err := store.Update(func(c *config.Config) error {
			for i := range c.Accounts {
				if c.Accounts[i].Email == "a@test.com" {
					c.Accounts[i].Disabled = v
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("set disabled=%v failed: %v", v, err)
		}
	}

	// 稳态：桥在运行，已应用的监听与当前期望一致（a 手动绑 hk、b 自动绑 jp）。
	mgr.mu.Lock()
	mgr.running = true
	mgr.listenPorts = appliedNow()
	runningCount := len(mgr.listenPorts)
	mgr.mu.Unlock()
	if runningCount != 2 {
		t.Fatalf("expected 2 applied listeners, got %d", runningCount)
	}
	if missing := mgr.missingListeners(store.Snapshot()); len(missing) != 0 {
		t.Fatalf("stable state must not report missing listeners: %v", missing)
	}

	// 休眠：a 的绑定保留，但禁用账号不占端口——这期间别的 Apply 写下的监听里
	// 只有 jp（这里等价地把它记为"已应用"）。
	setDisabled(true)
	mgr.mu.Lock()
	mgr.listenPorts = appliedNow()
	mgr.mu.Unlock()
	if missing := mgr.missingListeners(store.Snapshot()); len(missing) != 0 {
		t.Fatalf("asleep manual account must not be expected to listen: %v", missing)
	}

	// 唤醒：配置又需要 hk 的监听了，而运行中的监听里没有 → 必须报告缺失。
	setDisabled(false)
	want := store.Snapshot().Mihomo.PortMap[hk]
	missing := mgr.missingListeners(store.Snapshot())
	if len(missing) != 1 || missing[0] != want {
		t.Fatalf("woken manual account must report its missing listener (hk port %d), got %v", want, missing)
	}
	// 账号本身仍在手动绑定：reconcile 不会换节点，只能靠补齐监听修好。
	acc, _ := store.FindAccount("a@test.com")
	if !acc.ManualBinding || strings.TrimSpace(acc.ProxyID) != config.MihomoManagedProxyID(hk) {
		t.Fatalf("binding itself must survive sleep/wake: %+v", acc)
	}
}

// 监听落后时 reconcile 必须发起一次重新应用（而不是在"无绑定变更"处直接返回）。
// 桥指向假二进制使这次重新应用必然失败：用运行态翻转为 false 证明它确实发起了。
func TestReconcileReappliesWhenAppliedListenersLag(t *testing.T) {
	store := newAutoBindTestStore(t)
	bogus := filepath.Join(t.TempDir(), "mihomo")
	if err := os.WriteFile(bogus, []byte("not a mihomo binary\n"), 0o644); err != nil {
		t.Fatalf("write bogus binary failed: %v", err)
	}
	t.Setenv("MIHOMO_PATH", bogus)

	mgr := NewManager(store, nil)
	hk, jp, _ := nodeKeys3()
	if err := mgr.BindAccount(context.Background(), "a@test.com", hk); err != nil {
		t.Fatalf("manual bind failed: %v", err)
	}
	autoBind(t, mgr, store, "b@test.com", jp)
	enableAutoBridge(t, store)
	markHealth(mgr, hk, NodeHealthOK, 50, 0)
	markHealth(mgr, jp, NodeHealthOK, 60, 0)

	// a（手动绑定）在休眠期间被别的 Apply 摘掉了监听，此刻刚被唤醒：
	// 运行中的监听只有 jp，配置期望 hk + jp。
	snap := store.Snapshot()
	mgr.mu.Lock()
	mgr.running = true
	mgr.listenPorts = []int{snap.Mihomo.PortMap[jp]}
	mgr.mu.Unlock()

	if missing := mgr.missingListeners(store.Snapshot()); len(missing) != 1 {
		t.Fatalf("expected exactly the missing hk listener, got %v", missing)
	}
	if mgr.reconcileBindings() {
		t.Fatal("重新应用失败时不应报告调和成功（下一拍重试）")
	}
	mgr.mu.Lock()
	running := mgr.running
	mgr.mu.Unlock()
	if running {
		t.Fatal("reconcile 必须为落后的监听发起重新应用（Apply 失败会清 running）")
	}
	if lastErr, _ := mgr.Status()["last_error"].(string); strings.TrimSpace(lastErr) == "" {
		t.Fatal("expected the failed re-apply to be reported in last_error")
	}
}
