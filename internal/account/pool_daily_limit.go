package account

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"ds2api/internal/config"
)

// 24h 速率限制（runtime.account_daily_limit）在号池里的实现。
//
// 与每分钟限速（pool_rate_limit.go）的两点不同：
//
//   - "是否达上限"是算出来的（当天计数 >= 上限），不存"不可用"标记，也不起
//     定时器。日期比较本身就是唯一事实来源，因此跨天重置既不依赖定时器准时
//     触发，也不怕进程休眠或时钟回拨；上限被抬高时账号自动恢复。
//   - 计数要跨重启沿用，所以落盘（data/account_daily_limit.json），启动时先做
//     一次"今天要不要重置"的检查：文件里的日期不是今天就把计数清零，是今天
//     就接着用，重启不会白送一天的额度。
//
// 达到上限的账号不再被调度；当所有候选账号都达到上限时，取号侧不再排队等待
// （等到的是跨天重置），而是立即以"没有空闲账号"失败，见 canQueueLocked 与
// auth.ErrNoIdleAccount。

// dailyLimitState 是 24h 计数的落盘形态：GMT+8 自然日 + 每账号请求数。
type dailyLimitState struct {
	Date   string         `json:"date"`
	Counts map[string]int `json:"counts,omitempty"`
}

// dailyLimitZone 是每日计数的分桶时区。与用量账本（internal/usagestats）取同一
// 口径：GMT+8，即 DeepSeek 官方用量页的"今天"。
var dailyLimitZone = time.FixedZone("GMT+8", 8*60*60)

// dailyLimitPersistInterval 是计数落盘的最小间隔。取号路径持有号池锁，所以计数
// 变化时按此间隔最多每秒写一次盘，把落盘开销挡在请求热路径之外；代价是崩溃时
// 可能丢掉最后不到一秒的计数。
const dailyLimitPersistInterval = time.Second

// dailyLimitDate 返回 at 所在的那个 GMT+8 自然日。
func dailyLimitDate(at time.Time) string {
	return at.In(dailyLimitZone).Format("2006-01-02")
}

// dailyLimitMaxPerDayLocked 返回配置的每天上限；0 表示没填（该功能不生效）。
func (p *Pool) dailyLimitMaxPerDayLocked() int {
	if p.store == nil {
		return 0
	}
	return p.store.RuntimeAccountDailyLimitMaxPerDay()
}

// dailyLimitActiveLocked 报告 24h 速率限制此刻是否真的生效：开关打开**且**填了
// 上限。只打开开关不填数字时该功能不生效，否则一个没填的数字会变成"上限 0"，
// 把全部账号立刻打成不可用。
func (p *Pool) dailyLimitActiveLocked() bool {
	if p.store == nil || !p.store.RuntimeAccountDailyLimitEnabled() {
		return false
	}
	return p.dailyLimitMaxPerDayLocked() > 0
}

// dailyLimitDateLocked 报告当前计数所属的日期；还没有计过数时报告今天，使状态
// 接口给出的日期始终是个有意义的值，而不是空串。
func (p *Pool) dailyLimitDateLocked() string {
	if p.dailyDate != "" {
		return p.dailyDate
	}
	return dailyLimitDate(time.Now())
}

// startupDailyLimitCheck 在进程启动时执行一次"今天要不要重置"的检查，在号池
// 建好队列之后调用。计数文件不存在（含该功能从未开过）时什么都不做，因此关闭
// 状态下不会凭空生成文件。
func (p *Pool) startupDailyLimitCheck() {
	path := p.dailyPath
	if path == "" {
		return
	}
	state, err := readDailyLimitState(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			config.Logger.Warn("[account_daily_limit] state file unreadable, starting empty", "path", path, "error", err)
		}
		return
	}

	today := dailyLimitDate(time.Now())
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dailyLastPersist = time.Now()
	if state.Date == today {
		p.dailyDate = today
		p.dailyCounts = sanitizeDailyCounts(state.Counts)
		config.Logger.Info("[account_daily_limit] resumed today's counters",
			"date", today,
			"accounts", len(p.dailyCounts),
			"limit", p.dailyLimitMaxPerDayLocked(),
		)
		return
	}
	// 跨天（或文件来自旧版本 / 手写坏了日期）：以今天为空账开始，并把空账写回，
	// 使下一次启动看到的是已经翻过页的日期。
	p.dailyDate = today
	p.dailyCounts = map[string]int{}
	config.Logger.Info("[account_daily_limit] daily counters reset at startup",
		"date", today,
		"previous_date", state.Date,
		"discarded", len(state.Counts),
	)
	p.persistDailyLimitsLocked()
}

// rollDailyLimitDateLocked 做运行期的跨天翻页：日期变了就清零当天计数并落盘。
// 不在取号时调用也没关系——它只影响"什么时候观察到新的一天"，不影响到期这一
// 事实本身。
func (p *Pool) rollDailyLimitDateLocked() bool {
	today := dailyLimitDate(time.Now())
	if p.dailyDate == today {
		return false
	}
	previous := p.dailyDate
	p.dailyDate = today
	p.dailyCounts = map[string]int{}
	if previous != "" {
		config.Logger.Info("[account_daily_limit] daily counters reset", "date", today, "previous_date", previous)
	}
	p.persistDailyLimitsLocked()
	return true
}

// recordAccountDailyRequestLocked 记一次请求到账号当天的用量上。它是
// recordAccountRequestLocked 的一部分，与每分钟计数同一时机（取号成功时）。
func (p *Pool) recordAccountDailyRequestLocked(accountID string) {
	if accountID == "" || !p.dailyLimitActiveLocked() {
		return
	}
	p.rollDailyLimitDateLocked()
	if p.dailyCounts == nil {
		p.dailyCounts = map[string]int{}
	}
	p.dailyCounts[accountID]++
	count := p.dailyCounts[accountID]
	limit := p.dailyLimitMaxPerDayLocked()
	if count >= limit {
		if count == limit {
			config.Logger.Info("[account_daily_limit] account reached daily limit",
				"account", accountID,
				"count", count,
				"limit", limit,
				"date", p.dailyDate,
			)
		}
		// 用满额度是重启后必须还成立的状态，所以这一笔不受节流影响，立即落盘。
		p.persistDailyLimitsLocked()
		return
	}
	p.persistDailyLimitsThrottledLocked()
}

// isDailyLimitedLocked 报告账号当天是否已经用满额度。
func (p *Pool) isDailyLimitedLocked(accountID string) bool {
	if accountID == "" || !p.dailyLimitActiveLocked() {
		return false
	}
	p.rollDailyLimitDateLocked()
	return p.dailyCounts[accountID] >= p.dailyLimitMaxPerDayLocked()
}

// IsAccountDailyLimited 查询指定账号当天是否已达 24h 请求上限。
func (p *Pool) IsAccountDailyLimited(accountID string) bool {
	if accountID == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.isDailyLimitedLocked(accountID)
}

// DailyLimitExhausted 报告这次取号失败是否应归因于 24h 上限：target 模式看该
// 账号本人，轮询模式看"满足 filter 的可调度账号是否全部已达上限"（至少要有一个
// 候选账号才算全部）。取号失败后由 auth 用它把"没有空闲账号（额度用尽）"与
// "账号都在忙 / 没配账号"区分开，因此只回答"是不是全因额度"，不改变失败本身。
func (p *Pool) DailyLimitExhausted(target string, filter AccountFilter) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.store == nil || !p.dailyLimitActiveLocked() {
		return false
	}
	if target != "" {
		acc, ok := p.store.FindAccount(target)
		if !ok || !acc.IsSchedulable() {
			return false
		}
		if filter != nil && !filter(acc) {
			return false
		}
		return p.isDailyLimitedLocked(target)
	}
	candidates := 0
	for _, id := range p.queue {
		acc, ok := p.store.FindAccount(id)
		if !ok || !acc.IsSchedulable() {
			continue
		}
		if filter != nil && !filter(acc) {
			continue
		}
		candidates++
		if !p.isDailyLimitedLocked(id) {
			return false
		}
	}
	return candidates > 0
}

// SyncDailyLimits 在运行时配置变更时同步状态。上限是算出来的，所以抬高上限、
// 换一天或关掉开关都会自动让账号恢复可用；这里负责把结论落盘并唤醒等待者。
func (p *Pool) SyncDailyLimits() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.dailyLimitActiveLocked() {
		// 关闭（或数值被清空）：与每分钟限速一致，丢掉计数；再次打开时从当天
		// 重新计。没有计数就不落盘，也不凭空创建文件。
		if len(p.dailyCounts) > 0 {
			p.dailyCounts = map[string]int{}
			p.dailyDate = dailyLimitDate(time.Now())
			p.persistDailyLimitsLocked()
			p.drainWaitersLocked()
		}
		return
	}
	p.rollDailyLimitDateLocked()
	p.drainWaitersLocked()
}

// pruneDailyCountsLocked 在队列重建后丢掉已不可调度账号的计数：账号被删除或
// 改名后它那一天的计数不再属于任何候选账号。仍在队列里的账号保留计数——重建
// 队列（改账号、弹性号池补位）不是新的一天，清空等于把当天额度重新发一遍。
func (p *Pool) pruneDailyCountsLocked(inQueue map[string]bool) {
	for id := range p.dailyCounts {
		if !inQueue[id] {
			delete(p.dailyCounts, id)
		}
	}
}

// persistDailyLimitsThrottledLocked 按节流间隔落盘：窗口外的第一笔立即写，窗口
// 内的变化则安排一次补偿写盘，在窗口结束时把最新状态写下。只有"前导"写盘是不够
// 的——一轮流量结束后的最后一笔计数会一直停在旧值上，直到下一个请求才被带走。
func (p *Pool) persistDailyLimitsThrottledLocked() {
	interval := p.dailyLimitPersistIntervalLocked()
	now := time.Now()
	if p.dailyLastPersist.IsZero() || now.Sub(p.dailyLastPersist) >= interval {
		p.dailyLastPersist = now
		p.persistDailyLimitsLocked()
		return
	}
	if p.dailyFlushTimer != nil {
		return
	}
	delay := interval - now.Sub(p.dailyLastPersist)
	p.dailyFlushTimer = time.AfterFunc(delay, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.dailyFlushTimer = nil
		p.dailyLastPersist = time.Now()
		p.persistDailyLimitsLocked()
	})
}

// dailyLimitPersistIntervalLocked 返回当前的落盘间隔（测试用 SetDailyLimitPersistInterval 缩短）。
func (p *Pool) dailyLimitPersistIntervalLocked() time.Duration {
	if p.dailyPersistInterval > 0 {
		return p.dailyPersistInterval
	}
	return dailyLimitPersistInterval
}

// SetDailyLimitPersistInterval 设置计数的落盘间隔（主要用于快速测试）。
func (p *Pool) SetDailyLimitPersistInterval(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dailyPersistInterval = d
}

// persistDailyLimitsLocked 把当天计数原子写盘。写失败只告警：限额仍然按内存里的
// 计数生效，重启后最多少算当天已经写下去的那部分。
func (p *Pool) persistDailyLimitsLocked() {
	if p.dailyPath == "" {
		return
	}
	body, err := json.Marshal(dailyLimitState{Date: p.dailyDate, Counts: p.dailyCounts})
	if err != nil {
		config.Logger.Warn("[account_daily_limit] encode state failed", "error", err)
		return
	}
	if err := writeAccountStateFile(p.dailyPath, append(body, '\n')); err != nil {
		config.Logger.Warn("[account_daily_limit] persist failed", "path", p.dailyPath, "error", err)
	}
}

// readDailyLimitState 读取计数文件。文件不存在时返回 os.ErrNotExist。
func readDailyLimitState(path string) (dailyLimitState, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return dailyLimitState{}, err
	}
	var state dailyLimitState
	if err := json.Unmarshal(raw, &state); err != nil {
		return dailyLimitState{}, err
	}
	return state, nil
}

// sanitizeDailyCounts 丢弃非正数的计数项，并保证返回的 map 非 nil。
func sanitizeDailyCounts(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for id, count := range in {
		if id == "" || count <= 0 {
			continue
		}
		out[id] = count
	}
	return out
}

// writeAccountStateFile 原子替换写文件（临时文件 + rename），与用量账本、聊天
// 历史的落盘方式一致：崩溃时最多留下一个临时文件，读到的是完整的旧内容或完整
// 的新内容。
func writeAccountStateFile(path string, body []byte) error {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil && dir != "." {
		return err
	}
	tmpFile, err := os.CreateTemp(dir, ".account-daily-limit-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(body); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
