package client

import (
	"context"
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

func TestEnsureAccountDeviceIDGeneratesRandomByDefault(t *testing.T) {
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p"}]
	}`)
	deviceID, deviceType, err := c.ensureAccountDeviceID(context.Background(), storeAccount(t, c, "u@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(deviceID, "B") || len(deviceID) != 89 {
		t.Fatalf("expected 89-char random device id, got %q (len=%d)", deviceID, len(deviceID))
	}
	if deviceType != config.DeviceIDTypeRandom {
		t.Fatalf("expected type random, got %q", deviceType)
	}
	persisted := storeAccount(t, c, "u@example.com")
	if persisted.DeviceID != deviceID || persisted.DeviceIDType != config.DeviceIDTypeRandom {
		t.Fatalf("device id not persisted: %+v", persisted)
	}
}

func TestEnsureAccountDeviceIDKeepsLegacyUnmarkedID(t *testing.T) {
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p","device_id":"BLegacyDeviceIdValue"}]
	}`)
	deviceID, deviceType, err := c.ensureAccountDeviceID(context.Background(), storeAccount(t, c, "u@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if deviceID != "BLegacyDeviceIdValue" {
		t.Fatalf("legacy device id must be kept, got %q", deviceID)
	}
	if deviceType != config.DeviceIDTypeRandom {
		t.Fatalf("legacy id treated as random, got %q", deviceType)
	}
	persisted := storeAccount(t, c, "u@example.com")
	if persisted.DeviceID != "BLegacyDeviceIdValue" {
		t.Fatalf("legacy device id changed in store: %q", persisted.DeviceID)
	}
}

func TestEnsureAccountDeviceIDRealWithoutConfigFallsBackToRandom(t *testing.T) {
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p","device_id":"BLegacyDeviceIdValue"}],
		"runtime":{"device_id_mode":"real"}
	}`)
	deviceID, deviceType, err := c.ensureAccountDeviceID(context.Background(), storeAccount(t, c, "u@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if deviceID != "BLegacyDeviceIdValue" {
		t.Fatalf("real-unavailable must degrade to random and keep legacy id, got %q", deviceID)
	}
	if deviceType != config.DeviceIDTypeRandom {
		t.Fatalf("expected type random, got %q", deviceType)
	}
}

// real 配置缺失时，已有 type=real 的账号必须保留真实 device_id，
// 不得被降级逻辑覆盖成随机 ID。
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

func TestEnsureAccountDeviceIDRealFailureFallsBackWithoutRotation(t *testing.T) {
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
	acc := storeAccount(t, c, "u@example.com")

	deviceID1, deviceType1, err := c.ensureAccountDeviceID(context.Background(), acc)
	if err != nil {
		t.Fatal(err)
	}
	if deviceID1 == "" || !strings.HasPrefix(deviceID1, "B") {
		t.Fatalf("expected fallback random device id, got %q", deviceID1)
	}
	if deviceType1 != config.DeviceIDTypeRandom {
		t.Fatalf("expected type random after fallback, got %q", deviceType1)
	}

	// 冷却期内再次登录：不再重试数美，也不得轮换 device_id。
	deviceID2, deviceType2, err := c.ensureAccountDeviceID(context.Background(), storeAccount(t, c, "u@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if deviceID2 != deviceID1 {
		t.Fatalf("device id rotated during real outage: %q -> %q", deviceID1, deviceID2)
	}
	if deviceType2 != config.DeviceIDTypeRandom {
		t.Fatalf("expected type random, got %q", deviceType2)
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
