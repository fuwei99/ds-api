package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"ds2api/internal/config"
	dsprotocol "ds2api/internal/deepseek/protocol"
	trans "ds2api/internal/deepseek/transport"
	"ds2api/internal/devid"
)

// realFailCooldown 是 auto 生成失败后的冷却期，**按账号**计算：期内不再为该账号
// 尝试数美，它已有的 device_id 保持原值，避免在坏端点/坏节点上反复撞墙或轮换指纹。
// 按账号而不是全局：一个账号失败（常见原因是它绑的代理节点坏了）不该让别的账号
// 一起停摆，多账号下那等于一个坏节点拖住整批账号补/换 id。
const realFailCooldown = 5 * time.Minute

// realRiskRotationLimit 是 auto 模式下单次登录因 RISK_DEVICE_DETECTED 重新生成
// device_id 的次数上限。每次重生成都是一份全新的设备指纹，连续被风控识别说明
// 问题不在指纹本身，继续轮换只会拖长登录耗时。
const realRiskRotationLimit = 2

// deviceIDGenerator 是数美生成器的最小接口，便于测试注入确定性的 device_id。
type deviceIDGenerator interface {
	Generate(ctx context.Context, profilePath string) (string, error)
}

// newDeviceIDGenerator 由测试替换；生产恒为运行 fp SDK 的数美生成器。
// 身份版本取自传输层与协议层常量：指纹自称的 Chrome 版本必须与 uTLS 预设
// 同源，否则同一个上报请求内部就能对上矛盾。
var newDeviceIDGenerator = func(cfg devid.Config, assetDir string, httpDoer devid.Doer) deviceIDGenerator {
	return &devid.Generator{
		Config:   cfg,
		AssetDir: assetDir,
		HTTP:     httpDoer,
		Identity: devid.Identity{
			ChromeMajor: trans.ChromeMajorVersion,
			SecChUa:     dsprotocol.WebSecChUA(),
		},
	}
}

// ensureAccountDeviceID 返回账号应使用的 device_id 及其类型标注。
//
// manual 模式（默认）：从手工号池分配，优先选择已绑定账号数最少的 device_id；
// 账号在启用期间一直复用同一个 id，被关闭/休眠/禁言/封禁时解绑，
// 下次启用后重新分配。
// auto 模式：经数美 fp SDK 生成真实 ID。
func (c *Client) ensureAccountDeviceID(ctx context.Context, acc config.Account) (string, string, error) {
	deviceID := strings.TrimSpace(acc.DeviceID)
	deviceType := config.NormalizeDeviceIDType(acc.DeviceIDType)
	if c.deviceIDMode() == config.DeviceIDTypeAuto {
		return c.ensureRealDeviceID(ctx, acc, deviceID, deviceType)
	}
	return c.ensurePoolDeviceID(acc, deviceID)
}

// deviceIDMode 返回当前生效的设备 ID 模式（manual/auto，默认 manual）。
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

// ensureRealDeviceID 处理 auto 模式：数美可用时生成/复用真实 device_id，
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
		// 数美接入参数缺失时 auto 不可用。已有 device_id 的账号（含 type=auto）
		// 一律保留原值：覆盖会让真实设备指纹发生变化。
		config.Logger.Warn("[device_id] mode=auto but devid config unavailable",
			"account", acc.Identifier(), "error", err)
		if deviceID != "" {
			return keepExisting()
		}
		return "", "", fmt.Errorf("device_id 生成失败：auto 模式不可用（%v）", err)
	}
	if deviceID != "" && deviceType == config.DeviceIDTypeAuto {
		return deviceID, config.DeviceIDTypeAuto, nil
	}
	if deviceID != "" && c.realGenCooldownActive(acc.Identifier()) {
		// 冷却期内不重试数美：账号已有 device_id（含手工 id）时保持原值，
		// 避免在坏端点/坏节点上反复撞墙。
		//
		// 这里必须留痕：冷却跳过本身是"什么都没做"，而账号此时通常还是
		// manual/未标注类型，界面上的表现只是"真实设备标签一直不出现"——
		// 不记日志就分不清是冷却、是没触发登录、还是生成失败。
		config.Logger.Warn("[device_id] real generation skipped: cooling down after a recent failure",
			"account", acc.Identifier(),
			"cooldown_remaining", c.realGenCooldownRemaining(acc.Identifier()).Round(time.Second).String(),
			"device_id_type", deviceType)
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
	if err := c.persistAccountDeviceID(identifier, generated, config.DeviceIDTypeAuto); err != nil {
		return "", "", err
	}
	if deviceType != config.DeviceIDTypeAuto {
		// 手工/未标注 -> 真实 id 的升级只会发生一次，留一条记录，
		// 便于回溯"标签是什么时候打上的"。
		config.Logger.Info("[device_id] upgraded to real device id",
			"account", identifier, "previous_type", deviceType)
	}
	return generated, config.DeviceIDTypeAuto, nil
}

// generateRealDeviceIDGuarded 在 per-account 锁内生成真实 device_id。
// 等锁期间若他方已生成同模式 ID 则直接复用。
func (c *Client) generateRealDeviceIDGuarded(ctx context.Context, acc config.Account, identifier string) (string, error) {
	if identifier != "" {
		unlock := c.lockDeviceGen(identifier)
		defer unlock()
		if fresh, ok := c.Store.FindAccount(identifier); ok {
			freshID := strings.TrimSpace(fresh.DeviceID)
			if freshID != "" && config.NormalizeDeviceIDType(fresh.DeviceIDType) == config.DeviceIDTypeAuto {
				return freshID, nil
			}
		}
	}
	deviceID, err := c.generateRealDeviceID(ctx, acc, identifier)
	if err != nil {
		c.markRealGenFailed(identifier)
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
	doer := c.devidHTTPClient(acc)
	gen := newDeviceIDGenerator(cfg, config.DevidDir(), doer)
	if identifier == "" {
		identifier = "account"
	}
	profilePath := config.DeviceProfilePath(identifier)
	genCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	deviceID, err := gen.Generate(genCtx, profilePath)
	if client, ok := doer.(interface{ CloseIdleConnections() }); ok {
		client.CloseIdleConnections()
	}
	if err != nil {
		return "", err
	}
	return deviceID, nil
}

// RealDeviceIDRefreshResult 是一个账号的 real device_id 刷新结果。
// 批量刷新逐个返回，失败不连坐：能刷的先刷，失败的带上原因。
type RealDeviceIDRefreshResult struct {
	Identifier string `json:"identifier"`
	DeviceID   string `json:"device_id,omitempty"`
	Error      string `json:"error,omitempty"`
}

// realDeviceRefreshConcurrency 限制批量刷新的并发数。数美侧看到的是同一出口上
// 多个"设备"同时报到，并发拉满本身就是异常；3 是耗时与形迹之间的取值。
const realDeviceRefreshConcurrency = 3

// RefreshRealDeviceID 为账号重新生成 real device_id 并落回配置。
//
// resetProfile 为真时先删除设备档案再生成，等于换一台新设备（画像整体重建）；
// 为假时复用现有档案重算——数美每次上报都会换回一个新的 deviceId，因此这不是
// "拿回原来的 id"，而是"让同一台设备重新报一次"。
//
// 两种都会写回新的 device_id；区别只在设备身份是否保留：复用档案时数美看到的
// 是同一台设备再次出现，删档案则是台全新设备。
func (c *Client) RefreshRealDeviceID(ctx context.Context, identifier string, resetProfile bool) (string, error) {
	if c == nil || c.Store == nil {
		return "", errors.New("device_id 刷新不可用")
	}
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return "", errors.New("缺少账号标识")
	}
	acc, ok := c.Store.FindAccount(identifier)
	if !ok {
		return "", fmt.Errorf("账号不存在：%s", identifier)
	}
	// 与登录路径共用同一把 per-account 锁：管理端刷新和登录生成不能同时写档案。
	unlock := c.lockDeviceGen(identifier)
	defer unlock()
	if resetProfile {
		if err := devid.RemoveProfile(config.DeviceProfilePath(identifier)); err != nil {
			return "", err
		}
	}
	deviceID, err := c.generateRealDeviceID(ctx, acc, identifier)
	if err != nil {
		c.markRealGenFailed(identifier)
		return "", err
	}
	if err := c.persistAccountDeviceID(identifier, deviceID, config.DeviceIDTypeAuto); err != nil {
		return "", err
	}
	return deviceID, nil
}

// RefreshRealDeviceIDs 批量刷新 real device_id，按入参顺序返回每个账号的结果。
// 一个账号失败不影响其它账号，管理端据此逐行展示失败原因。
func (c *Client) RefreshRealDeviceIDs(ctx context.Context, identifiers []string, resetProfile bool) []RealDeviceIDRefreshResult {
	results := make([]RealDeviceIDRefreshResult, len(identifiers))
	if c == nil || c.Store == nil {
		for i, identifier := range identifiers {
			results[i] = RealDeviceIDRefreshResult{Identifier: identifier, Error: "device_id 刷新不可用"}
		}
		return results
	}
	sem := make(chan struct{}, realDeviceRefreshConcurrency)
	var wg sync.WaitGroup
	for i := range identifiers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			identifier := identifiers[i]
			deviceID, err := c.RefreshRealDeviceID(ctx, identifier, resetProfile)
			results[i] = RealDeviceIDRefreshResult{Identifier: identifier, DeviceID: deviceID}
			if err != nil {
				results[i].Error = err.Error()
			}
		}(i)
	}
	wg.Wait()
	return results
}

// rotateRealDeviceID 丢弃账号当前的 real device_id：删除设备档案并清除账号上的
// 绑定，使下一次生成得到一份全新的设备指纹。
//
// 只清账号绑定是不够的：数美按设备档案认定设备，档案还在就等于同一台已被标记的
// 设备再报一次，因此档案必须一并删除。
func (c *Client) rotateRealDeviceID(identifier string) error {
	if c == nil || c.Store == nil {
		return nil
	}
	// 空标识与 generateRealDeviceID 的回退一致（DeviceProfilePath 同样落到 "account"）。
	if err := devid.RemoveProfile(config.DeviceProfilePath(identifier)); err != nil {
		return err
	}
	return c.persistAccountDeviceID(identifier, "", "")
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
// 均缺失则报错，auto 模式下的调用方会保留账号已有 device_id。
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
//
// 一律走项目统一的浏览器指纹传输（httpcloak 的 Chrome 预设：TLS ClientHello、
// HTTP/2 指纹、头顺序）。指纹上报是数美判断"这是不是脚本"的第一现场，用裸
// crypto/tls 发出去等于把所有账号归进同一个 TLS 指纹——这比 payload 更容易被
// 聚类，且脚本用户越多越明显。协议协商用 auto（H2 优先，端点不支持时退
// HTTP/1.1），因为数美端点不像 chat.deepseek.com 那样有已抓包的协议形态。
//
// 账号绑定了代理时跟随账号代理：指纹出口 IP 与登录 IP 不一致本身就显眼。
func (c *Client) devidHTTPClient(acc config.Account) devid.Doer {
	if c != nil {
		if proxyCfg, ok := c.resolveProxyForAccount(acc); ok {
			return trans.NewFallbackWithProxy(30*time.Second, httpCloakProxyURL(proxyCfg))
		}
	}
	return trans.NewFallback(30 * time.Second)
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

// realGenCooldownActive 报告该账号是否正处于 auto 生成失败后的冷却期。
func (c *Client) realGenCooldownActive(identifier string) bool {
	return c.realGenCooldownRemaining(identifier) > 0
}

// realGenCooldownRemaining 返回该账号冷却期的剩余时间，供日志说明"还要等多久"。
func (c *Client) realGenCooldownRemaining(identifier string) time.Duration {
	if c == nil {
		return 0
	}
	return c.realFailUntil.remaining(identifier, time.Now())
}

// markRealGenFailed 让该账号进入冷却期。冷却只作用于这个账号：别的账号
// （哪怕共用同一个出口）不该替它一起停摆。
func (c *Client) markRealGenFailed(identifier string) {
	if c == nil {
		return
	}
	c.realFailUntil.arm(identifier, time.Now(), realFailCooldown)
}
