package account

import (
	"time"

	"ds2api/internal/config"
)

type rateLimitEntry struct {
	windowStart time.Time
	count       int
	unavailable bool
	timer       *time.Timer
}

func (p *Pool) rateLimitWindowLocked() time.Duration {
	if p.rateLimitWindow > 0 {
		return p.rateLimitWindow
	}
	return time.Minute
}

func (p *Pool) rateLimitEnabledLocked() bool {
	if p.store == nil {
		return false
	}
	return p.store.RuntimeAccountRateLimitEnabled()
}

func (p *Pool) rateLimitMaxPerMinuteLocked() int {
	if p.store == nil {
		return config.DefaultAccountRateLimitMaxPerMinute
	}
	return p.store.RuntimeAccountRateLimitMaxPerMinute()
}

func (p *Pool) recordAccountRequestLocked(accountID string) {
	if accountID == "" || !p.rateLimitEnabledLocked() {
		return
	}
	if p.rateLimits == nil {
		p.rateLimits = make(map[string]*rateLimitEntry)
	}
	entry, ok := p.rateLimits[accountID]
	if !ok {
		entry = &rateLimitEntry{}
		p.rateLimits[accountID] = entry
	}

	window := p.rateLimitWindowLocked()
	now := time.Now()

	if entry.windowStart.IsZero() || now.Sub(entry.windowStart) >= window {
		if entry.timer != nil {
			entry.timer.Stop()
			entry.timer = nil
		}
		entry.windowStart = now
		entry.count = 1
		entry.unavailable = false
		entry.timer = time.AfterFunc(window, func() {
			p.resetAccountRateLimit(accountID)
		})
	} else {
		entry.count++
	}

	limit := p.rateLimitMaxPerMinuteLocked()
	if entry.count >= limit {
		entry.unavailable = true
		config.Logger.Info(
			"[account_rate_limit] account reached limit, marked unavailable",
			"account", accountID,
			"count", entry.count,
			"limit", limit,
			"reset_in", time.Until(entry.windowStart.Add(window)).String(),
		)
	}
}

func (p *Pool) resetAccountRateLimit(accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry, ok := p.rateLimits[accountID]
	if !ok {
		return
	}
	if entry.timer != nil {
		entry.timer.Stop()
		entry.timer = nil
	}
	entry.count = 0
	entry.unavailable = false
	entry.windowStart = time.Time{}
	config.Logger.Info("[account_rate_limit] account rate limit reset", "account", accountID)

	p.drainWaitersLocked()
}

func (p *Pool) isRateLimitedLocked(accountID string) bool {
	if accountID == "" || !p.rateLimitEnabledLocked() {
		return false
	}
	if p.rateLimits == nil {
		return false
	}
	entry, ok := p.rateLimits[accountID]
	if !ok {
		return false
	}

	window := p.rateLimitWindowLocked()
	now := time.Now()
	if !entry.windowStart.IsZero() && now.Sub(entry.windowStart) >= window {
		if entry.timer != nil {
			entry.timer.Stop()
			entry.timer = nil
		}
		entry.count = 0
		entry.unavailable = false
		entry.windowStart = time.Time{}
		return false
	}

	return entry.unavailable
}

// IsAccountRateLimited 查询指定账号当前是否由于达到速率限制被打上不可用标签。
func (p *Pool) IsAccountRateLimited(accountID string) bool {
	if accountID == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.isRateLimitedLocked(accountID)
}

func (p *Pool) clearRateLimitsLocked() {
	if p.rateLimits != nil {
		for _, entry := range p.rateLimits {
			if entry.timer != nil {
				entry.timer.Stop()
				entry.timer = nil
			}
		}
	}
	p.rateLimits = make(map[string]*rateLimitEntry)
}

// SetRateLimitWindow 设置限速重置窗口（主要用于快速测试）。
func (p *Pool) SetRateLimitWindow(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rateLimitWindow = d
}

// SyncRateLimits 在运行时配置变更时同步状态。
func (p *Pool) SyncRateLimits() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.rateLimitEnabledLocked() {
		hasUnavailable := false
		for _, entry := range p.rateLimits {
			if entry.timer != nil {
				entry.timer.Stop()
				entry.timer = nil
			}
			if entry.unavailable {
				hasUnavailable = true
				entry.unavailable = false
			}
			entry.count = 0
			entry.windowStart = time.Time{}
		}
		if hasUnavailable {
			p.drainWaitersLocked()
		}
		return
	}

	limit := p.rateLimitMaxPerMinuteLocked()
	recovered := false
	for _, entry := range p.rateLimits {
		if entry.unavailable && entry.count < limit {
			entry.unavailable = false
			recovered = true
		}
	}
	if recovered {
		p.drainWaitersLocked()
	}
}
