package shared

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"ds2api/internal/config"
	"ds2api/internal/util"
)

var intFrom = util.IntFrom

var WriteJSON = util.WriteJSON
var IntFrom = util.IntFrom

func ReverseAccounts(a []config.Account) { reverseAccounts(a) }
func IntFromQuery(r *http.Request, key string, d int) int {
	return intFromQuery(r, key, d)
}
func NilIfEmpty(s string) any { return nilIfEmpty(s) }
func NilIfZero(v int64) any   { return nilIfZero(v) }
func MaskSecretPreview(secret string) string {
	return maskSecretPreview(secret)
}
func ToStringSlice(v any) ([]string, bool) { return toStringSlice(v) }
func ToAccount(m map[string]any) config.Account {
	return toAccount(m)
}
func ToAPIKeys(v any) ([]config.APIKey, bool) {
	return toAPIKeys(v)
}
func NormalizeAPIKeyForStorage(item config.APIKey) config.APIKey {
	return config.NormalizeAPIKey(item)
}
func APIKeyHasMetadata(item config.APIKey) bool {
	return apiKeyHasMetadata(item)
}
func MergeAPIKeysPreferStructured(existing, incoming []config.APIKey) ([]config.APIKey, int) {
	return mergeAPIKeysPreferStructured(existing, incoming)
}
func MergeAPIKeyRecord(existing, incoming config.APIKey) config.APIKey {
	return mergeAPIKeyRecord(existing, incoming)
}
func FieldString(m map[string]any, key string) string {
	return fieldString(m, key)
}
func FieldStringOptional(m map[string]any, key string) (string, bool) {
	return fieldStringOptional(m, key)
}
func FieldBoolOptional(m map[string]any, key string) (bool, bool) {
	return fieldBoolOptional(m, key)
}
func FieldIntOptionalOK(m map[string]any, key string) (int, bool) {
	return fieldIntOptionalOK(m, key)
}
func StatusOr(v int, d int) int { return statusOr(v, d) }
func AccountMatchesIdentifier(acc config.Account, identifier string) bool {
	return accountMatchesIdentifier(acc, identifier)
}
func NormalizeAccountForStorage(acc config.Account) config.Account {
	return normalizeAccountForStorage(acc)
}
func ToProxy(m map[string]any) config.Proxy {
	return toProxy(m)
}
func FindProxyByID(c config.Config, proxyID string) (config.Proxy, bool) {
	return findProxyByID(c, proxyID)
}
func AccountDedupeKey(acc config.Account) string { return accountDedupeKey(acc) }
func NormalizeAndDedupeAccounts(accounts []config.Account) []config.Account {
	return normalizeAndDedupeAccounts(accounts)
}
func FindAccountByIdentifier(store ConfigStore, identifier string) (config.Account, bool) {
	return findAccountByIdentifier(store, identifier)
}

func ComputeSyncHash(store ConfigStore) string {
	if store == nil {
		return ""
	}
	snap := store.Snapshot().Clone()
	snap.ClearAccountTokens()
	snap.ClearVercelCredentials()
	snap.VercelSyncHash = ""
	snap.VercelSyncTime = 0
	b, _ := json.Marshal(snap)
	sum := md5.Sum(b)
	return fmt.Sprintf("%x", sum)
}

func SyncHashForJSON(s string) string {
	var cfg config.Config
	if err := json.Unmarshal([]byte(s), &cfg); err != nil {
		return ""
	}
	cfg.VercelSyncHash = ""
	cfg.VercelSyncTime = 0
	cfg.ClearAccountTokens()
	cfg.ClearVercelCredentials()
	b, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	sum := md5.Sum(b)
	return fmt.Sprintf("%x", sum)
}

func reverseAccounts(a []config.Account) {
	for i, j := 0, len(a)-1; i < j; i, j = i+1, j-1 {
		a[i], a[j] = a[j], a[i]
	}
}

func intFromQuery(r *http.Request, key string, d int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return d
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return d
	}
	return n
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nilIfZero(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func maskSecretPreview(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	if len(secret) <= 4 {
		return strings.Repeat("*", len(secret))
	}
	return secret[:2] + "****" + secret[len(secret)-2:]
}

func toStringSlice(v any) ([]string, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		out = append(out, strings.TrimSpace(fmt.Sprintf("%v", item)))
	}
	return out, true
}

func toAccount(m map[string]any) config.Account {
	email := fieldString(m, "email")
	mobile := config.NormalizeMobileForStorage(fieldString(m, "mobile"))
	return config.Account{
		Name:     fieldString(m, "name"),
		Remark:   fieldString(m, "remark"),
		Email:    email,
		Mobile:   mobile,
		Password: fieldString(m, "password"),
		DeviceID: fieldString(m, "device_id"),
		ProxyID:  fieldString(m, "proxy_id"),
		PoolType: config.NormalizePoolType(fieldString(m, "pool_type")),
		Priority: fieldInt(m, "priority"),
		Locale:   fieldString(m, "locale"),
	}
}

func toAPIKeys(v any) ([]config.APIKey, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]config.APIKey, 0, len(arr))
	seen := map[string]struct{}{}
	for _, item := range arr {
		switch x := item.(type) {
		case map[string]any:
			key := fieldString(x, "key")
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, apiKeyFromMap(x))
		default:
			key := strings.TrimSpace(fmt.Sprintf("%v", item))
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, config.APIKey{Key: key})
		}
	}
	return out, true
}

// apiKeyFromMap builds one API key from a decoded request object. The typed
// fields are read by name; the boolean switches come from the credential-flag
// registry, so a new per-key flag needs no change here.
func apiKeyFromMap(m map[string]any) config.APIKey {
	item := config.APIKey{
		Key:    fieldString(m, "key"),
		Name:   fieldString(m, "name"),
		Remark: fieldString(m, "remark"),
	}
	applyCredentialFlags(&item, m)
	return item
}

// ApplyCredentialFlags copies every registered credential flag supplied by m
// onto item, leaving flags the request did not supply untouched so a partial
// update keeps them.
func ApplyCredentialFlags(item *config.APIKey, m map[string]any) {
	applyCredentialFlags(item, m)
}

// applyCredentialFlags copies every registered credential flag supplied by m
// onto item. A flag that is absent, or that carries an explicit JSON null, is
// "not supplied": it keeps the value already on item, so a partial update never
// clears a switch the request did not actually set. Both readings come from
// fieldBoolOptional and the registry, so a newly registered flag inherits the
// rule without any change here.
func applyCredentialFlags(item *config.APIKey, m map[string]any) {
	for _, spec := range config.CredentialFlagSpecs() {
		value, ok := fieldBoolOptional(m, spec.Name)
		if !ok {
			continue
		}
		item.SetFlag(spec.Name, value)
	}
}

func apiKeyHasMetadata(item config.APIKey) bool {
	return strings.TrimSpace(item.Name) != "" || strings.TrimSpace(item.Remark) != ""
}

func mergeAPIKeysPreferStructured(existing, incoming []config.APIKey) ([]config.APIKey, int) {
	if len(existing) == 0 && len(incoming) == 0 {
		return nil, 0
	}

	merged := make([]config.APIKey, 0, len(existing)+len(incoming))
	index := make(map[string]int, len(existing)+len(incoming))
	for _, item := range existing {
		item = config.NormalizeAPIKey(item)
		if item.Key == "" {
			continue
		}
		if _, ok := index[item.Key]; ok {
			continue
		}
		index[item.Key] = len(merged)
		merged = append(merged, item)
	}

	imported := 0
	for _, item := range incoming {
		item = config.NormalizeAPIKey(item)
		if item.Key == "" {
			continue
		}
		if idx, ok := index[item.Key]; ok {
			keep := merged[idx]
			next := mergeAPIKeyRecord(keep, item)
			if !config.APIKeyEqual(next, keep) {
				merged[idx] = next
				imported++
			}
			continue
		}
		index[item.Key] = len(merged)
		merged = append(merged, item)
		imported++
	}

	if len(merged) == 0 {
		return nil, imported
	}
	return merged, imported
}

func mergeAPIKeyRecord(existing, incoming config.APIKey) config.APIKey {
	existing = config.NormalizeAPIKey(existing)
	incoming = config.NormalizeAPIKey(incoming)
	if existing.Key == "" {
		return incoming
	}
	var keep config.APIKey
	if apiKeyHasMetadata(existing) {
		keep = existing
	} else if apiKeyHasMetadata(incoming) {
		keep = incoming
	} else {
		keep = existing
	}
	// 各凭据开关的合并策略由注册表声明（限制性开关两侧取或，避免配置导入
	// 静默丢掉用户已显式设置的限制）。
	return config.MergeCredentialFlags(keep, incoming)
}

func fieldString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

func fieldStringOptional(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return "", false
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v)), true
}

func fieldBoolOptional(m map[string]any, key string) (bool, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return false, false
	}
	return util.ToBool(v), true
}

// fieldInt 读取整数字段，缺失或无法解析时返回 0。
func fieldInt(m map[string]any, key string) int {
	v, ok := fieldIntOptionalOK(m, key)
	if !ok {
		return 0
	}
	return v
}

// fieldIntOptionalOK 读取可选的整数字段。第二个返回值表示字段是否存在且可解析；
// JSON 数字（float64）、Go int 以及数字字符串均被接受，因此允许负值。
func fieldIntOptionalOK(m map[string]any, key string) (int, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		parsed, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), true
	case string:
		s := strings.TrimSpace(n)
		if s == "" {
			return 0, false
		}
		parsed, err := strconv.Atoi(s)
		if err != nil {
			return 0, false
		}
		return parsed, true
	}
	return 0, false
}

func statusOr(v int, d int) int {
	if v == 0 {
		return d
	}
	return v
}

func accountMatchesIdentifier(acc config.Account, identifier string) bool {
	id := strings.TrimSpace(identifier)
	if id == "" {
		return false
	}
	if strings.TrimSpace(acc.Email) == id {
		return true
	}
	if mobileKey := config.CanonicalMobileKey(id); mobileKey != "" && mobileKey == config.CanonicalMobileKey(acc.Mobile) {
		return true
	}
	return acc.Identifier() == id
}

func normalizeAccountForStorage(acc config.Account) config.Account {
	acc.Name = strings.TrimSpace(acc.Name)
	acc.Remark = strings.TrimSpace(acc.Remark)
	acc.Email = strings.TrimSpace(acc.Email)
	acc.Mobile = config.NormalizeMobileForStorage(acc.Mobile)
	acc.DeviceID = strings.TrimSpace(acc.DeviceID)
	acc.ProxyID = strings.TrimSpace(acc.ProxyID)
	acc.PoolType = config.NormalizePoolType(acc.PoolType)
	acc.Locale = strings.TrimSpace(acc.Locale)
	return acc
}

func toProxy(m map[string]any) config.Proxy {
	return config.NormalizeProxy(config.Proxy{
		ID:       fieldString(m, "id"),
		Name:     fieldString(m, "name"),
		Type:     fieldString(m, "type"),
		Host:     fieldString(m, "host"),
		Port:     intFrom(m["port"]),
		Username: fieldString(m, "username"),
		Password: fieldString(m, "password"),
	})
}

func findProxyByID(c config.Config, proxyID string) (config.Proxy, bool) {
	id := strings.TrimSpace(proxyID)
	if id == "" {
		return config.Proxy{}, false
	}
	for _, proxy := range c.Proxies {
		proxy = config.NormalizeProxy(proxy)
		if proxy.ID == id {
			return proxy, true
		}
	}
	return config.Proxy{}, false
}

func accountDedupeKey(acc config.Account) string {
	if email := strings.TrimSpace(acc.Email); email != "" {
		return "email:" + email
	}
	if mobile := config.CanonicalMobileKey(acc.Mobile); mobile != "" {
		return "mobile:" + mobile
	}
	if id := strings.TrimSpace(acc.Identifier()); id != "" {
		return "id:" + id
	}
	return ""
}

func normalizeAndDedupeAccounts(accounts []config.Account) []config.Account {
	if len(accounts) == 0 {
		return nil
	}
	out := make([]config.Account, 0, len(accounts))
	seen := make(map[string]struct{}, len(accounts))
	for _, acc := range accounts {
		acc = normalizeAccountForStorage(acc)
		key := accountDedupeKey(acc)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, acc)
	}
	return out
}

func findAccountByIdentifier(store ConfigStore, identifier string) (config.Account, bool) {
	id := strings.TrimSpace(identifier)
	if id == "" {
		return config.Account{}, false
	}
	if acc, ok := store.FindAccount(id); ok {
		return acc, true
	}
	accounts := store.Snapshot().Accounts
	for _, acc := range accounts {
		if accountMatchesIdentifier(acc, id) {
			return acc, true
		}
	}
	return config.Account{}, false
}
