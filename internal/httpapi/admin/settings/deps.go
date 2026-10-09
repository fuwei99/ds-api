package settings

import (
	"ds2api/internal/chathistory"
	adminshared "ds2api/internal/httpapi/admin/shared"
)

type Handler struct {
	Store       adminshared.ConfigStore
	Pool        adminshared.PoolController
	DS          adminshared.DeepSeekCaller
	OpenAI      adminshared.OpenAIChatCaller
	ChatHistory *chathistory.Store
}

var writeJSON = adminshared.WriteJSON

func fieldString(m map[string]any, key string) string {
	return adminshared.FieldString(m, key)
}

func (h *Handler) computeSyncHash() string {
	return adminshared.ComputeSyncHash(h.Store)
}
