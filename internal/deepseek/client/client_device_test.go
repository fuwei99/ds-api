package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ds2api/internal/config"
	dsprotocol "ds2api/internal/deepseek/protocol"
	trans "ds2api/internal/deepseek/transport"
	"ds2api/internal/devid"
)

func newDeviceTestClient(t *testing.T, cfgJSON string) *Client {
	t.Helper()
	t.Setenv("DS2API_CONFIG_JSON", cfgJSON)
	store, err := config.LoadStoreWithError()
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	return NewClient(store, nil)
}

func storeAccount(t *testing.T, c *Client, identifier string) config.Account {
	t.Helper()
	acc, ok := c.Store.FindAccount(identifier)
	if !ok {
		t.Fatalf("account %q not found in store", identifier)
	}
	return acc
}

// testDeviceID 生成一个格式合法（64 字节 base64，带 B 前缀）的 device_id。
func testDeviceID(seed byte) string {
	return "B" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{seed}, 64))
}

func poolConfigJSON(items ...string) string {
	entries := make([]string, 0, len(items))
	for _, id := range items {
		entries = append(entries, `{"id":"`+id+`"}`)
	}
	return `"device_id_pool":{"items":[` + strings.Join(entries, ",") + `]}`
}

func TestEnsureAccountDeviceIDAssignsFromPoolByDefault(t *testing.T) {
	first := testDeviceID(1)
	second := testDeviceID(2)
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p"}],
		`+poolConfigJSON(first, second)+`
	}`)
	deviceID, deviceType, err := c.ensureAccountDeviceID(context.Background(), storeAccount(t, c, "u@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if deviceID != first {
		t.Fatalf("expected least-bound (first) pool id %q, got %q", first, deviceID)
	}
	if deviceType != config.DeviceIDTypeManual {
		t.Fatalf("expected type manual, got %q", deviceType)
	}
	persisted := storeAccount(t, c, "u@example.com")
	if persisted.DeviceID != first || persisted.DeviceIDType != config.DeviceIDTypeManual {
		t.Fatalf("device id not persisted: %+v", persisted)
	}
	items := c.Store.DeviceIDPoolItems()
	if len(items) != 2 || items[0].Bound != 1 || items[1].Bound != 0 {
		t.Fatalf("unexpected bound counts: %+v", items)
	}
}

// 账号启用期间一直复用同一个 device_id，不会每次登录都换。
func TestEnsureAccountDeviceIDReusesBoundIDWhileEnabled(t *testing.T) {
	first := testDeviceID(3)
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p"}],
		`+poolConfigJSON(first)+`
	}`)
	id1, _, err := c.ensureAccountDeviceID(context.Background(), storeAccount(t, c, "u@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	id2, _, err := c.ensureAccountDeviceID(context.Background(), storeAccount(t, c, "u@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("device id rotated while enabled: %q -> %q", id1, id2)
	}
	if items := c.Store.DeviceIDPoolItems(); items[0].Bound != 1 {
		t.Fatalf("bound count must stay 1, got %+v", items)
	}
}

// 新账号优先分配到绑定账号数最少的 device_id。
func TestEnsureAccountDeviceIDPicksLeastBound(t *testing.T) {
	first := testDeviceID(4)
	second := testDeviceID(5)
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[
			{"email":"a@example.com","password":"p","device_id":"`+first+`","device_id_type":"manual"},
			{"email":"b@example.com","password":"p"}
		],
		`+poolConfigJSON(first, second)+`
	}`)
	deviceID, _, err := c.ensureAccountDeviceID(context.Background(), storeAccount(t, c, "b@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if deviceID != second {
		t.Fatalf("expected least-bound id %q, got %q", second, deviceID)
	}
}

// 号池为空时必须报"请先配置device_id"。
func TestEnsureAccountDeviceIDErrorsWhenPoolEmpty(t *testing.T) {
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p"}]
	}`)
	_, _, err := c.ensureAccountDeviceID(context.Background(), storeAccount(t, c, "u@example.com"))
	if !errors.Is(err, config.ErrDeviceIDPoolEmpty) {
		t.Fatalf("expected ErrDeviceIDPoolEmpty, got %v", err)
	}
}

// 账号持有的 device_id 已不在号池（例如随机 ID 或被删除）时必须重新分配。
func TestEnsureAccountDeviceIDReassignsStaleID(t *testing.T) {
	pooled := testDeviceID(6)
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p","device_id":"`+testDeviceID(7)+`","device_id_type":"manual"}],
		`+poolConfigJSON(pooled)+`
	}`)
	deviceID, _, err := c.ensureAccountDeviceID(context.Background(), storeAccount(t, c, "u@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if deviceID != pooled {
		t.Fatalf("expected stale id replaced by %q, got %q", pooled, deviceID)
	}
}

// 历史配置里 type=random 的账号按 manual 处理（随机 ID 已失效，需换绑号池）。
func TestEnsureAccountDeviceIDTreatsLegacyRandomAsManual(t *testing.T) {
	pooled := testDeviceID(8)
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p","device_id":"`+pooled+`","device_id_type":"random"}],
		`+poolConfigJSON(pooled)+`
	}`)
	deviceID, deviceType, err := c.ensureAccountDeviceID(context.Background(), storeAccount(t, c, "u@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if deviceID != pooled || deviceType != config.DeviceIDTypeManual {
		t.Fatalf("legacy random must be treated as manual, got %q/%q", deviceID, deviceType)
	}
}

// real 配置缺失时，已有 device_id 的账号必须保留原值，不得被覆盖。
func TestEnsureAccountDeviceIDRealWithoutConfigKeepsExistingID(t *testing.T) {
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p","device_id":"BLegacyDeviceIdValue"}],
		"runtime":{"device_id_mode":"auto"}
	}`)
	deviceID, _, err := c.ensureAccountDeviceID(context.Background(), storeAccount(t, c, "u@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if deviceID != "BLegacyDeviceIdValue" {
		t.Fatalf("real-unavailable must keep legacy id, got %q", deviceID)
	}
	persisted := storeAccount(t, c, "u@example.com")
	if persisted.DeviceID != "BLegacyDeviceIdValue" {
		t.Fatalf("legacy device id changed in store: %q", persisted.DeviceID)
	}
}

// real 配置缺失时，已有 type=real 的账号必须保留真实 device_id。
func TestEnsureAccountDeviceIDRealWithoutConfigKeepsRealID(t *testing.T) {
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p","device_id":"BRealDeviceIdValue","device_id_type":"auto"}],
		"runtime":{"device_id_mode":"auto"}
	}`)
	deviceID, deviceType, err := c.ensureAccountDeviceID(context.Background(), storeAccount(t, c, "u@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if deviceID != "BRealDeviceIdValue" {
		t.Fatalf("real id must be kept when devid config unavailable, got %q", deviceID)
	}
	if deviceType != config.DeviceIDTypeAuto {
		t.Fatalf("expected type real, got %q", deviceType)
	}
	persisted := storeAccount(t, c, "u@example.com")
	if persisted.DeviceID != "BRealDeviceIdValue" || persisted.DeviceIDType != config.DeviceIDTypeAuto {
		t.Fatalf("real id changed in store: %+v", persisted)
	}
}

// auto 生成失败且账号尚无 device_id 时如实报错（随机回退已被删除）。
func TestEnsureAccountDeviceIDRealFailureWithoutIDErrors(t *testing.T) {
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p"}],
		"runtime":{
			"device_id_mode":"auto",
			"devid_public_key":"test-key"
		}
	}`)
	// 资产目录指向空临时目录：fp-1.min.js 缺失 → auto 生成必失败且不触网
	t.Setenv("DS2API_DEVID_DIR", t.TempDir())
	_, _, err := c.ensureAccountDeviceID(context.Background(), storeAccount(t, c, "u@example.com"))
	if err == nil {
		t.Fatal("expected error when real generation fails without existing id")
	}
	if errors.Is(err, config.ErrDeviceIDPoolEmpty) {
		t.Fatalf("real mode must not fall back to the manual pool, got %v", err)
	}
}

// auto 生成失败时保留已有 device_id，且冷却期内不再轮换。
func TestEnsureAccountDeviceIDRealFailureKeepsExistingWithoutRotation(t *testing.T) {
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p","device_id":"BExistingDeviceIdValue","device_id_type":"auto"}],
		"runtime":{
			"device_id_mode":"auto",
			"devid_public_key":"test-key"
		}
	}`)
	t.Setenv("DS2API_DEVID_DIR", t.TempDir())
	deviceID1, deviceType1, err := c.ensureAccountDeviceID(context.Background(), storeAccount(t, c, "u@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	deviceID2, deviceType2, err := c.ensureAccountDeviceID(context.Background(), storeAccount(t, c, "u@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if deviceID1 != "BExistingDeviceIdValue" || deviceID2 != deviceID1 {
		t.Fatalf("existing real id must be kept, got %q -> %q", deviceID1, deviceID2)
	}
	if deviceType1 != config.DeviceIDTypeAuto || deviceType2 != config.DeviceIDTypeAuto {
		t.Fatalf("expected type real, got %q/%q", deviceType1, deviceType2)
	}
}

func TestForgetDeviceGenLock(t *testing.T) {
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p"}]
	}`)
	c.ForgetDeviceGenLock("u@example.com")
	unlock := c.lockDeviceGen("u@example.com")
	unlock()
	c.ForgetDeviceGenLock("u@example.com")
	if _, ok := c.deviceGenLocks["u@example.com"]; ok {
		t.Fatal("expected lock entry to be removed")
	}
	// 重复删除与空标识不得 panic。
	c.ForgetDeviceGenLock("u@example.com")
	c.ForgetDeviceGenLock("")
}

// InvalidateDeviceID 移除失效 id 并给原绑定账号换绑；号池清空后返回 false。
func TestInvalidateDeviceIDRebindsAccounts(t *testing.T) {
	first := testDeviceID(9)
	second := testDeviceID(10)
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[
			{"email":"a@example.com","password":"p","device_id":"`+first+`","device_id_type":"manual"},
			{"email":"b@example.com","password":"p","device_id":"`+first+`","device_id_type":"manual"}
		],
		`+poolConfigJSON(first, second)+`
	}`)
	if remaining := c.InvalidateDeviceID(first); !remaining {
		t.Fatal("expected remaining pool id")
	}
	if _, ok := c.Store.FindDeviceIDPoolItem(first); ok {
		t.Fatal("invalidated id must be removed from the pool")
	}
	for _, identifier := range []string{"a@example.com", "b@example.com"} {
		acc := storeAccount(t, c, identifier)
		if acc.DeviceID != second {
			t.Fatalf("account %s must be rebound to %q, got %q", identifier, second, acc.DeviceID)
		}
	}
	items := c.Store.DeviceIDPoolItems()
	if len(items) != 1 || items[0].Bound != 2 {
		t.Fatalf("unexpected bound counts after rebind: %+v", items)
	}

	if remaining := c.InvalidateDeviceID(second); remaining {
		t.Fatal("expected pool to be empty after removing the last id")
	}
	if got := c.Store.DeviceIDPoolSize(); got != 0 {
		t.Fatalf("expected empty pool, got %d", got)
	}
}

// RISK_DEVICE_DETECTED 只在 manual 模式且 id 来自号池时才触发换号重试。
func TestShouldRotateDeviceIDOnlyForPooledManualIDs(t *testing.T) {
	pooled := testDeviceID(11)
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p"}],
		`+poolConfigJSON(pooled)+`
	}`)
	riskErr := errors.New("login failed: RISK_DEVICE_DETECTED")
	if !isRiskDeviceDetected(riskErr) {
		t.Fatal("expected RISK_DEVICE_DETECTED to be detected")
	}
	if isRiskDeviceDetected(errors.New("login failed: USER_IS_BANNED")) {
		t.Fatal("unrelated errors must not trigger rotation")
	}
	if !c.shouldRotateDeviceID(pooled, riskErr) {
		t.Fatal("pooled manual id must rotate on RISK_DEVICE_DETECTED")
	}
	if c.shouldRotateDeviceID(testDeviceID(12), riskErr) {
		t.Fatal("id outside the pool must not trigger rotation")
	}
	if c.shouldRotateDeviceID(pooled, errors.New("login failed: bad password")) {
		t.Fatal("non-risk errors must not trigger rotation")
	}

	realMode := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p"}],
		"runtime":{"device_id_mode":"auto"},
		`+poolConfigJSON(pooled)+`
	}`)
	if realMode.shouldRotateDeviceID(pooled, riskErr) {
		t.Fatal("real mode must not rotate pool ids")
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"user@example.com": "user_example.com",
		"+8613800138000":   "_8613800138000",
		"plain":            "plain",
		"":                 "account",
	}
	for in, want := range cases {
		if got := config.SanitizeFilename(in); got != want {
			t.Fatalf("SanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

// 指纹上报必须走项目统一的浏览器指纹传输（httpcloak 的 Chrome 预设），不能是
// stdlib http.Client：后者的 crypto/tls 握手对所有账号完全一致，是最容易被
// 聚类的团伙特征——比 payload 更好识别，而且账号越多越明显。
func TestRealDeviceIDUsesBrowserFingerprintTransport(t *testing.T) {
	c := &Client{}
	doer := c.devidHTTPClient(config.Account{})
	if _, bare := doer.(*http.Client); bare {
		t.Fatal("devid submit must not use a bare http.Client")
	}
	if _, ok := doer.(interface{ CloseIdleConnections() }); !ok {
		t.Fatalf("devid transport should expose CloseIdleConnections, got %T", doer)
	}
}

// 指纹自称的 Chrome 版本与 sec-ch-ua 必须与传输层/协议层同源：两边各写一份
// 版本常量，就会重演「TLS 是某个版本、UA 却自称另一个版本」的矛盾。
func TestRealDeviceIDIdentityComesFromTransport(t *testing.T) {
	c := &Client{}
	gen := newDeviceIDGenerator(devid.Config{PublicKey: "k"}, "assets", c.devidHTTPClient(config.Account{}))
	concrete, ok := gen.(*devid.Generator)
	if !ok {
		t.Fatalf("unexpected generator type %T", gen)
	}
	if concrete.Identity.ChromeMajor != trans.ChromeMajorVersion {
		t.Errorf("fingerprint Chrome major = %q, want transport's %q",
			concrete.Identity.ChromeMajor, trans.ChromeMajorVersion)
	}
	if concrete.Identity.SecChUa != dsprotocol.WebSecChUA() {
		t.Errorf("fingerprint sec-ch-ua = %q, want protocol's %q",
			concrete.Identity.SecChUa, dsprotocol.WebSecChUA())
	}
}

// 账号绑定了代理时，上报必须复用同一出口：指纹出口 IP 与登录 IP 不一致，
// 比"所有账号同一出口"更容易被对上。
func TestRealDeviceIDTransportFollowsAccountProxy(t *testing.T) {
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"proxies":[{"id":"p1","type":"socks5","host":"127.0.0.1","port":1080}],
		"accounts":[{"email":"u@example.com","password":"p","proxy_id":"p1"}]
	}`)
	acc := storeAccount(t, c, "u@example.com")
	proxied := c.devidHTTPClient(acc)
	direct := c.devidHTTPClient(config.Account{})
	if proxied == nil || direct == nil {
		t.Fatal("devid transport must never be nil")
	}
	if _, bare := proxied.(*http.Client); bare {
		t.Fatal("proxied devid submit must not use a bare http.Client")
	}
	if fmt.Sprintf("%T", proxied) != fmt.Sprintf("%T", direct) {
		t.Fatalf("proxied and direct transports should share one type: %T vs %T", proxied, direct)
	}
}

// seedDeviceProfile 写入一份占位设备档案，用于验证刷新是否动了档案。
func seedDeviceProfile(t *testing.T, identifier string) string {
	t.Helper()
	path := config.DeviceProfilePath(identifier)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"screenW":1920,"screenH":1080}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func realModeClient(t *testing.T, deviceID string) *Client {
	t.Helper()
	t.Setenv("DS2API_DEVICE_PROFILE_DIR", t.TempDir())
	return newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p","device_id":"`+deviceID+`","device_id_type":"auto"}],
		"runtime":{"device_id_mode":"auto","devid_public_key":"test-key"}
	}`)
}

// 默认刷新复用现有档案（reset=false）：档案必须还在，新 id 落回配置。
func TestRefreshRealDeviceIDKeepsProfileByDefault(t *testing.T) {
	oldID, newID := testDeviceID(50), testDeviceID(51)
	c := realModeClient(t, oldID)
	profilePath := seedDeviceProfile(t, "u@example.com")
	gen := &fakeDeviceGenerator{ids: []string{newID}}
	withFakeDeviceGenerator(t, gen)

	got, err := c.RefreshRealDeviceID(context.Background(), "u@example.com", false)
	if err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	if got != newID {
		t.Fatalf("unexpected device id: %q", got)
	}
	if exists := gen.seenProfileExists(); len(exists) != 1 || !exists[0] {
		t.Fatalf("refresh without reset must keep the profile, saw %v", exists)
	}
	if _, err := os.Stat(profilePath); err != nil {
		t.Fatalf("profile file should still exist: %v", err)
	}
	acc := storeAccount(t, c, "u@example.com")
	if acc.DeviceID != newID || config.NormalizeDeviceIDType(acc.DeviceIDType) != config.DeviceIDTypeAuto {
		t.Fatalf("new id must be persisted as real, got %q / %q", acc.DeviceID, acc.DeviceIDType)
	}
}

// reset=true 必须先删档案：档案就是设备身份，删掉才是换一台新设备。
func TestRefreshRealDeviceIDResetDeletesProfile(t *testing.T) {
	oldID, newID := testDeviceID(52), testDeviceID(53)
	c := realModeClient(t, oldID)
	profilePath := seedDeviceProfile(t, "u@example.com")
	gen := &fakeDeviceGenerator{ids: []string{newID}}
	withFakeDeviceGenerator(t, gen)

	got, err := c.RefreshRealDeviceID(context.Background(), "u@example.com", true)
	if err != nil {
		t.Fatalf("reset refresh failed: %v", err)
	}
	if got != newID {
		t.Fatalf("unexpected device id: %q", got)
	}
	if exists := gen.seenProfileExists(); len(exists) != 1 || exists[0] {
		t.Fatalf("reset must delete the profile before generating, saw %v", exists)
	}
	if _, err := os.Stat(profilePath); !os.IsNotExist(err) {
		t.Fatalf("profile file should be gone, stat err=%v", err)
	}
}

// 生成失败时账号上的 id 保持不变：宁可留着旧 id，也不能把它清成空。
func TestRefreshRealDeviceIDKeepsExistingIDOnFailure(t *testing.T) {
	oldID := testDeviceID(54)
	c := realModeClient(t, oldID)
	seedDeviceProfile(t, "u@example.com")
	withFakeDeviceGenerator(t, &fakeDeviceGenerator{ids: nil})

	if _, err := c.RefreshRealDeviceID(context.Background(), "u@example.com", false); err == nil {
		t.Fatal("expected generation failure to surface")
	}
	if acc := storeAccount(t, c, "u@example.com"); acc.DeviceID != oldID {
		t.Fatalf("failed refresh must keep the previous id, got %q", acc.DeviceID)
	}
}

// 冷却期内不请求数美：账号保留原 id 与原类型，并留下一条带剩余时间的日志。
// 没有这条日志时，"标签一直不出现"在运行日志里是查不到任何线索的。
func TestRealGenerationSkippedDuringCooldownKeepsIDAndLogs(t *testing.T) {
	manualID, realID := testDeviceID(60), testDeviceID(61)
	t.Setenv("DS2API_DEVICE_PROFILE_DIR", t.TempDir())
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p","device_id":"`+manualID+`","device_id_type":"manual"}],
		"runtime":{"device_id_mode":"auto","devid_public_key":"test-key"}
	}`)
	withFakeLogin(t, c, &fakeLoginDoer{riskIDs: map[string]bool{}})
	gen := &fakeDeviceGenerator{ids: []string{realID}}
	withFakeDeviceGenerator(t, gen)

	logs := captureClientLogs(t)
	c.markRealGenFailed("u@example.com")

	if _, err := c.Login(context.Background(), storeAccount(t, c, "u@example.com")); err != nil {
		t.Fatalf("login should still succeed with the kept id: %v", err)
	}
	if calls := len(gen.seenProfileExists()); calls != 0 {
		t.Fatalf("cooldown must skip generation entirely, saw %d calls", calls)
	}
	acc := storeAccount(t, c, "u@example.com")
	if acc.DeviceID != manualID || config.NormalizeDeviceIDType(acc.DeviceIDType) != config.DeviceIDTypeManual {
		t.Fatalf("cooldown must keep the existing id untouched, got %q / %q", acc.DeviceID, acc.DeviceIDType)
	}
	if logged := logs(); !strings.Contains(logged, "cooling down after a recent failure") ||
		!strings.Contains(logged, "cooldown_remaining") || !strings.Contains(logged, "u@example.com") {
		t.Fatalf("cooldown skip must be logged with account and remaining time, got:\n%s", logged)
	}

	// 冷却过去后重试：这次真正生成，并把账号升级成 real，同时留一条升级日志。
	c.realFailUntil.until = nil
	if _, err := c.Login(context.Background(), storeAccount(t, c, "u@example.com")); err != nil {
		t.Fatalf("login after cooldown failed: %v", err)
	}
	if calls := len(gen.seenProfileExists()); calls != 1 {
		t.Fatalf("expected exactly one generation after the cooldown, saw %d", calls)
	}
	acc = storeAccount(t, c, "u@example.com")
	if acc.DeviceID != realID || config.NormalizeDeviceIDType(acc.DeviceIDType) != config.DeviceIDTypeAuto {
		t.Fatalf("expected an upgraded real id, got %q / %q", acc.DeviceID, acc.DeviceIDType)
	}
	if logged := logs(); !strings.Contains(logged, "upgraded to real device id") {
		t.Fatalf("id upgrade should be logged, got:\n%s", logged)
	}
}

// 冷却按账号记：一个账号生成失败（例如它绑的代理节点坏了）不该让别的账号停摆。
func TestRealGenCooldownIsPerAccount(t *testing.T) {
	t.Setenv("DS2API_DEVICE_PROFILE_DIR", t.TempDir())
	manualA, manualB, realB := testDeviceID(62), testDeviceID(63), testDeviceID(64)
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[
			{"email":"a@example.com","password":"p","device_id":"`+manualA+`","device_id_type":"manual"},
			{"email":"b@example.com","password":"p","device_id":"`+manualB+`","device_id_type":"manual"}
		],
		"runtime":{"device_id_mode":"auto","devid_public_key":"test-key"}
	}`)
	withFakeLogin(t, c, &fakeLoginDoer{riskIDs: map[string]bool{}})

	// a 先失败：生成器已无 id 可发，a 因此进入自己的冷却期，但保留原 id。
	withFakeDeviceGenerator(t, &fakeDeviceGenerator{})
	if _, err := c.Login(context.Background(), storeAccount(t, c, "a@example.com")); err != nil {
		t.Fatalf("a 的登录应在保留原 id 的前提下继续成功: %v", err)
	}
	if !c.realGenCooldownActive("a@example.com") {
		t.Fatal("a 失败后应处于冷却期")
	}

	// b 不该被 a 的冷却牵连：换成能出 id 的生成器后，b 必须真的生成。
	gen := &fakeDeviceGenerator{ids: []string{realB}}
	withFakeDeviceGenerator(t, gen)
	if _, err := c.Login(context.Background(), storeAccount(t, c, "b@example.com")); err != nil {
		t.Fatalf("b 的登录失败: %v", err)
	}
	accB := storeAccount(t, c, "b@example.com")
	if accB.DeviceID != realB || config.NormalizeDeviceIDType(accB.DeviceIDType) != config.DeviceIDTypeAuto {
		t.Fatalf("a 的冷却不该阻塞 b 的生成，b 现在 %q / %q", accB.DeviceID, accB.DeviceIDType)
	}

	// a 仍在自己那 5 分钟里：再登录一次不会去请求数美。
	if _, err := c.Login(context.Background(), storeAccount(t, c, "a@example.com")); err != nil {
		t.Fatalf("a 的第二次登录失败: %v", err)
	}
	if calls := len(gen.seenProfileExists()); calls != 1 {
		t.Fatalf("a 处于冷却期时不该再次生成（应只有 b 的那一次），实际 %d 次", calls)
	}
	accA := storeAccount(t, c, "a@example.com")
	if accA.DeviceID != manualA || config.NormalizeDeviceIDType(accA.DeviceIDType) != config.DeviceIDTypeManual {
		t.Fatalf("a 应保留原 id 与原类型，现在 %q / %q", accA.DeviceID, accA.DeviceIDType)
	}
}

// captureClientLogs 捕获该用例期间的日志输出，返回读取函数。
func captureClientLogs(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	var mu sync.Mutex
	prev := config.Logger
	config.Logger = slog.New(slog.NewTextHandler(&lockedWriter{w: &buf, mu: &mu}, nil))
	t.Cleanup(func() { config.Logger = prev })
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

// lockedWriter 让 slog 并发写缓冲区时不至于互相踩。
type lockedWriter struct {
	w  *bytes.Buffer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// 批量刷新逐个返回结果：一个账号失败不影响其它账号，也不阻断整个批次。
func TestRefreshRealDeviceIDsReportsPerAccountResults(t *testing.T) {
	oldID, newID := testDeviceID(55), testDeviceID(56)
	c := realModeClient(t, oldID)
	seedDeviceProfile(t, "u@example.com")
	gen := &fakeDeviceGenerator{ids: []string{newID, newID}}
	withFakeDeviceGenerator(t, gen)

	results := c.RefreshRealDeviceIDs(context.Background(), []string{"u@example.com", "missing@example.com"}, false)
	if len(results) != 2 {
		t.Fatalf("expected one result per account, got %d", len(results))
	}
	if results[0].Identifier != "u@example.com" || results[0].DeviceID != newID || results[0].Error != "" {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
	if results[1].Identifier != "missing@example.com" || results[1].Error == "" {
		t.Fatalf("missing account should report an error: %+v", results[1])
	}
	if acc := storeAccount(t, c, "u@example.com"); acc.DeviceID != newID {
		t.Fatalf("successful account should be updated, got %q", acc.DeviceID)
	}
}
