package configmgmt

import (
	"ds2api/internal/chathistory"
	"ds2api/internal/config"
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

// inflightAccounts 返回正在生成回复的账号标识快照（池未接线时为 nil）。
// 必须在 Store.Update 事务之前调用：账号池会在持自身锁的状态下回读 Store。
func (h *Handler) inflightAccounts() map[string]bool {
	if h == nil || h.Pool == nil {
		return nil
	}
	return h.Pool.InUseIDs()
}

func maskSecretPreview(secret string) string {
	return adminshared.MaskSecretPreview(secret)
}
func toStringSlice(v any) ([]string, bool) { return adminshared.ToStringSlice(v) }
func toAccount(m map[string]any) config.Account {
	return adminshared.ToAccount(m)
}
func toAPIKeys(v any) ([]config.APIKey, bool) { return adminshared.ToAPIKeys(v) }
func applyCredentialFlags(item *config.APIKey, m map[string]any) {
	adminshared.ApplyCredentialFlags(item, m)
}
func mergeAPIKeysPreferStructured(existing, incoming []config.APIKey) ([]config.APIKey, int) {
	return adminshared.MergeAPIKeysPreferStructured(existing, incoming)
}
func fieldString(m map[string]any, key string) string {
	return adminshared.FieldString(m, key)
}
func fieldStringOptional(m map[string]any, key string) (string, bool) {
	return adminshared.FieldStringOptional(m, key)
}
func normalizeAccountForStorage(acc config.Account) config.Account {
	return adminshared.NormalizeAccountForStorage(acc)
}
func accountDedupeKey(acc config.Account) string { return adminshared.AccountDedupeKey(acc) }
func normalizeAndDedupeAccounts(accounts []config.Account) []config.Account {
	return adminshared.NormalizeAndDedupeAccounts(accounts)
}
func newRequestError(detail string) error { return adminshared.NewRequestError(detail) }
func requestErrorDetail(err error) (string, bool) {
	return adminshared.RequestErrorDetail(err)
}
func normalizeSettingsConfig(c *config.Config) { adminshared.NormalizeSettingsConfig(c) }
func validateSettingsConfig(c config.Config) error {
	return adminshared.ValidateSettingsConfig(c)
}
