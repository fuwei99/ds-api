package client

import (
	"context"
	"errors"
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
// 触发（重新）生成的条件：
//  1. 账号尚无 device_id；
//  2. 生效模式与账号 device_id_type 不一致（模式切换后滚动覆盖；
//     未标注类型的历史 device_id 视为 random，仅在被切到 real 时覆盖一次）。
//
// real 模式经数美 fp SDK 生成真实 ID，任何失败都回退本地随机 ID，登录不被阻塞。
func (c *Client) ensureAccountDeviceID(ctx context.Context, acc config.Account) (string, string, error) {
	mode := config.DeviceIDTypeRandom
	if c != nil && c.Store != nil {
		mode = c.Store.RuntimeDeviceIDMode()
	}
	if mode == config.DeviceIDTypeReal && config.IsVercel() {
		// serverless 文件系统只读且实例随用随弃：设备档案与公钥无法持久化，
		// 每次冷启动都会换 device_id，指纹频繁变化比纯 random 更危险。
		mode = config.DeviceIDTypeRandom
	}
	deviceID := strings.TrimSpace(acc.DeviceID)
	deviceType := config.NormalizeDeviceIDType(acc.DeviceIDType)
	if deviceType == "" {
		deviceType = config.DeviceIDTypeRandom
	}
	if mode == config.DeviceIDTypeReal {
		// 数美接入参数缺失时 real 不可用。此时已有 device_id 的账号（含
		// type=real）一律保留原值：若降级为 random，会让 type=real 账号
		// 被判定为与模式不一致，下次登录把真实设备指纹覆盖成随机 ID；
		// 仅对尚无 device_id 的账号回退随机生成。
		if _, err := c.devidConfig(); err != nil {
			config.Logger.Warn("[device_id] mode=real but devid config unavailable",
				"account", acc.Identifier(), "error", err)
			if deviceID != "" {
				return deviceID, deviceType, nil
			}
			mode = config.DeviceIDTypeRandom
		}
	}
	if deviceID != "" && deviceType == mode {
		return deviceID, deviceType, nil
	}
	deviceID, deviceType, err := c.generateAccountDeviceID(ctx, acc, mode)
	if err != nil {
		return "", "", err
	}
	if c == nil || c.Store == nil {
		return deviceID, deviceType, nil
	}
	identifier := acc.Identifier()
	if identifier == "" {
		return deviceID, deviceType, nil
	}
	if err := c.Store.Update(func(cfg *config.Config) error {
		for i := range cfg.Accounts {
			if cfg.Accounts[i].Identifier() == identifier {
				cfg.Accounts[i].DeviceID = deviceID
				cfg.Accounts[i].DeviceIDType = deviceType
				return nil
			}
		}
		return errors.New("account not found")
	}); err != nil {
		return "", "", err
	}
	return deviceID, deviceType, nil
}

// generateAccountDeviceID 按 mode 生成新 device_id。同一账号并发登录时
// 通过 per-account 锁串行化，等锁期间若他方已生成同模式 ID 则直接复用。
func (c *Client) generateAccountDeviceID(ctx context.Context, acc config.Account, mode string) (string, string, error) {
	identifier := acc.Identifier()
	if identifier != "" && c != nil {
		unlock := c.lockDeviceGen(identifier)
		defer unlock()

		if c.Store != nil {
			if fresh, ok := c.Store.FindAccount(identifier); ok {
				freshID := strings.TrimSpace(fresh.DeviceID)
				freshType := config.NormalizeDeviceIDType(fresh.DeviceIDType)
				if freshType == "" {
					freshType = config.DeviceIDTypeRandom
				}
				if freshID != "" && freshType == mode {
					return freshID, freshType, nil
				}
			}
		}
	}
	if mode == config.DeviceIDTypeReal {
		// 冷却期内不重试数美：账号已有 device_id 时保持原值，
		// 彻底没有时才退回随机，避免端点故障期间轮换设备指纹。
		if existing := strings.TrimSpace(acc.DeviceID); existing != "" && c.realGenCooldownActive() {
			existingType := config.NormalizeDeviceIDType(acc.DeviceIDType)
			if existingType == "" {
				existingType = config.DeviceIDTypeRandom
			}
			return existing, existingType, nil
		}
		deviceID, err := c.generateRealDeviceID(ctx, acc, identifier)
		if err == nil {
			return deviceID, config.DeviceIDTypeReal, nil
		}
		c.markRealGenFailed()
		config.Logger.Warn("[device_id] real generation failed, falling back to random",
			"account", identifier, "error", err)
	}
	deviceID, err := createRandomDeviceID()
	if err != nil {
		return "", "", err
	}
	return deviceID, config.DeviceIDTypeRandom, nil
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

// devidConfig 解析数美公钥，优先级：
//  1. config.json 的 runtime.devid_public_key；
//  2. data/devid/public_key（纯文本或 JSON 内容）；
//  3. data/devid/config.json（旧版 JSON 内容，兼容早期部署）。
//
// 均缺失则报错，调用方回退随机 ID。
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
