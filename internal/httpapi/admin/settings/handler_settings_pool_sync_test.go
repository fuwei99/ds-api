package settings

import (
	"testing"

	"ds2api/internal/config"
)

// recordingPool 记录段后置动作调用过哪些号池方法。设置写入之后必须把新值推给
// 正在运行的号池（见 handler_settings_write.go 的 sectionPostWriteActions），
// 否则"关闭 24h 限额 / 抬高上限"要等到重启才生效。
type recordingPool struct {
	dailySyncs int
	rateSyncs  int
	limitSyncs int
}

func (p *recordingPool) Reset()                           {}
func (p *recordingPool) Status() map[string]any           { return map[string]any{} }
func (p *recordingPool) SchedulableCount() int            { return 0 }
func (p *recordingPool) InUseIDs() map[string]bool        { return nil }
func (p *recordingPool) ApplyRuntimeLimits(int, int, int) { p.limitSyncs++ }
func (p *recordingPool) SyncRateLimits()                  { p.rateSyncs++ }
func (p *recordingPool) SyncDailyLimits()                 { p.dailySyncs++ }

func TestRuntimeUpdatePushesDailyLimitToThePool(t *testing.T) {
	pool := &recordingPool{}
	h := &Handler{Store: &config.Store{}, Pool: pool}

	runSectionPostWriteActions(h, config.SectionUpdates(map[string]any{
		"runtime": map[string]any{
			"account_daily_limit": map[string]any{"enabled": true, "max_per_day": 20},
		},
	}))

	if pool.dailySyncs != 1 {
		t.Fatalf("a runtime update must push the 24h limit to the pool, got %d syncs", pool.dailySyncs)
	}
	// 同一个后置动作也负责另外两项：它们同样是运行中的号池状态。
	if pool.rateSyncs != 1 || pool.limitSyncs != 1 {
		t.Fatalf("expected the runtime post-write action to sync every pool limit, got rate=%d limits=%d",
			pool.rateSyncs, pool.limitSyncs)
	}
}

func TestUpdateToAnotherSectionLeavesThePoolAlone(t *testing.T) {
	pool := &recordingPool{}
	h := &Handler{Store: &config.Store{}, Pool: pool}

	runSectionPostWriteActions(h, config.SectionUpdates(map[string]any{
		"auto_delete": map[string]any{"mode": "all"},
	}))

	if pool.dailySyncs != 0 || pool.rateSyncs != 0 || pool.limitSyncs != 0 {
		t.Fatal("a section that does not govern pool limits must not touch the pool")
	}
}
