package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func poolTestID(seed byte) string {
	return "B" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{seed}, 64))
}

func TestNormalizeDeviceIDInputAcceptsBothForms(t *testing.T) {
	withPrefix := poolTestID(1)
	body := strings.TrimPrefix(withPrefix, "B")

	got, err := NormalizeDeviceIDInput(withPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if got != withPrefix {
		t.Fatalf("B-prefixed input must be kept as-is, got %q", got)
	}
	got, err = NormalizeDeviceIDInput("  " + body + "  ")
	if err != nil {
		t.Fatal(err)
	}
	if got != withPrefix {
		t.Fatalf("raw input must be normalized to %q, got %q", withPrefix, got)
	}
}

func TestNormalizeDeviceIDInputRejectsInvalid(t *testing.T) {
	valid := strings.TrimPrefix(poolTestID(2), "B")
	cases := map[string]string{
		"empty":          "   ",
		"too short":      "Babc",
		"too long":       valid + "A",
		"missing pad":    strings.TrimSuffix(valid, "=="),
		"illegal base64": "B" + strings.Repeat("!", 86) + "==",
	}
	for name, raw := range cases {
		if _, err := NormalizeDeviceIDInput(raw); err == nil {
			t.Fatalf("%s: expected error for %q", name, raw)
		}
	}
}

func TestNormalizeDeviceIDPoolDedupesAndDropsInvalid(t *testing.T) {
	id := poolTestID(3)
	items := NormalizeDeviceIDPool([]DeviceIDPoolItem{
		{ID: id, Bound: 2},
		{ID: id, Bound: 9},
		{ID: "not-a-device-id"},
		{ID: ""},
	})
	if len(items) != 1 {
		t.Fatalf("expected single deduped item, got %+v", items)
	}
	if items[0].ID != id || items[0].Bound != 2 {
		t.Fatalf("unexpected item: %+v", items[0])
	}
}

func TestReconcileDeviceIDBindingsUnbindsIneligibleAccounts(t *testing.T) {
	first, second := poolTestID(4), poolTestID(5)
	cfg := &Config{
		DeviceIDPool: DeviceIDPoolConfig{Items: []DeviceIDPoolItem{{ID: first}, {ID: second}}},
		Accounts: []Account{
			{Email: "a@example.com", DeviceID: first, DeviceIDType: DeviceIDTypeManual},
			{Email: "b@example.com", DeviceID: first, DeviceIDType: DeviceIDTypeManual},
			{Email: "c@example.com", DeviceID: first, DeviceIDType: DeviceIDTypeManual, Disabled: true},
			{Email: "d@example.com", DeviceID: second, DeviceIDType: DeviceIDTypeManual, MutedUntil: 4102444800},
			{Email: "e@example.com", DeviceID: poolTestID(6), DeviceIDType: DeviceIDTypeManual},
		},
	}
	ReconcileDeviceIDBindings(cfg)

	if cfg.Accounts[2].DeviceID != "" {
		t.Fatalf("disabled account must be unbound, got %q", cfg.Accounts[2].DeviceID)
	}
	if cfg.Accounts[3].DeviceID != "" {
		t.Fatalf("muted account must be unbound, got %q", cfg.Accounts[3].DeviceID)
	}
	// 号池之外的 device_id 只解绑，不在这里重新分配（分配只发生在登录时）。
	if cfg.Accounts[4].DeviceID != "" {
		t.Fatalf("stale binding must be released, got %q", cfg.Accounts[4].DeviceID)
	}
	if cfg.DeviceIDPool.Items[0].Bound != 2 || cfg.DeviceIDPool.Items[1].Bound != 0 {
		t.Fatalf("unexpected bound counts: %+v", cfg.DeviceIDPool.Items)
	}
}

// 新增 device_id 不得把尚未绑定的账号一次性绑到号池里（否则第一个 id 会吸走
// 全部账号，后续新增的 id 永远分不到账号）。
func TestReconcileDeviceIDBindingsNeverAssignsUnboundAccounts(t *testing.T) {
	first, second := poolTestID(13), poolTestID(14)
	cfg := &Config{
		Accounts: []Account{
			{Email: "a@example.com", DeviceID: poolTestID(15)},
			{Email: "b@example.com"},
			{Email: "c@example.com", DeviceID: poolTestID(16)},
		},
	}
	// 只加入一个 id：不应有任何账号被绑定。
	cfg.AddDeviceID(first)
	ReconcileDeviceIDBindings(cfg)
	for _, acc := range cfg.Accounts {
		if acc.DeviceID != "" {
			t.Fatalf("account %s must stay unbound, got %q", acc.Identifier(), acc.DeviceID)
		}
	}
	if cfg.DeviceIDPool.Items[0].Bound != 0 {
		t.Fatalf("bound count must stay 0, got %+v", cfg.DeviceIDPool.Items)
	}

	// 再加入一个 id：仍然不应有账号被绑定。
	cfg.AddDeviceID(second)
	ReconcileDeviceIDBindings(cfg)
	if cfg.DeviceIDPool.Items[0].Bound != 0 || cfg.DeviceIDPool.Items[1].Bound != 0 {
		t.Fatalf("bound counts must stay 0, got %+v", cfg.DeviceIDPool.Items)
	}
}

func TestReconcileDeviceIDBindingsSkipsRealMode(t *testing.T) {
	id := poolTestID(7)
	cfg := &Config{
		Runtime:      RuntimeConfig{DeviceIDMode: DeviceIDTypeReal},
		DeviceIDPool: DeviceIDPoolConfig{Items: []DeviceIDPoolItem{{ID: id}}},
		Accounts:     []Account{{Email: "a@example.com", DeviceID: "BRealID", DeviceIDType: DeviceIDTypeReal}},
	}
	ReconcileDeviceIDBindings(cfg)
	if cfg.Accounts[0].DeviceID != "BRealID" {
		t.Fatalf("real mode must keep device ids untouched, got %q", cfg.Accounts[0].DeviceID)
	}
}

func TestRemoveDeviceIDAndRebind(t *testing.T) {
	first, second := poolTestID(8), poolTestID(9)
	cfg := &Config{
		DeviceIDPool: DeviceIDPoolConfig{Items: []DeviceIDPoolItem{{ID: first}, {ID: second}}},
		Accounts: []Account{
			{Email: "a@example.com", DeviceID: first, DeviceIDType: DeviceIDTypeManual},
			{Email: "b@example.com", DeviceID: first, DeviceIDType: DeviceIDTypeManual},
			{Email: "c@example.com"},
		},
	}
	if remaining := RemoveDeviceIDAndRebind(cfg, first); remaining != 1 {
		t.Fatalf("expected one remaining id, got %d", remaining)
	}
	if cfg.HasDeviceID(first) {
		t.Fatal("removed id must be gone from the pool")
	}
	for _, acc := range cfg.Accounts[:2] {
		if acc.DeviceID != second {
			t.Fatalf("account %s must be rebound to %q, got %q", acc.Identifier(), second, acc.DeviceID)
		}
	}
	// 未持有绑定的账号不受影响。
	if cfg.Accounts[2].DeviceID != "" {
		t.Fatalf("unbound account must stay unbound, got %q", cfg.Accounts[2].DeviceID)
	}
	if cfg.DeviceIDPool.Items[0].Bound != 2 {
		t.Fatalf("unexpected bound count: %+v", cfg.DeviceIDPool.Items)
	}

	if remaining := RemoveDeviceIDAndRebind(cfg, second); remaining != 0 {
		t.Fatalf("expected empty pool, got %d", remaining)
	}
	for _, acc := range cfg.Accounts {
		if acc.DeviceID != "" {
			t.Fatalf("empty pool must leave accounts unbound, got %q", acc.DeviceID)
		}
	}
}

func TestLeastBoundDeviceIDPrefersPoolOrder(t *testing.T) {
	first, second := poolTestID(10), poolTestID(11)
	cfg := &Config{DeviceIDPool: DeviceIDPoolConfig{Items: []DeviceIDPoolItem{
		{ID: first, Bound: 3},
		{ID: second, Bound: 1},
	}}}
	got, ok := cfg.LeastBoundDeviceID()
	if !ok || got != second {
		t.Fatalf("expected %q, got %q (ok=%v)", second, got, ok)
	}
	empty := &Config{}
	if _, ok := empty.LeastBoundDeviceID(); ok {
		t.Fatal("empty pool must report not found")
	}
}

func TestDeviceIDPoolSurvivesClone(t *testing.T) {
	id := poolTestID(12)
	original := Config{DeviceIDPool: DeviceIDPoolConfig{Items: []DeviceIDPoolItem{{ID: id, Bound: 4}}}}
	clone := original.Clone()
	if len(clone.DeviceIDPool.Items) != 1 || clone.DeviceIDPool.Items[0].Bound != 4 {
		t.Fatalf("clone must copy device id pool, got %+v", clone.DeviceIDPool)
	}
	clone.DeviceIDPool.Items[0].Bound = 9
	if original.DeviceIDPool.Items[0].Bound != 4 {
		t.Fatal("clone must not share backing array with the original")
	}
}

func TestErrDeviceIDPoolEmptyMessage(t *testing.T) {
	if !errors.Is(ErrDeviceIDPoolEmpty, ErrDeviceIDPoolEmpty) {
		t.Fatal("sentinel error must be comparable")
	}
	if ErrDeviceIDPoolEmpty.Error() != "请先配置device_id" {
		t.Fatalf("unexpected message: %q", ErrDeviceIDPoolEmpty.Error())
	}
}
