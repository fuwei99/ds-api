// Package devid 在 goja 中运行数美 fp SDK，为 DeepSeek 账号生成真实设备 ID。
//
// 本包在仓库里只保留加载与解释代码：SDK（fp-1.min.js）、浏览器环境模拟
// （browser_env.js）、画像目录（profiles.json）、SDK 补丁（patches.json）、
// 解密校验配方（verify.js）都来自扩展包目录（data/devid/，见 assets.go）。
// 缺失时的降级语义写在 assets.go 顶部：必需件缺失即报错，调用方保留账号已有
// device_id；可选件缺失只会降低 Debug 能力，不影响生成。
//
// 上报必须走与登录同一套浏览器指纹传输（internal/deepseek/transport 的 Chrome
// 预设）：数美看到的除了加密指纹，还有 TLS ClientHello 与 HTTP/2 指纹。一批账号
// 共用 Go 默认 HTTP 栈的握手，是比 payload 稳定得多的团伙特征。
package devid

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
	_ "time/tzdata" // 档案时区按 IANA 名解析：Windows 部署没有系统时区库

	"github.com/dop251/goja"
)

// SDK 侧接入参数，全部固化为常量；仅 public_key 允许外置/覆盖。
// 这些值在任何一次真实浏览器请求里都明文可见，不是秘密。
const (
	smOrg     = "P9usCUBauxft8eAmUXaZ"
	smAppID   = "default"
	smApiHost = "fp-it-acc.portal101.cn"
	smApiPath = "/deviceprofile/v4"
	smProto   = "https"
)

// apiHostOverride 仅供测试注入假端点，生产恒为 smApiHost。
var apiHostOverride string

func effectiveApiHost() string {
	if apiHostOverride != "" {
		return apiHostOverride
	}
	return smApiHost
}

// Config 是数美接入参数，仅公钥一项。
type Config struct {
	PublicKey string `json:"public_key,omitempty"`
}

// Normalize 填充默认值并 trim 输入。
func (c Config) Normalize() Config {
	return Config{PublicKey: strings.TrimSpace(c.PublicKey)}
}

// Validate 检查必填项。
func (c Config) Validate() error {
	if strings.TrimSpace(c.PublicKey) == "" {
		return errors.New("devid: public_key is required")
	}
	return nil
}

// Identity 是指纹身份里与传输层共享的部分：自称的 Chrome 版本与 sec-ch-ua。
//
// 版本号必须与传输层 uTLS 预设同源（transport.ChromeMajorVersion）。指纹自称
// Chrome 153、TLS ClientHello 却是另一个版本，是同一个请求内部就能对上的矛盾。
// 三项全留空时沿用扩展包内的默认版本。
type Identity struct {
	// ChromeMajor 是 UA / brands / UA-CH 里的主版本号。
	ChromeMajor string
	// SecChUa 是 sec-ch-ua 头。GREASE 元组（品牌顺序、占位品牌名）每个大版本
	// 都变，必须用真实抓包核对过的那一份（protocol.WebSecChUA）。
	SecChUa string
	// UserAgent 整体覆盖 UA。留空则由扩展包按档案的 OS 画像生成——覆盖成固定值
	// 会让 UA 与 navigator.platform / sec-ch-ua-platform 互相矛盾。
	UserAgent string
}

// LoadPublicKeyFile 读取公钥文件，兼容两种内容格式：
//   - 纯文本：文件内容即公钥本身；
//   - JSON：{"public_key": "..."}（旧版 data/devid/config.json 格式）。
//
// 文件缺失、JSON 非法或不含非空 public_key 时返回错误。
func LoadPublicKeyFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	key := ParsePublicKey(data)
	if key == "" {
		return "", fmt.Errorf("devid: no public_key found in %s", path)
	}
	return key, nil
}

// ParsePublicKey 从文件内容解析公钥，自动识别纯文本与 JSON 两种格式。
// 以 "{" 开头视为 JSON：解析失败或 public_key 为空时返回空串；
// 其余内容按纯文本处理（去除 BOM 与首尾空白）。
func ParsePublicKey(data []byte) string {
	trimmed := trimKey(string(data))
	if strings.HasPrefix(trimmed, "{") {
		var cfg Config
		if err := json.Unmarshal([]byte(trimmed), &cfg); err != nil {
			return ""
		}
		return strings.TrimSpace(cfg.PublicKey)
	}
	return trimmed
}

func trimKey(s string) string {
	s = strings.TrimPrefix(s, "\uFEFF")
	return strings.TrimSpace(s)
}

// Doer 与标准库 http.RoundTripper 调用形态一致，*http.Client 天然满足。
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Generator 运行 fp SDK 并向数美接口换取 deviceId。
// 零值不可用，请以带 Config 与 AssetDir 的方式构造。
type Generator struct {
	Config Config
	// AssetDir 指向扩展包目录（data/devid/），见 assets.go。
	AssetDir string
	// HTTP 发送指纹上报请求；nil 时使用 30s 超时的默认客户端。
	// 生产路径必须传带浏览器指纹的传输（client.devidHTTPClient）。
	HTTP Doer
	// Identity 是上报与指纹共用的浏览器身份版本，见 Identity。
	Identity Identity
	// Debug 开启后读取泄露桩、按补丁表应用调试补丁，并跑扩展包的解密校验配方。
	Debug bool
	// Logger 为调试日志回调，nil 时丢弃。
	Logger func(format string, args ...any)

	vm atomic.Pointer[goja.Runtime]
}

func (g *Generator) logf(format string, args ...any) {
	if g != nil && g.Logger != nil {
		g.Logger(format, args...)
	}
}

type genResult struct {
	deviceID string
	err      error
}

// Generate 返回 "B" 前缀的真实 device_id。
// profilePath 指向设备档案文件：存在则复用（同一台"设备"），
// 不存在则按扩展包的画像目录新建并写入。path 为空时每次使用临时档案。
func (g *Generator) Generate(ctx context.Context, profilePath string) (string, error) {
	if err := g.Config.Validate(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	resultCh := make(chan genResult, 1)
	go func() {
		resultCh <- g.run(ctx, profilePath, rng)
	}()
	select {
	case r := <-resultCh:
		if r.err != nil {
			return "", r.err
		}
		return r.deviceID, nil
	case <-ctx.Done():
		if vm := g.vm.Load(); vm != nil {
			vm.Interrupt("devid generation canceled")
		}
		return "", ctx.Err()
	}
}

// loadProfile 复用已有档案；不存在则用扩展包的画像目录新建。
// 目录缺失且没有档案时如实报错（不在仓库里塞一份兜底分布）。
func (g *Generator) loadProfile(pkg *assets, profilePath string, rng *rand.Rand) (*Profile, error) {
	if profilePath != "" {
		if _, err := os.Stat(profilePath); err == nil {
			profile, err := LoadProfile(profilePath)
			if err != nil {
				return nil, err
			}
			// 目录在时用它补齐旧档案缺的画像字段；不在时交给环境脚本的兜底表。
			if cat, err := pkg.catalog(); err == nil {
				cat.normalizeWith(profile)
			}
			return profile, nil
		}
	}
	cat, err := pkg.catalog()
	if err != nil {
		return nil, fmt.Errorf("devid: profile catalog unavailable: %w", err)
	}
	return loadOrMakeProfile(cat, profilePath, rng)
}

func (g *Generator) run(ctx context.Context, profilePath string, rng *rand.Rand) genResult {
	pkg, err := loadAssets(g.AssetDir)
	if err != nil {
		return genResult{err: err}
	}
	profile, err := g.loadProfile(pkg, profilePath, rng)
	if err != nil {
		return genResult{err: err}
	}

	cfg := g.Config.Normalize()
	// 模拟页面加载耗时后触发 SDK (0.8~3.8s)
	startMs := float64(time.Now().UnixMilli() - int64(rng.Intn(3000)) - 800)
	// 时区按档案实时换算而非取档案里的存值：带夏令时的地区一年有半年是另一个偏移，
	// 而指纹里的 timezone 必须与"当前"一致。
	tzOffset := profile.TzOffset
	if off, ok := TzOffsetMinutes(profile.TzName); ok {
		tzOffset = off
	}

	vm := goja.New()
	g.vm.Store(vm)
	el, err := newEventLoop(vm)
	if err != nil {
		return genResult{err: fmt.Errorf("init event loop: %w", err)}
	}
	if err := g.injectGlobals(vm, profile, cfg, tzOffset, startMs, pkg); err != nil {
		return genResult{err: err}
	}

	prelude, err := goja.Compile(envAssetFile, pkg.envJS, false)
	if err != nil {
		return genResult{err: fmt.Errorf("compile browser env: %w", err)}
	}
	if _, err := vm.RunProgram(prelude); err != nil {
		return genResult{err: fmt.Errorf("run browser env: %w", err)}
	}

	ident := readBrowserIdentity(vm)
	if ident.UserAgent == "" {
		return genResult{err: errors.New("devid: browser env produced no identity")}
	}
	headers := readRequestHeaders(vm)

	src, patchNotes := pkg.applyPatches(pkg.fpJS, g.Debug)
	if g.Debug {
		for _, note := range patchNotes {
			g.logf("[devid] %s", note)
		}
	}
	if _, err := vm.RunString("var __leak = {};"); err != nil {
		return genResult{err: fmt.Errorf("init leak stub: %w", err)}
	}
	sdkProg, err := goja.Compile(fpAssetFile, src, false)
	if err != nil {
		return genResult{err: fmt.Errorf("compile fp sdk: %w", err)}
	}
	if _, err := vm.RunProgram(sdkProg); err != nil {
		return genResult{err: fmt.Errorf("run fp sdk: %w", err)}
	}

	captured := func() int {
		capObj := vm.Get("__captured")
		if capObj == nil || goja.IsUndefined(capObj) {
			return 0
		}
		return int(capObj.ToObject(vm).Get("length").ToInteger())
	}

	deadline := time.Now().Add(sdkDeadline(ctx))
	el.runUntil(deadline, func() bool { return captured() > 0 }, 300*time.Millisecond)

	if captured() == 0 {
		return genResult{err: errors.New("devid: fp sdk produced no request")}
	}
	capObj := vm.Get("__captured").ToObject(vm)
	last := capObj.Get(fmt.Sprint(captured() - 1)).ToObject(vm)
	reqURL := last.Get("url").String()
	reqBody := last.Get("body").String()

	var payload struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal([]byte(reqBody), &payload); err != nil {
		return genResult{err: fmt.Errorf("devid: parse payload: %w", err)}
	}
	if g.Debug {
		g.dumpLeaks(vm)
		g.verifyFingerprint(vm, pkg, payload.Data)
	}
	g.persistSmidV2(vm, profilePath, profile)

	return g.submit(ctx, reqURL, reqBody, headers, ident)
}

// persistSmidV2 把 SDK 生成的 smidV2 落回档案。
//
// 真实浏览器二次访问会从 localStorage 复用同一个值，不落盘则每次生成都是一台
// 全新设备——数美会据此换算出不同的 device_id，"复用档案 = 同一台设备"也就失效了。
func (g *Generator) persistSmidV2(vm *goja.Runtime, profilePath string, profile *Profile) {
	if profilePath == "" {
		return
	}
	value, err := vm.RunString("localStorage.getItem('smidV2')")
	if err != nil || value == nil || goja.IsNull(value) || goja.IsUndefined(value) {
		return
	}
	smid := strings.TrimSpace(value.String())
	if smid == "" || smid == profile.SmidV2 {
		return
	}
	profile.SmidV2 = smid
	if err := SaveProfile(profilePath, profile); err != nil {
		g.logf("[devid] save smidV2 failed: %v", err)
	}
}

// dumpLeaks 打印补丁写进 __leak 的调试字段（内容由扩展包的补丁表决定）。
func (g *Generator) dumpLeaks(vm *goja.Runtime) {
	leak := vm.Get("__leak")
	if leak == nil || goja.IsUndefined(leak) {
		return
	}
	obj := leak.ToObject(vm)
	for _, key := range obj.Keys() {
		g.logf("[devid] leak.%s = %v", key, obj.Get(key))
	}
}

// verifyFingerprint 跑扩展包里的解密校验配方（verify.js）。
// 配方或密钥材料缺失时只记一行日志，不影响生成。
func (g *Generator) verifyFingerprint(vm *goja.Runtime, pkg *assets, dataHex string) {
	if strings.TrimSpace(pkg.verifyJS) == "" {
		g.logf("[devid] verify skipped: 扩展包未提供解密校验配方")
		return
	}
	prog, err := goja.Compile(verifyFile, pkg.verifyJS, false)
	if err != nil {
		g.logf("[devid] verify skipped: %v", err)
		return
	}
	if _, err := vm.RunProgram(prog); err != nil {
		g.logf("[devid] verify skipped: %v", err)
		return
	}
	fn, ok := goja.AssertFunction(vm.Get("__verify"))
	if !ok {
		g.logf("[devid] verify skipped: 配方未定义 __verify")
		return
	}
	// 密钥材料就在 __leak 里，由配方自己按补丁表的约定取：Go 侧不认识任何
	// 具体字段名，也就不会在源码里重复一遍逆出来的协议细节。
	res, err := fn(goja.Undefined(), vm.ToValue(dataHex), vm.Get("__leak"))
	if err != nil {
		g.logf("[devid] verify failed: %v", err)
		return
	}
	if res == nil || goja.IsNull(res) || goja.IsUndefined(res) {
		g.logf("[devid] verify failed: 配方未返回结果")
		return
	}
	obj := res.ToObject(vm)
	if fp := obj.Get("fingerprint"); fp != nil && !goja.IsNull(fp) && !goja.IsUndefined(fp) {
		if raw, err := json.Marshal(fp.Export()); err == nil {
			g.logf("[devid] fingerprint: %s", string(raw))
		}
	}
	// problems 是判失败的问题，notes 是逐条检查结论——两个列表都由配方自己定义。
	g.logVerifyList(vm, obj, "problems", "[devid] verify: %s")
	g.logVerifyList(vm, obj, "notes", "[devid] check: %s")
}

// logVerifyList 逐条打印配方返回的字符串数组。
func (g *Generator) logVerifyList(vm *goja.Runtime, obj *goja.Object, key, format string) {
	val := obj.Get(key)
	if val == nil || goja.IsNull(val) || goja.IsUndefined(val) {
		return
	}
	list := val.ToObject(vm)
	for i := 0; i < int(list.Get("length").ToInteger()); i++ {
		g.logf(format, list.Get(fmt.Sprint(i)).String())
	}
}

// ==================== 注入给环境脚本的宿主能力 ====================
//
// 这里只提供通用原语（压缩、随机、摘要算法、调用栈取帧）：任何一份环境脚本都能
// 用它们搭自己的配方，因此原语本身不暴露"我们怎么伪装"。渲染格式、补丁内容、
// 解密配方都在扩展包里。

// injectGlobals 把宿主能力、档案与身份覆盖注入 goja。pkg 里的数据类文件
// （GPU 池、字体实测表）原样交给环境脚本，由它自己解析与挑选；pkg 为 nil 时
// 环境脚本用内置兜底。
func (g *Generator) injectGlobals(vm *goja.Runtime, profile *Profile, cfg Config, tzOffset int, startMs float64, pkg *assets) error {
	profileJSON, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("marshal profile: %w", err)
	}
	globals := map[string]any{
		"__zlibDeflate":      zlibDeflate(vm),
		"__inflate":          inflatePrimitive(vm),
		"__randomBytes":      randomBytes(vm),
		"__b64Decode":        b64DecodePrimitive(vm),
		"__aesCbcDecryptHex": aesCbcDecryptHex(vm),
		"__stackFrames":      stackFrames(vm),
		"__captureStack":     legacyCaptureStack(vm),
		"__profile":          string(profileJSON),
		"__START":            startMs,
		"__TZ_OFFSET":        tzOffset,
		"__smAppId":          smAppID,
		"__smOrg":            smOrg,
		"__smPubKey":         cfg.PublicKey,
		"__smProtocol":       smProto,
		"__smApiHost":        effectiveApiHost(),
		"__smApiPath":        smApiPath,
	}
	// 身份覆盖只在调用方给了值时才注入：留空即用扩展包内的默认版本。
	if major := strings.TrimSpace(g.Identity.ChromeMajor); major != "" {
		globals["__chromeMajor"] = major
	}
	// 数据类文件同理：不注入时环境脚本退回内置兜底（单条渲染器串 / 粗略量宽模型）。
	if pkg != nil {
		if strings.TrimSpace(pkg.gpuPool) != "" {
			globals["__gpuPool"] = pkg.gpuPool
		}
		if strings.TrimSpace(pkg.fonts) != "" {
			globals["__fonts"] = pkg.fonts
		}
	}
	if chUa := strings.TrimSpace(g.Identity.SecChUa); chUa != "" {
		globals["__secChUaOverride"] = chUa
	}
	if ua := strings.TrimSpace(g.Identity.UserAgent); ua != "" {
		globals["__uaOverride"] = ua
	}
	for name, value := range globals {
		if err := vm.Set(name, value); err != nil {
			return fmt.Errorf("set %s: %w", name, err)
		}
	}
	return nil
}

// zlibDeflate 供环境脚本生成 PNG 的真实 deflate 流（goja 无 zlib）。
func zlibDeflate(vm *goja.Runtime) func(call goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		raw := bytesFromValue(vm, call.Argument(0))
		var buf bytes.Buffer
		zw := zlib.NewWriter(&buf)
		if _, err := zw.Write(raw); err != nil {
			panic(vm.ToValue(err.Error()))
		}
		if err := zw.Close(); err != nil {
			panic(vm.ToValue(err.Error()))
		}
		return vm.ToValue(vm.NewArrayBuffer(buf.Bytes()))
	}
}

// inflatePrimitive 是通用解压：gzip 与 zlib 都认，失败返回 null。
func inflatePrimitive(vm *goja.Runtime) func(call goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		raw := bytesFromValue(vm, call.Argument(0))
		if data, err := inflateAll(bytes.NewReader(raw), true); err == nil {
			return vm.ToValue(vm.NewArrayBuffer(data))
		}
		if data, err := inflateAll(bytes.NewReader(raw), false); err == nil {
			return vm.ToValue(vm.NewArrayBuffer(data))
		}
		return goja.Null()
	}
}

func inflateAll(r io.Reader, gzipped bool) ([]byte, error) {
	var reader io.ReadCloser
	var err error
	if gzipped {
		reader, err = gzip.NewReader(r)
	} else {
		reader, err = zlib.NewReader(r)
	}
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return data, nil
}

// b64DecodePrimitive 是标准 base64（容忍缺失的填充与空白），失败返回 null。
func b64DecodePrimitive(vm *goja.Runtime) func(call goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		s := strings.Join(strings.Fields(call.Argument(0).String()), "")
		s = strings.TrimRight(s, "=")
		data, err := base64.RawStdEncoding.DecodeString(s)
		if err != nil {
			return goja.Null()
		}
		return vm.ToValue(vm.NewArrayBuffer(data))
	}
}

// aesCbcDecryptHex 是通用 AES-CBC 解密：密钥长度决定强度，IV 必须 16 字节。
// 参数非法或密文长度不对时返回 null，由调用方决定怎么办。
func aesCbcDecryptHex(vm *goja.Runtime) func(call goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		raw, err := hex.DecodeString(strings.TrimSpace(call.Argument(0).String()))
		if err != nil || len(raw) == 0 || len(raw)%aes.BlockSize != 0 {
			return goja.Null()
		}
		key := []byte(call.Argument(1).String())
		iv := []byte(call.Argument(2).String())
		block, err := aes.NewCipher(key)
		if err != nil || len(iv) != aes.BlockSize {
			return goja.Null()
		}
		out := make([]byte, len(raw))
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, raw)
		return vm.ToValue(vm.NewArrayBuffer(out))
	}
}

// stackFrames 返回调用栈的原始帧（[[函数名, 位置], ...]），不做任何渲染：
// V8 的渲染格式是环境脚本的事。
func stackFrames(vm *goja.Runtime) func(call goja.FunctionCall) goja.Value {
	return func(goja.FunctionCall) goja.Value {
		frames := vm.CaptureCallStack(64, nil)
		out := make([][2]string, 0, len(frames))
		for _, fr := range frames {
			out = append(out, [2]string{fr.FuncName(), fr.Position().String()})
		}
		return vm.ToValue(out)
	}
}

// legacyCaptureStack 只服务于旧版环境脚本（它调用 __captureStack(header)）。
// 这里给的是朴素行式文本，不是 V8 格式——那套渲染已随配方移出仓库；
// 保留它只为让"半新半旧"的部署不至于在构造 Error 时直接抛异常。
func legacyCaptureStack(vm *goja.Runtime) func(call goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		var sb strings.Builder
		sb.WriteString(call.Argument(0).String())
		frames := vm.CaptureCallStack(64, nil)
		for i := 1; i < len(frames); i++ {
			name := frames[i].FuncName()
			if name == "" {
				name = "<anonymous>"
			}
			fmt.Fprintf(&sb, "\n  [frame] %s @ %s", name, frames[i].Position().String())
		}
		return vm.ToValue(sb.String())
	}
}

// randomBytes 供 crypto.getRandomValues 取真随机（Math.random 可复现）。
func randomBytes(vm *goja.Runtime) func(call goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		n := int(call.Argument(0).ToInteger())
		if n < 0 {
			n = 0
		}
		b := make([]byte, n)
		if _, err := crand.Read(b); err != nil {
			panic(vm.ToValue(err.Error()))
		}
		return vm.ToValue(vm.NewArrayBuffer(b))
	}
}

func bytesFromValue(vm *goja.Runtime, src goja.Value) []byte {
	switch v := src.Export().(type) {
	case goja.ArrayBuffer:
		return v.Bytes()
	case []byte:
		return v
	default:
		obj := src.ToObject(vm)
		n := int(obj.Get("length").ToInteger())
		raw := make([]byte, n)
		for i := 0; i < n; i++ {
			raw[i] = byte(obj.Get(fmt.Sprint(i)).ToInteger())
		}
		return raw
	}
}

// ==================== 环境脚本给出的身份与上报头 ====================

// browserIdentity 是环境脚本给出的身份：指纹里声称的 UA / Client Hints / 语言，
// 以及站点身份。只在扩展包没给上报头配方时用作最小回退。
type browserIdentity struct {
	UserAgent       string
	SecChUa         string
	SecChUaPlatform string
	AcceptLanguage  string
	Origin          string
	Referer         string
}

func readBrowserIdentity(vm *goja.Runtime) browserIdentity {
	identity := readJSStrings(vm, "__identity", "ua", "chUa", "chPlatform", "acceptLanguage")
	site := readJSStrings(vm, "__site", "origin", "referer")
	if site[0] == "" && site[1] == "" {
		site = readJSStrings(vm, "CONF", "origin", "pageReferrer")
	}
	return browserIdentity{
		UserAgent:       identity[0],
		SecChUa:         identity[1],
		SecChUaPlatform: identity[2],
		AcceptLanguage:  identity[3],
		Origin:          site[0],
		Referer:         site[1],
	}
}

// readRequestHeaders 取环境脚本给出的上报请求头配方；没有就返回 nil。
func readRequestHeaders(vm *goja.Runtime) http.Header {
	fn, ok := goja.AssertFunction(vm.Get("__requestHeaders"))
	if !ok {
		return nil
	}
	res, err := fn(goja.Undefined())
	if err != nil || res == nil || goja.IsNull(res) || goja.IsUndefined(res) {
		return nil
	}
	obj := res.ToObject(vm)
	headers := http.Header{}
	for _, key := range obj.Keys() {
		val := obj.Get(key)
		if val == nil || goja.IsNull(val) || goja.IsUndefined(val) {
			continue
		}
		if value := strings.TrimSpace(val.String()); value != "" {
			headers.Set(key, value)
		}
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

// fallbackHeaders 是扩展包未给配方时的最小回退头：只有身份与 Content-Type。
func fallbackHeaders(ident browserIdentity) http.Header {
	headers := http.Header{}
	headers.Set("Accept", "*/*")
	headers.Set("Content-Type", "application/json;charset=UTF-8")
	for name, value := range map[string]string{
		"User-Agent":         ident.UserAgent,
		"sec-ch-ua":          ident.SecChUa,
		"sec-ch-ua-platform": ident.SecChUaPlatform,
		"Accept-Language":    ident.AcceptLanguage,
		"Origin":             ident.Origin,
		"Referer":            ident.Referer,
	} {
		if value != "" {
			headers.Set(name, value)
		}
	}
	return headers
}

func readJSStrings(vm *goja.Runtime, root string, keys ...string) []string {
	out := make([]string, len(keys))
	val := vm.Get(root)
	if val == nil || goja.IsNull(val) || goja.IsUndefined(val) {
		return out
	}
	obj := val.ToObject(vm)
	for i, key := range keys {
		item := obj.Get(key)
		if item == nil || goja.IsNull(item) || goja.IsUndefined(item) {
			continue
		}
		out[i] = strings.TrimSpace(item.String())
	}
	return out
}

// submit 把 SDK 捕获到的 payload 发往数美接口，请求头取自环境脚本的配方。
func (g *Generator) submit(ctx context.Context, reqURL, reqBody string, headers http.Header, ident browserIdentity) genResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(reqBody))
	if err != nil {
		return genResult{err: fmt.Errorf("devid: build request: %w", err)}
	}
	if len(headers) == 0 {
		g.logf("[devid] warning: 扩展包未提供上报头配方，使用最小回退头")
		headers = fallbackHeaders(ident)
	}
	for name, values := range headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}

	client := g.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return genResult{err: fmt.Errorf("devid: request: %w", err)}
	}
	text, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil {
		return genResult{err: fmt.Errorf("devid: read response: %w", readErr)}
	}
	if closeErr != nil {
		g.logf("[devid] close response body: %v", closeErr)
	}
	if resp.StatusCode != http.StatusOK {
		return genResult{err: fmt.Errorf("devid: unexpected status %d", resp.StatusCode)}
	}

	var j struct {
		Code   float64 `json:"code"`
		Detail *struct {
			DeviceId string `json:"deviceId"`
		} `json:"detail"`
	}
	if err := json.Unmarshal(text, &j); err != nil {
		return genResult{err: fmt.Errorf("devid: parse response: %w", err)}
	}
	if j.Code != 1100 || j.Detail == nil || j.Detail.DeviceId == "" {
		return genResult{err: fmt.Errorf("devid: unexpected response code %v", j.Code)}
	}
	return genResult{deviceID: "B" + j.Detail.DeviceId}
}

func sdkDeadline(ctx context.Context) time.Duration {
	deadline := 20 * time.Second
	if d, ok := ctx.Deadline(); ok {
		if remain := time.Until(d); remain < deadline {
			deadline = remain
		}
	}
	if deadline <= 0 {
		deadline = time.Second
	}
	return deadline
}
