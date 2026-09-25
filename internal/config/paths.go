package config

import (
	"os"
	"path/filepath"
	"strings"
)

func BaseDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func IsVercel() bool {
	return strings.TrimSpace(os.Getenv("VERCEL")) != "" || strings.TrimSpace(os.Getenv("NOW_REGION")) != ""
}

func ResolvePath(envKey, defaultRel string) string {
	raw := strings.TrimSpace(os.Getenv(envKey))
	if raw != "" {
		if filepath.IsAbs(raw) {
			return raw
		}
		return filepath.Join(BaseDir(), raw)
	}
	return filepath.Join(BaseDir(), defaultRel)
}

func ConfigPath() string {
	if strings.TrimSpace(os.Getenv("DS2API_CONFIG_PATH")) == "" && BaseDir() == "/app" {
		return containerDefaultConfigPath()
	}
	return ResolvePath("DS2API_CONFIG_PATH", "config.json")
}

func containerDefaultConfigPath() string {
	// Container images run as non-root by default. Only use /data when mounted/provisioned.
	// Otherwise keep /app/config.json so admin-side save does not fail on MkdirAll("/data").
	if st, err := os.Stat("/data"); err == nil && st.IsDir() {
		return "/data/config.json"
	}
	return "/app/config.json"
}

func legacyContainerConfigPath() string {
	return "/app/config.json"
}

func shouldTryLegacyContainerConfigPath() bool {
	return strings.TrimSpace(os.Getenv("DS2API_CONFIG_PATH")) == "" && BaseDir() == "/app"
}

func RawStreamSampleRoot() string {
	return ResolvePath("DS2API_RAW_STREAM_SAMPLE_ROOT", "tests/raw_stream_samples")
}

func ChatHistoryPath() string {
	// On Vercel, /var/task is read-only at runtime. If no explicit path is set,
	// default to /tmp/chat_history.json (the only writable directory).
	if IsVercel() && strings.TrimSpace(os.Getenv("DS2API_CHAT_HISTORY_PATH")) == "" {
		return "/tmp/chat_history.json"
	}
	return ResolvePath("DS2API_CHAT_HISTORY_PATH", "data/chat_history.json")
}

// UsageStatsPath 返回 Token 用量账本路径。账本与聊天历史刻意分离，避免历史
// 保留上限影响用量核算。Vercel 上 /var/task 运行时只读，因此与聊天历史一致
// 回退到 /tmp。
func UsageStatsPath() string {
	if IsVercel() && strings.TrimSpace(os.Getenv("DS2API_USAGE_PATH")) == "" {
		return "/tmp/usage.json"
	}
	return ResolvePath("DS2API_USAGE_PATH", "data/usage.json")
}

// DevidDir 返回数美 fp SDK 资产目录（fp-1.min.js / browser_env.js / config.json）。
func DevidDir() string {
	return ResolvePath("DS2API_DEVID_DIR", "data/devid")
}

// DevidPublicKeyPath 返回数美公钥文件路径（data/devid/public_key，纯文本）。
func DevidPublicKeyPath() string {
	return DevidDir() + string(os.PathSeparator) + "public_key"
}

// DevidLegacyConfigPath 返回旧版 JSON 公钥文件路径（data/devid/config.json），
// 用于兼容早期版本部署的用户。
func DevidLegacyConfigPath() string {
	return DevidDir() + string(os.PathSeparator) + "config.json"
}

// DeviceProfileDir 返回按账号持久化设备档案的目录。
func DeviceProfileDir() string {
	return ResolvePath("DS2API_DEVICE_PROFILE_DIR", "data/device_profiles")
}

// DeviceProfilePath 返回账号设备档案的完整路径。
func DeviceProfilePath(identifier string) string {
	return DeviceProfileDir() + string(os.PathSeparator) + SanitizeFilename(identifier) + ".json"
}

// SanitizeFilename 把账号标识转成安全的文件名片段。
func SanitizeFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "account"
	}
	return b.String()
}

func StaticAdminDir() string {
	return ResolvePath("DS2API_STATIC_ADMIN_DIR", "static/admin")
}
