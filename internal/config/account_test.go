package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizePoolType(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", PoolTypeDefault},
		{"default", PoolTypeDefault},
		{"DEFAULT", PoolTypeDefault},
		{"no_tools", PoolTypeNoTools},
		{"NO_TOOLS", PoolTypeNoTools},
		{"  no_tools  ", PoolTypeNoTools},
		{"tools_only", PoolTypeToolsOnly},
		{"TOOLS_ONLY", PoolTypeToolsOnly},
		{"unknown", PoolTypeDefault},
	}
	for _, c := range cases {
		if got := NormalizePoolType(c.in); got != c.want {
			t.Fatalf("NormalizePoolType(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMatchesPoolType(t *testing.T) {
	defaultAcc := Account{PoolType: PoolTypeDefault}
	noToolsAcc := Account{PoolType: PoolTypeNoTools}
	toolsOnlyAcc := Account{PoolType: PoolTypeToolsOnly}
	emptyAcc := Account{}

	if !defaultAcc.MatchesPoolType(true) || !defaultAcc.MatchesPoolType(false) {
		t.Fatal("default account should match both tools-enabled and tools-disabled requests")
	}
	if !emptyAcc.MatchesPoolType(true) || !emptyAcc.MatchesPoolType(false) {
		t.Fatal("account with empty pool_type should match both tools-enabled and tools-disabled requests")
	}
	if noToolsAcc.MatchesPoolType(true) {
		t.Fatal("no_tools account should not match tools-enabled request")
	}
	if !noToolsAcc.MatchesPoolType(false) {
		t.Fatal("no_tools account should match tools-disabled request")
	}
	if toolsOnlyAcc.MatchesPoolType(false) {
		t.Fatal("tools_only account should not match tools-disabled request")
	}
	if !toolsOnlyAcc.MatchesPoolType(true) {
		t.Fatal("tools_only account should match tools-enabled request")
	}
}

// manual_binding 是账号记录的持久化字段（手动绑定节点后退出自动调度），
// 配置文件往返与 JSON 键名由这条测试锁定：键名写错会静默丢失整个标记。
func TestAccountManualBindingJSONRoundTrip(t *testing.T) {
	const raw = `{"email":"u@example.com","proxy_id":"mihomo-abc","manual_binding":true}`
	var acc Account
	if err := json.Unmarshal([]byte(raw), &acc); err != nil {
		t.Fatalf("unmarshal account failed: %v", err)
	}
	if !acc.ManualBinding {
		t.Fatalf("manual_binding not decoded: %+v", acc)
	}
	out, err := json.Marshal(acc)
	if err != nil {
		t.Fatalf("marshal account failed: %v", err)
	}
	if !strings.Contains(string(out), `"manual_binding":true`) {
		t.Fatalf("manual_binding missing from encoded account: %s", out)
	}
	// 未设置的账号保持紧凑：等于零值不落盘。
	plain, err := json.Marshal(Account{Email: "v@example.com"})
	if err != nil {
		t.Fatalf("marshal plain account failed: %v", err)
	}
	if strings.Contains(string(plain), "manual_binding") {
		t.Fatalf("zero manual_binding must be omitted: %s", plain)
	}
}
