package auth

import (
	"context"
	"net/http"
	"testing"

	"ds2api/internal/account"
	"ds2api/internal/config"
)

// TestForceDisableToolsForRequestReadsManagedKey 验证只有托管 Key 上的
// 「强制禁用工具调用」开关会被识别，直连 token 与未知 Key 恒为关闭。
func TestForceDisableToolsForRequestReadsManagedKey(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"api_keys":[
			{"key":"plain-key"},
			{"key":"blocked-key","force_disable_tools":true}
		],
		"accounts":[
			{"email":"acc@example.com","token":"tok","pool_type":"default"}
		]
	}`)
	store := config.LoadStore()
	r := NewResolver(store, account.NewPool(store), func(_ context.Context, _ config.Account) (string, error) {
		return "fresh-token", nil
	})

	newReq := func(headers map[string]string) *http.Request {
		req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return req
	}

	cases := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{name: "nil request", headers: nil, want: false},
		{name: "no credential", headers: map[string]string{}, want: false},
		{name: "managed key default off", headers: map[string]string{"Authorization": "Bearer plain-key"}, want: false},
		{name: "managed key switched on", headers: map[string]string{"Authorization": "Bearer blocked-key"}, want: true},
		{name: "x-api-key switched on", headers: map[string]string{"x-api-key": "blocked-key"}, want: true},
		{name: "direct token stays off", headers: map[string]string{"Authorization": "Bearer some-direct-deepseek-token"}, want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.headers == nil {
				if r.ForceDisableToolsForRequest(nil) {
					t.Fatal("expected nil request to report false")
				}
				return
			}
			if got := r.ForceDisableToolsForRequest(newReq(c.headers)); got != c.want {
				t.Fatalf("ForceDisableToolsForRequest = %v, want %v", got, c.want)
			}
		})
	}
}

// TestForceDisableToolsForRequestNilResolver 验证空解析器不会 panic。
func TestForceDisableToolsForRequestNilResolver(t *testing.T) {
	var r *Resolver
	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer any-key")
	if r.ForceDisableToolsForRequest(req) {
		t.Fatal("expected nil resolver to report false")
	}
}
