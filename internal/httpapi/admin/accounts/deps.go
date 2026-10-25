package accounts

import (
	"net/http"

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
	// OnAccountsChanged 在账号启用/禁用、弹性号池重算等操作后触发（若非 nil），
	// 供 mihomo 代理桥立即按已有测速结果为新启用账号分配节点。
	OnAccountsChanged func()
}

var writeJSON = adminshared.WriteJSON

func (h *Handler) notifyAccountsChanged() {
	if h != nil && h.OnAccountsChanged != nil {
		h.OnAccountsChanged()
	}
}

// inflightAccounts 返回正在生成回复的账号标识快照（池未接线时为 nil）。
// 必须在 Store.Update 事务之前调用：账号池会在持自身锁的状态下回读 Store。
func (h *Handler) inflightAccounts() map[string]bool {
	if h == nil || h.Pool == nil {
		return nil
	}
	return h.Pool.InUseIDs()
}

func reverseAccounts(a []config.Account) { adminshared.ReverseAccounts(a) }
func intFromQuery(r *http.Request, key string, d int) int {
	return adminshared.IntFromQuery(r, key, d)
}
func maskSecretPreview(secret string) string {
	return adminshared.MaskSecretPreview(secret)
}
func toAccount(m map[string]any) config.Account {
	return adminshared.ToAccount(m)
}
func fieldStringOptional(m map[string]any, key string) (string, bool) {
	return adminshared.FieldStringOptional(m, key)
}
func fieldBoolOptional(m map[string]any, key string) (bool, bool) {
	return adminshared.FieldBoolOptional(m, key)
}
func fieldIntOptionalOK(m map[string]any, key string) (int, bool) {
	return adminshared.FieldIntOptionalOK(m, key)
}
func accountMatchesIdentifier(acc config.Account, identifier string) bool {
	return adminshared.AccountMatchesIdentifier(acc, identifier)
}
func findProxyByID(c config.Config, proxyID string) (config.Proxy, bool) {
	return adminshared.FindProxyByID(c, proxyID)
}
func findAccountByIdentifier(store adminshared.ConfigStore, identifier string) (config.Account, bool) {
	return adminshared.FindAccountByIdentifier(store, identifier)
}
func newRequestError(detail string) error { return adminshared.NewRequestError(detail) }
func requestErrorDetail(err error) (string, bool) {
	return adminshared.RequestErrorDetail(err)
}
