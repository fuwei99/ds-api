package config

import (
	"encoding/json"
	"strings"
)

// 服务端内部键与历史遗留键。它们同样是「顶层配置键」，所以走同一张注册表：
// 注册表是编解码的唯一分发点，一个新键要么在这里声明，要么落到
// AdditionalFields 原样透传——没有第三条路。
func init() {
	RegisterSection(SectionSpec{
		Key: "_vercel_sync_hash",
		Decode: func(c *Config, raw json.RawMessage) error {
			return json.Unmarshal(raw, &c.VercelSyncHash)
		},
		Encode: func(c Config) (any, bool) {
			if c.VercelSyncHash == "" {
				return nil, false
			}
			return c.VercelSyncHash, true
		},
		Clone: func(dst, src *Config) {
			dst.VercelSyncHash = src.VercelSyncHash
		},
	})
	RegisterSection(SectionSpec{
		Key: "_vercel_sync_time",
		Decode: func(c *Config, raw json.RawMessage) error {
			return json.Unmarshal(raw, &c.VercelSyncTime)
		},
		Encode: func(c Config) (any, bool) {
			if c.VercelSyncTime == 0 {
				return nil, false
			}
			return c.VercelSyncTime, true
		},
		Clone: func(dst, src *Config) {
			dst.VercelSyncTime = src.VercelSyncTime
		},
	})
	RegisterSection(SectionSpec{
		Key: "tool_marker_secret",
		Decode: func(c *Config, raw json.RawMessage) error {
			return json.Unmarshal(raw, &c.ToolMarkerSecret)
		},
		Encode: func(c Config) (any, bool) {
			if strings.TrimSpace(c.ToolMarkerSecret) == "" {
				return nil, false
			}
			return c.ToolMarkerSecret, true
		},
		Clone: func(dst, src *Config) {
			dst.ToolMarkerSecret = src.ToolMarkerSecret
		},
	})

	// 已移除或已改名的历史键：保留声明是为了让老配置文件不至于报错，同时
	// 让它们占住名字（注册设置不能与段重名）。没有 Encode，所以它们不会再
	// 被写回配置文件。
	registerIgnoredKeys("claude_mapping", "claude_model_mapping", "toolcall", "history_split")
	registerIgnoredKeysWithWarning("compat",
		"config key \"compat\" is deprecated and ignored; remove it from your configuration")
	registerIgnoredKeysWithWarning("current_input_file",
		"config key \"current_input_file\" has been renamed to \"input_file\" and is now ignored; please migrate the section manually")
}

// registerIgnoredKeys 声明只读不写的键：解码时丢弃，写回时不出现。
func registerIgnoredKeys(keys ...string) {
	for _, key := range keys {
		RegisterSection(SectionSpec{
			Key:    key,
			Decode: func(*Config, json.RawMessage) error { return nil },
		})
	}
}

// registerIgnoredKeysWithWarning 额外在加载时提示一次，供用户迁移。
func registerIgnoredKeysWithWarning(key, message string) {
	RegisterSection(SectionSpec{
		Key: key,
		Decode: func(*Config, json.RawMessage) error {
			if Logger != nil {
				Logger.Warn(message)
			}
			return nil
		},
	})
}
