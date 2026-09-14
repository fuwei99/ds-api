// Package devid 在 goja 中运行数美 fp SDK，为 DeepSeek 账号生成真实设备 ID。
//
// fp-1.min.js（数美 SDK）与 browser_env.js（浏览器环境模拟，含指纹特征值）
// 均不随二进制/仓库分发：Generator 从 AssetDir（data/devid/）运行时加载，
// 任一缺失时返回错误，调用方应回退随机 device_id。
package devid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
)

// fp-1.min.js 与 browser_env.js 均须由部署方置于 AssetDir 下。
const (
	fpAssetFile  = "fp-1.min.js"
	envAssetFile = "browser_env.js"
)

// SDK 侧接入参数，全部固化为常量；仅 public_key 允许外置/覆盖。
const (
	smOrg     = "P9usCUBauxft8eAmUXaZ"
	smAppID   = "default"
	smApiHost = "fp-it-acc.portal101.cn"
	smApiPath = "/deviceprofile/v4"
	smProto   = "https"

	defaultUA      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36"
	defaultSecChUa = `"Chromium";v="152", "Not?A_Brand";v="24", "Google Chrome";v="152"`
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
	// AssetDir 指向 fp-1.min.js 与 browser_env.js 所在目录（data/devid/），
	// 任一缺失即报错。
	AssetDir string
	// HTTP 发送指纹上报请求；nil 时使用 30s 超时的默认客户端。
	HTTP Doer
	// Debug 开启后把 SDK 内部泄露桩与本地解密验证输出到 Logger。
	Debug bool
	// Logger 为调试日志回调，nil 时丢弃。
	Logger func(format string, args ...any)
	// UA / SecChUa 覆盖，留空使用 Chrome 152 / Win32 默认值。
	UA      string
	SecChUa string

	vm atomic.Pointer[goja.Runtime]
}

// loadAssets 从 AssetDir 读取 SDK 与环境模拟脚本，缺失时报错。
func (g *Generator) loadAssets() (string, string, error) {
	dir := strings.TrimSpace(g.AssetDir)
	if dir == "" {
		return "", "", errors.New("devid: asset dir is not set")
	}
	fpJS, err := os.ReadFile(filepath.Join(dir, fpAssetFile))
	if err != nil {
		return "", "", fmt.Errorf("devid: load %s: %w", fpAssetFile, err)
	}
	envJS, err := os.ReadFile(filepath.Join(dir, envAssetFile))
	if err != nil {
		return "", "", fmt.Errorf("devid: load %s: %w", envAssetFile, err)
	}
	return string(fpJS), string(envJS), nil
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
// 不存在则新建并写入。path 为空时每次使用临时档案。
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

func (g *Generator) run(ctx context.Context, profilePath string, rng *rand.Rand) genResult {
	fpJS, envJS, err := g.loadAssets()
	if err != nil {
		return genResult{err: err}
	}
	profile, err := loadOrMakeProfile(profilePath, rng)
	if err != nil {
		return genResult{err: err}
	}

	ua := strings.TrimSpace(g.UA)
	if ua == "" {
		ua = defaultUA
	}
	secChUa := strings.TrimSpace(g.SecChUa)
	if secChUa == "" {
		secChUa = defaultSecChUa
	}

	cfg := g.Config.Normalize()
	// 模拟页面加载耗时后触发 SDK (0.8~3.8s)
	startMs := float64(time.Now().UnixMilli() - int64(rng.Intn(3000)) - 800)
	profileJSON, _ := json.Marshal(profile)

	vm := goja.New()
	g.vm.Store(vm)
	el, err := newEventLoop(vm)
	if err != nil {
		return genResult{err: fmt.Errorf("init event loop: %w", err)}
	}

	for name, value := range map[string]any{
		"__captureStack": func(call goja.FunctionCall) goja.Value {
			return vm.ToValue(captureStack(vm, call.Argument(0).String()))
		},
		"__profile":    string(profileJSON),
		"__START":      startMs,
		"__smAppId":    smAppID,
		"__smOrg":      smOrg,
		"__smPubKey":   cfg.PublicKey,
		"__smProtocol": smProto,
		"__smApiHost":  effectiveApiHost(),
		"__smApiPath":  smApiPath,
	} {
		if err := vm.Set(name, value); err != nil {
			return genResult{err: fmt.Errorf("set %s: %w", name, err)}
		}
	}

	prelude, err := goja.Compile(envAssetFile, envJS, false)
	if err != nil {
		return genResult{err: fmt.Errorf("compile browser env: %w", err)}
	}
	if _, err := vm.RunProgram(prelude); err != nil {
		return genResult{err: fmt.Errorf("run browser env: %w", err)}
	}

	patched := strings.Replace(fpJS, "_0x213c6f('priId',", "_0x213c6f('priId', __leak.priId =", 1)
	if g.Debug {
		patched = strings.Replace(patched,
			"_0x28b88f+=_0x30bf1e+''+_0x1c4170,",
			"_0x28b88f+=_0x30bf1e+''+_0x1c4170,__leak.c2=_0x1c4170,", 1)
		patched = strings.Replace(patched,
			"_0x4ac93b['Cookie']['remove'](_0x3acb52),_0x28b88f+=",
			"_0x4ac93b['Cookie']['remove'](_0x3acb52),__leak.cookieTest=(_0x357504==_0x221499),_0x28b88f+=", 1)
		patched = strings.Replace(patched,
			"_0x28b88f+=_0x520fd8;",
			"_0x28b88f+=_0x520fd8;__leak.statusRaw=_0x28b88f;", 1)
	}
	if _, err := vm.RunString("var __leak = {};"); err != nil {
		return genResult{err: fmt.Errorf("init leak stub: %w", err)}
	}
	sdkProg, err := goja.Compile("fp-1.min.js", patched, false)
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
		priId := ""
		if leak := vm.Get("__leak"); leak != nil && !goja.IsUndefined(leak) {
			if v := leak.ToObject(vm).Get("priId"); v != nil {
				priId = v.String()
			}
		}
		g.verifyFingerprint(priId, payload.Data)
	}

	return g.submit(ctx, reqURL, reqBody, ua, secChUa)
}

func (g *Generator) submit(ctx context.Context, reqURL, reqBody, ua, secChUa string) genResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(reqBody))
	if err != nil {
		return genResult{err: fmt.Errorf("devid: build request: %w", err)}
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	req.Header.Set("Origin", "https://chat.deepseek.com")
	req.Header.Set("Referer", "https://chat.deepseek.com/")
	req.Header.Set("User-Agent", ua)
	req.Header.Set("sec-ch-ua", secChUa)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Windows"`)

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
