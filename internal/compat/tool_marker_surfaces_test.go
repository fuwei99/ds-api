package compat

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 工具调用标识（tool marker，见 docs/prompt-compatibility.md §6.1.2）是**按 API Key
// 派生**的：提示词出口把规范里的 EPSE 渲染成调用者专属标识 M（EPSE → M），模型输出
// 在进入索引 / 解析 / 意图识别 / 兜底修复之前又必须被归一化回 EPSE（M → EPSE）。
//
// 两个方向都是「出口必须自己做一次」的接线，而新出口最容易漏掉的正是它们：
// 漏了出口替换，这个出口的 prompt 会退回共享关键字（正是该特性要消灭的跨调用者
// 指纹）；漏了入口归一化，模型按 M 输出的工具调用会被流式 sieve 当作普通文本透传。
// 已有的同类守卫只覆盖按 Key 开关（caller_flag_action_test.go）与思考注入
// （thinking_injection_surfaces_test.go），marker 这两个方向此前没有任何检查——
// 新增一个协议出口时会静默缺一整套行为。
//
// 出口集合由源码派生（复用 surface_packages_test.go 的 exitPackages），不维护手工
// 清单：新出口自动进入集合，于是下面两条检查立刻生效。

// callerMarkerPattern 匹配「出口取本次调用者标识」的写法：`a` 是各出口的
// *auth.RequestAuth（详情/行内两种命名都有，这里只认字段访问本身）。
var callerMarkerPattern = regexp.MustCompile(`\.ToolMarker\b`)

// inboundMarkerPatterns 匹配「把标识接回入口归一化」的落点，三种写法对应四个出口
// 现在各自的实现，任一命中即算接线：
//   - 流式 sieve：`sieve.Marker` / `toolSieve.Marker`（OpenAI Chat / Responses / Claude）
//   - 运行时不持有 sieve 字段的出口：`toolMarker = a.ToolMarker`（Gemini 的 runtime 字段）
//   - 收口兜底：`ToolMarker:`（assistantturn.BuildOptions 的字段，四个出口都有）
var inboundMarkerPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bsieve\.Marker\b`),
	regexp.MustCompile(`\btoolSieve\.Marker\b`),
	regexp.MustCompile(`\btoolMarker\s*=\s*\w+\.ToolMarker\b`),
	regexp.MustCompile(`\bToolMarker:`),
}

// markerTestPattern 匹配包内测试对标识的引用（大小写不敏感）：
// 每个出口都必须有一条会因「漏接线」而变红的用例，否则上面两条源码检查只是
// 字符串存在性，接线被改成空操作也没人知道。
var markerTestPattern = regexp.MustCompile(`(?i)tool[_]?marker`)

// TestExitPackagesUseCallerToolMarker 要求每个协议出口都取本次调用者的标识
// （而不是某个全局值 / 常量）。缺少这一步的出口会把共享的 `<|EPSE|` 关键字
// 一路发到上游，本特性对它完全失效。
func TestExitPackagesUseCallerToolMarker(t *testing.T) {
	for dir, files := range exitPackages(t) {
		if exitPackageMatches(files, callerMarkerPattern) {
			continue
		}
		t.Errorf("protocol exit %q never reads the caller tool marker (.ToolMarker): the per-API-key EPSE randomization would silently not apply to this exit; files=%v",
			dir, exitPackageFileNames(files))
	}
}

// TestExitPackagesNormalizeMarkerBackToEPSE 要求每个协议出口都把标识接回入口
// 归一化：模型按 M 输出的工具调用必须先折回 EPSE 才会被 sieve / 解析 / 修复识别，
// 否则会被当作普通正文透传给客户端。
func TestExitPackagesNormalizeMarkerBackToEPSE(t *testing.T) {
	for dir, files := range exitPackages(t) {
		matched := ""
		for _, pattern := range inboundMarkerPatterns {
			if exitPackageMatches(files, pattern) {
				matched = pattern.String()
				break
			}
		}
		if matched == "" {
			t.Errorf("protocol exit %q never wires the caller marker back into its inbound normalization (expected one of %v); model output in marker form would leak as plain text; files=%v",
				dir, patternSources(), exitPackageFileNames(files))
		}
	}
}

// TestExitPackagesHaveToolMarkerRegressionTest 要求每个出口都有一条引用标识的
// 回归用例。源码存在性检查抓不到「接线被改成传空字符串 / 传常量」这类退化，
// 出口自己的用例才抓得到（现有用例见 chat/vercel_marker_test.go、
// gemini/marker_stream_test.go、claude/marker_surface_test.go、
// responses/marker_surface_test.go）。
func TestExitPackagesHaveToolMarkerRegressionTest(t *testing.T) {
	for dir := range exitPackages(t) {
		pkgDir := filepath.Join("..", "httpapi", dir)
		entries, err := os.ReadDir(pkgDir)
		if err != nil {
			t.Fatalf("read %s: %v", pkgDir, err)
		}
		found := false
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(pkgDir, entry.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", entry.Name(), err)
			}
			if markerTestPattern.Match(raw) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("protocol exit %q has no tool-marker regression test: add one that asserts the upstream prompt carries the caller marker and that a marker-form tool call is parsed back", dir)
		}
	}
}

func patternSources() []string {
	out := make([]string, 0, len(inboundMarkerPatterns))
	for _, pattern := range inboundMarkerPatterns {
		out = append(out, pattern.String())
	}
	return out
}

// exitPackageMatches 报告出口包里是否有任意一个非测试文件匹配 pattern。
func exitPackageMatches(files map[string]string, pattern *regexp.Regexp) bool {
	for _, src := range files {
		if pattern.MatchString(src) {
			return true
		}
	}
	return false
}

// TestToolMarkerCorpusIsReplayedOnBothSides 要求标识归一化器的 Go↔Node 契约语料
// 存在，且两侧回放都在。归一化器是本特性唯一实现两份的部分（Go
// internal/toolcall/marker.go、Node internal/js/chat-stream/marker.js），语料是
// 它们之间唯一的共同判据：删掉任一侧的回放，两边就能悄悄分叉——一侧把半截标识
// 透传成正文，或者两侧对同一个工具块给出不同结论。
func TestToolMarkerCorpusIsReplayedOnBothSides(t *testing.T) {
	fixtures, err := filepath.Glob(compatPath("fixtures", "tool_marker", "*.json"))
	if err != nil {
		t.Fatalf("glob tool marker fixtures failed: %v", err)
	}
	if len(fixtures) == 0 {
		t.Error("no tool marker fixtures under tests/compat/fixtures/tool_marker/; the Go and Node marker normalizers are then only tested apart and may drift")
	}
	for _, fixturePath := range fixtures {
		name := trimExt(filepath.Base(fixturePath))
		if _, err := os.Stat(compatPath("expected", "tool_marker_"+name+".json")); err != nil {
			t.Errorf("fixture %s has no expected file: %v", filepath.Base(fixturePath), err)
		}
	}

	nodeSuite, err := os.ReadFile(filepath.Join("..", "..", "tests", "node", "js_compat_test.js"))
	if err != nil {
		t.Fatalf("read tests/node/js_compat_test.js: %v", err)
	}
	for _, needle := range []string{"tool_marker", "createMarkerNormalizer"} {
		if !strings.Contains(string(nodeSuite), needle) {
			t.Errorf("tests/node/js_compat_test.js no longer replays the tool marker corpus (missing %q); the Node mirror would stop being held to the shared fixtures", needle)
		}
	}
}
