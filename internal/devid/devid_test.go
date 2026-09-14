package devid

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 测试不得携带真实公钥：需要真公钥的用例从本地 data/devid/public_key 读取，
// 缺失时跳过。
const dummyPubKey = "test-public-key-placeholder"

var fakeDeviceID = "fake-device-id-0123456789abcdef"

func TestConfigValidate(t *testing.T) {
	if err := (Config{}).Validate(); err == nil {
		t.Fatal("expected error for empty config")
	}
	if err := (Config{PublicKey: dummyPubKey}).Validate(); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}
	normalized := Config{PublicKey: " " + dummyPubKey + " "}.Normalize()
	if normalized.PublicKey != dummyPubKey {
		t.Fatalf("expected trimmed public_key")
	}
}

func TestLoadPublicKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "public_key")
	if _, err := LoadPublicKeyFile(path); err == nil {
		t.Fatal("expected error for missing file")
	}
	if err := os.WriteFile(path, []byte("  "+dummyPubKey+"\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPublicKeyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != dummyPubKey {
		t.Fatalf("expected trimmed key, got %q", got)
	}
	// 带中文编辑器常见 BOM 也能读取
	if err := os.WriteFile(path, append([]byte{0xEF, 0xBB, 0xBF}, []byte(dummyPubKey)...), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = LoadPublicKeyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != dummyPubKey {
		t.Fatalf("expected BOM-stripped key, got %q", got)
	}

	// 兼容旧版 JSON 内容 {"public_key": "..."}
	jsonBody, _ := json.Marshal(Config{PublicKey: dummyPubKey})
	if err := os.WriteFile(path, jsonBody, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = LoadPublicKeyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != dummyPubKey {
		t.Fatalf("expected JSON public_key, got %q", got)
	}
	// JSON 含 BOM 也能读取
	if err := os.WriteFile(path, append([]byte{0xEF, 0xBB, 0xBF}, jsonBody...), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err = LoadPublicKeyFile(path); err != nil || got != dummyPubKey {
		t.Fatalf("expected BOM JSON public_key, got %q err=%v", got, err)
	}
	// 非法 JSON 视为解析失败，不把原文当公钥
	if err := os.WriteFile(path, []byte("{not-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPublicKeyFile(path); err == nil {
		t.Fatal("expected error for invalid JSON content")
	}
	// JSON 中 public_key 为空同样报错
	if err := os.WriteFile(path, []byte(`{"public_key":""}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPublicKeyFile(path); err == nil {
		t.Fatal("expected error for empty JSON public_key")
	}
}

func TestProfileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p", "profile.json")
	profile, err := loadOrMakeProfile(path, newTestRNG())
	if err != nil {
		t.Fatal(err)
	}
	if profile.ScreenW <= 0 || profile.ScreenH <= 0 {
		t.Fatalf("invalid generated profile: %+v", profile)
	}
	reloaded, err := LoadProfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if *reloaded != *profile {
		t.Fatalf("profile round-trip mismatch: %+v vs %+v", reloaded, profile)
	}
	// 已有档案必须复用，不得覆盖
	profile.CanvasSeed = 12345
	if err := SaveProfile(path, profile); err != nil {
		t.Fatal(err)
	}
	reused, err := loadOrMakeProfile(path, newTestRNG())
	if err != nil {
		t.Fatal(err)
	}
	if reused.CanvasSeed != 12345 {
		t.Fatalf("expected existing profile to be reused, got %+v", reused)
	}
}

// TestGenerateWithFakeEndpoint 用本地假数美端点跑通完整 SDK 生成链路。
// SDK 资产不入库，本地 data/devid/ 缺文件时跳过。
func TestGenerateWithFakeEndpoint(t *testing.T) {
	assetDir := findAssetDir(t)
	testPubKey, err := LoadPublicKeyFile(filepath.Join(assetDir, "public_key"))
	if err != nil {
		t.Skipf("public_key not available: %v", err)
	}
	var gotPath string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":   1100,
			"detail": map[string]any{"deviceId": fakeDeviceID},
		})
	}))
	defer server.Close()

	saved := apiHostOverride
	apiHostOverride = server.URL[len("https://"):]
	defer func() { apiHostOverride = saved }()

	gen := &Generator{
		Config:   Config{PublicKey: testPubKey},
		AssetDir: assetDir,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
	deviceID, err := gen.Generate(context.Background(), filepath.Join(t.TempDir(), "profile.json"))
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if deviceID != "B"+fakeDeviceID {
		t.Fatalf("unexpected device id: %q", deviceID)
	}
	if gotPath != smApiPath {
		t.Fatalf("unexpected request path: %q", gotPath)
	}
}

// TestGenerateMissingConfig 验证 public_key 缺失时直接报错（调用方据此回退随机）。
func TestGenerateMissingConfig(t *testing.T) {
	gen := &Generator{}
	if _, err := gen.Generate(context.Background(), ""); err == nil {
		t.Fatal("expected error for missing devid config")
	}
}

// TestGenerateMissingAssets 验证 SDK 资产缺失时报错（调用方据此回退随机）。
func TestGenerateMissingAssets(t *testing.T) {
	gen := &Generator{
		Config:   Config{PublicKey: dummyPubKey},
		AssetDir: t.TempDir(),
	}
	if _, err := gen.Generate(context.Background(), filepath.Join(t.TempDir(), "profile.json")); err == nil {
		t.Fatal("expected error for missing fp assets")
	}
}

func findAssetDir(t *testing.T) string {
	t.Helper()
	for _, dir := range []string{"../../data/devid", "../../../data/devid", "../data/devid", "data/devid"} {
		if _, err := os.Stat(filepath.Join(dir, "fp-1.min.js")); err == nil {
			return dir
		}
	}
	t.Skip("fp SDK assets not available; place fp-1.min.js and browser_env.js under data/devid/")
	return ""
}
