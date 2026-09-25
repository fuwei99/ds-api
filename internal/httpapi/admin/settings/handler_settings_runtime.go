package settings

import (
	"strings"

	"ds2api/internal/config"
)

func validateMergedRuntimeSettings(current config.RuntimeConfig, incoming *config.RuntimeConfig) error {
	merged := current
	if incoming != nil {
		if incoming.AccountMaxInflight > 0 {
			merged.AccountMaxInflight = incoming.AccountMaxInflight
		}
		if incoming.AccountMaxQueue > 0 {
			merged.AccountMaxQueue = incoming.AccountMaxQueue
		}
		if incoming.GlobalMaxInflight > 0 {
			merged.GlobalMaxInflight = incoming.GlobalMaxInflight
		}
		if incoming.TokenRefreshIntervalHours > 0 {
			merged.TokenRefreshIntervalHours = incoming.TokenRefreshIntervalHours
		}
		if incoming.DeviceIDMode != "" {
			merged.DeviceIDMode = incoming.DeviceIDMode
		}
		if incoming.MuteReport.Enabled != nil {
			merged.MuteReport.Enabled = incoming.MuteReport.Enabled
		}
		if strings.TrimSpace(incoming.MuteReport.URL) != "" {
			merged.MuteReport.URL = incoming.MuteReport.URL
		}
		if incoming.AccountRateLimit.Enabled != nil {
			merged.AccountRateLimit.Enabled = incoming.AccountRateLimit.Enabled
		}
		if incoming.AccountRateLimit.MaxPerMinute > 0 {
			merged.AccountRateLimit.MaxPerMinute = incoming.AccountRateLimit.MaxPerMinute
		}
	}
	return validateRuntimeSettings(merged)
}

func (h *Handler) applyRuntimeSettings() {
	if h == nil || h.Store == nil || h.Pool == nil {
		return
	}
	accountCount := h.Pool.SchedulableCount()
	maxPer := h.Store.RuntimeAccountMaxInflight()
	recommended := defaultRuntimeRecommended(accountCount, maxPer)
	maxQueue := h.Store.RuntimeAccountMaxQueue(recommended)
	global := h.Store.RuntimeGlobalMaxInflight(recommended)
	h.Pool.ApplyRuntimeLimits(maxPer, maxQueue, global)
	h.Pool.SyncRateLimits()
}

func defaultRuntimeRecommended(accountCount, maxPer int) int {
	if maxPer <= 0 {
		maxPer = 1
	}
	if accountCount <= 0 {
		return maxPer
	}
	return accountCount * maxPer
}
