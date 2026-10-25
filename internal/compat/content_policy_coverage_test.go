package compat

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"ds2api/internal/sse"
)

// cutReasonDeclPattern 匹配 internal/sse/content_policy.go 里的 CutReason 声明，
// 例如 `CutRepeatLoop CutReason = "repeat_loop"`。
//
// 用源码扫描而不是手写清单：新增一条规则时它会自动出现在集合里，于是下面几条
// 「新规则必须配套什么」的检查立刻生效，不需要谁记得回来更新测试。
var cutReasonDeclPattern = regexp.MustCompile(`(?m)^\s*(\w+)\s+CutReason\s*=\s*"([^"]*)"`)

// declaredCutReasons 返回注册表声明的全部 CutReason 及其取值，CutNone（空值）在
// 内，便于下面区分"还在转发"与"真的截断"。
func declaredCutReasons(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "sse", "content_policy.go"))
	if err != nil {
		t.Fatalf("read internal/sse/content_policy.go: %v", err)
	}
	reasons := map[string]string{}
	for _, m := range cutReasonDeclPattern.FindAllStringSubmatch(string(raw), -1) {
		reasons[m[1]] = m[2]
	}
	if len(reasons) == 0 {
		t.Fatal("no CutReason declaration found; the declaration shape in internal/sse/content_policy.go may have changed")
	}
	return reasons
}

// 每条截断规则都必须有语料。语料是 Go 与 Node 两个实现的共同契约：没有语料，
// 规则就可以只在一侧实现而无人发现——镜像测试只回放语料覆盖到的行为。
func TestEveryCutReasonHasAFixture(t *testing.T) {
	covered := map[string]string{}
	files, err := filepath.Glob(compatPath("fixtures", "content_policy", "*.json"))
	if err != nil {
		t.Fatalf("glob fixtures failed: %v", err)
	}
	for _, fixturePath := range files {
		name := trimExt(filepath.Base(fixturePath))
		var expected struct {
			Cut string `json:"cut"`
		}
		mustLoadJSON(t, compatPath("expected", "content_policy_"+name+".json"), &expected)
		if _, seen := covered[expected.Cut]; !seen {
			covered[expected.Cut] = name
		}
	}

	for name, value := range declaredCutReasons(t) {
		if value == "" {
			continue // CutNone: 还在转发，没有可截断的行为
		}
		if _, ok := covered[value]; !ok {
			t.Errorf("CutReason %s (%q) has no fixture; add tests/compat/fixtures/content_policy/<name>.json plus its expected file so both the Go and the Node implementation are held to the rule", name, value)
		}
	}
}

// 语料里写的 cut 必须是声明过的取值。反向检查：规则被删掉或改名后，语料若还留
// 着旧取值，回放测试会以为一切正常。
func TestContentPolicyFixturesUseDeclaredCutReasons(t *testing.T) {
	declared := declaredCutReasons(t)
	known := map[string]bool{}
	for _, value := range declared {
		known[value] = true
	}
	files, err := filepath.Glob(compatPath("fixtures", "content_policy", "*.json"))
	if err != nil {
		t.Fatalf("glob fixtures failed: %v", err)
	}
	for _, fixturePath := range files {
		name := trimExt(filepath.Base(fixturePath))
		var expected struct {
			Cut string `json:"cut"`
		}
		mustLoadJSON(t, compatPath("expected", "content_policy_"+name+".json"), &expected)
		if !known[expected.Cut] {
			t.Errorf("fixture %s expects cut %q, which is not a declared CutReason", name, expected.Cut)
		}
	}
}

// 每条规则都要有日志文案：各出口共用同一句话，缺了就会在某个出口打出空消息。
func TestEveryCutReasonHasALogMessage(t *testing.T) {
	for name, value := range declaredCutReasons(t) {
		if value == "" {
			continue
		}
		if strings.TrimSpace(sse.CutReason(value).LogMessage()) == "" {
			t.Errorf("CutReason %s (%q) has no LogMessage; every surface logs the same sentence for the same rule", name, value)
		}
	}
}

// Node 镜像必须认得每条规则的取值。Go 侧新增一条规则而 Node 侧没跟上时，这里会
// 先失败，而不是等语料回放（语料只覆盖它写到的分片）或线上才发现。
func TestNodeContentPolicyMirrorsEveryCutReason(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "js", "chat-stream", "content_policy.js"))
	if err != nil {
		t.Fatalf("read internal/js/chat-stream/content_policy.js: %v", err)
	}
	mirror := string(raw)
	var missing []string
	for name, value := range declaredCutReasons(t) {
		if value == "" {
			continue
		}
		if !strings.Contains(mirror, "'"+value+"'") && !strings.Contains(mirror, `"`+value+`"`) {
			missing = append(missing, name+" ("+value+")")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("internal/js/chat-stream/content_policy.js does not name these CutReason values: %s; mirror the rule or the two runtimes will disagree", strings.Join(missing, ", "))
	}
}
