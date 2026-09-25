package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// deviceIDBodyLen 是 device_id 去掉 "B" 前缀后的长度：
// 64 字节原始数据经标准 base64 编码得到 88 个字符（含结尾的 "=="）。
const deviceIDBodyLen = 88

// ErrDeviceIDPoolEmpty 表示 manual 模式下号池中没有任何可用 device_id。
// 请求与刷新 Token 都会在真正发起上游调用之前以此报错。
var ErrDeviceIDPoolEmpty = errors.New("请先配置device_id")

// DeviceIDPoolItem 是一条手工录入的 device_id 及其已绑定账号数。
// 绑定数单独持久化（而不是每次遍历全部账号统计），以降低超多账号场景下的开销。
type DeviceIDPoolItem struct {
	ID    string `json:"id"`
	Bound int    `json:"bound,omitempty"`
}

// DeviceIDPoolConfig 是手工 device_id 号池。
// manual 模式下账号登录时从中选取绑定账号数最少的 id，账号启用期间一直复用
// 同一个 id；账号被关闭/休眠/禁言/封禁时解除绑定。
type DeviceIDPoolConfig struct {
	Items []DeviceIDPoolItem `json:"items,omitempty"`
}

// NormalizeDeviceIDInput 校验并规范化用户录入的 device_id。
//
// 接受两种写法：网页上拿到的原始值（88 字符）与带 "B" 前缀的值（89 字符），
// 以长度区分，统一存储为带 "B" 前缀的规范形式。长度或格式不合法（例如结尾
// 缺少 "=="）时返回错误。
func NormalizeDeviceIDInput(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("device_id 不能为空")
	}
	body := s
	if len(s) == deviceIDBodyLen+1 && (s[0] == 'B' || s[0] == 'b') {
		body = s[1:]
	}
	if len(body) != deviceIDBodyLen {
		return "", fmt.Errorf("device_id 长度不正确：应为 %d 或 %d 个字符，当前 %d 个", deviceIDBodyLen, deviceIDBodyLen+1, len(s))
	}
	if !strings.HasSuffix(body, "==") {
		return "", errors.New(`device_id 格式不正确：应以 "==" 结尾`)
	}
	decoded, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return "", errors.New("device_id 格式不正确：不是合法的 base64 编码")
	}
	if len(decoded) != 64 {
		return "", errors.New("device_id 格式不正确：解码后长度不是 64 字节")
	}
	return "B" + body, nil
}

// NormalizeDeviceIDPool 规范化号池：trim、去重（忽略大小写与前缀差异）、
// 丢弃非法条目，并保留条目原有的绑定数。
func NormalizeDeviceIDPool(items []DeviceIDPoolItem) []DeviceIDPoolItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]DeviceIDPoolItem, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		id, err := NormalizeDeviceIDInput(item.ID)
		if err != nil {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		bound := item.Bound
		if bound < 0 {
			bound = 0
		}
		out = append(out, DeviceIDPoolItem{ID: id, Bound: bound})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// DeviceIDPoolIndex 返回 device_id 在号池中的下标，不存在时返回 -1。
func (c *Config) DeviceIDPoolIndex(deviceID string) int {
	if c == nil {
		return -1
	}
	id := strings.TrimSpace(deviceID)
	if id == "" {
		return -1
	}
	for i := range c.DeviceIDPool.Items {
		if c.DeviceIDPool.Items[i].ID == id {
			return i
		}
	}
	return -1
}

// HasDeviceID 报告号池中是否存在该 device_id。
func (c *Config) HasDeviceID(deviceID string) bool {
	return c.DeviceIDPoolIndex(deviceID) >= 0
}

// AddDeviceID 把规范化后的 device_id 加入号池；已存在时返回 false。
func (c *Config) AddDeviceID(deviceID string) bool {
	if c == nil || c.HasDeviceID(deviceID) {
		return false
	}
	c.DeviceIDPool.Items = append(c.DeviceIDPool.Items, DeviceIDPoolItem{ID: deviceID})
	return true
}

// RemoveDeviceID 从号池删除 device_id；不存在时静默返回。
func (c *Config) RemoveDeviceID(deviceID string) {
	idx := c.DeviceIDPoolIndex(deviceID)
	if idx < 0 {
		return
	}
	items := c.DeviceIDPool.Items
	c.DeviceIDPool.Items = append(items[:idx], items[idx+1:]...)
	if len(c.DeviceIDPool.Items) == 0 {
		c.DeviceIDPool.Items = nil
	}
}

// LeastBoundDeviceID 返回绑定账号数最少的 device_id。
// 并列时取号池顺序靠前者，保证分配结果稳定可预期。
func (c *Config) LeastBoundDeviceID() (string, bool) {
	if c == nil || len(c.DeviceIDPool.Items) == 0 {
		return "", false
	}
	best := c.DeviceIDPool.Items[0]
	for _, item := range c.DeviceIDPool.Items[1:] {
		if item.Bound < best.Bound {
			best = item
		}
	}
	return best.ID, true
}

// BindDeviceIDToAccount 把号池中的 device_id 记到账号上并累加其绑定数。
// 仅修改内存中的配置，持久化由调用方在 Store.Update 内完成。
func (c *Config) BindDeviceIDToAccount(identifier, deviceID string) {
	if c == nil {
		return
	}
	if idx := c.DeviceIDPoolIndex(deviceID); idx >= 0 {
		c.DeviceIDPool.Items[idx].Bound++
	}
	if strings.TrimSpace(identifier) == "" {
		return
	}
	for i := range c.Accounts {
		if c.Accounts[i].Identifier() != identifier {
			continue
		}
		c.Accounts[i].DeviceID = deviceID
		c.Accounts[i].DeviceIDType = DeviceIDTypeManual
		return
	}
}

// ReconcileDeviceIDBindings 在 manual 模式下重算 device_id 绑定关系：
//   - 账号被手动关闭 / 弹性号池休眠 / 禁言 / 封禁 -> 解除绑定；
//   - 账号持有的 device_id 已不在号池中（历史随机 ID、被删除或被风控移除）
//     -> 解除绑定；
//   - 最后按实际绑定情况回写每个 device_id 的已绑定账号数。
//
// 本函数只做「解绑 + 计数」，**不会**给尚未绑定的账号分配 device_id：
// 分配只发生在账号登录时（见 client.assignDeviceIDFromPool）。否则在号池里
// 只有一两个 id 时，一次配置变更就会把全部账号一次性绑到同一个 id 上，
// 后续新增 id 也再没有账号可以被分配过去。
//
// real 模式下号池不参与分配，直接返回。
func ReconcileDeviceIDBindings(cfg *Config) {
	if cfg == nil || EffectiveDeviceIDMode(cfg.Runtime.DeviceIDMode) != DeviceIDTypeManual {
		return
	}
	counts := make(map[string]int, len(cfg.DeviceIDPool.Items))
	for _, item := range cfg.DeviceIDPool.Items {
		counts[item.ID] = 0
	}
	for i := range cfg.Accounts {
		acc := &cfg.Accounts[i]
		if !DeviceBindingEligible(*acc) {
			// 解除绑定只清 device_id：device_id_type 描述的是账号所处的模式，
			// 与是否持有绑定无关（历史 random 已在此前的规范化中迁移为 manual）。
			acc.DeviceID = ""
			continue
		}
		id := strings.TrimSpace(acc.DeviceID)
		if _, ok := counts[id]; !ok {
			acc.DeviceID = ""
			continue
		}
		counts[id]++
		if acc.DeviceIDType != DeviceIDTypeManual {
			acc.DeviceIDType = DeviceIDTypeManual
		}
	}
	applyDeviceIDCounts(cfg, counts)
}

// RemoveDeviceIDAndRebind 从号池删除 device_id，并把原先绑定它、且仍可绑定的
// 账号立即换绑到剩余 id 中绑定数最少者（device_id 被手工删除或被风控判定失效
// 时使用）。未持有绑定的账号不受影响。返回删除后号池剩余的 device_id 数量。
func RemoveDeviceIDAndRebind(cfg *Config, deviceID string) int {
	if cfg == nil {
		return 0
	}
	cfg.RemoveDeviceID(deviceID)
	if deviceID == "" {
		return len(cfg.DeviceIDPool.Items)
	}
	var orphans []int
	for i := range cfg.Accounts {
		if strings.TrimSpace(cfg.Accounts[i].DeviceID) == deviceID {
			cfg.Accounts[i].DeviceID = ""
			orphans = append(orphans, i)
		}
	}
	counts := bindingCounts(cfg)
	for _, i := range orphans {
		acc := &cfg.Accounts[i]
		if !DeviceBindingEligible(*acc) {
			continue
		}
		next, ok := leastBoundFrom(counts, cfg.DeviceIDPool.Items)
		if !ok {
			continue
		}
		acc.DeviceID = next
		acc.DeviceIDType = DeviceIDTypeManual
		counts[next]++
	}
	applyDeviceIDCounts(cfg, counts)
	return len(cfg.DeviceIDPool.Items)
}

// bindingCounts 统计号池中每个 device_id 当前被多少个可绑定账号持有。
func bindingCounts(cfg *Config) map[string]int {
	counts := make(map[string]int, len(cfg.DeviceIDPool.Items))
	for _, item := range cfg.DeviceIDPool.Items {
		counts[item.ID] = 0
	}
	for i := range cfg.Accounts {
		acc := cfg.Accounts[i]
		if !DeviceBindingEligible(acc) {
			continue
		}
		id := strings.TrimSpace(acc.DeviceID)
		if _, ok := counts[id]; ok {
			counts[id]++
		}
	}
	return counts
}

func applyDeviceIDCounts(cfg *Config, counts map[string]int) {
	for i := range cfg.DeviceIDPool.Items {
		cfg.DeviceIDPool.Items[i].Bound = counts[cfg.DeviceIDPool.Items[i].ID]
	}
}

// leastBoundFrom 在 counts 中挑选绑定数最少的 id（并列取号池顺序靠前者）。
func leastBoundFrom(counts map[string]int, items []DeviceIDPoolItem) (string, bool) {
	if len(items) == 0 {
		return "", false
	}
	best := items[0].ID
	bestCount := counts[best]
	for _, item := range items[1:] {
		if counts[item.ID] < bestCount {
			best = item.ID
			bestCount = counts[item.ID]
		}
	}
	return best, true
}
