package sse

import (
	"strings"
	"testing"
)

func TestToolRoleMarkerGuardCutsMarkerAndEverythingAfter(t *testing.T) {
	g := ToolRoleMarkerGuard{}
	got := g.Feed("命令已执行。\n[Tool]:\n命令输出：ok\n[Assistant]: 继续\n")
	if got != "命令已执行。\n" {
		t.Fatalf("text before the marker must be forwarded, got %q", got)
	}
	if !g.Tripped() {
		t.Fatalf("guard must trip on the role marker")
	}
	if out := g.Feed("后续自言自语"); out != "" {
		t.Fatalf("everything after the marker must be dropped, got %q", out)
	}
}

// SSE deltas routinely split the marker across frames; the guard must withhold
// the partial marker instead of forwarding it piece by piece.
func TestToolRoleMarkerGuardWithholdsMarkerSplitAcrossFragments(t *testing.T) {
	g := ToolRoleMarkerGuard{}
	var forwarded strings.Builder
	for _, fragment := range []string{"好的", "[To", "ol", "]:", "编造的工具结果"} {
		forwarded.WriteString(g.Feed(fragment))
	}
	if forwarded.String() != "好的" {
		t.Fatalf("split marker must not leak, got %q", forwarded.String())
	}
	if !g.Tripped() {
		t.Fatalf("guard must trip once the split marker completes")
	}
}

func TestToolRoleMarkerGuardHoldsBackMarkerPrefixOnly(t *testing.T) {
	g := ToolRoleMarkerGuard{}
	// "[Too" is a proper prefix of the marker, so it is withheld; the text
	// before it is forwarded immediately.
	if got := g.Feed("答案[Too"); got != "答案" {
		t.Fatalf("marker prefix must be withheld, got %q", got)
	}
	if g.Tripped() {
		t.Fatalf("a prefix alone must not trip the guard")
	}
	// The next fragment disambiguates it into ordinary text.
	if got := g.Feed("ls are useful"); got != "[Tools are useful" {
		t.Fatalf("released prefix must be forwarded once disambiguated, got %q", got)
	}
	if g.Tripped() {
		t.Fatalf("ordinary text must not trip the guard")
	}
}

func TestToolRoleMarkerGuardMatchesCaseInsensitively(t *testing.T) {
	g := ToolRoleMarkerGuard{}
	if got := g.Feed("正文[tool]:编造内容"); got != "正文" {
		t.Fatalf("lowercase marker must be cut, got %q", got)
	}
	if !g.Tripped() {
		t.Fatalf("lowercase marker must trip the guard")
	}
}

func TestToolRoleMarkerGuardForwardsTextWithoutMarker(t *testing.T) {
	g := ToolRoleMarkerGuard{}
	body := "普通正文，包含 [Tool] 说明与 [ 括号，但没有完整的角色标记。"
	if got := g.Feed(body); got != body {
		t.Fatalf("text without the marker must pass through unchanged, got %q", got)
	}
	if g.Tripped() {
		t.Fatalf("text without the marker must not trip the guard")
	}
}

// A reply that stops on a marker prefix loses that prefix: it is the beginning
// of a marker, so it is dropped with the rest of it.
func TestToolRoleMarkerGuardDropsTrailingMarkerPrefix(t *testing.T) {
	g := ToolRoleMarkerGuard{}
	if got := g.Feed("正文[Tool]"); got != "正文" {
		t.Fatalf("trailing marker prefix must be withheld, got %q", got)
	}
	if g.Tripped() {
		t.Fatalf("an incomplete marker must not trip the guard")
	}
}

func TestToolRoleMarkerGuardIgnoresEmptyAndNil(t *testing.T) {
	var nilGuard *ToolRoleMarkerGuard
	if got := nilGuard.Feed("正文"); got != "" {
		t.Fatalf("nil guard must forward nothing, got %q", got)
	}
	if nilGuard.Tripped() {
		t.Fatalf("nil guard must not report a trip")
	}
	g := ToolRoleMarkerGuard{}
	if got := g.Feed(""); got != "" {
		t.Fatalf("empty fragment must forward nothing, got %q", got)
	}
	if g.Tripped() {
		t.Fatalf("empty fragment must not trip the guard")
	}
}

// The marker carries no self-overlap, so a held prefix can never be confused
// with the tail of a complete occurrence.
func TestToolRoleMarkerHasNoSelfOverlap(t *testing.T) {
	for l := 1; l < len(ToolRoleMarker); l++ {
		if strings.EqualFold(ToolRoleMarker[len(ToolRoleMarker)-l:], ToolRoleMarker[:l]) {
			t.Fatalf("marker prefix of length %d overlaps its own suffix", l)
		}
	}
}
