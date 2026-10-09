package config

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

type Config struct {
	Keys           []string           `json:"keys,omitempty"`
	APIKeys        []APIKey           `json:"api_keys,omitempty"`
	Accounts       []Account          `json:"accounts,omitempty"`
	Proxies        []Proxy            `json:"proxies,omitempty"`
	ModelAliases   map[string]string  `json:"model_aliases,omitempty"`
	Admin          AdminConfig        `json:"admin,omitempty"`
	Runtime        RuntimeConfig      `json:"runtime,omitempty"`
	AutoDelete     AutoDeleteConfig   `json:"auto_delete"`
	DeviceIDPool   DeviceIDPoolConfig `json:"device_id_pool,omitempty"`
	ElasticPool    ElasticPoolConfig  `json:"elastic_pool,omitempty"`
	Mihomo         MihomoConfig       `json:"mihomo,omitempty"`
	Vercel         VercelConfig       `json:"vercel,omitempty"`
	VercelSyncHash string             `json:"_vercel_sync_hash,omitempty"`
	VercelSyncTime int64              `json:"_vercel_sync_time,omitempty"`
	// ToolMarkerSecret 是派生按 Key 随机工具调用标识的服务端密钥。
	// 首次加载时自动生成并持久化；可通过 DS2API_TOOL_MARKER_SECRET 覆盖。
	ToolMarkerSecret string         `json:"tool_marker_secret,omitempty"`
	AdditionalFields map[string]any `json:"-"`
	// Settings holds the values of the settings declared in settings.go, keyed
	// by section and then by field. They are written to the config file at the
	// top level, in the same `{"<key>": {"<field>": <value>}}` shape a
	// hand-written section uses, but the codec handles them generically instead
	// of one case per key. A field is present here only when the config file or
	// an admin update actually set it, which is what lets a reader tell an
	// explicit zero from an unset field.
	Settings map[string]map[string]any `json:"-"`
}

type Account struct {
	Name         string `json:"name,omitempty"`
	Remark       string `json:"remark,omitempty"`
	Email        string `json:"email,omitempty"`
	Mobile       string `json:"mobile,omitempty"`
	Password     string `json:"password,omitempty"`
	Token        string `json:"token,omitempty"`
	DeviceID     string `json:"device_id,omitempty"`
	DeviceIDType string `json:"device_id_type,omitempty"`
	ProxyID      string `json:"proxy_id,omitempty"`
	PoolType     string `json:"pool_type,omitempty"`
	// Priority 是账号在弹性号池选取时的优先级，数值越大越优先被启用。
	// 0 为默认优先级；负数表示低于默认，仅在更高优先级账号不足时才会被启用。
	// 同优先级的账号按配置中的原始顺序选取。
	Priority       int     `json:"priority,omitempty"`
	Locale         string  `json:"locale,omitempty"`
	Disabled       bool    `json:"disabled,omitempty"`
	DisabledReason string  `json:"disabled_reason,omitempty"`
	Banned         bool    `json:"banned,omitempty"`
	MutedUntil     float64 `json:"muted_until,omitempty"`
	// AuthFailed 表示账号被判定为"鉴权异常"：上游连续以 Token 失效拒绝请求，
	// 且重新登录也无法拿到可用 Token（例如密码被改、账号被上游注销）。
	// 与 Banned 相同，弹性号池把它视为不可调度且不占名额——被判定后立即让出
	// 名额，由后面的休眠账号自动补位。成功刷新 Token 或管理员手动启用后清除。
	AuthFailed bool `json:"auth_failed,omitempty"`
	// CooldownUntil 是本地风控冷却到期时间（Unix 秒）。
	// 与 MutedUntil 不同：MutedUntil 由上游下发，CooldownUntil 是我们自己
	// 在收到验证码挑战后主动设置的——挑战意味着风控已经盯上这个账号，
	// 继续用它只会把情况变得更糟。
	CooldownUntil float64 `json:"cooldown_until,omitempty"`
	// NodeCooldownUntil 是坏节点自动安全转移后账号的换号冷却到期时间（Unix 秒）。
	// 冷却期内自动调度不会再次切换该账号的节点，避免故障节点间反复横跳。
	NodeCooldownUntil float64 `json:"node_cooldown_until,omitempty"`
	// NoProxy 表示账号被显式解绑、强制走直连（不走代理）。置位后自动调度
	// （auto_bind 补位 / 一键分配）不再给该账号分配节点，直到用户显式重新绑定。
	NoProxy bool `json:"no_proxy,omitempty"`
	// ManualBinding 表示账号的节点绑定是用户手动指定的（账号管理里选定代理桥
	// 节点，或直接指定某个代理）。置位后该账号完全退出自动调度：既不故障转移、
	// 也不补位，休眠（停用）期间保留绑定。绑定的节点失效时请求直接失败，
	// 不会自动换号。用户显式解绑、桥关闭或绑定节点随订阅消失时清除。
	ManualBinding bool `json:"manual_binding,omitempty"`
}

// APIKey is one managed API key. Its identity and free-text metadata are typed
// fields; its boolean switches are credential flags, declared once in
// credential_flags.go. Read a flag with Flag and write it with SetFlag, so a new
// switch needs no field here and no codec, admin or web UI change.
type APIKey struct {
	Key    string `json:"key"`
	Name   string `json:"name,omitempty"`
	Remark string `json:"remark,omitempty"`
	// flags holds only the registered flags whose value differs from their
	// declared default. A flag at its default is absent, which is what keeps a
	// key that never touched a switch as compact in the config file as it was
	// before the flag existed.
	flags map[string]bool
}

// Flag reports the value of a registered credential flag. An unset flag reads
// as its declared default, and an unregistered name reads as false rather than
// panicking: a stale caller after a flag is retired should lose the value, not
// take the process down.
func (k APIKey) Flag(name string) bool {
	spec, ok := LookupCredentialFlag(name)
	if !ok {
		return false
	}
	if v, ok := k.flags[name]; ok {
		return v
	}
	return spec.Default
}

// SetFlag sets a registered credential flag. Setting a flag to its declared
// default removes it, so the stored form stays minimal. An unregistered name is
// ignored, matching Flag.
func (k *APIKey) SetFlag(name string, value bool) {
	spec, ok := LookupCredentialFlag(name)
	if !ok {
		return
	}
	if value == spec.Default {
		delete(k.flags, name)
		return
	}
	if k.flags == nil {
		k.flags = map[string]bool{}
	}
	k.flags[name] = value
}

// MarshalJSON writes the key with its flags inlined as top-level fields, which
// is the shape the hand-written field used: {"key":…,"name":…,
// "force_disable_tools":true}. Only non-default flags are stored, so only those
// are written.
func (k APIKey) MarshalJSON() ([]byte, error) {
	out := map[string]any{"key": k.Key}
	if k.Name != "" {
		out["name"] = k.Name
	}
	if k.Remark != "" {
		out["remark"] = k.Remark
	}
	for _, spec := range CredentialFlagSpecs() {
		if v, ok := k.flags[spec.Name]; ok && v != spec.Default {
			out[spec.Name] = v
		}
	}
	return json.Marshal(out)
}

// UnmarshalJSON decodes a key object. A field the registry does not declare and
// that is not one of the typed fields is dropped, which is what decoding into a
// struct already did, and reported so it is not lost silently. A declared flag
// whose value does not match its kind is an error rather than a silently ignored
// switch: the key would otherwise load with the switch off and the user would
// have no way to notice.
func (k *APIKey) UnmarshalJSON(b []byte) error {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*k = APIKey{}
	for name, value := range raw {
		field, ok := lookupAPIKeyField(name)
		if !ok {
			warnDroppedConfigField("api_keys", name)
			continue
		}
		switch field {
		case "key":
			if err := json.Unmarshal(value, &k.Key); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		case "name":
			if err := json.Unmarshal(value, &k.Name); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		case "remark":
			if err := json.Unmarshal(value, &k.Remark); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		default:
			var flag bool
			if err := json.Unmarshal(value, &flag); err != nil {
				return fmt.Errorf("credential flag %q: %w", name, err)
			}
			k.SetFlag(field, flag)
		}
	}
	return nil
}

// apiKeyTypedFields are the typed fields of a key object. A credential flag is
// the only other field a key object may carry.
var apiKeyTypedFields = []string{"key", "name", "remark"}

// lookupAPIKeyField resolves a key-object field name to its canonical spelling,
// exactly first and case-insensitively after that.
//
// The fallback matches the codec's field lookup and the struct decode it
// replaced: a key written as `{"Key":"k1"}` used to load, and dropping it now
// would erase the credential from the file on the next save, silently turning a
// managed key into an unknown one.
func lookupAPIKeyField(name string) (string, bool) {
	for _, candidate := range apiKeyTypedFields {
		if candidate == name {
			return candidate, true
		}
	}
	if _, ok := LookupCredentialFlag(name); ok {
		return name, true
	}
	for _, candidate := range apiKeyTypedFields {
		if strings.EqualFold(candidate, name) {
			return candidate, true
		}
	}
	for _, candidate := range CredentialFlagNames() {
		if strings.EqualFold(candidate, name) {
			return candidate, true
		}
	}
	return "", false
}

type Proxy struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Type     string `json:"type,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

func NormalizeProxy(p Proxy) Proxy {
	p.ID = strings.TrimSpace(p.ID)
	p.Name = strings.TrimSpace(p.Name)
	p.Type = strings.ToLower(strings.TrimSpace(p.Type))
	p.Host = strings.TrimSpace(p.Host)
	p.Username = strings.TrimSpace(p.Username)
	p.Password = strings.TrimSpace(p.Password)
	if p.ID == "" {
		p.ID = StableProxyID(p)
	}
	if p.Name == "" && p.Host != "" && p.Port > 0 {
		p.Name = fmt.Sprintf("%s:%d", p.Host, p.Port)
	}
	return p
}

func StableProxyID(p Proxy) string {
	sum := sha1.Sum([]byte(strings.ToLower(strings.TrimSpace(p.Type)) + "|" + strings.ToLower(strings.TrimSpace(p.Host)) + "|" + fmt.Sprintf("%d", p.Port) + "|" + strings.TrimSpace(p.Username)))
	return "proxy_" + hex.EncodeToString(sum[:6])
}

func (c *Config) ClearAccountTokens() {
	if c == nil {
		return
	}
	for i := range c.Accounts {
		c.Accounts[i].Token = ""
	}
}

func (c *Config) NormalizeCredentials() {
	if c == nil {
		return
	}
	normalizedAPIKeys := normalizeAPIKeys(c.APIKeys)
	if len(normalizedAPIKeys) > 0 {
		c.APIKeys = normalizedAPIKeys
		c.Keys = apiKeysToStrings(c.APIKeys)
	} else {
		c.Keys = normalizeKeys(c.Keys)
		c.APIKeys = apiKeysFromStrings(c.Keys, nil)
	}

	for i := range c.Accounts {
		c.Accounts[i].Name = strings.TrimSpace(c.Accounts[i].Name)
		c.Accounts[i].Remark = strings.TrimSpace(c.Accounts[i].Remark)
		c.Accounts[i].DeviceID = strings.TrimSpace(c.Accounts[i].DeviceID)
		c.Accounts[i].DeviceIDType = NormalizeDeviceIDType(c.Accounts[i].DeviceIDType)
		c.Accounts[i].Locale = strings.TrimSpace(c.Accounts[i].Locale)
		c.Accounts[i].PoolType = NormalizePoolType(c.Accounts[i].PoolType)
	}

	// 注册段（sections.go）各自的归一化：trim、枚举迁移、去重等都由段的声明
	// 负责，这里只负责顺序。设备 ID 号池的归一化必须在绑定重算之前。
	normalizeRegisteredSections(c)
	// 号池的已绑定账号数是派生数据：每次配置变更后按账号实际绑定情况重算，
	// 顺带解除不可用账号（已关闭/休眠/禁言/封禁）的绑定。
	ReconcileDeviceIDBindings(c)
}

// DropInvalidAccounts removes accounts that cannot be addressed by admin APIs
// (no email and no normalizable mobile). This prevents legacy token-only
// records from becoming orphaned empty entries after token stripping.
func (c *Config) DropInvalidAccounts() {
	if c == nil || len(c.Accounts) == 0 {
		return
	}
	kept := make([]Account, 0, len(c.Accounts))
	for _, acc := range c.Accounts {
		if acc.Identifier() == "" {
			continue
		}
		kept = append(kept, acc)
	}
	c.Accounts = kept
}

func (c *Config) normalizeModelAliases() {
	if c == nil {
		return
	}

	aliases := map[string]string{}
	for k, v := range c.ModelAliases {
		key := strings.TrimSpace(lower(k))
		val := strings.TrimSpace(lower(v))
		if key == "" || val == "" {
			continue
		}
		aliases[key] = val
	}
	if len(aliases) == 0 {
		c.ModelAliases = nil
	} else {
		c.ModelAliases = aliases
	}
}
