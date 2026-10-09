package usage

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"ds2api/internal/usagestats"
)

func (h *Handler) getUsage(w http.ResponseWriter, _ *http.Request) {
	if h == nil || h.Usage == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "usage stats store is not configured"})
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	writeJSON(w, http.StatusOK, map[string]any{
		"items": h.Usage.Summary(),
		"path":  h.Usage.Path(),
	})
}

func (h *Handler) clearUsage(w http.ResponseWriter, _ *http.Request) {
	if h == nil || h.Usage == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "usage stats store is not configured"})
		return
	}
	if err := h.Usage.Clear(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) getUsageSettings(w http.ResponseWriter, _ *http.Request) {
	if h == nil || h.Usage == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "usage stats store is not configured"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": h.Usage.Settings()})
}

func (h *Handler) updateUsageSettings(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Usage == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "usage stats store is not configured"})
		return
	}
	var body struct {
		Settings usagestats.UsageSettings `json:"settings"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid JSON body"})
		return
	}
	defer func() {
		if err := r.Body.Close(); err != nil {
			log.Printf("[admin_usage] close request body: %v", err)
		}
	}()
	if err := h.Usage.SaveSettings(body.Settings); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, usagestats.ErrInvalidUsageSettings) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": h.Usage.Settings()})
}

// importUsage 合并外部导出的 CSV 还原出的条目（前端解析后以 JSON 提交）。
// 合并按日期 + 模型 + 调用方累加，费用沿用导出端数值，不做重新估算。
func (h *Handler) importUsage(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Usage == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "usage stats store is not configured"})
		return
	}
	var body struct {
		Entries []usagestats.Entry `json:"entries"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid JSON body"})
		return
	}
	defer func() {
		if err := r.Body.Close(); err != nil {
			log.Printf("[admin_usage] close request body: %v", err)
		}
	}()
	merged, err := h.Usage.Import(body.Entries)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": merged, "items": h.Usage.Summary()})
}
