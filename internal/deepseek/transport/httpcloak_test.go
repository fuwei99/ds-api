package transport

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	httpcloak "github.com/sardanioss/httpcloak/client"
)

func TestMergeHeaderOrderPreservesDeepSeekHeaderPosition(t *testing.T) {
	base := []string{"content-length", "authorization", "accept", "user-agent", "cookie"}
	got := mergeHeaderOrder(base, []string{"x-ds-pow-response", "authorization", "accept"})
	want := []string{"x-ds-pow-response", "authorization", "accept", "content-length", "user-agent", "cookie"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d]=%q, want %q; full=%v", i, got[i], want[i], got)
		}
	}
}

func TestMergeHeaderOrderDoesNotDuplicateCustomHeader(t *testing.T) {
	got := mergeHeaderOrder([]string{"accept", "x-ds-pow-response"}, []string{"x-ds-pow-response"})
	want := []string{"x-ds-pow-response", "accept"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestHTTPCloakClientKeepsDeepSeekHeaderOrder(t *testing.T) {
	client := newHTTPCloakClient(time.Minute, "", false)
	order := client.GetHeaderOrder()
	if len(order) == 0 {
		t.Fatal("expected a non-empty header order")
	}
	seen := map[string]bool{}
	for _, key := range order {
		if seen[key] {
			t.Fatalf("duplicate header %q in %v", key, order)
		}
		seen[key] = true
	}
	if !seen["x-ds-pow-response"] {
		t.Fatalf("DeepSeek header missing from %v", order)
	}
}

// 兜底传输必须与主传输共用同一头顺序（同一指纹的组成部分），
// 否则主路径失败切兜底的瞬间，线上头顺序会变。
func TestHTTPCloakFallbackKeepsSameHeaderOrderAsPrimary(t *testing.T) {
	primary := newHTTPCloakClient(time.Minute, "", false)
	fallback := newHTTPCloakClient(time.Minute, "", true)
	got := fallback.GetHeaderOrder()
	want := primary.GetHeaderOrder()
	if len(got) != len(want) {
		t.Fatalf("fallback order length %d != primary %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("fallback order[%d]=%q, want %q; full fallback=%v", i, got[i], want[i], got)
		}
	}
}

func TestToHTTPCloakRequestCopiesHeadersAndBody(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://chat.deepseek.com/api", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Add("X-Ds-Pow-Response", "pow")
	converted := toHTTPCloakRequest(req)
	if converted.Method != http.MethodPost || converted.URL != req.URL.String() {
		t.Fatalf("request metadata was not preserved: %+v", converted)
	}
	if got := converted.Headers["X-Ds-Pow-Response"]; len(got) != 1 || got[0] != "pow" {
		t.Fatalf("headers were not copied: %v", converted.Headers)
	}
	if converted.Body == nil {
		t.Fatal("request body was not copied")
	}
}

func TestHTTPCloakResponseHeadersDoNotTriggerSecondDecompression(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://chat.deepseek.com/api", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := fromHTTPCloakResponse(&httpcloak.Response{
		StatusCode: 200,
		Headers: map[string][]string{
			"content-encoding": {"gzip"},
			"content-length":   {"123"},
			"content-type":     {"application/json"},
		},
		Body: io.NopCloser(bytes.NewReader([]byte(`{"ok":true}`))),
	}, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want it stripped after httpcloak decoding", got)
	}
	if got := resp.Header.Get("Content-Length"); got != "" || resp.ContentLength != -1 {
		t.Fatalf("Content-Length metadata was not cleared: header=%q length=%d", got, resp.ContentLength)
	}
	if !resp.Uncompressed {
		t.Fatal("response should be marked uncompressed")
	}
}

func TestHTTPCloakStreamResponseUsesNormalizedHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://chat.deepseek.com/api", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := fromHTTPCloakStreamResponse(&httpcloak.StreamResponse{
		StatusCode: 200,
		Headers: map[string][]string{
			"content-encoding": {"br"},
			"content-length":   {"456"},
		},
		ContentLength: 456,
	}, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Content-Encoding") != "" || resp.Header.Get("Content-Length") != "" {
		t.Fatalf("stream response retained compressed metadata: %v", resp.Header)
	}
	if resp.ContentLength != -1 || !resp.Uncompressed {
		t.Fatalf("stream metadata = length %d uncompressed %v", resp.ContentLength, resp.Uncompressed)
	}
}

// ChromePresetName 必须能解析出真实 ClientHello，且 TLS 主版本不得比 HTTP 层
// 自称的版本更新。这正是旧 TLSChromeVersion=133 常量漂移（HTTP 150 / TLS 实际
// 146）留下的教训：TLS 版本只能从 preset 读取，不能再单写一个常量。
func TestChromeClientHelloResolvedFromPreset(t *testing.T) {
	name, ok := ChromeClientHelloName()
	if !ok {
		t.Fatalf("preset %q 未解析到已知的 ClientHello", ChromePresetName)
	}
	major, ok := ChromeClientHelloMajor()
	if !ok {
		t.Fatalf("无法从 ClientHello %q 解析主版本", name)
	}
	tlsMajor, err := strconv.Atoi(major)
	if err != nil {
		t.Fatalf("ClientHello 主版本 %q 不是数字", major)
	}
	httpMajor, err := strconv.Atoi(ChromeMajorVersion)
	if err != nil {
		t.Fatalf("ChromeMajorVersion %q 不是数字", ChromeMajorVersion)
	}
	if tlsMajor > httpMajor {
		t.Fatalf("TLS ClientHello 是 Chrome %s，比自称的 Chrome %s 还新", major, ChromeMajorVersion)
	}
}
