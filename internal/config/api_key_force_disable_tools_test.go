package config

import "testing"

// TestAPIKeyForceDisableToolsIndex 验证 store 的 O(1) 索引：
// 只收录开启 force_disable_tools 的 key，未命中即视为关闭（默认关）。
func TestAPIKeyForceDisableToolsIndex(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"api_keys":[
			{"key":"k-plain","name":"plain"},
			{"key":"k-disabled","name":"disabled","force_disable_tools":true}
		]
	}`)
	store := LoadStore()

	if !store.APIKeyForceDisableTools("k-disabled") {
		t.Fatal("expected k-disabled to report force_disable_tools=true")
	}
	if store.APIKeyForceDisableTools("k-plain") {
		t.Fatal("expected k-plain to default to force_disable_tools=false")
	}
	if store.APIKeyForceDisableTools("k-unknown") {
		t.Fatal("expected unknown key to default to force_disable_tools=false")
	}

	// Store.Update 后索引必须重建，开关可双向翻转。
	if err := store.Update(func(c *Config) error {
		for i := range c.APIKeys {
			if c.APIKeys[i].Key == "k-plain" {
				c.APIKeys[i].ForceDisableTools = true
			}
			if c.APIKeys[i].Key == "k-disabled" {
				c.APIKeys[i].ForceDisableTools = false
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if !store.APIKeyForceDisableTools("k-plain") {
		t.Fatal("expected k-plain to be force-disabled after update")
	}
	if store.APIKeyForceDisableTools("k-disabled") {
		t.Fatal("expected k-disabled to be re-enabled after update")
	}
}

// TestNormalizeCredentialsPreservesForceDisableTools 验证结构化 api_keys 归一化
// 不会丢掉开关，且开关参与 credentials 变更比较。
func TestNormalizeCredentialsPreservesForceDisableTools(t *testing.T) {
	cfg := Config{APIKeys: []APIKey{
		{Key: " k1 ", Name: " a ", ForceDisableTools: true},
		{Key: "k2", ForceDisableTools: false},
	}}
	cfg.NormalizeCredentials()

	if len(cfg.APIKeys) != 2 {
		t.Fatalf("unexpected api keys: %#v", cfg.APIKeys)
	}
	if !cfg.APIKeys[0].ForceDisableTools {
		t.Fatalf("expected force_disable_tools to survive normalization: %#v", cfg.APIKeys[0])
	}
	if cfg.APIKeys[0].Key != "k1" || cfg.APIKeys[0].Name != "a" {
		t.Fatalf("expected trimmed key/name: %#v", cfg.APIKeys[0])
	}
	if cfg.APIKeys[1].ForceDisableTools {
		t.Fatalf("expected default off: %#v", cfg.APIKeys[1])
	}

	if equalAPIKeys(
		[]APIKey{{Key: "k1", ForceDisableTools: true}},
		[]APIKey{{Key: "k1", ForceDisableTools: false}},
	) {
		t.Fatal("expected force_disable_tools to participate in api key equality")
	}
}

// TestAPIKeysFromStringsKeepsForceDisableTools 验证 legacy keys 回退路径
// 会从 meta 里带回开关。
func TestAPIKeysFromStringsKeepsForceDisableTools(t *testing.T) {
	meta := map[string]APIKey{
		"k1": {Key: "k1", Name: "a", ForceDisableTools: true},
	}
	out := apiKeysFromStrings([]string{"k1", "k2"}, meta)
	if len(out) != 2 {
		t.Fatalf("unexpected result: %#v", out)
	}
	if !out[0].ForceDisableTools {
		t.Fatalf("expected meta flag to be carried over: %#v", out[0])
	}
	if out[1].ForceDisableTools {
		t.Fatalf("expected key without meta to default off: %#v", out[1])
	}

	mapped := apiKeyMap([]APIKey{{Key: "k1", ForceDisableTools: true}})
	if !mapped["k1"].ForceDisableTools {
		t.Fatalf("expected apiKeyMap to keep the flag: %#v", mapped)
	}
}
