package config

import (
	"encoding/json"
	"strings"
)

// AdminConfig 是 admin 段：管理员口令哈希与 JWT 有效期。
// 口令本身不走设置接口（有独立的 /admin/settings/password），所以这里的表单
// 只声明 jwt_expire_hours；口令哈希与 jwt_valid_after_unix 作为只读元数据随
// 设置接口下发（见 Extra）。
type AdminConfig struct {
	PasswordHash      string `json:"password_hash,omitempty"`
	JWTExpireHours    int    `json:"jwt_expire_hours,omitempty"`
	JWTValidAfterUnix int64  `json:"jwt_valid_after_unix,omitempty"`
}

func init() {
	RegisterSection(SectionSpec{
		Key: "admin",
		Form: &SectionForm{
			Order:    1,
			TitleKey: "settings.securityTitle",
			// 安全卡片由 SecuritySection 自己渲染（口令修改是它独有的交互），
			// 但 jwt_expire_hours 仍走注册表，所以它的读写不需要额外接线。
			Bespoke: true,
			Fields: []SectionField{
				SectionInt("admin.jwt_expire_hours", "settings.jwtExpireHours", 1, 720,
					func(s *Store) int { return s.AdminJWTExpireHours() },
					func(c *Config, v int) { c.Admin.JWTExpireHours = v }),
			},
			Extra: func(s *Store) map[string]any {
				return map[string]any{
					"has_password_hash":    strings.TrimSpace(s.AdminPasswordHash()) != "",
					"jwt_valid_after_unix": s.AdminJWTValidAfterUnix(),
				}
			},
		},
		Decode: func(c *Config, raw json.RawMessage) error {
			return json.Unmarshal(raw, &c.Admin)
		},
		Encode: func(c Config) (any, bool) {
			if strings.TrimSpace(c.Admin.PasswordHash) == "" && c.Admin.JWTExpireHours == 0 && c.Admin.JWTValidAfterUnix == 0 {
				return nil, false
			}
			return c.Admin, true
		},
		Clone: func(dst, src *Config) {
			dst.Admin = src.Admin
		},
		Normalize: func(c *Config) {
			c.Admin.PasswordHash = strings.TrimSpace(c.Admin.PasswordHash)
		},
		Validate: func(c Config) error {
			return ValidateAdminConfig(c.Admin)
		},
		// 导入合并保留整个 admin 段（口令哈希也要能随备份迁移），而不是只合并
		// 设置页上的那一个字段。
		Merge: func(next, incoming *Config) error {
			if strings.TrimSpace(incoming.Admin.PasswordHash) != "" {
				next.Admin.PasswordHash = incoming.Admin.PasswordHash
			}
			if incoming.Admin.JWTExpireHours > 0 {
				next.Admin.JWTExpireHours = incoming.Admin.JWTExpireHours
			}
			if incoming.Admin.JWTValidAfterUnix > 0 {
				next.Admin.JWTValidAfterUnix = incoming.Admin.JWTValidAfterUnix
			}
			return nil
		},
	})
}
