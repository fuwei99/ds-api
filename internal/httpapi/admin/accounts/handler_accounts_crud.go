package accounts

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"ds2api/internal/account"
	"ds2api/internal/config"
	adminshared "ds2api/internal/httpapi/admin/shared"
)

func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	page := intFromQuery(r, "page", 1)
	pageSize := intFromQuery(r, "page_size", 10)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 1
	}
	if pageSize > 5000 {
		pageSize = 5000
	}
	accounts := h.Store.Snapshot().Accounts
	reverseAccounts(accounts)
	// 将已启用且未禁言的账号排在前面，方便管理后台优先看到可用账号。
	sort.SliceStable(accounts, func(i, j int) bool {
		ai, aj := accounts[i], accounts[j]
		activeI := ai.IsEnabled() && !ai.IsMuted()
		activeJ := aj.IsEnabled() && !aj.IsMuted()
		return activeI && !activeJ
	})
	q := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("q")))
	if q != "" {
		filtered := make([]config.Account, 0, len(accounts))
		for _, acc := range accounts {
			id := strings.ToLower(acc.Identifier())
			if strings.Contains(id, q) ||
				strings.Contains(strings.ToLower(acc.Name), q) ||
				strings.Contains(strings.ToLower(acc.Remark), q) ||
				strings.Contains(strings.ToLower(acc.Email), q) ||
				strings.Contains(strings.ToLower(acc.Mobile), q) {
				filtered = append(filtered, acc)
			}
		}
		accounts = filtered
	}
	total := len(accounts)
	totalPages := 1
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	items := make([]map[string]any, 0, end-start)
	for _, acc := range accounts[start:end] {
		testStatus, _ := h.Store.AccountTestStatus(acc.Identifier())
		token := strings.TrimSpace(acc.Token)
		items = append(items, map[string]any{
			"identifier":      acc.Identifier(),
			"name":            acc.Name,
			"remark":          acc.Remark,
			"email":           acc.Email,
			"mobile":          acc.Mobile,
			"proxy_id":        acc.ProxyID,
			"pool_type":       config.NormalizePoolType(acc.PoolType),
			"priority":        acc.Priority,
			"has_password":    acc.Password != "",
			"has_token":       token != "",
			"token_preview":   maskSecretPreview(token),
			"test_status":     testStatus,
			"device_id_type":  config.NormalizeDeviceIDType(acc.DeviceIDType),
			"enabled":         acc.IsEnabled(),
			"disabled_reason": acc.DisabledReason,
			"banned":          acc.IsBanned(),
			"auth_failed":     acc.IsAuthFailed(),
			"muted":           acc.IsMuted(),
			"muted_until":     acc.MutedUntil,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "page_size": pageSize, "total_pages": totalPages})
}

func (h *Handler) addAccount(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	_ = json.NewDecoder(r.Body).Decode(&req)
	acc := toAccount(req)
	if acc.Identifier() == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "需要 email 或 mobile"})
		return
	}
	err := h.Store.Update(func(c *config.Config) error {
		if acc.ProxyID != "" {
			if _, ok := findProxyByID(*c, acc.ProxyID); !ok {
				return fmt.Errorf("代理不存在")
			}
		}
		mobileKey := config.CanonicalMobileKey(acc.Mobile)
		for _, a := range c.Accounts {
			if acc.Email != "" && a.Email == acc.Email {
				return fmt.Errorf("邮箱已存在")
			}
			if mobileKey != "" && config.CanonicalMobileKey(a.Mobile) == mobileKey {
				return fmt.Errorf("手机号已存在")
			}
		}
		c.Accounts = append(c.Accounts, acc)
		if c.ElasticPool.Enabled {
			account.ReconcileElasticPool(c)
		}
		return nil
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	h.Pool.Reset()
	h.notifyAccountsChanged()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "total_accounts": len(h.Store.Snapshot().Accounts)})
}

func (h *Handler) updateAccount(w http.ResponseWriter, r *http.Request) {
	identifier := chi.URLParam(r, "identifier")
	if decoded, err := url.PathUnescape(identifier); err == nil {
		identifier = decoded
	}

	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid json"})
		return
	}
	name, nameOK := fieldStringOptional(req, "name")
	remark, remarkOK := fieldStringOptional(req, "remark")
	poolType, poolTypeOK := fieldStringOptional(req, "pool_type")
	priority, priorityOK := fieldIntOptionalOK(req, "priority")

	var elasticReconciled bool
	err := h.Store.Update(func(c *config.Config) error {
		for i, acc := range c.Accounts {
			if !accountMatchesIdentifier(acc, identifier) {
				continue
			}
			if nameOK {
				c.Accounts[i].Name = name
			}
			if remarkOK {
				c.Accounts[i].Remark = remark
			}
			if poolTypeOK {
				c.Accounts[i].PoolType = config.NormalizePoolType(poolType)
			}
			if priorityOK {
				c.Accounts[i].Priority = priority
			}
			if c.ElasticPool.Enabled {
				// 优先级/号池变化会改变弹性号池的启用集合，需重算并让号池与
				// 代理桥按新集合刷新（新启用的账号要及时补上节点）。
				account.ReconcileElasticPool(c)
				elasticReconciled = true
			}
			return nil
		}
		return newRequestError("账号不存在")
	})
	if err != nil {
		if detail, ok := requestErrorDetail(err); ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": detail})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	if elasticReconciled {
		h.Pool.Reset()
		h.notifyAccountsChanged()
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "total_accounts": len(h.Store.Snapshot().Accounts)})
}

func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	identifier := chi.URLParam(r, "identifier")
	if decoded, err := url.PathUnescape(identifier); err == nil {
		identifier = decoded
	}
	var removedEmail, removedMobile string
	err := h.Store.Update(func(c *config.Config) error {
		idx := -1
		for i, a := range c.Accounts {
			if accountMatchesIdentifier(a, identifier) {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("账号不存在")
		}
		removedEmail = c.Accounts[idx].Email
		removedMobile = c.Accounts[idx].Mobile
		c.Accounts = append(c.Accounts[:idx], c.Accounts[idx+1:]...)
		if c.ElasticPool.Enabled {
			account.ReconcileElasticPool(c)
		}
		// 账号被删除后重算号池绑定数。
		config.ReconcileDeviceIDBindings(c)
		return nil
	})
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": err.Error()})
		return
	}
	removeDeviceProfile(config.Account{Email: removedEmail, Mobile: removedMobile}.Identifier())
	forgetDeviceGenLock(h.DS, config.Account{Email: removedEmail, Mobile: removedMobile}.Identifier())
	forgetAuthFailures(h.DS, config.Account{Email: removedEmail, Mobile: removedMobile}.Identifier())
	h.Pool.Reset()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "total_accounts": len(h.Store.Snapshot().Accounts)})
}

// removeDeviceProfile 删除账号的设备档案文件；文件不存在时静默返回。
func removeDeviceProfile(identifier string) {
	if identifier == "" {
		return
	}
	if err := os.Remove(config.DeviceProfilePath(identifier)); err != nil && !os.IsNotExist(err) {
		config.Logger.Warn("[device_id] remove profile failed", "account", identifier, "error", err)
	}
}

// deviceLockForgetter 由 deepseek client 可选实现，用于清理账号的 device_id 生成锁。
type deviceLockForgetter interface {
	ForgetDeviceGenLock(identifier string)
}

// forgetDeviceGenLock 删除账号后清理 device_id 生成锁（DS 未实现时静默跳过）。
func forgetDeviceGenLock(ds adminshared.DeepSeekCaller, identifier string) {
	if ds == nil || identifier == "" {
		return
	}
	if fg, ok := ds.(deviceLockForgetter); ok {
		fg.ForgetDeviceGenLock(identifier)
	}
}

// authFailureForgetter 由 deepseek client 可选实现，用于清理账号的连续鉴权失败计数。
type authFailureForgetter interface {
	ForgetAuthFailures(identifier string)
}

// forgetAuthFailures 删除账号后清理其连续鉴权失败计数（DS 未实现时静默跳过）。
func forgetAuthFailures(ds adminshared.DeepSeekCaller, identifier string) {
	if ds == nil || identifier == "" {
		return
	}
	if fg, ok := ds.(authFailureForgetter); ok {
		fg.ForgetAuthFailures(identifier)
	}
}

func (h *Handler) toggleAccountEnabled(w http.ResponseWriter, r *http.Request) {
	identifier := chi.URLParam(r, "identifier")
	if decoded, err := url.PathUnescape(identifier); err == nil {
		identifier = decoded
	}

	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid json"})
		return
	}
	enabled, ok := fieldBoolOptional(req, "enabled")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "enabled is required"})
		return
	}

	var current bool
	err := h.Store.Update(func(c *config.Config) error {
		for i, acc := range c.Accounts {
			if !accountMatchesIdentifier(acc, identifier) {
				continue
			}
			c.Accounts[i].Disabled = !enabled
			// 手动启用同时解除"鉴权异常"判定：账号被移出号池通常是因为密码/
			// 账号状态出了问题，管理员修好后重新启用应当让它重新参与调度。
			if enabled {
				c.Accounts[i].AuthFailed = false
				c.Accounts[i].DisabledReason = ""
			}
			if c.ElasticPool.Enabled {
				account.ReconcileElasticPool(c)
			}
			// 账号被手动关闭时解除 device_id 绑定，重新启用后重新分配。
			config.ReconcileDeviceIDBindings(c)
			current = c.Accounts[i].IsEnabled()
			return nil
		}
		return newRequestError("账号不存在")
	})
	if err != nil {
		if detail, ok := requestErrorDetail(err); ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": detail})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	h.Pool.Reset()
	h.notifyAccountsChanged()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "enabled": current})
}

// batchDeleteBannedAccounts 处理 POST /admin/accounts/banned/delete 请求。
// 删除所有被上游停用(IsBanned)的账号，并清理其设备档案与 device_id 生成锁。
func (h *Handler) batchDeleteBannedAccounts(w http.ResponseWriter, r *http.Request) {
	var removed []string
	err := h.Store.Update(func(c *config.Config) error {
		kept := make([]config.Account, 0, len(c.Accounts))
		for _, acc := range c.Accounts {
			if acc.IsBanned() {
				if id := acc.Identifier(); id != "" {
					removed = append(removed, id)
				}
				continue
			}
			kept = append(kept, acc)
		}
		c.Accounts = kept
		if c.ElasticPool.Enabled {
			account.ReconcileElasticPool(c)
		}
		// 被停用账号被删除后重算号池绑定数。
		config.ReconcileDeviceIDBindings(c)
		return nil
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	for _, identifier := range removed {
		removeDeviceProfile(identifier)
		forgetDeviceGenLock(h.DS, identifier)
		forgetAuthFailures(h.DS, identifier)
	}
	h.Pool.Reset()
	h.notifyAccountsChanged()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "total": len(removed)})
}

func (h *Handler) batchToggleAccountEnabled(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid json"})
		return
	}
	enabled, ok := fieldBoolOptional(req, "enabled")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "enabled is required"})
		return
	}

	var total int
	err := h.Store.Update(func(c *config.Config) error {
		total = len(c.Accounts)
		for i := range c.Accounts {
			c.Accounts[i].Disabled = !enabled
			// 批量启用同样解除"鉴权异常"判定，让修好凭据的账号重新参与调度。
			if enabled {
				c.Accounts[i].AuthFailed = false
				c.Accounts[i].DisabledReason = ""
			}
		}
		if c.ElasticPool.Enabled {
			account.ReconcileElasticPool(c)
		}
		// 批量关闭时解除全部 device_id 绑定，重新启用后重新分配。
		config.ReconcileDeviceIDBindings(c)
		return nil
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	h.Pool.Reset()
	h.notifyAccountsChanged()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "total": total, "enabled": enabled})
}
