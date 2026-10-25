package accounts

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"ds2api/internal/config"
	dsclient "ds2api/internal/deepseek/client"
)

// realDeviceIDRefresher 由 deepseek client 可选实现（与 deviceLockForgetter 同样的做法，
// 便于测试注入替身）。未实现时 real 设备刷新接口返回 501。
type realDeviceIDRefresher interface {
	RefreshRealDeviceID(ctx context.Context, identifier string, resetProfile bool) (string, error)
	RefreshRealDeviceIDs(ctx context.Context, identifiers []string, resetProfile bool) []dsclient.RealDeviceIDRefreshResult
}

func (h *Handler) realDeviceRefresher() (realDeviceIDRefresher, bool) {
	if h == nil || h.DS == nil {
		return nil, false
	}
	refresher, ok := h.DS.(realDeviceIDRefresher)
	return refresher, ok
}

// realDeviceAccounts 返回标注为 real 的账号标识，保持配置顺序。
func (h *Handler) realDeviceAccounts() []string {
	if h == nil || h.Store == nil {
		return nil
	}
	accounts := h.Store.Snapshot().Accounts
	out := make([]string, 0, len(accounts))
	for _, acc := range accounts {
		if config.NormalizeDeviceIDType(acc.DeviceIDType) != config.DeviceIDTypeAuto {
			continue
		}
		if identifier := acc.Identifier(); identifier != "" {
			out = append(out, identifier)
		}
	}
	return out
}

// requireRealMode 返回 auto 模式不可用时给调用方的说明。
// Vercel 部署下 auto 会被强制降级为 manual（见 EffectiveDeviceIDMode），
// 此时这些接口没有意义，应直接拒绝而不是生成一堆用不上的 id。
func (h *Handler) requireRealMode() (string, bool) {
	if h == nil || h.Store == nil {
		return "配置未就绪", false
	}
	if config.EffectiveDeviceIDMode(h.Store.RuntimeDeviceIDMode()) != config.DeviceIDTypeAuto {
		return "当前不是 auto 模式，无真实设备 id 可管理", false
	}
	return "", true
}

// listRealDeviceIDs 处理 GET /admin/device-ids/real?page=&page_size=&q=。
// 返回绑定真实设备 id 的账号：auto 模式下这份清单就是"设备"清单。
// 分页与搜索参数与 GET /admin/accounts 同形，管理端复用同一套控件与语义。
func (h *Handler) listRealDeviceIDs(w http.ResponseWriter, r *http.Request) {
	if detail, ok := h.requireRealMode(); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": detail})
		return
	}
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
	accounts := append([]config.Account(nil), h.Store.Snapshot().Accounts...)
	// 已启用且未被禁言的账号排前面，其余保持配置顺序：管理端先看到在跑的设备。
	sort.SliceStable(accounts, func(i, j int) bool {
		activeI := accounts[i].IsEnabled() && !accounts[i].IsMuted()
		activeJ := accounts[j].IsEnabled() && !accounts[j].IsMuted()
		return activeI && !activeJ
	})
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	all := make([]map[string]any, 0, len(accounts))
	for _, acc := range accounts {
		identifier := acc.Identifier()
		if identifier == "" || config.NormalizeDeviceIDType(acc.DeviceIDType) != config.DeviceIDTypeAuto {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(identifier), q) {
			continue
		}
		all = append(all, map[string]any{
			"identifier": identifier,
			"device_id":  strings.TrimSpace(acc.DeviceID),
		})
	}
	total := len(all)
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
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"mode":    h.Store.RuntimeDeviceIDMode(),
		"items":   all[start:end],
		"total":   total,
		// total_all 是过滤前的 real 账号数：批量刷新作用于全量，界面据此决定
		// 批量按钮是否可用（搜索把当前页筛空不等于没有可刷新的账号）。
		"total_all":   len(h.realDeviceAccounts()),
		"page":        page,
		"page_size":   pageSize,
		"total_pages": totalPages,
	})
}

// realDeviceRefreshRequest 是刷新接口的请求体。两个接口共用一个形状：
// reset_profile=true 表示先删设备档案再生成（换一台新设备），false 表示复用档案重算。
type realDeviceRefreshRequest struct {
	Identifier   string `json:"identifier"`
	ResetProfile bool   `json:"reset_profile"`
}

// refreshRealDeviceID 处理 POST /admin/device-ids/real/refresh。
func (h *Handler) refreshRealDeviceID(w http.ResponseWriter, r *http.Request) {
	if detail, ok := h.requireRealMode(); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": detail})
		return
	}
	var req realDeviceRefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid json"})
		return
	}
	identifier := strings.TrimSpace(req.Identifier)
	if identifier == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "需要账号标识（identifier / email / mobile）"})
		return
	}
	if _, ok := findAccountByIdentifier(h.Store, identifier); !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "账号不存在"})
		return
	}
	refresher, ok := h.realDeviceRefresher()
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"detail": "当前部署不支持刷新真实设备 id"})
		return
	}
	deviceID, err := refresher.RefreshRealDeviceID(r.Context(), identifier, req.ResetProfile)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"identifier": identifier,
		"device_id":  deviceID,
	})
}

// refreshAllRealDeviceIDs 处理 POST /admin/device-ids/real/refresh-all。
// 逐个账号刷新并返回每个账号的结果：失败不连坐，管理端据此逐行提示。
func (h *Handler) refreshAllRealDeviceIDs(w http.ResponseWriter, r *http.Request) {
	if detail, ok := h.requireRealMode(); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": detail})
		return
	}
	var req realDeviceRefreshRequest
	// 允许空 body：批量刷新只带 reset_profile 一个开关。
	_ = json.NewDecoder(r.Body).Decode(&req)
	refresher, ok := h.realDeviceRefresher()
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"detail": "当前部署不支持刷新真实设备 id"})
		return
	}
	identifiers := h.realDeviceAccounts()
	results := refresher.RefreshRealDeviceIDs(r.Context(), identifiers, req.ResetProfile)
	items := make([]map[string]any, 0, len(results))
	failed := 0
	for _, res := range results {
		item := map[string]any{"identifier": res.Identifier, "device_id": res.DeviceID}
		if res.Error != "" {
			item["error"] = res.Error
			failed++
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":   failed == 0,
		"refreshed": len(results) - failed,
		"failed":    failed,
		"total":     len(results),
		"items":     items,
	})
}
