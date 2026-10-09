package accounts

import (
	"encoding/json"
	"net/http"

	"ds2api/internal/config"
)

// listDeviceIDs 处理 GET /admin/device-ids。
// 返回手工 device_id 号池及每个 id 的已绑定账号数。
func (h *Handler) listDeviceIDs(w http.ResponseWriter, _ *http.Request) {
	items := h.Store.DeviceIDPoolItems()
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"id":          item.ID,
			"bound_count": item.Bound,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"mode":      h.Store.RuntimeDeviceIDMode(),
		"items":     out,
		"available": len(items),
	})
}

// addDeviceID 处理 POST /admin/device-ids。
// 接受带 "B" 前缀与不带前缀两种写法，统一存储为带前缀的规范形式。
func (h *Handler) addDeviceID(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid json"})
		return
	}
	raw, _ := fieldStringOptional(req, "id")
	id, err := config.NormalizeDeviceIDInput(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	added := false
	if err := h.Store.Update(func(c *config.Config) error {
		added = c.AddDeviceID(id)
		return nil
	}); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	if !added {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "该 device_id 已存在"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"id":        id,
		"available": h.Store.DeviceIDPoolSize(),
	})
}

// deleteDeviceID 处理 POST /admin/device-ids/delete。
// 删除后原先绑定该 id 的账号会立即换绑到剩余 id 中绑定数最少者。
func (h *Handler) deleteDeviceID(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid json"})
		return
	}
	raw, _ := fieldStringOptional(req, "id")
	id, err := config.NormalizeDeviceIDInput(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	err = h.Store.Update(func(c *config.Config) error {
		if !c.HasDeviceID(id) {
			return newRequestError("device_id 不存在")
		}
		config.RemoveDeviceIDAndRebind(c, id)
		return nil
	})
	if err != nil {
		if detail, ok := requestErrorDetail(err); ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": detail})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"available": h.Store.DeviceIDPoolSize(),
	})
}
