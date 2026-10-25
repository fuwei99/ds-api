package sse

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCollectStreamDedupesContinueSnapshotReplay(t *testing.T) {
	prefix := "我们被问到：这是一个很长的续答快照前缀，用来验证去重逻辑不会误伤正常 token。"
	body := strings.Join([]string{
		`data: {"v":{"response":{"fragments":[{"id":2,"type":"THINK","content":"` + prefix + `","references":[],"stage_id":1}]}}}`,
		``,
		`data: {"p":"response/status","v":"INCOMPLETE"}`,
		``,
		`data: {"v":{"response":{"fragments":[{"id":2,"type":"THINK","content":"` + prefix + `继续","references":[],"stage_id":1}]}}}`,
		``,
		`data: {"v":"分析"}`,
		``,
		`data: {"p":"response/status","v":"FINISHED"}`,
		``,
	}, "\n")

	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	got := CollectStream(resp, true, true)
	if got.Thinking != prefix+"继续分析" {
		t.Fatalf("unexpected thinking after dedupe: %q", got.Thinking)
	}
}

func TestCollectStreamCutsHallucinatedToolRoleMarker(t *testing.T) {
	body := strings.Join([]string{
		`data: {"v":"命令已执行。"}`,
		``,
		`data: {"v":"\n[Tool]:\n输出：ok\n[Assistant]: 继续"}`,
		``,
		`data: {"v":"\n更多虚构内容"}`,
		``,
		`data: {"p":"response/status","v":"FINISHED"}`,
		``,
	}, "\n")

	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	got := CollectStream(resp, false, true)
	if got.Cut != CutToolRoleMarker {
		t.Fatalf("expected the hallucinated role marker to be reported")
	}
	if got.Text != "命令已执行。\n" {
		t.Fatalf("text must stop at the marker, got %q", got.Text)
	}
}

func TestCollectStreamCutsToolRoleMarkerSplitAcrossFragments(t *testing.T) {
	body := strings.Join([]string{
		`data: {"v":"好的，"}`,
		``,
		`data: {"v":"[To"}`,
		``,
		`data: {"v":"ol"}`,
		``,
		`data: {"v":"]:编造内容"}`,
		``,
	}, "\n")

	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	got := CollectStream(resp, false, true)
	if got.Cut != CutToolRoleMarker {
		t.Fatalf("expected the split role marker to be reported")
	}
	if got.Text != "好的，" {
		t.Fatalf("split marker must not leak, got %q", got.Text)
	}
}
