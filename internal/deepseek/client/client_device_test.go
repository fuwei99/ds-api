package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"ds2api/internal/config"
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
		"runtime":{"device_id_mode":"real"}
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
		"accounts":[{"email":"u@example.com","password":"p","device_id":"BRealDeviceIdValue","device_id_type":"real"}],
		"runtime":{"device_id_mode":"real"}
	}`)
	deviceID, deviceType, err := c.ensureAccountDeviceID(context.Background(), storeAccount(t, c, "u@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if deviceID != "BRealDeviceIdValue" {
		t.Fatalf("real id must be kept when devid config unavailable, got %q", deviceID)
	}
	if deviceType != config.DeviceIDTypeReal {
		t.Fatalf("expected type real, got %q", deviceType)
	}
	persisted := storeAccount(t, c, "u@example.com")
	if persisted.DeviceID != "BRealDeviceIdValue" || persisted.DeviceIDType != config.DeviceIDTypeReal {
		t.Fatalf("real id changed in store: %+v", persisted)
	}
}

// real 生成失败且账号尚无 device_id 时如实报错（随机回退已被删除）。
func TestEnsureAccountDeviceIDRealFailureWithoutIDErrors(t *testing.T) {
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p"}],
		"runtime":{
			"device_id_mode":"real",
			"devid_public_key":"test-key"
		}
	}`)
	// 资产目录指向空临时目录：fp-1.min.js 缺失 → real 生成必失败且不触网
	t.Setenv("DS2API_DEVID_DIR", t.TempDir())
	_, _, err := c.ensureAccountDeviceID(context.Background(), storeAccount(t, c, "u@example.com"))
	if err == nil {
		t.Fatal("expected error when real generation fails without existing id")
	}
	if errors.Is(err, config.ErrDeviceIDPoolEmpty) {
		t.Fatalf("real mode must not fall back to the manual pool, got %v", err)
	}
}

// real 生成失败时保留已有 device_id，且冷却期内不再轮换。
func TestEnsureAccountDeviceIDRealFailureKeepsExistingWithoutRotation(t *testing.T) {
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p","device_id":"BExistingDeviceIdValue","device_id_type":"real"}],
		"runtime":{
			"device_id_mode":"real",
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
	if deviceType1 != config.DeviceIDTypeReal || deviceType2 != config.DeviceIDTypeReal {
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
		"runtime":{"device_id_mode":"real"},
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
