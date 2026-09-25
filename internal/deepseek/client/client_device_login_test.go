package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"ds2api/internal/config"
)

// fakeLoginDoer 拦截登录请求：命中 riskIDs 的 device_id 返回
// RISK_DEVICE_DETECTED，其余返回登录成功。
type fakeLoginDoer struct {
	mu       sync.Mutex
	riskIDs  map[string]bool
	failAll  bool
	attempts []string
}

func (f *fakeLoginDoer) Do(req *http.Request) (*http.Response, error) {
	if !strings.Contains(req.URL.Path, "/login") {
		return fakeJSONResponse(map[string]any{"code": 0, "data": map[string]any{}}), nil
	}
	deviceID := ""
	if req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		deviceID, _ = payload["device_id"].(string)
	}

	f.mu.Lock()
	f.attempts = append(f.attempts, deviceID)
	risk := f.riskIDs[deviceID]
	failAll := f.failAll
	f.mu.Unlock()

	if risk {
		return fakeJSONResponse(map[string]any{
			"code": 0,
			"data": map[string]any{"biz_code": 1, "biz_msg": "RISK_DEVICE_DETECTED"},
		}), nil
	}
	if failAll {
		return fakeJSONResponse(map[string]any{
			"code": 0,
			"data": map[string]any{"biz_code": 2, "biz_msg": "invalid password"},
		}), nil
	}
	return fakeJSONResponse(map[string]any{
		"code": 0,
		"data": map[string]any{
			"biz_code": 0,
			"biz_data": map[string]any{
				"user": map[string]any{"token": "fresh-token", "id": "sso-1"},
			},
		},
	}), nil
}

func (f *fakeLoginDoer) seenDeviceIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.attempts))
	copy(out, f.attempts)
	return out
}

func fakeJSONResponse(payload map[string]any) *http.Response {
	b, _ := json.Marshal(payload)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(b)),
	}
}

func withFakeLogin(t *testing.T, c *Client, doer *fakeLoginDoer) {
	t.Helper()
	c.regular = doer
	c.fallback = doer
}

// 登录遇到 RISK_DEVICE_DETECTED 时应移除该 id、换绑并无感重试。
func TestLoginRotatesDeviceIDAfterRiskDetection(t *testing.T) {
	first, second := testDeviceID(20), testDeviceID(21)
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p"}],
		`+poolConfigJSON(first, second)+`
	}`)
	doer := &fakeLoginDoer{riskIDs: map[string]bool{first: true}}
	withFakeLogin(t, c, doer)

	token, err := c.Login(context.Background(), storeAccount(t, c, "u@example.com"))
	if err != nil {
		t.Fatalf("login should succeed after rotating the risky device id: %v", err)
	}
	if token != "fresh-token" {
		t.Fatalf("unexpected token %q", token)
	}
	seen := doer.seenDeviceIDs()
	if len(seen) != 2 || seen[0] != first || seen[1] != second {
		t.Fatalf("expected attempts [first second], got %v", seen)
	}
	if _, ok := c.Store.FindDeviceIDPoolItem(first); ok {
		t.Fatal("risky device id must be removed from the pool")
	}
	if got := storeAccount(t, c, "u@example.com").DeviceID; got != second {
		t.Fatalf("account must be rebound to %q, got %q", second, got)
	}
}

// 号池里最后一个 device_id 也失效时才向上游调用方报错。
func TestLoginReportsAllDeviceIDsInvalid(t *testing.T) {
	first, second := testDeviceID(22), testDeviceID(23)
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p"}],
		`+poolConfigJSON(first, second)+`
	}`)
	doer := &fakeLoginDoer{riskIDs: map[string]bool{first: true, second: true}}
	withFakeLogin(t, c, doer)

	_, err := c.Login(context.Background(), storeAccount(t, c, "u@example.com"))
	if !errors.Is(err, errAllDeviceIDsInvalid) {
		t.Fatalf("expected all-device-ids-invalid error, got %v", err)
	}
	if got := c.Store.DeviceIDPoolSize(); got != 0 {
		t.Fatalf("expected empty pool, got %d", got)
	}
	if len(doer.seenDeviceIDs()) != 2 {
		t.Fatalf("expected exactly two attempts, got %v", doer.seenDeviceIDs())
	}
}

// 号池为空时登录直接报"请先配置device_id"，不触网。
func TestLoginFailsFastWhenPoolEmpty(t *testing.T) {
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p"}]
	}`)
	doer := &fakeLoginDoer{riskIDs: map[string]bool{}}
	withFakeLogin(t, c, doer)

	_, err := c.Login(context.Background(), storeAccount(t, c, "u@example.com"))
	if !errors.Is(err, config.ErrDeviceIDPoolEmpty) {
		t.Fatalf("expected ErrDeviceIDPoolEmpty, got %v", err)
	}
	if len(doer.seenDeviceIDs()) != 0 {
		t.Fatalf("no upstream call expected, got %v", doer.seenDeviceIDs())
	}
}

// 非风控类登录失败不得触发换号。
func TestLoginKeepsDeviceIDOnNonRiskFailure(t *testing.T) {
	first, second := testDeviceID(24), testDeviceID(25)
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[{"email":"u@example.com","password":"p"}],
		`+poolConfigJSON(first, second)+`
	}`)
	doer := &fakeLoginDoer{failAll: true}
	withFakeLogin(t, c, doer)
	// 让登录返回业务错误而非风控。

	_, err := c.Login(context.Background(), storeAccount(t, c, "u@example.com"))
	if err == nil || !strings.Contains(err.Error(), "login failed") {
		t.Fatalf("expected login failure, got %v", err)
	}
	if got := c.Store.DeviceIDPoolSize(); got != 2 {
		t.Fatalf("pool must stay intact, got %d", got)
	}
	if len(doer.seenDeviceIDs()) != 1 {
		t.Fatalf("expected a single attempt, got %v", doer.seenDeviceIDs())
	}
}

// 号池里的 id 应该随登录逐个铺开：先加入的 id 不会一次性吸走全部账号，
// 后加入的 id 在下一个账号登录时被分配到。
func TestDeviceIDPoolSpreadsAcrossLogins(t *testing.T) {
	first, second := testDeviceID(30), testDeviceID(31)
	c := newDeviceTestClient(t, `{
		"keys":["k1"],
		"accounts":[
			{"email":"a@example.com","password":"p"},
			{"email":"b@example.com","password":"p"}
		],
		`+poolConfigJSON(first)+`
	}`)
	withFakeLogin(t, c, &fakeLoginDoer{})

	if _, err := c.Login(context.Background(), storeAccount(t, c, "a@example.com")); err != nil {
		t.Fatal(err)
	}
	if got := storeAccount(t, c, "a@example.com").DeviceID; got != first {
		t.Fatalf("first login must take %q, got %q", first, got)
	}

	// 加入第二个 id：已绑定的账号保持原 id，未登录的账号仍未被绑定。
	if err := c.Store.Update(func(cfg *config.Config) error {
		cfg.AddDeviceID(second)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := storeAccount(t, c, "a@example.com").DeviceID; got != first {
		t.Fatalf("existing binding must be kept, got %q", got)
	}
	if got := storeAccount(t, c, "b@example.com").DeviceID; got != "" {
		t.Fatalf("account that never logged in must stay unbound, got %q", got)
	}
	items := c.Store.DeviceIDPoolItems()
	if len(items) != 2 || items[0].Bound != 1 || items[1].Bound != 0 {
		t.Fatalf("unexpected bound counts: %+v", items)
	}

	// 第二个账号登录时拿到绑定数最少的那个新 id。
	if _, err := c.Login(context.Background(), storeAccount(t, c, "b@example.com")); err != nil {
		t.Fatal(err)
	}
	if got := storeAccount(t, c, "b@example.com").DeviceID; got != second {
		t.Fatalf("second login must take %q, got %q", second, got)
	}
}
