package account

import (
	"sort"

	"ds2api/internal/config"
)

// DefaultElasticPoolGlobalCount 是弹性号池全局模式下每批启用账号数的默认值。
const DefaultElasticPoolGlobalCount = 3

// ReconcileElasticPool 根据弹性号池配置重算每个账号的 Disabled 状态。
//
// 开启时按账号优先级从高到低遍历：被封禁言(muted)、已被上游停用(banned)或
// 被判定鉴权异常(auth_failed)的账号一律禁用且不占名额，其余可调度账号按顺序
// 启用前 N 个，超出 N 的禁用(休眠)。
// 优先级相同时保持 config.Accounts 的原始顺序（靠前者优先）。
// PerPool=false 时所有账号共用 GlobalCount；PerPool=true 时 default/no_tools/
// tools_only 三种号池类型分别使用各自的 Count。
//
// 调用方需在 Store.Update 的 mutator 内调用，随后执行 Pool.Reset()。
// 若弹性号池未开启则不做任何操作。
func ReconcileElasticPool(cfg *config.Config) {
	if cfg == nil || !cfg.ElasticPool.Enabled {
		return
	}
	ep := cfg.ElasticPool
	if !ep.PerPool {
		count := ep.GlobalCount
		if count < 0 {
			count = 0
		}
		reconcileGroupFiltered(cfg.Accounts, config.PoolTypeDefault, count)
		reconcileGroupFiltered(cfg.Accounts, config.PoolTypeNoTools, count)
		reconcileGroupFiltered(cfg.Accounts, config.PoolTypeToolsOnly, count)
	} else {
		reconcileGroupFiltered(cfg.Accounts, config.PoolTypeDefault, effectivePoolCount(ep.DefaultCount))
		reconcileGroupFiltered(cfg.Accounts, config.PoolTypeNoTools, effectivePoolCount(ep.NoToolsCount))
		reconcileGroupFiltered(cfg.Accounts, config.PoolTypeToolsOnly, effectivePoolCount(ep.ToolsOnlyCount))
	}
	// 被弹性号池休眠的账号解除 device_id 绑定，重新启用后重新分配。
	config.ReconcileDeviceIDBindings(cfg)
}

// DisableAllAccounts 将所有账号设为启用(Disabled=false)。
// 用于关闭弹性号池时恢复全部账号可用。
func DisableAllAccounts(cfg *config.Config) {
	if cfg == nil {
		return
	}
	for i := range cfg.Accounts {
		cfg.Accounts[i].Disabled = false
	}
}

func effectivePoolCount(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// reconcileGroupFiltered 对属于指定 poolType 的账号执行弹性启用，
// 不属于该 poolType 的账号保持原状。
//
// 选取顺序：优先按 Priority 从高到低；Priority 相同时保持账号在
// accounts 中的原始顺序。排序使用稳定排序，因此同优先级账号的
// 相对次序与配置顺序一致。
func reconcileGroupFiltered(accounts []config.Account, poolType string, count int) {
	order := make([]int, 0, len(accounts))
	for i := range accounts {
		if config.NormalizePoolType(accounts[i].PoolType) != poolType {
			continue
		}
		order = append(order, i)
	}
	sort.SliceStable(order, func(a, b int) bool {
		return accounts[order[a]].Priority > accounts[order[b]].Priority
	})

	enabled := 0
	for _, i := range order {
		applyElasticState(&accounts[i], &enabled, count)
	}
}

// applyElasticState 对单个账号应用弹性号池规则：
//   - 被封禁言(muted)、已被上游停用(banned)或鉴权异常(auth_failed)
//     -> Disabled=true，不计数
//   - 已启用数 < count -> Disabled=false，计数+1
//   - 否则 -> Disabled=true
func applyElasticState(acc *config.Account, enabled *int, count int) {
	if acc.IsMuted() || acc.IsBanned() || acc.IsAuthFailed() {
		acc.Disabled = true
		return
	}
	if *enabled < count {
		acc.Disabled = false
		*enabled++
		return
	}
	acc.Disabled = true
}
