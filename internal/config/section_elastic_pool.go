package config

import "encoding/json"

// ElasticPoolConfig 控制弹性号池行为。
// 开启后按账号优先级从高到低在可调度账号中启用前 N 个，
// 其余禁用；封号(muted/banned)账号一律禁用且不占名额。
// PerPool=false 时所有账号共用 GlobalCount；PerPool=true 时
// default/no_tools/tools_only 三种号池类型分别使用各自的 Count。
//
// 它由账号管理页的独立接口（/admin/accounts/elastic-pool）编辑，所以只注册
// codec 与生命周期钩子，不声明设置页表单。
type ElasticPoolConfig struct {
	Enabled        bool `json:"enabled,omitempty"`
	PerPool        bool `json:"per_pool,omitempty"`
	GlobalCount    int  `json:"global_count,omitempty"`
	DefaultCount   int  `json:"default_count,omitempty"`
	NoToolsCount   int  `json:"no_tools_count,omitempty"`
	ToolsOnlyCount int  `json:"tools_only_count,omitempty"`
}

func init() {
	RegisterSection(SectionSpec{
		Key: "elastic_pool",
		Decode: func(c *Config, raw json.RawMessage) error {
			return json.Unmarshal(raw, &c.ElasticPool)
		},
		Encode: func(c Config) (any, bool) {
			if c.ElasticPool == (ElasticPoolConfig{}) {
				return nil, false
			}
			return c.ElasticPool, true
		},
		Clone: func(dst, src *Config) {
			dst.ElasticPool = src.ElasticPool
		},
	})
}
