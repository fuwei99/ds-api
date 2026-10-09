package settings

import (
	"ds2api/internal/config"
)

// applyRuntimeSettings 把 runtime 段的新值推给号池。它是 runtime 的段后置动作
// （见 handler_settings_write.go 的 sectionPostWriteActions）：注册表只负责
// 判断这次更新是否碰到了 runtime，具体要通知谁由这里决定。
func (h *Handler) applyRuntimeSettings() {
	if h == nil || h.Store == nil || h.Pool == nil {
		return
	}
	accountCount := h.Pool.SchedulableCount()
	maxPer := h.Store.RuntimeAccountMaxInflight()
	recommended := config.DefaultRuntimeRecommended(accountCount, maxPer)
	maxQueue := h.Store.RuntimeAccountMaxQueue(recommended)
	global := h.Store.RuntimeGlobalMaxInflight(recommended)
	h.Pool.ApplyRuntimeLimits(maxPer, maxQueue, global)
	h.Pool.SyncRateLimits()
	h.Pool.SyncDailyLimits()
}
