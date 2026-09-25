package account

import (
	"sort"
	"sync"
	"time"

	"ds2api/internal/config"
)

type Pool struct {
	store                  *config.Store
	mu                     sync.Mutex
	queue                  []string
	inUse                  map[string]int
	waiters                []chan struct{}
	maxInflightPerAccount  int
	recommendedConcurrency int
	maxQueueSize           int
	globalMaxInflight      int
	rateLimits             map[string]*rateLimitEntry
	rateLimitWindow        time.Duration
}

func NewPool(store *config.Store) *Pool {
	maxPer := 2
	if store != nil {
		maxPer = store.RuntimeAccountMaxInflight()
	}
	p := &Pool{
		store:                 store,
		inUse:                 map[string]int{},
		maxInflightPerAccount: maxPer,
		rateLimits:            make(map[string]*rateLimitEntry),
	}
	p.Reset()
	return p
}

func (p *Pool) Reset() {
	accounts := p.store.Accounts()
	sort.SliceStable(accounts, func(i, j int) bool {
		iHas := accounts[i].Token != ""
		jHas := accounts[j].Token != ""
		if iHas == jHas {
			return i < j
		}
		return iHas
	})
	ids := make([]string, 0, len(accounts))
	for _, a := range accounts {
		// 禁用/被封/鉴权异常账号不进入取号队列：它们不会被调度，留在队列里
		// 只会让每次取号退化为对全量账号（含大量休眠账号）的线性扫描，
		// 推高 CPU 占用。
		if a.Disabled || a.Banned || a.IsAuthFailed() {
			continue
		}
		id := a.Identifier()
		if id != "" {
			ids = append(ids, id)
		}
	}
	if p.store != nil {
		p.maxInflightPerAccount = p.store.RuntimeAccountMaxInflight()
	} else {
		p.maxInflightPerAccount = maxInflightFromEnv()
	}
	recommended := defaultRecommendedConcurrency(len(ids), p.maxInflightPerAccount)
	queueLimit := maxQueueFromEnv(recommended)
	globalLimit := recommended
	if p.store != nil {
		queueLimit = p.store.RuntimeAccountMaxQueue(recommended)
		globalLimit = p.store.RuntimeGlobalMaxInflight(recommended)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.drainWaitersLocked()
	p.clearRateLimitsLocked()
	p.queue = ids
	// 保留仍在新队列中账号的 in-use 计数：账号被禁用/封禁/删除后重建队列时，
	// 若清空所有计数，会让仍在处理请求、但未被移除的账号看似空闲而被再次调度，
	// 造成同一账号并发超卖。仅清理已不在队列（不再可调度）账号的计数。
	inQueue := make(map[string]bool, len(ids))
	for _, id := range ids {
		inQueue[id] = true
	}
	for id := range p.inUse {
		if !inQueue[id] {
			delete(p.inUse, id)
		}
	}
	p.recommendedConcurrency = recommended
	p.maxQueueSize = queueLimit
	p.globalMaxInflight = globalLimit
	config.Logger.Info(
		"[init_account_queue] initialized",
		"total", len(ids),
		"max_inflight_per_account", p.maxInflightPerAccount,
		"global_max_inflight", p.globalMaxInflight,
		"recommended_concurrency", p.recommendedConcurrency,
		"max_queue_size", p.maxQueueSize,
	)
}

func (p *Pool) Release(accountID string) {
	if accountID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	count := p.inUse[accountID]
	if count <= 0 {
		return
	}
	if count == 1 {
		delete(p.inUse, accountID)
		p.notifyWaiterLocked()
		return
	}
	p.inUse[accountID] = count - 1
	p.notifyWaiterLocked()
}

// SchedulableCount 返回取号队列中的可调度账号数（已排除禁用/封禁账号）。
func (p *Pool) SchedulableCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.queue)
}

func (p *Pool) Status() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	available := make([]string, 0, len(p.queue))
	inUseAccounts := make([]string, 0, len(p.inUse))
	rateLimited := make([]string, 0)
	inUseSlots := 0
	for _, id := range p.queue {
		acc, ok := p.store.FindAccount(id)
		if !ok || !acc.IsEnabled() || acc.IsMuted() {
			continue
		}
		if p.isRateLimitedLocked(id) {
			rateLimited = append(rateLimited, id)
			continue
		}
		if p.inUse[id] < p.maxInflightPerAccount {
			available = append(available, id)
		}
	}
	for id, count := range p.inUse {
		if count > 0 {
			inUseAccounts = append(inUseAccounts, id)
			inUseSlots += count
		}
	}
	sort.Strings(inUseAccounts)
	sort.Strings(rateLimited)
	return map[string]any{
		"available":                len(available),
		"in_use":                   inUseSlots,
		"total":                    len(p.store.Accounts()),
		"available_accounts":       available,
		"in_use_accounts":          inUseAccounts,
		"rate_limited_accounts":    rateLimited,
		"max_inflight_per_account": p.maxInflightPerAccount,
		"global_max_inflight":      p.globalMaxInflight,
		"recommended_concurrency":  p.recommendedConcurrency,
		"waiting":                  len(p.waiters),
		"max_queue_size":           p.maxQueueSize,
	}
}
