package config

import "encoding/json"

// AutoDeleteConfig 是 auto_delete 段：请求结束后如何清理远端会话。
// mode 是枚举，历史配置里的 sessions 布尔字段仍被读取（AutoDeleteMode 会把它
// 折成 all），但设置页只编辑 mode：接口读只返回 mode，写只接受 mode，仍然携带
// sessions 的写入会被 Form.RemovedFields 拒绝（见下）。
type AutoDeleteConfig struct {
	Mode     string `json:"mode,omitempty"`
	Sessions bool   `json:"sessions,omitempty"`
}

func init() {
	RegisterSection(SectionSpec{
		Key: "auto_delete",
		Form: &SectionForm{
			// 卡片顺序与其它设置卡片共用一个序列（见 settings.go 的
			// SettingCardSpec.Order）：自动删除排在行为设置、独立拆分、专家分段之后。
			Order:          6,
			TitleKey:       "settings.autoDeleteTitle",
			DescriptionKey: "settings.autoDeleteDesc",
			// sessions 是 mode 之前的布尔字段：配置文件里的历史值仍由 Decode 读入并
			// 被 AutoDeleteMode 折成 all，但设置接口只认 mode。这里显式声明它已被
			// 移除，让仍然发 sessions 的写入得到 400（错误信息指向 mode），而不是
			// 返回 200 却什么都没改。
			RemovedFields: map[string]string{"sessions": "mode"},
			Fields: []SectionField{
				SectionEnum("auto_delete.mode", "settings.autoDeleteMode",
					[]SectionOption{
						{Value: "none", LabelKey: "settings.autoDeleteNone",
							DescriptionKey: "settings.autoDeleteNoneDesc"},
						{Value: "single", LabelKey: "settings.autoDeleteSingle",
							DescriptionKey: "settings.autoDeleteSingleDesc",
							WarningKey:     "settings.autoDeleteWarning"},
						{Value: "all", LabelKey: "settings.autoDeleteAll",
							DescriptionKey: "settings.autoDeleteAllDesc",
							WarningKey:     "settings.autoDeleteWarning"},
					},
					func(s *Store) string { return s.AutoDeleteMode() },
					func(c *Config, v string) error {
						if v == "" {
							v = "none"
						}
						c.AutoDelete.Mode = v
						return nil
					}),
			},
		},
		Decode: func(c *Config, raw json.RawMessage) error {
			return json.Unmarshal(raw, &c.AutoDelete)
		},
		Encode: func(c Config) (any, bool) {
			return c.AutoDelete, true
		},
		Clone: func(dst, src *Config) {
			dst.AutoDelete = src.AutoDelete
		},
		Validate: func(c Config) error {
			return ValidateAutoDeleteConfig(c.AutoDelete)
		},
		// 导入合并有意留空（no-op）：merge 模式绝不打开「删除远端会话」这种破坏性
		// 行为，只有 replace（整体替换）才会写入 auto_delete。声明一个显式的空
		// Merge 就是让 MergeRegisteredSections 跳过该段的逐字段合并——没有这个
		// 钩子时，注册表会按 Encode 的结果逐字段套用，于是一次 merge 导入就会把
		// 备份里的 mode 静默写进当前配置（API.md 的导入说明承诺的正是相反的行为）。
		Merge: func(next, incoming *Config) error {
			return nil
		},
	})
}
