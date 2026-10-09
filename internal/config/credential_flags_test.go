package config

import (
	"encoding/json"
	"sync"
	"testing"
)

// The credential-flag registry exists so that a new per-key switch needs a
// registry entry plus the code that acts on it, and nothing else: no codec case,
// no normalization, no index rebuild, no admin handler, no web UI component.
// These tests exercise the whole path for a flag registered at test time, which
// is the same path a real one takes.

const (
	testFlagName    = "test_credential_flag"
	testFlagDefault = false
)

var testFlagOnce sync.Once

// registerTestFlag declares a flag the production registry does not carry, so
// the tests below prove the machinery is generic rather than special-cased for
// force_disable_tools.
func registerTestFlag(t *testing.T) {
	t.Helper()
	testFlagOnce.Do(func() {
		RegisterCredentialFlag(CredentialFlagSpec{
			Name:        testFlagName,
			Default:     testFlagDefault,
			Restrictive: true,
			LabelKey:    "accountManager.testFlagLabel",
		})
	})
	if _, ok := LookupCredentialFlag(testFlagName); !ok {
		t.Fatalf("test flag %q is not registered", testFlagName)
	}
}

func TestRegisterCredentialFlagRejectsBadSpecs(t *testing.T) {
	cases := []struct {
		name string
		spec CredentialFlagSpec
	}{
		{"empty name", CredentialFlagSpec{Name: "   ", LabelKey: "accountManager.x"}},
		// A visible flag is rendered from its spec, so a missing label key would
		// render an empty row on the page rather than failing the build.
		{"missing label key", CredentialFlagSpec{Name: "test_flag_no_label"}},
		{"duplicate", CredentialFlagSpec{Name: CredentialFlagForceDisableTools, LabelKey: "accountManager.x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("expected RegisterCredentialFlag(%+v) to panic", tc.spec)
				}
			}()
			RegisterCredentialFlag(tc.spec)
		})
	}
}

// TestCredentialFlagIndexReadsStore 验证 store 的 O(1) 索引：只收录与该开关
// 声明默认值不同的项，未命中即视为默认值。
func TestCredentialFlagIndexReadsStore(t *testing.T) {
	registerTestFlag(t)
	t.Setenv("DS2API_CONFIG_JSON", `{
		"api_keys":[
			{"key":"k-plain","name":"plain"},
			{"key":"k-on","name":"on","`+testFlagName+`":true}
		]
	}`)
	store := LoadStore()

	if !store.APIKeyFlag("k-on", testFlagName) {
		t.Fatalf("expected k-on to report %s=true", testFlagName)
	}
	if store.APIKeyFlag("k-plain", testFlagName) {
		t.Fatalf("expected k-plain to read the declared default")
	}
	if store.APIKeyFlag("k-unknown", testFlagName) {
		t.Fatal("expected an unknown key to read the declared default")
	}
	if store.APIKeyFlag("k-on", "no_such_flag") {
		t.Fatal("expected an unregistered flag name to read false")
	}

	// Store.Update 后索引必须重建，开关可双向翻转。
	if err := store.Update(func(c *Config) error {
		for i := range c.APIKeys {
			switch c.APIKeys[i].Key {
			case "k-plain":
				c.APIKeys[i].SetFlag(testFlagName, true)
			case "k-on":
				c.APIKeys[i].SetFlag(testFlagName, false)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if !store.APIKeyFlag("k-plain", testFlagName) {
		t.Fatal("expected k-plain to be on after update")
	}
	if store.APIKeyFlag("k-on", testFlagName) {
		t.Fatal("expected k-on to be off after update")
	}

	// APIKeyFlags 报告每个注册开关的生效值，供管理接口下发前端。
	flags := store.APIKeyFlags("k-plain")
	if len(flags) != len(CredentialFlagSpecs()) {
		t.Fatalf("expected every registered flag, got %#v", flags)
	}
	if !flags[testFlagName] {
		t.Fatalf("expected the effective value of %s in %#v", testFlagName, flags)
	}
	if _, ok := flags[CredentialFlagForceDisableTools]; !ok {
		t.Fatalf("expected the production flag in %#v", flags)
	}
}

// TestCredentialFlagJSONRoundTrip 验证配置文件编解码：开关写成 key 对象上的
// 顶层字段，等于默认值时不落盘，未知字段被丢弃，类型不符则报错。
func TestCredentialFlagJSONRoundTrip(t *testing.T) {
	registerTestFlag(t)

	cfg, err := parseConfigString(`{"api_keys":[
		{"key":"k1","name":"a","` + testFlagName + `":true,"force_disable_tools":true},
		{"key":"k2","name":"b","` + testFlagName + `":false,"unknown_field":"ignored"}
	]}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !cfg.APIKeys[0].Flag(testFlagName) || !cfg.APIKeys[0].Flag(CredentialFlagForceDisableTools) {
		t.Fatalf("expected both flags on k1: %#v", cfg.APIKeys[0])
	}
	if cfg.APIKeys[1].Flag(testFlagName) {
		t.Fatalf("expected k2 to read the declared default: %#v", cfg.APIKeys[1])
	}

	encoded, err := json.Marshal(cfg.APIKeys)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("encoded keys are not JSON objects: %v\n%s", err, encoded)
	}
	if decoded[0][testFlagName] != true || decoded[0][CredentialFlagForceDisableTools] != true {
		t.Fatalf("expected flags inlined as top-level fields: %s", encoded)
	}
	// A flag equal to its default is not written, so a key that never touched a
	// switch stays as compact as it was before the flag existed.
	if _, ok := decoded[1][testFlagName]; ok {
		t.Fatalf("expected a default-valued flag to stay out of the file: %s", encoded)
	}
	if _, ok := decoded[1]["unknown_field"]; ok {
		t.Fatalf("expected an undeclared field to be dropped: %s", encoded)
	}

	if _, err := parseConfigString(`{"api_keys":[{"key":"k1","` + testFlagName + `":"yes"}]}`); err == nil {
		t.Fatal("expected a non-bool flag value to fail decoding")
	}
}

// TestNormalizeCredentialsPreservesCredentialFlags 验证结构化 api_keys 归一化
// 不丢开关，且开关参与 credentials 变更比较。
func TestNormalizeCredentialsPreservesCredentialFlags(t *testing.T) {
	registerTestFlag(t)
	on := APIKey{Key: " k1 ", Name: " a "}
	on.SetFlag(testFlagName, true)
	off := APIKey{Key: "k2"}

	cfg := Config{APIKeys: []APIKey{on, off}}
	cfg.NormalizeCredentials()

	if len(cfg.APIKeys) != 2 {
		t.Fatalf("unexpected api keys: %#v", cfg.APIKeys)
	}
	if !cfg.APIKeys[0].Flag(testFlagName) {
		t.Fatalf("expected the flag to survive normalization: %#v", cfg.APIKeys[0])
	}
	if cfg.APIKeys[0].Key != "k1" || cfg.APIKeys[0].Name != "a" {
		t.Fatalf("expected trimmed key/name: %#v", cfg.APIKeys[0])
	}
	if cfg.APIKeys[1].Flag(testFlagName) {
		t.Fatalf("expected default off: %#v", cfg.APIKeys[1])
	}

	if APIKeyEqual(APIKey{Key: "k1"}, cfg.APIKeys[0]) {
		t.Fatal("expected the flag to participate in api key equality")
	}
}

// TestCredentialFlagsSurviveLegacyKeyFallback 验证 legacy keys 回退路径会从
// meta 里带回开关。
func TestCredentialFlagsSurviveLegacyKeyFallback(t *testing.T) {
	registerTestFlag(t)
	meta := APIKey{Key: "k1", Name: "a"}
	meta.SetFlag(testFlagName, true)

	out := apiKeysFromStrings([]string{"k1", "k2"}, map[string]APIKey{"k1": meta})
	if len(out) != 2 {
		t.Fatalf("unexpected result: %#v", out)
	}
	if !out[0].Flag(testFlagName) {
		t.Fatalf("expected the meta flag to be carried over: %#v", out[0])
	}
	if out[1].Flag(testFlagName) {
		t.Fatalf("expected a key without meta to read the default: %#v", out[1])
	}

	mapped := apiKeyMap([]APIKey{meta})
	if !mapped["k1"].Flag(testFlagName) {
		t.Fatalf("expected apiKeyMap to keep the flag: %#v", mapped)
	}
}

// TestCloneDoesNotShareCredentialFlags 验证 Clone 深拷贝开关，改动快照不会
// 反向污染 Store 内部状态。
func TestCloneDoesNotShareCredentialFlags(t *testing.T) {
	registerTestFlag(t)
	src := APIKey{Key: "k1"}
	src.SetFlag(testFlagName, true)
	cfg := Config{APIKeys: []APIKey{src}}

	clone := cfg.Clone()
	clone.APIKeys[0].SetFlag(testFlagName, false)

	if !cfg.APIKeys[0].Flag(testFlagName) {
		t.Fatal("mutating a clone must not reach back into the original")
	}
}

// TestMergeCredentialFlagsKeepsRestrictive 验证限制性开关在导入合并时两侧取或，
// 配置导入不会静默丢掉用户已显式设置的限制。合并只处理开关，保留哪一侧的
// name/remark 由调用方决定。
func TestMergeCredentialFlagsKeepsRestrictive(t *testing.T) {
	registerTestFlag(t)
	existing := APIKey{Key: "k1", Name: "kept"}
	existing.SetFlag(testFlagName, true)
	incoming := APIKey{Key: "k1", Name: "imported"}

	merged := MergeCredentialFlags(existing, incoming)
	if !merged.Flag(testFlagName) {
		t.Fatalf("expected a restrictive flag to stay on through a merge: %#v", merged)
	}
	if merged.Name != "kept" {
		t.Fatalf("expected the merge to leave metadata to the caller: %#v", merged)
	}

	// 非限制性开关跟随 incoming，即合并选中的那一条记录。
	off := MergeCredentialFlags(incoming, APIKey{Key: "k1"})
	if off.Flag(testFlagName) {
		t.Fatalf("expected a non-restrictive flag to follow incoming: %#v", off)
	}
}

// TestMergeCredentialFlagsDoesNotMutateInputs 验证合并结果独占 flags map，
// 改动合并结果不会反向污染两侧入参。
func TestMergeCredentialFlagsDoesNotMutateInputs(t *testing.T) {
	registerTestFlag(t)
	existing := APIKey{Key: "k1"}
	existing.SetFlag(testFlagName, true)
	incoming := APIKey{Key: "k1"}

	merged := MergeCredentialFlags(existing, incoming)
	merged.SetFlag(testFlagName, false)

	if !existing.Flag(testFlagName) {
		t.Fatalf("the merge must not modify its inputs: %#v", existing)
	}
	if incoming.Flag(testFlagName) {
		t.Fatalf("the merge must not modify its inputs: %#v", incoming)
	}
}
