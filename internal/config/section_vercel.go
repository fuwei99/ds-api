package config

import (
	"encoding/json"
	"strings"
)

// VercelConfig 是 vercel 段的部署凭据。它由 Vercel Sync 页的独立接口读写，
// 所以只注册 codec、归一化与导入合并，不声明设置页表单；导出接口另行把它
// 遮蔽成 has_token/token_preview（见 admin/configmgmt）。
type VercelConfig struct {
	Token     string `json:"token,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	TeamID    string `json:"team_id,omitempty"`
}

func NormalizeVercelConfig(v VercelConfig) VercelConfig {
	return VercelConfig{
		Token:     strings.TrimSpace(v.Token),
		ProjectID: strings.TrimSpace(v.ProjectID),
		TeamID:    strings.TrimSpace(v.TeamID),
	}
}

func (c *Config) ClearVercelCredentials() {
	if c == nil {
		return
	}
	c.Vercel = VercelConfig{}
}

func init() {
	RegisterSection(SectionSpec{
		Key: "vercel",
		Decode: func(c *Config, raw json.RawMessage) error {
			return json.Unmarshal(raw, &c.Vercel)
		},
		Encode: func(c Config) (any, bool) {
			v := NormalizeVercelConfig(c.Vercel)
			if v.Token == "" && v.ProjectID == "" && v.TeamID == "" {
				return nil, false
			}
			return v, true
		},
		Clone: func(dst, src *Config) {
			dst.Vercel = src.Vercel
		},
		Normalize: func(c *Config) {
			c.Vercel = NormalizeVercelConfig(c.Vercel)
		},
		// 导入合并：整体覆盖非空的凭据，空值不动当前配置。
		Merge: func(next, incoming *Config) error {
			v := NormalizeVercelConfig(incoming.Vercel)
			if v.Token == "" && v.ProjectID == "" && v.TeamID == "" {
				return nil
			}
			next.Vercel = v
			return nil
		},
	})
}
