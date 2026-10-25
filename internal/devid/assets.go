package devid

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 扩展包（Generator.AssetDir，默认 data/devid/）的文件布局与降级语义。
//
// 仓库里只有这份加载代码：画像目录、SDK 补丁、解密校验配方、上报头配方
// 都在扩展包里——它们是"怎么伪装、怎么验证"的部分，不进仓库。
// 缺失时的降级是显式的，不会用仓库里的兜底值偷偷顶替：
//
//	fp-1.min.js    必需，缺失即报错（调用方保留账号已有 device_id）
//	browser_env.js 必需，同上
//	profiles.json  新建档案时必需；已有档案不需要它（画像补齐由环境脚本兜底）
//	patches.json   可选，缺失即不做任何补丁（Debug 的解密验证随之失效）
//	verify.js      可选，缺失即跳过本地解密验证
//	gpus.json      可选，GPU 池；缺失时环境脚本退回它内置的那一条渲染器串
//	fonts.json     可选，字体实测表；缺失时环境脚本退回按字符估算的粗略模型
const (
	fpAssetFile  = "fp-1.min.js"
	envAssetFile = "browser_env.js"
	catalogFile  = "profiles.json"
	patchFile    = "patches.json"
	verifyFile   = "verify.js"
	gpuPoolFile  = "gpus.json"
	fontsFile    = "fonts.json"
)

// patchSpec 是对 SDK 源码的一条定点替换。Find 必须逐字符精确命中；
// Count<=0 表示只替换首处，Debug 补丁只在 Generator.Debug 打开时应用。
type patchSpec struct {
	Find    string `json:"find"`
	Replace string `json:"replace"`
	Reason  string `json:"reason,omitempty"`
	Count   int    `json:"count,omitempty"`
	Debug   bool   `json:"debug,omitempty"`
}

type patchFileSpec struct {
	Version int         `json:"version"`
	Patches []patchSpec `json:"patches"`
}

// assets 是一个已加载的扩展包。
type assets struct {
	dir      string
	fpJS     string
	envJS    string
	patches  []patchSpec
	verifyJS string
	// gpuPool 是 GPU 池的原文，原样注入给环境脚本（它自己解析与挑选）。
	gpuPool string
	// fonts 是字体实测表的原文，同样交由环境脚本解析。
	fonts string
}

func loadAssets(dir string) (*assets, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("devid: asset dir is not set")
	}
	fpJS, err := os.ReadFile(filepath.Join(dir, fpAssetFile))
	if err != nil {
		return nil, fmt.Errorf("devid: load %s: %w", fpAssetFile, err)
	}
	envJS, err := os.ReadFile(filepath.Join(dir, envAssetFile))
	if err != nil {
		return nil, fmt.Errorf("devid: load %s: %w", envAssetFile, err)
	}
	pkg := &assets{dir: dir, fpJS: string(fpJS), envJS: string(envJS)}
	// 可选件缺失不是错误：补丁与校验缺失只影响 Debug 能力，生成照常。
	if data, err := os.ReadFile(filepath.Join(dir, patchFile)); err == nil {
		var spec patchFileSpec
		if err := json.Unmarshal(data, &spec); err != nil {
			return nil, fmt.Errorf("devid: parse %s: %w", patchFile, err)
		}
		pkg.patches = spec.Patches
	}
	if data, err := os.ReadFile(filepath.Join(dir, verifyFile)); err == nil {
		pkg.verifyJS = string(data)
	}
	if data, err := os.ReadFile(filepath.Join(dir, gpuPoolFile)); err == nil {
		pkg.gpuPool = string(data)
	}
	if data, err := os.ReadFile(filepath.Join(dir, fontsFile)); err == nil {
		pkg.fonts = string(data)
	}
	return pkg, nil
}

// catalog 读取画像目录。只在需要新建档案、或为已有档案补画像时调用。
func (pkg *assets) catalog() (*catalog, error) {
	return loadCatalog(filepath.Join(pkg.dir, catalogFile))
}

// applyPatches 按补丁表改写 SDK 源码，返回改写结果与逐条命中说明。
// 未命中的补丁不报错：SDK 升级后某条补丁失效是正常情况，说明进日志即可。
func (pkg *assets) applyPatches(src string, debug bool) (string, []string) {
	var notes []string
	out := src
	for _, spec := range pkg.patches {
		if spec.Find == "" {
			continue
		}
		if spec.Debug && !debug {
			continue
		}
		count := spec.Count
		if count <= 0 {
			count = 1
		}
		next := strings.Replace(out, spec.Find, spec.Replace, count)
		label := spec.Reason
		if label == "" {
			label = truncateForLog(spec.Find, 40)
		}
		if next == out {
			notes = append(notes, fmt.Sprintf("补丁未命中: %s", label))
			continue
		}
		notes = append(notes, fmt.Sprintf("补丁已应用: %s", label))
		out = next
	}
	return out, notes
}

func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
