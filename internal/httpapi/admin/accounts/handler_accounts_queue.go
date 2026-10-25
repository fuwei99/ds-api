package accounts

import "net/http"

// queueStatus 处理 GET /admin/queue/status。
// 在号池状态之外附带设备 ID 模式与手工 device_id 剩余可用数量，
// 供账号管理界面实时展示"剩余可用device_id数量"标签。
func (h *Handler) queueStatus(w http.ResponseWriter, _ *http.Request) {
	status := h.Pool.Status()
	status["device_id_mode"] = h.Store.RuntimeDeviceIDMode()
	status["device_id_remaining"] = h.Store.DeviceIDPoolSize()
	writeJSON(w, http.StatusOK, status)
}
