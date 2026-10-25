package devid

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	dsprotocol "ds2api/internal/deepseek/protocol"
	trans "ds2api/internal/deepseek/transport"

	"github.com/dop251/goja"
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
	cat := loadTestCatalog(t)
	path := filepath.Join(t.TempDir(), "p", "profile.json")
	profile, err := loadOrMakeProfile(cat, path, newTestRNG())
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
	if !reflect.DeepEqual(*reloaded, *profile) {
		t.Fatalf("profile round-trip mismatch: %+v vs %+v", reloaded, profile)
	}
	// 已有档案必须复用，不得覆盖
	profile.CanvasSeed = 12345
	if err := SaveProfile(path, profile); err != nil {
		t.Fatal(err)
	}
	reused, err := loadOrMakeProfile(cat, path, newTestRNG())
	if err != nil {
		t.Fatal(err)
	}
	if reused.CanvasSeed != 12345 {
		t.Fatalf("expected existing profile to be reused, got %+v", reused)
	}
}

// v1 档案的 `"lang": "zh-CN"` 必须能读出来：读不出来就成了损坏档案，
// 调用方会重建一份新档案，等于给账号换了台设备。
// 补齐后的语言/时区/Accept-Language 必须与目录里同一条目一致。
func TestProfileReadsLegacyLangString(t *testing.T) {
	cat := loadTestCatalog(t)
	loc := cat.Locales[0]
	if len(cat.Locales) > 1 {
		loc = cat.Locales[1] // 取一条非默认语言，才测得到"按语言回填"
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	legacy := `{"screenW":1920,"screenH":1080,"availW":1920,"availH":1040,"cores":8,"lang":"` + loc.Code + `"}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	profile, err := LoadProfile(path)
	if err != nil {
		t.Fatalf("legacy profile must stay readable: %v", err)
	}
	if profile.Lang.Code != loc.Code {
		t.Fatalf("expected lang code from legacy string, got %q", profile.Lang.Code)
	}
	cat.normalizeWith(profile)
	if profile.OSFamily != cat.OSFamilies[0].Key || profile.Locale != loc.Code {
		t.Fatalf("unexpected normalized identity: %+v", profile)
	}
	if profile.TzName != loc.Tz || profile.AcceptLanguage != loc.AcceptLanguage {
		t.Fatalf("timezone/language should follow the legacy language: %+v", profile)
	}
	if len(profile.Langs) == 0 || profile.Langs[0] != loc.Langs[0] {
		t.Fatalf("language fields not derived from legacy lang: %+v", profile)
	}
	if profile.DeviceMemory <= 0 || profile.ColorDepth <= 0 {
		t.Fatalf("missing scalar fields not filled: %+v", profile)
	}
	if _, ok := TzOffsetMinutes(profile.TzName); !ok {
		t.Fatalf("normalized timezone %q is not loadable", profile.TzName)
	}
}

// 手工写了非 Windows OS 的档案必须收敛到 Windows：httpcloak 预设与登录 UA 都是
// Windows 版，一份 macOS 指纹却用 Windows 的 TLS 握手出去，比「整批同为 Windows」
// 更扎眼。这里同时校验环境渲染出的上报身份也确实是 Windows。
// 目录里没有的 OS 画像必须被换掉：画像决定 UA/platform，而传输层预设与登录 UA
// 都是固定平台——留着目录外的值等于让指纹与握手各说一套。这里同时校验环境渲染出的
// 身份确实来自目录里那一条画像。
func TestProfileNormalizeUsesCatalogFamily(t *testing.T) {
	cat := loadTestCatalog(t)
	assetDir := findAssetDir(t)
	envJS, err := os.ReadFile(filepath.Join(assetDir, envAssetFile))
	if err != nil {
		t.Skipf("browser env not available: %v", err)
	}
	fam := cat.OSFamilies[0]
	locale := cat.Locales[0]
	path := filepath.Join(t.TempDir(), "profile.json")
	alien := `{"screenW":1512,"screenH":982,"availW":1512,"availH":982,"dpr":2,"cores":10,
		"osFamily":"not-in-catalog","locale":"` + locale.Code + `","langs":["` + locale.Langs[0] + `"],
		"tzName":"` + locale.Tz + `","lang":{"code":"` + locale.Code + `","list":["` + locale.Langs[0] + `"]},
		"acceptLanguage":"` + locale.AcceptLanguage + `"}`
	if err := os.WriteFile(path, []byte(alien), 0o644); err != nil {
		t.Fatal(err)
	}
	profile, err := LoadProfile(path)
	if err != nil {
		t.Fatal(err)
	}
	cat.normalizeWith(profile)
	if profile.OSFamily != fam.Key {
		t.Fatalf("unknown OS family must fall back to the catalog's first (%q), got %q", fam.Key, profile.OSFamily)
	}

	vm := goja.New()
	if _, err := newEventLoop(vm); err != nil {
		t.Fatal(err)
	}
	offset, ok := TzOffsetMinutes(profile.TzName)
	if !ok {
		t.Fatalf("timezone %q not loadable", profile.TzName)
	}
	gen := &Generator{}
	if err := gen.injectGlobals(vm, profile, Config{PublicKey: dummyPubKey}, offset, float64(time.Now().UnixMilli()), nil); err != nil {
		t.Fatal(err)
	}
	prog, err := goja.Compile(envAssetFile, string(envJS), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunProgram(prog); err != nil {
		t.Fatal(err)
	}
	identity := readBrowserIdentity(vm)
	// UA 必须正好是目录里那条画像的模板（版本号取自 UA 自身，模板用 %s 占位）。
	major := ""
	if m := regexp.MustCompile(`Chrome/(\d+)\.`).FindStringSubmatch(identity.UserAgent); m != nil {
		major = m[1]
	}
	if major == "" || identity.UserAgent != strings.ReplaceAll(fam.UA, "%s", major) {
		t.Errorf("rendered UA %q does not match catalog family %q", identity.UserAgent, fam.Key)
	}
	if identity.SecChUaPlatform != `"`+fam.UAPlatform+`"` {
		t.Errorf("sec-ch-ua-platform = %q, want %q for family %q", identity.SecChUaPlatform, `"`+fam.UAPlatform+`"`, fam.Key)
	}
	// 语言/时区轴不受影响：只有 OS 画像被换掉。
	if identity.AcceptLanguage != locale.AcceptLanguage {
		t.Errorf("locale axis should be untouched, got %q", identity.AcceptLanguage)
	}
}

// TestBrowserEnvSelftest 跑环境脚本自带的「检测方视角」探针（原生指纹、原型链、
// 插件、canvas PNG、时区、globalThis 等 60 余条）。这是伪装升级后最硬的一道
// 回归闸门：探针全过才说明这版环境真的立得住，而不是只在 Go 侧编译通过。
//
// 两种身份都要过：资产内置的默认版本，以及生产路径注入的身份（版本来自传输层，
// sec-ch-ua 取抓包核对过的那份——它的 GREASE 占位品牌排在第一位）。
func TestBrowserEnvSelftest(t *testing.T) {
	assetDir := findAssetDir(t)
	envJS, err := os.ReadFile(filepath.Join(assetDir, envAssetFile))
	if err != nil {
		t.Skipf("browser env not available: %v", err)
	}
	cases := []struct {
		name     string
		identity Identity
	}{
		{name: "asset-default"},
		{name: "transport-identity", identity: Identity{
			ChromeMajor: trans.ChromeMajorVersion,
			SecChUa:     dsprotocol.WebSecChUA(),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runEnvSelftest(t, envJS, tc.identity)
		})
	}
}

// TestBrowserEnvTimezoneFollowsProfile 校验时区重写真的生效：指纹的 timezone
// 字段（getTimezoneOffset）与 smidV2 的时间戳都取自 JS 的本地时间。若退回宿主
// 进程时区，档案声称 Asia/Taipei 而时间戳按 UTC 走，二者就对不上。
func TestBrowserEnvTimezoneFollowsProfile(t *testing.T) {
	assetDir := findAssetDir(t)
	envJS, err := os.ReadFile(filepath.Join(assetDir, envAssetFile))
	if err != nil {
		t.Skipf("browser env not available: %v", err)
	}
	cases := []struct{ tz, locale string }{
		{"America/Los_Angeles", "en-US"},
		{"Asia/Taipei", "zh-TW"},
	}
	for _, tc := range cases {
		t.Run(tc.tz, func(t *testing.T) {
			profile := loadTestCatalog(t).newProfile(newTestRNG())
			profile.TzName, profile.Locale = tc.tz, tc.locale
			profile.Lang = LangPref{Code: tc.locale, List: []string{tc.locale}}
			profile.Langs = []string{tc.locale}
			offset, ok := TzOffsetMinutes(tc.tz)
			if !ok {
				t.Fatalf("timezone %s not loadable", tc.tz)
			}
			vm := goja.New()
			if _, err := newEventLoop(vm); err != nil {
				t.Fatal(err)
			}
			gen := &Generator{}
			if err := gen.injectGlobals(vm, profile, Config{PublicKey: dummyPubKey}, offset, float64(time.Now().UnixMilli()), nil); err != nil {
				t.Fatal(err)
			}
			prog, err := goja.Compile(envAssetFile, string(envJS), false)
			if err != nil {
				t.Fatalf("compile browser env: %v", err)
			}
			if _, err := vm.RunProgram(prog); err != nil {
				t.Fatalf("run browser env: %v", err)
			}
			val, err := vm.RunString(`(function () {
				var d = new Date();
				// 墙上时钟（本地分钟数）= 真实时间戳 - 时区偏移，与 Date 的本地读取方法同源
				var wallMinutes = Math.floor((d.getTime() - d.getTimezoneOffset() * 60000) / 60000);
				return d.getTimezoneOffset() + "|" + wallMinutes +
					"|" + new Intl.DateTimeFormat().resolvedOptions().timeZone +
					"|" + (d.getFullYear() + "-" + (d.getMonth() + 1) + "-" + d.getDate() + "T" + d.getHours()) +
					"|" + d.toString();
			})()`)
			if err != nil {
				t.Fatalf("probe failed: %v", err)
			}
			parts := strings.Split(val.String(), "|")
			if len(parts) != 5 {
				t.Fatalf("unexpected probe output %q", val.String())
			}
			if parts[0] != fmt.Sprint(offset) {
				t.Errorf("getTimezoneOffset = %s, want %d", parts[0], offset)
			}
			if parts[2] != tc.tz {
				t.Errorf("Intl timeZone = %s, want %s", parts[2], tc.tz)
			}
			// 字符串表面（新版 SDK 会收进指纹）必须与档案时区同源：
			// 形状像 Chrome，偏移标签与 getTimezoneOffset 一致，时区名已本地化。
			label := fmt.Sprintf("GMT%s%02d%02d", map[bool]string{true: "+", false: "-"}[offset <= 0],
				abs(offset)/60, abs(offset)%60)
			if !regexp.MustCompile(`^\w{3} \w{3} \d{2} \d{4} \d{2}:\d{2}:\d{2} GMT[+-]\d{4} \(.+\)$`).MatchString(parts[4]) {
				t.Errorf("Date.toString() 形状不像 Chrome: %q", parts[4])
			}
			if !strings.Contains(parts[4], label) {
				t.Errorf("Date.toString() = %q, 缺少偏移标签 %s", parts[4], label)
			}
			if strings.Contains(parts[4], "/") {
				t.Errorf("Date.toString() 的时区名没被本地化: %q", parts[4])
			}
			gotWall, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				t.Fatalf("parse wall clock %q: %v", parts[1], err)
			}
			loc, err := time.LoadLocation(tc.tz)
			if err != nil {
				t.Fatal(err)
			}
			// 容忍 1 分钟：JS 取值与这里比较之间可能跨过分钟边界。
			// 墙上时钟 = 把当地年月日时分当作 UTC 折算出的分钟数。
			local := time.Now().In(loc)
			wantWall := time.Date(local.Year(), local.Month(), local.Day(),
				local.Hour(), local.Minute(), 0, 0, time.UTC).Unix() / 60
			if gotWall-wantWall > 1 || wantWall-gotWall > 1 {
				t.Errorf("local wall clock = %s (%d min since epoch), want %d",
					parts[3], gotWall, wantWall)
			}
		})
	}
}

func runEnvSelftest(t *testing.T, envJS []byte, identity Identity) {
	t.Helper()
	profile := loadTestCatalog(t).newProfile(newTestRNG())
	vm := goja.New()
	if _, err := newEventLoop(vm); err != nil {
		t.Fatal(err)
	}
	gen := &Generator{Identity: identity}
	if err := gen.injectGlobals(vm, profile, Config{PublicKey: dummyPubKey}, profile.TzOffset, float64(time.Now().UnixMilli()), nil); err != nil {
		t.Fatal(err)
	}
	if err := vm.Set("__SELFTEST", true); err != nil {
		t.Fatal(err)
	}
	prog, err := goja.Compile(envAssetFile, string(envJS), false)
	if err != nil {
		t.Fatalf("compile browser env: %v", err)
	}
	if _, err := vm.RunProgram(prog); err != nil {
		t.Fatalf("run browser env: %v", err)
	}
	results := vm.Get("__probeResults")
	if results == nil || goja.IsUndefined(results) {
		t.Fatal("selftest produced no results")
	}
	obj := results.ToObject(vm)
	total := int(obj.Get("length").ToInteger())
	if total < 40 {
		t.Fatalf("selftest probe count looks truncated: %d", total)
	}
	failures := []string{}
	for i := 0; i < total; i++ {
		item := obj.Get(fmt.Sprint(i)).ToObject(vm)
		if item.Get("ok").ToBoolean() {
			continue
		}
		failures = append(failures, fmt.Sprintf("%s: %s = %v (want %v)",
			item.Get("name").String(), item.Get("expr").String(),
			item.Get("actual").Export(), item.Get("expect").Export()))
	}
	if len(failures) > 0 {
		t.Fatalf("browser env selftest failed %d/%d:\n%s", len(failures), total, strings.Join(failures, "\n"))
	}
}

// TestGenerateWithFakeEndpoint 用本地假数美端点跑通完整 SDK 生成链路，
// 并校验上报请求头与指纹身份逐项一致（身份来自环境脚本，不再是 Go 侧的硬编码常量）。
// SDK 资产不入库，本地 data/devid/ 缺文件时跳过。
func TestGenerateWithFakeEndpoint(t *testing.T) {
	assetDir := findAssetDir(t)
	testPubKey, err := LoadPublicKeyFile(filepath.Join(assetDir, "public_key"))
	if err != nil {
		t.Skipf("public_key not available: %v", err)
	}
	const (
		chromeMajor = "150"
		secChUa     = `"Not;A=Brand";v="8", "Chromium";v="150", "Google Chrome";v="150"`
	)
	var gotPath string
	var gotHeader http.Header
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeader = r.Header.Clone()
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

	// 先建好档案，才好在生成后断言上报头与该档案的画像一致。
	cat := loadTestCatalog(t)
	profilePath := filepath.Join(t.TempDir(), "profile.json")
	profile, err := loadOrMakeProfile(cat, profilePath, newTestRNG())
	if err != nil {
		t.Fatal(err)
	}

	var logLines []string
	gen := &Generator{
		Config:   Config{PublicKey: testPubKey},
		AssetDir: assetDir,
		Identity: Identity{ChromeMajor: chromeMajor, SecChUa: secChUa},
		// Debug 打开才会应用调试补丁并跑扩展包的解密校验配方：
		// 这条链路同时验证 patches.json 与 verify.js 都在、都能用。
		Debug:  true,
		Logger: func(format string, args ...any) { logLines = append(logLines, fmt.Sprintf(format, args...)) },
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
	deviceID, err := gen.Generate(context.Background(), profilePath)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if deviceID != "B"+fakeDeviceID {
		t.Fatalf("unexpected device id: %q", deviceID)
	}
	if gotPath != smApiPath {
		t.Fatalf("unexpected request path: %q", gotPath)
	}

	// 站点身份对齐真实抓包：跨站请求只带 origin，Sec-Fetch-* 按 cross-site 填。
	wantHeaders := map[string]string{
		"Accept":           "*/*",
		"Content-Type":     "application/json;charset=UTF-8",
		"Origin":           "https://platform.deepseek.com",
		"Referer":          "https://platform.deepseek.com/",
		"Sec-Fetch-Dest":   "empty",
		"Sec-Fetch-Mode":   "cors",
		"Sec-Fetch-Site":   "cross-site",
		"Accept-Language":  profile.AcceptLanguage,
		"sec-ch-ua":        secChUa,
		"sec-ch-ua-mobile": "?0",
	}
	for name, want := range wantHeaders {
		if got := gotHeader.Get(name); got != want {
			t.Errorf("header %s = %q, want %q", name, got, want)
		}
	}
	// 自称的浏览器版本与 UA-CH 必须同源；UA 的 OS 与 sec-ch-ua-platform 必须自洽。
	ua := gotHeader.Get("User-Agent")
	if !strings.Contains(ua, "Chrome/"+chromeMajor+".") {
		t.Errorf("User-Agent %q does not claim Chrome %s", ua, chromeMajor)
	}
	platform := gotHeader.Get("sec-ch-ua-platform")
	wantPlatform := ""
	switch {
	case strings.Contains(ua, "Windows NT"):
		wantPlatform = `"Windows"`
	case strings.Contains(ua, "Macintosh"):
		wantPlatform = `"macOS"`
	case strings.Contains(ua, "X11; Linux"):
		wantPlatform = `"Linux"`
	}
	if wantPlatform == "" {
		t.Fatalf("unexpected User-Agent OS token: %q", ua)
	}
	if platform != wantPlatform {
		t.Errorf("sec-ch-ua-platform = %q, want %q for UA %q", platform, wantPlatform, ua)
	}

	// 扩展包的解密校验配方必须真的解开了这次上报的 payload：
	// 指纹内容非空、且配方没报任何问题（补丁、原语、配方任一环节坏了都会在这里露出来）。
	fingerprintLine := ""
	for _, line := range logLines {
		if strings.Contains(line, "[devid] fingerprint:") {
			fingerprintLine = line
		}
		if strings.Contains(line, "[devid] verify:") {
			t.Errorf("verify recipe reported a problem: %s", line)
		}
	}
	if fingerprintLine == "" {
		t.Fatalf("verify recipe did not decode the payload; logs:\n%s", strings.Join(logLines, "\n"))
	}
	// 只断言"解出来的确实是一份有内容的指纹 json"，不在这里点名具体字段：
	// 字段含义属于逆出来的协议细节，仓库里不重复一遍。
	raw := strings.TrimPrefix(fingerprintLine, "[devid] fingerprint: ")
	var fingerprint map[string]any
	if err := json.Unmarshal([]byte(raw), &fingerprint); err != nil {
		t.Fatalf("decoded fingerprint is not valid json: %v", err)
	}
	if len(fingerprint) < 5 {
		t.Errorf("decoded fingerprint looks empty: %s", raw)
	}

	// smidV2 必须落回档案：不落盘等于每次生成都是一台全新设备。
	saved2, err := LoadProfile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if saved2.SmidV2 == "" {
		t.Fatal("smidV2 was not persisted into the device profile")
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

// 扩展包只放必需件（没有 profiles.json）时的两条回退路径：
//   - 需要新建档案 → 如实报错并点出缺的是画像目录，不用仓库里的兜底分布顶替；
//   - 档案已存在 → 照常生成，画像补齐交给环境脚本的兜底表。
func TestCatalogMissingFallsBack(t *testing.T) {
	srcDir := findAssetDir(t)
	envJS, err := os.ReadFile(filepath.Join(srcDir, envAssetFile))
	if err != nil {
		t.Skipf("browser env not available: %v", err)
	}
	fpJS, err := os.ReadFile(filepath.Join(srcDir, fpAssetFile))
	if err != nil {
		t.Skipf("fp SDK not available: %v", err)
	}
	testPubKey, err := LoadPublicKeyFile(filepath.Join(srcDir, "public_key"))
	if err != nil {
		t.Skipf("public_key not available: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fpAssetFile), fpJS, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, envAssetFile), envJS, 0o644); err != nil {
		t.Fatal(err)
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
		AssetDir: dir,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
	_, err = gen.Generate(context.Background(), filepath.Join(t.TempDir(), "profile.json"))
	if err == nil || !strings.Contains(err.Error(), catalogFile) {
		t.Fatalf("missing catalog with no profile must fail naming %s, got %v", catalogFile, err)
	}

	// 用完整扩展包生成一份档案，再让"只有必需件"的扩展包复用它。
	profilePath := filepath.Join(t.TempDir(), "profile.json")
	if _, err := loadOrMakeProfile(loadTestCatalog(t), profilePath, newTestRNG()); err != nil {
		t.Fatal(err)
	}
	deviceID, err := gen.Generate(context.Background(), profilePath)
	if err != nil {
		t.Fatalf("an existing profile must not need the catalog: %v", err)
	}
	if deviceID != "B"+fakeDeviceID {
		t.Fatalf("unexpected device id: %q", deviceID)
	}
	if gotPath != smApiPath {
		t.Fatalf("unexpected request path: %q", gotPath)
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

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// GPU 池：同一份档案必须每次挑到同一块显卡，不同档案要挑到不同的；
// 档位还得跟核心数匹配（4 核不该配 4090，24 核不该配 HD 3000）；
// 池子缺失时退回环境脚本内置的那一条。
func TestBrowserEnvGPUPoolSelection(t *testing.T) {
	assetDir := findAssetDir(t)
	envJS, err := os.ReadFile(filepath.Join(assetDir, envAssetFile))
	if err != nil {
		t.Skipf("browser env not available: %v", err)
	}
	poolRaw, err := os.ReadFile(filepath.Join(assetDir, gpuPoolFile))
	if err != nil {
		t.Skipf("gpu pool not available: %v", err)
	}
	var pool struct {
		Entries []struct {
			Renderer string `json:"renderer"`
			Tier     string `json:"tier"`
		} `json:"entries"`
		TierByCores []struct {
			MaxCores int      `json:"maxCores"`
			Tiers    []string `json:"tiers"`
		} `json:"tierByCores"`
	}
	if err := json.Unmarshal(poolRaw, &pool); err != nil {
		t.Fatalf("parse gpu pool: %v", err)
	}
	rendererFor := func(t *testing.T, profile *Profile, poolJSON string) string {
		t.Helper()
		vm := goja.New()
		if _, err := newEventLoop(vm); err != nil {
			t.Fatal(err)
		}
		gen := &Generator{}
		offset, _ := TzOffsetMinutes(profile.TzName)
		if err := gen.injectGlobals(vm, profile, Config{PublicKey: dummyPubKey}, offset, float64(time.Now().UnixMilli()), assetsFor(poolJSON)); err != nil {
			t.Fatal(err)
		}
		prog, err := goja.Compile(envAssetFile, string(envJS), false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := vm.RunProgram(prog); err != nil {
			t.Fatal(err)
		}
		val, err := vm.RunString(`(function () {
			var g = document.createElement('canvas').getContext('webgl');
			var ext = g.getExtension('WEBGL_debug_renderer_info');
			return g.getParameter(ext.UNMASKED_RENDERER_WEBGL);
		})()`)
		if err != nil {
			t.Fatalf("renderer probe failed: %v", err)
		}
		return val.String()
	}
	tierOf := func(renderer string) string {
		for _, e := range pool.Entries {
			if e.Renderer == renderer {
				return e.Tier
			}
		}
		return ""
	}
	allowedTiers := func(cores int) []string {
		for _, rule := range pool.TierByCores {
			if cores <= rule.MaxCores {
				return rule.Tiers
			}
		}
		return nil
	}

	cat := loadTestCatalog(t)
	profile := cat.newProfile(newTestRNG())
	first := rendererFor(t, profile, string(poolRaw))
	if first == "" {
		t.Fatal("empty renderer")
	}
	if second := rendererFor(t, profile, string(poolRaw)); second != first {
		t.Fatalf("same profile must keep the same GPU: %q vs %q", first, second)
	}

	seen := map[string]bool{first: true}
	for i := 0; i < 12; i++ {
		seen[rendererFor(t, cat.newProfile(newTestRNG()), string(poolRaw))] = true
	}
	if len(seen) < 4 {
		t.Fatalf("GPU not varied enough: %d distinct in 13 draws", len(seen))
	}

	// 档位与核心数必须匹配：抽查低/中/高三档
	for _, cores := range []int{4, 8, 24} {
		p := cat.newProfile(newTestRNG())
		p.Cores = cores
		renderer := rendererFor(t, p, string(poolRaw))
		tier := tierOf(renderer)
		if tier == "" {
			t.Fatalf("renderer %q is not from the pool", renderer)
		}
		allowed := allowedTiers(cores)
		ok := false
		for _, want := range allowed {
			if want == tier {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%d 核拿到 %s 档显卡（允许 %v）: %q", cores, tier, allowed, renderer)
		}
	}

	// 池子缺失 → 退回内置串（不报错，也不出现空值）
	if fallback := rendererFor(t, profile, ""); !strings.Contains(fallback, "ANGLE (") {
		t.Errorf("fallback renderer looks wrong: %q", fallback)
	}
}

// assetsFor 造一个只带 GPU 池的扩展包，供环境测试注入。
func assetsFor(gpuPoolJSON string) *assets {
	if gpuPoolJSON == "" {
		return nil
	}
	return &assets{gpuPool: gpuPoolJSON}
}
