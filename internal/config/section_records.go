package config

import (
	"encoding/json"
	"slices"
)

// 记录型配置段：值是「一条条记录」的顶层键。它们各有自己的 admin 接口与界面
// （Key 管理、账号管理、代理管理、模型别名编辑），因此只注册 codec 与生命周期
// 钩子；导入合并也留在各自的处理里，因为「同名记录怎么合并」是记录自己的策略
// （按 Key 合并保留更完整的定义、账号按标识去重），不是通用的字段合并。
//
// 这里的每个条目都是 4 行，这也是这个类别应有的成本：新增一个记录型段，
// 除了本文件的声明，只需要它自己的接口与界面。
func init() {
	RegisterSection(SectionSpec{
		Key: "keys",
		Decode: func(c *Config, raw json.RawMessage) error {
			return json.Unmarshal(raw, &c.Keys)
		},
		Encode: func(c Config) (any, bool) {
			if len(c.Keys) == 0 {
				return nil, false
			}
			return c.Keys, true
		},
		Clone: func(dst, src *Config) {
			dst.Keys = slices.Clone(src.Keys)
		},
	})
	RegisterSection(SectionSpec{
		Key: "api_keys",
		Decode: func(c *Config, raw json.RawMessage) error {
			return json.Unmarshal(raw, &c.APIKeys)
		},
		Encode: func(c Config) (any, bool) {
			if len(c.APIKeys) == 0 {
				return nil, false
			}
			return c.APIKeys, true
		},
		Clone: func(dst, src *Config) {
			dst.APIKeys = cloneAPIKeys(src.APIKeys)
		},
	})
	RegisterSection(SectionSpec{
		Key: "accounts",
		Decode: func(c *Config, raw json.RawMessage) error {
			return json.Unmarshal(raw, &c.Accounts)
		},
		Encode: func(c Config) (any, bool) {
			if len(c.Accounts) == 0 {
				return nil, false
			}
			return c.Accounts, true
		},
		Clone: func(dst, src *Config) {
			dst.Accounts = slices.Clone(src.Accounts)
		},
	})
	RegisterSection(SectionSpec{
		Key: "proxies",
		Decode: func(c *Config, raw json.RawMessage) error {
			return json.Unmarshal(raw, &c.Proxies)
		},
		Encode: func(c Config) (any, bool) {
			if len(c.Proxies) == 0 {
				return nil, false
			}
			return c.Proxies, true
		},
		Clone: func(dst, src *Config) {
			dst.Proxies = slices.Clone(src.Proxies)
		},
		Validate: func(c Config) error {
			return ValidateProxyConfig(c.Proxies)
		},
	})
	RegisterSection(SectionSpec{
		Key: "model_aliases",
		Decode: func(c *Config, raw json.RawMessage) error {
			return json.Unmarshal(raw, &c.ModelAliases)
		},
		Encode: func(c Config) (any, bool) {
			if len(c.ModelAliases) == 0 {
				return nil, false
			}
			return c.ModelAliases, true
		},
		Clone: func(dst, src *Config) {
			dst.ModelAliases = cloneStringMap(src.ModelAliases)
		},
		Normalize: func(c *Config) {
			c.normalizeModelAliases()
		},
		// 别名是 map：导入时逐条并入，导入未提到的别名保持原样。
		Merge: func(next, incoming *Config) error {
			if len(incoming.ModelAliases) == 0 {
				return nil
			}
			if next.ModelAliases == nil {
				next.ModelAliases = map[string]string{}
			}
			for k, v := range incoming.ModelAliases {
				next.ModelAliases[k] = v
			}
			return nil
		},
	})
	RegisterSection(SectionSpec{
		Key: "device_id_pool",
		Decode: func(c *Config, raw json.RawMessage) error {
			return json.Unmarshal(raw, &c.DeviceIDPool)
		},
		Encode: func(c Config) (any, bool) {
			if len(c.DeviceIDPool.Items) == 0 {
				return nil, false
			}
			return c.DeviceIDPool, true
		},
		Clone: func(dst, src *Config) {
			dst.DeviceIDPool = DeviceIDPoolConfig{Items: slices.Clone(src.DeviceIDPool.Items)}
		},
		Normalize: func(c *Config) {
			c.DeviceIDPool.Items = NormalizeDeviceIDPool(c.DeviceIDPool.Items)
		},
	})
	RegisterSection(SectionSpec{
		Key: "mihomo",
		Decode: func(c *Config, raw json.RawMessage) error {
			return json.Unmarshal(raw, &c.Mihomo)
		},
		Encode: func(c Config) (any, bool) {
			if IsZeroMihomoConfig(c.Mihomo) {
				return nil, false
			}
			return c.Mihomo, true
		},
		Clone: func(dst, src *Config) {
			dst.Mihomo = cloneMihomoConfig(src.Mihomo)
		},
		Normalize: func(c *Config) {
			c.Mihomo = NormalizeMihomoConfig(c.Mihomo)
		},
		Validate: func(c Config) error {
			return ValidateMihomoConfig(c.Mihomo)
		},
	})
}
