package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"ds2api/internal/config"
	trans "ds2api/internal/deepseek/transport"
	"ds2api/internal/devid"
)

// realFailCooldown 是 real 生成失败后的冷却期：期内不再尝试数美，
// 已有 device_id 的账号保持原值，避免端点故障期间每次登录都轮换设备指纹。
const realFailCooldown = 5 * time.Minute

// ensureAccountDeviceID 返回账号应使用的 device_id 及其类型标注。
//
// manual 模式（默认）：从手工号池分配，优先选择已绑定账号数最少的 device_id；
// 账号在启用期间一直复用同一个 id，被关闭/休眠/禁言/封禁时解绑，
// 下次启用后重新分配。
// real 模式：经数美 fp SDK 生成真实 ID。
func (c *Client) ensureAccountDeviceID(ctx context.Context, acc config.Account) (string, string, error) {
	deviceID := strings.TrimSpace(acc.DeviceID)
	deviceType := config.NormalizeDeviceIDType(acc.DeviceIDType)
	if c.deviceIDMode() == config.DeviceIDTypeReal {
		return c.ensureRealDeviceID(ctx, acc, deviceID, deviceType)
	}
	return c.ensurePoolDeviceID(acc, deviceID)
}

// deviceIDMode 返回当前生效的设备 ID 模式（manual/real，默认 manual）。
func (c *Client) deviceIDMode() string {
	if c != nil && c.Store != nil {
		return c.Store.RuntimeDeviceIDMode()
	}
	return config.DeviceIDTypeManual
}

// ensurePoolDeviceID 在 manual 模式下返回账号应使用的号池 device_id。
// 账号已绑定且该 id 仍在号池中时直接复用（账号启用期间不换 id）。
func (c *Client) ensurePoolDeviceID(acc config.Account, deviceID string) (string, string, error) {
	if deviceID != "" && c.poolHasDeviceID(deviceID) {
		return deviceID, config.DeviceIDTypeManual, nil
	}
	return c.assignDeviceIDFromPool(acc)
}

func (c *Client) poolHasDeviceID(deviceID string) bool {
	if c == nil || c.Store == nil || strings.TrimSpace(deviceID) == "" {
		return false
	}
	_, ok := c.Store.FindDeviceIDPoolItem(deviceID)
	return ok
}

// assignDeviceIDFromPool 从号池分配绑定账号数最少的 device_id 并持久化到账号。
// 同一账号并发登录时通过 per-account 锁串行化，等锁期间若他方已分配则直接复用。
func (c *Client) assignDeviceIDFromPool(acc config.Account) (string, string, error) {
	if c == nil || c.Store == nil {
		return "", "", config.ErrDeviceIDPoolEmpty
	}
	identifier := acc.Identifier()
	if identifier != "" {
		unlock := c.lockDeviceGen(identifier)
		defer unlock()

		if fresh, ok := c.Store.FindAccount(identifier); ok {
			if id := strings.TrimSpace(fresh.DeviceID); id != "" && c.poolHasDeviceID(id) {
				return id, config.DeviceIDTypeManual, nil
			}
		}
	}
	var assigned string
	err := c.Store.Update(func(cfg *config.Config) error {
		id, ok := cfg.LeastBoundDeviceID()
		if !ok {
			return config.ErrDeviceIDPoolEmpty
		}
		assigned = id
		cfg.BindDeviceIDToAccount(identifier, id)
		return nil
	})
	if err != nil {
		if errors.Is(err, config.ErrDeviceIDPoolEmpty) {
			return "", "", config.ErrDeviceIDPoolEmpty
		}
		return "", "", err
	}
	return assigned, config.DeviceIDTypeManual, nil
}

// ensureRealDeviceID 处理 real 模式：数美可用时生成/复用真实 device_id，
// 数美不可用或生成失败时保留账号已有的 device_id（不轮换指纹），
// 完全没有可用 device_id 时才报错。
func (c *Client) ensureRealDeviceID(ctx context.Context, acc config.Account, deviceID string, deviceType string) (string, string, error) {
	keepExisting := func() (string, string, error) {
		if deviceType == "" {
			deviceType = config.DeviceIDTypeManual
		}
		return deviceID, deviceType, nil
	}
	if _, err := c.devidConfig(); err != nil {
		// 数美接入参数缺失时 real 不可用。已有 device_id 的账号（含 type=real）
		// 一律保留原值：覆盖会让真实设备指纹发生变化。
		config.Logger.Warn("[device_id] mode=real but devid config unavailable",
			"account", acc.Identifier(), "error", err)
		if deviceID != "" {
			return keepExisting()
		}
		return "", "", fmt.Errorf("device_id 生成失败：real 模式不可用（%v）", err)
	}
	if deviceID != "" && deviceType == config.DeviceIDTypeReal {
		return deviceID, config.DeviceIDTypeReal, nil
	}
	if deviceID != "" && c.realGenCooldownActive() {
		// 冷却期内不重试数美：账号已有 device_id 时保持原值，
		// 避免端点故障期间轮换设备指纹。
		return keepExisting()
	}
	identifier := acc.Identifier()
	generated, err := c.generateRealDeviceIDGuarded(ctx, acc, identifier)
	if err != nil {
		if deviceID != "" {
			config.Logger.Warn("[device_id] real generation failed, keeping existing id",
				"account", identifier, "error", err)
			return keepExisting()
		}
		return "", "", fmt.Errorf("device_id 生成失败：%v", err)
	}
	if err := c.persistAccountDeviceID(identifier, generated, config.DeviceIDTypeReal); err != nil {
		return "", "", err
	}
	return generated, config.DeviceIDTypeReal, nil
}

// generateRealDeviceIDGuarded 在 per-account 锁内生成真实 device_id。
// 等锁期间若他方已生成同模式 ID 则直接复用。
func (c *Client) generateRealDeviceIDGuarded(ctx context.Context, acc config.Account, identifier string) (string, error) {
	if identifier != "" {
		unlock := c.lockDeviceGen(identifier)
		defer unlock()
		if fresh, ok := c.Store.FindAccount(identifier); ok {
			freshID := strings.TrimSpace(fresh.DeviceID)
			if freshID != "" && config.NormalizeDeviceIDType(fresh.DeviceIDType) == config.DeviceIDTypeReal {
				return freshID, nil
			}
		}
	}
	deviceID, err := c.generateRealDeviceID(ctx, acc, identifier)
	if err != nil {
		c.markRealGenFailed()
		return "", err
	}
	return deviceID, nil
}

// generateRealDeviceID 运行数美 fp SDK 生成真实 device_id。
// 设备档案按账号持久化，保证重试/重生成时设备特征稳定。
func (c *Client) generateRealDeviceID(ctx context.Context, acc config.Account, identifier string) (string, error) {
	cfg, err := c.devidConfig()
	if err != nil {
		return "", err
	}
	gen := &devid.Generator{
		Config:   cfg,
		AssetDir: config.DevidDir(),
		HTTP:     c.devidHTTPClient(acc),
	}
	if identifier == "" {
		identifier = "account"
	}
	profilePath := config.DeviceProfilePath(identifier)
	genCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	deviceID, err := gen.Generate(genCtx, profilePath)
	if client, ok := gen.HTTP.(interface{ CloseIdleConnections() }); ok {
		client.CloseIdleConnections()
	}
	if err != nil {
		return "", err
	}
	return deviceID, nil
}

// persistAccountDeviceID 把 device_id 与类型标注写回配置。
func (c *Client) persistAccountDeviceID(identifier, deviceID, deviceType string) error {
	if c == nil || c.Store == nil || strings.TrimSpace(identifier) == "" {
		return nil
	}
	return c.Store.Update(func(cfg *config.Config) error {
		for i := range cfg.Accounts {
			if cfg.Accounts[i].Identifier() != identifier {
				continue
			}
			cfg.Accounts[i].DeviceID = deviceID
			cfg.Accounts[i].DeviceIDType = deviceType
			return nil
		}
		return nil
	})
}

// InvalidateDeviceID 从号池移除已被上游风控识别（RISK_DEVICE_DETECTED）的
// device_id，并把原先绑定该 id 的账号立即换绑到绑定数最少的剩余 id。
// 返回号池中是否仍有可用 device_id。
func (c *Client) InvalidateDeviceID(deviceID string) bool {
	if c == nil || c.Store == nil || strings.TrimSpace(deviceID) == "" {
		return false
	}
	remaining := 0
	if err := c.Store.Update(func(cfg *config.Config) error {
		remaining = config.RemoveDeviceIDAndRebind(cfg, deviceID)
		return nil
	}); err != nil {
		// 持久化失败时保守处理：不因为写盘问题而误报"全部失效"。
		config.Logger.Error("[device_id] invalidate failed", "device_id", deviceID, "error", err)
		return true
	}
	config.Logger.Warn("[device_id] removed after RISK_DEVICE_DETECTED",
		"device_id", deviceID, "remaining", remaining)
	return remaining > 0
}

// devidConfig 解析数美公钥，优先级：
//  1. config.json 的 runtime.devid_public_key；
//  2. data/devid/public_key（纯文本或 JSON 内容）；
//  3. data/devid/config.json（旧版 JSON 内容，兼容早期部署）。
//
// 均缺失则报错，real 模式下的调用方会保留账号已有 device_id。
func (c *Client) devidConfig() (devid.Config, error) {
	cfg := devid.Config{}
	if c != nil && c.Store != nil {
		cfg.PublicKey = c.Store.RuntimeDevidPublicKey()
	}
	if cfg.PublicKey == "" {
		if key, err := devid.LoadPublicKeyFile(config.DevidPublicKeyPath()); err == nil {
			cfg.PublicKey = key
		}
	}
	if cfg.PublicKey == "" {
		if key, err := devid.LoadPublicKeyFile(config.DevidLegacyConfigPath()); err == nil {
			cfg.PublicKey = key
		}
	}
	if err := cfg.Validate(); err != nil {
		return devid.Config{}, err
	}
	return cfg.Normalize(), nil
}

// devidHTTPClient 返回发送数美指纹上报的 HTTP 客户端。
// 账号绑定了代理时跟随账号代理，保证指纹出口 IP 与登录 IP 一致。
func (c *Client) devidHTTPClient(acc config.Account) devid.Doer {
	if c != nil {
		if proxyCfg, ok := c.resolveProxyForAccount(acc); ok {
			return trans.NewFallbackWithProxy(30*time.Second, httpCloakProxyURL(proxyCfg))
		}
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) lockDeviceGen(identifier string) func() {
	c.deviceGenMu.Lock()
	if c.deviceGenLocks == nil {
		c.deviceGenLocks = map[string]*sync.Mutex{}
	}
	mu, ok := c.deviceGenLocks[identifier]
	if !ok {
		mu = &sync.Mutex{}
		c.deviceGenLocks[identifier] = mu
	}
	c.deviceGenMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// ForgetDeviceGenLock 删除账号的 device_id 生成锁。
// 账号被删除后调用，防止 deviceGenLocks 随账号增删无限增长。
func (c *Client) ForgetDeviceGenLock(identifier string) {
	if c == nil || identifier == "" {
		return
	}
	c.deviceGenMu.Lock()
	delete(c.deviceGenLocks, identifier)
	c.deviceGenMu.Unlock()
}

func (c *Client) realGenCooldownActive() bool {
	if c == nil {
		return false
	}
	return time.Now().UnixNano() < c.realFailUntil.Load()
}

func (c *Client) markRealGenFailed() {
	if c == nil {
		return
	}
	c.realFailUntil.Store(time.Now().Add(realFailCooldown).UnixNano())
}
