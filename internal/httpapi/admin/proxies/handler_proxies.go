package proxies

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"ds2api/internal/config"
	dsclient "ds2api/internal/deepseek/client"
)

var proxyConnectivityTester = func(ctx context.Context, proxy config.Proxy) map[string]any {
	return dsclient.TestProxyConnectivity(ctx, proxy)
}

func validateProxyMutation(cfg *config.Config) error {
	if cfg == nil {
		return nil
	}
	if err := config.ValidateProxyConfig(cfg.Proxies); err != nil {
		return err
	}
	return config.ValidateAccountProxyReferences(cfg.Accounts, cfg.Proxies)
}

func proxyResponse(proxy config.Proxy) map[string]any {
	proxy = config.NormalizeProxy(proxy)
	return map[string]any{
		"id":           proxy.ID,
		"name":         proxy.Name,
		"type":         proxy.Type,
		"host":         proxy.Host,
		"port":         proxy.Port,
		"username":     proxy.Username,
		"has_password": strings.TrimSpace(proxy.Password) != "",
	}
}

func (h *Handler) listProxies(w http.ResponseWriter, _ *http.Request) {
	proxies := h.Store.Snapshot().Proxies
	items := make([]map[string]any, 0, len(proxies))
	for _, proxy := range proxies {
		proxy = config.NormalizeProxy(proxy)
		items = append(items, map[string]any{
			"id":           proxy.ID,
			"name":         proxy.Name,
			"type":         proxy.Type,
			"host":         proxy.Host,
			"port":         proxy.Port,
			"username":     proxy.Username,
			"has_password": strings.TrimSpace(proxy.Password) != "",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (h *Handler) addProxy(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	_ = json.NewDecoder(r.Body).Decode(&req)
	proxy := toProxy(req)
	err := h.Store.Update(func(c *config.Config) error {
		c.Proxies = append(c.Proxies, proxy)
		return validateProxyMutation(c)
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	h.ProxyChanged()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "proxy": proxyResponse(proxy)})
}

// ProxyChanged 在代理新增/变更后刷新运行侧缓存（账号池 + DeepSeek client 代理连接池）。
func (h *Handler) ProxyChanged() {
	if h == nil {
		return
	}
	if h.Pool != nil {
		h.Pool.Reset()
	}
	if h.ResetProxyClients != nil {
		h.ResetProxyClients()
	}
}

func (h *Handler) updateProxy(w http.ResponseWriter, r *http.Request) {
	proxyID := chi.URLParam(r, "proxyID")
	if decoded, err := url.PathUnescape(proxyID); err == nil {
		proxyID = decoded
	}
	var req map[string]any
	_ = json.NewDecoder(r.Body).Decode(&req)
	proxy := toProxy(req)
	proxy.ID = strings.TrimSpace(proxyID)

	err := h.Store.Update(func(c *config.Config) error {
		for i, existing := range c.Proxies {
			existing = config.NormalizeProxy(existing)
			if existing.ID != proxy.ID {
				continue
			}
			if proxy.Password == "" {
				proxy.Password = existing.Password
			}
			c.Proxies[i] = proxy
			return validateProxyMutation(c)
		}
		return newRequestError("代理不存在")
	})
	if err != nil {
		if detail, ok := requestErrorDetail(err); ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": detail})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	h.ProxyChanged()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "proxy": proxyResponse(proxy)})
}

func (h *Handler) deleteProxy(w http.ResponseWriter, r *http.Request) {
	proxyID := chi.URLParam(r, "proxyID")
	if decoded, err := url.PathUnescape(proxyID); err == nil {
		proxyID = decoded
	}
	err := h.Store.Update(func(c *config.Config) error {
		idx := -1
		for i, existing := range c.Proxies {
			existing = config.NormalizeProxy(existing)
			if existing.ID == strings.TrimSpace(proxyID) {
				idx = i
				break
			}
		}
		if idx < 0 {
			return newRequestError("代理不存在")
		}
		c.Proxies = append(c.Proxies[:idx], c.Proxies[idx+1:]...)
		for i := range c.Accounts {
			if strings.TrimSpace(c.Accounts[i].ProxyID) == strings.TrimSpace(proxyID) {
				c.Accounts[i].ProxyID = ""
				// 代理已删除，手动绑定不再有对象：清除标记让账号回到可调度状态。
				c.Accounts[i].ManualBinding = false
			}
		}
		return validateProxyMutation(c)
	})
	if err != nil {
		if detail, ok := requestErrorDetail(err); ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": detail})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	h.ProxyChanged()
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (h *Handler) testProxy(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	_ = json.NewDecoder(r.Body).Decode(&req)
	proxyID := fieldString(req, "proxy_id")

	var proxy config.Proxy
	if proxyID != "" {
		var ok bool
		proxy, ok = findProxyByID(h.Store.Snapshot(), proxyID)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": "代理不存在"})
			return
		}
	} else {
		proxy = toProxy(req)
	}

	result := proxyConnectivityTester(r.Context(), proxy)
	writeJSON(w, http.StatusOK, result)
}

// updateAccountProxy 更新账号的代理/节点绑定。三种语义：
//   - body {"proxy_id":"<id>"}：绑定手动代理（非空）或解绑直连（空串）；
//   - body {"mode":"auto"}：把账号交回代理桥自动调度——清空绑定并同时清除
//     no_proxy 与 manual_binding，下一次绑定调和会给它分配健康节点。
//     这是手动绑定之后回到自动分配的唯一入口。
//
// 非空的 proxy_id 表示用户显式指定代理由用户掌握：账号退出自动调度
// （故障转移/补位都不再改动它）；手动选定的 mihomo 节点同样落在这条语义上
// （前端走 /admin/mihomo/bindings 时由 BindAccount 置位，两条路径一致）。
func (h *Handler) updateAccountProxy(w http.ResponseWriter, r *http.Request) {
	identifier := chi.URLParam(r, "identifier")
	if decoded, err := url.PathUnescape(identifier); err == nil {
		identifier = decoded
	}
	var req map[string]any
	_ = json.NewDecoder(r.Body).Decode(&req)
	proxyID := strings.TrimSpace(fieldString(req, "proxy_id"))
	autoMode := strings.EqualFold(strings.TrimSpace(fieldString(req, "mode")), "auto")

	err := h.Store.Update(func(c *config.Config) error {
		if proxyID != "" {
			if _, ok := findProxyByID(*c, proxyID); !ok {
				return newRequestError("代理不存在")
			}
		}
		for i, acc := range c.Accounts {
			if !accountMatchesIdentifier(acc, identifier) {
				continue
			}
			if autoMode {
				c.Accounts[i].ProxyID = ""
				c.Accounts[i].NoProxy = false       // 不再强制直连
				c.Accounts[i].ManualBinding = false // 撤销手动绑定，重新纳入自动调度
				return validateProxyMutation(c)
			}
			c.Accounts[i].ProxyID = proxyID
			// 显式选择"不代理"（proxy_id 为空）后，自动调度不再给该账号分配节点。
			c.Accounts[i].NoProxy = proxyID == ""
			c.Accounts[i].ManualBinding = proxyID != ""
			return validateProxyMutation(c)
		}
		return newRequestError("账号不存在")
	})
	if err != nil {
		if detail, ok := requestErrorDetail(err); ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"detail": detail})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	h.ProxyChanged()
	if autoMode {
		// 账号刚回到自动调度：立即按已有测速结果补位分配，不必等下一轮巡检。
		// 桥未装配时回调为 nil（见 deps.go），与账号侧 notifyAccountsChanged 同规则跳过。
		if h.OnAccountsChanged != nil {
			h.OnAccountsChanged()
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "proxy_id": "", "mode": "auto"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "proxy_id": proxyID})
}
