package client

import (
	"context"
	dsprotocol "ds2api/internal/deepseek/protocol"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ds2api/internal/auth"
	powpkg "ds2api/pow"
)

func TestPreloadPowNoOp(t *testing.T) {
	client := NewClient(nil, nil)
	if err := client.PreloadPow(context.Background()); err != nil {
		t.Fatalf("PreloadPow should be no-op, got error: %v", err)
	}
}

func TestComputePowUnsupportedAlgorithm(t *testing.T) {
	_, err := ComputePow(context.Background(), map[string]any{"algorithm": "unknown"})
	if err == nil {
		t.Fatal("expected error for unsupported algorithm")
	}
}

func TestGetPowForTargetUsesCachedPrefetchedChallenge(t *testing.T) {
	// 关闭后台预取，保证上游调用次数可断言。
	t.Setenv("DS2API_POW_PREFETCH_ENABLED", "false")
	targetPath := dsprotocol.DeepSeekCompletionTargetPath
	cachedChallenge := testPowChallenge(7, targetPath, "cached")
	freshChallenge := testPowChallenge(42, targetPath, "fresh")

	body, err := json.Marshal(map[string]any{
		"code": 0,
		"msg":  "ok",
		"data": map[string]any{
			"biz_code": 0,
			"biz_data": map[string]any{
				"challenge": freshChallenge,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal pow response: %v", err)
	}

	callCount := 0
	client := &Client{
		regular: doerFunc(func(req *http.Request) (*http.Response, error) {
			callCount++
			reqBody, _ := io.ReadAll(req.Body)
			if !strings.Contains(string(reqBody), `"target_path":"`+targetPath+`"`) {
				t.Fatalf("expected completion target_path in pow request, got %s", string(reqBody))
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body))), Request: req}, nil
		}),
		fallback:   &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) { return nil, nil })},
		maxRetries: 1,
		powCache:   newPowChallengeCache(),
	}
	client.powCache.set("acct", targetPath, cachedChallenge)

	a := &auth.RequestAuth{
		DeepSeekToken: "token",
		AccountID:     "acct",
		TriedAccounts: map[string]bool{},
	}

	// 首次调用命中缓存：零上游请求，返回缓存 challenge 的签名。
	header, err := client.GetPowForTarget(context.Background(), a, targetPath, 1)
	if err != nil {
		t.Fatalf("GetPowForTarget error: %v", err)
	}
	if callCount != 0 {
		t.Fatalf("expected cached challenge to skip upstream pow request, got %d calls", callCount)
	}
	if got := powHeaderPayload(t, header)["signature"]; got != "cached" {
		t.Fatalf("expected cached challenge signature, got %#v", got)
	}

	// 缓存条目取出即消费：第二次调用回落到上游同步取 fresh challenge。
	header, err = client.GetPowForTarget(context.Background(), a, targetPath, 1)
	if err != nil {
		t.Fatalf("GetPowForTarget error: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected one upstream pow request after cache consumption, got %d calls", callCount)
	}
	if got := powHeaderPayload(t, header)["signature"]; got != "fresh" {
		t.Fatalf("expected fresh challenge signature, got %#v", got)
	}
}

func TestGetPowForTargetPrefetchesNextChallengeInBackground(t *testing.T) {
	t.Setenv("DS2API_POW_PREFETCH_ENABLED", "true")
	targetPath := dsprotocol.DeepSeekCompletionTargetPath

	var calls atomic.Int32
	client := &Client{
		regular: doerFunc(func(req *http.Request) (*http.Response, error) {
			n := calls.Add(1)
			challenge := testPowChallenge(int64(100+n), targetPath, "sig"+strconv.Itoa(int(n)))
			body, err := json.Marshal(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"biz_code": 0,
					"biz_data": map[string]any{
						"challenge": challenge,
					},
				},
			})
			if err != nil {
				t.Fatalf("marshal pow response: %v", err)
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body))), Request: req}, nil
		}),
		fallback:   &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) { return nil, nil })},
		maxRetries: 1,
		powCache:   newPowChallengeCache(),
	}
	a := &auth.RequestAuth{
		DeepSeekToken: "token",
		AccountID:     "acct",
		TriedAccounts: map[string]bool{},
	}

	// 首次调用：同步取 challenge（第 1 次上游请求），随后后台预取回填缓存。
	if _, err := client.GetPowForTarget(context.Background(), a, targetPath, 1); err != nil {
		t.Fatalf("GetPowForTarget error: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !client.powCache.hasFresh("acct", targetPath) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !client.powCache.hasFresh("acct", targetPath) {
		t.Fatal("expected background prefetch to cache the next challenge")
	}

	// 第二次调用：命中预取缓存，直接返回第 2 次上游请求取到的 challenge。
	header, err := client.GetPowForTarget(context.Background(), a, targetPath, 1)
	if err != nil {
		t.Fatalf("GetPowForTarget error: %v", err)
	}
	if got := powHeaderPayload(t, header)["signature"]; got != "sig2" {
		t.Fatalf("expected prefetched challenge signature sig2, got %#v", got)
	}
}

func powHeaderPayload(t *testing.T, header string) map[string]any {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		t.Fatalf("decode pow header: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(decoded, &payload); err != nil {
		t.Fatalf("unmarshal pow header: %v", err)
	}
	return payload
}

func testPowChallenge(answer int64, targetPath string, signature string) map[string]any {
	expireAt := time.Now().Add(time.Hour).Unix()
	salt := "salt-" + signature
	hash := powpkg.DeepSeekHashV1([]byte(powpkg.BuildPrefix(salt, expireAt) + strconv.FormatInt(answer, 10)))
	return map[string]any{
		"algorithm":   "DeepSeekHashV1",
		"challenge":   fmtHex(hash[:]),
		"salt":        salt,
		"expire_at":   expireAt,
		"difficulty":  int64(1000),
		"signature":   signature,
		"target_path": targetPath,
	}
}

func fmtHex(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = digits[v>>4]
		out[i*2+1] = digits[v&0x0f]
	}
	return string(out)
}
