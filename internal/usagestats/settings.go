package usagestats

import (
	"fmt"
)

// 默认值集中在这里，供 DefaultSettings、normalizeSettings 与各访问器复用。
const (
	defaultInputMissPrice = 1.0
	defaultCacheHitRate   = 98.0
)

// ModelPrice 描述某个模型的单价（每 100 万 tokens）。开启「模拟缓存命中」后
// 输入被拆成命中与未命中两部分：InputPrice 是命中部分的单价，InputMissPrice
// 是未命中部分的单价；关闭时输入整体按 InputPrice 计价（即改造前的行为）。
// InputMissPrice 用指针是为了区分「旧设置文件里没有这个键」（nil → 回落默认
// 值）与「用户显式填 0」（免费），否则升级后会静默按 0 计费。
type ModelPrice struct {
	InputPrice     float64  `json:"input_price"`
	InputMissPrice *float64 `json:"input_miss_price,omitempty"`
	OutputPrice    float64  `json:"output_price"`
}

// MissInputPrice 返回缓存未命中部分的输入单价，未配置时回落默认值。
func (p ModelPrice) MissInputPrice() float64 {
	if p.InputMissPrice == nil {
		return defaultInputMissPrice
	}
	return *p.InputMissPrice
}

// CacheHitSettings 控制「模拟缓存命中」。上游不返回缓存命中信息，因此这里按
// 配置的命中率把输入 tokens 拆成命中 / 未命中两部分分别计价。两个字段都用
// 指针，以便把「旧设置文件里没有这一段」与「用户显式关闭 / 显式填 0」区分开：
// 前者回落默认（开启 + 98%），后者必须原样保留。
type CacheHitSettings struct {
	Enabled *bool    `json:"enabled,omitempty"`
	Rate    *float64 `json:"rate,omitempty"`
}

// IsEnabled 返回开关状态，未配置时视为开启（默认开）。
func (c CacheHitSettings) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// HitRate 返回缓存命中率（0-100 的百分比），未配置时回落默认值。
func (c CacheHitSettings) HitRate() float64 {
	if c.Rate == nil {
		return defaultCacheHitRate
	}
	return *c.Rate
}

type PeakSettings struct {
	Enabled    bool    `json:"enabled"`
	Start1     string  `json:"start1"`
	End1       string  `json:"end1"`
	Start2     string  `json:"start2"`
	End2       string  `json:"end2"`
	Multiplier float64 `json:"multiplier"`
	// WeekendNormal 对应设置页的「排除周末及节假日」：开启后周末与法定节假日
	// 全程按空闲价计价，不进入波峰时段。字段名与 JSON 键沿用历史名称
	// weekend_normal，避免既有 .settings 文件与 /admin/usage/settings 客户端失效。
	WeekendNormal bool `json:"weekend_normal"`
}

type UsageSettings struct {
	Enabled bool                  `json:"enabled"`
	Models  map[string]ModelPrice `json:"models"`
	Peak    PeakSettings          `json:"peak"`
	// CacheHit 对应设置页「价格设置 → 模拟缓存命中」，默认开启。
	CacheHit CacheHitSettings `json:"cache_hit"`
}

// mergedModelPriceKey 是模型合并后唯一的基础模型 ID。代理只暴露
// deepseek-v4.1-flash 系列（含 -search / -nothinking 变体）且它们共用同一套
// 单价，因此单价表只保留这一个条目：匹配时 -search / -nothinking 变体会按
// 「-」分段命中该键，设置页也只需要一组输入/输出单价输入框。
const mergedModelPriceKey = "deepseek-v4.1-flash"

// modelPriceInheritOrder 决定单价表收敛为单一条目时继承哪一条既有取值，按
// 「越接近当前模型」的顺序排列：合并后的基础键优先，其次是合并后一度单列的
// search 键，最后才是合并前的档位键（flash / vision 与合并后的模型同属 flash
// 系列且默认同价，pro / v4-pro 仅在用户只配置过它们时兜底，避免直接丢弃用户
// 显式填写的单价）。不在该表里的键一律丢弃。
var modelPriceInheritOrder = []string{
	mergedModelPriceKey,
	"deepseek-v4.1-flash-search",
	"flash",
	"vision",
	"pro",
	"v4-pro",
}

func DefaultSettings() UsageSettings {
	return UsageSettings{
		Enabled: true,
		Models: map[string]ModelPrice{
			mergedModelPriceKey: {
				InputPrice:     0.02,
				InputMissPrice: float64Ptr(defaultInputMissPrice),
				OutputPrice:    4,
			},
		},
		Peak: PeakSettings{
			Enabled:       true,
			Start1:        "09:00",
			End1:          "12:00",
			Start2:        "14:00",
			End2:          "18:00",
			Multiplier:    2,
			WeekendNormal: true,
		},
		CacheHit: CacheHitSettings{
			Enabled: boolPtr(true),
			Rate:    float64Ptr(defaultCacheHitRate),
		},
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func float64Ptr(v float64) *float64 {
	return &v
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	return boolPtr(*v)
}

func cloneFloat64Ptr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	return float64Ptr(*v)
}

func validatePeakSettings(peak PeakSettings) error {
	ranges := []struct {
		start string
		end   string
		label string
	}{
		{peak.Start1, peak.End1, "peak window 1"},
		{peak.Start2, peak.End2, "peak window 2"},
	}
	for _, r := range ranges {
		if r.start == "" && r.end == "" {
			continue
		}
		if r.start == "" || r.end == "" {
			return fmt.Errorf("%w: %s must have both start and end times", ErrInvalidUsageSettings, r.label)
		}
		startMinutes, err := clockMinutes(r.start)
		if err != nil {
			return fmt.Errorf("%w: %s start %q is invalid", ErrInvalidUsageSettings, r.label, r.start)
		}
		endMinutes, err := clockMinutes(r.end)
		if err != nil {
			return fmt.Errorf("%w: %s end %q is invalid", ErrInvalidUsageSettings, r.label, r.end)
		}
		if startMinutes >= endMinutes {
			return fmt.Errorf("%w: %s start must be before end", ErrInvalidUsageSettings, r.label)
		}
	}
	if peak.Multiplier < 0 {
		return fmt.Errorf("%w: peak multiplier must be >= 0", ErrInvalidUsageSettings)
	}
	return nil
}

func clockMinutes(value string) (int, error) {
	var hour, minute int
	if _, err := fmt.Sscanf(value, "%d:%d", &hour, &minute); err != nil {
		return 0, err
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("invalid clock value")
	}
	return hour*60 + minute, nil
}

// normalizeSettings 把单价表收敛成唯一一条并补齐缺失的波峰参数。单价 0 是
// 合法取值（表示不计费），不会被默认价覆盖；负数一律钳制为 0，防止手改文件
// 写出负价。「模拟缓存命中」段缺失（旧设置文件）时整体回落默认值（开启 +
// 98%），命中率钳制到 [0, 100]。
func normalizeSettings(settings UsageSettings) UsageSettings {
	defaults := DefaultSettings()
	settings.Models = collapseModelPrices(settings.Models, defaults.Models)
	settings.CacheHit = normalizeCacheHitSettings(settings.CacheHit)
	if settings.Peak.Enabled {
		if settings.Peak.Start1 == "" {
			settings.Peak.Start1 = defaults.Peak.Start1
		}
		if settings.Peak.End1 == "" {
			settings.Peak.End1 = defaults.Peak.End1
		}
		if settings.Peak.Start2 == "" {
			settings.Peak.Start2 = defaults.Peak.Start2
		}
		if settings.Peak.End2 == "" {
			settings.Peak.End2 = defaults.Peak.End2
		}
		if settings.Peak.Multiplier <= 0 {
			settings.Peak.Multiplier = defaults.Peak.Multiplier
		}
	}
	return settings
}

// normalizeCacheHitSettings 补齐「模拟缓存命中」的默认值并钳制命中率。
// 指针为 nil 表示该键缺失，此时回落默认；显式 false / 显式 0 会被保留。
func normalizeCacheHitSettings(cacheHit CacheHitSettings) CacheHitSettings {
	enabled := true
	if cacheHit.Enabled != nil {
		enabled = *cacheHit.Enabled
	}
	rate := defaultCacheHitRate
	if cacheHit.Rate != nil {
		rate = *cacheHit.Rate
	}
	if rate < 0 {
		rate = 0
	}
	if rate > 100 {
		rate = 100
	}
	return CacheHitSettings{Enabled: boolPtr(enabled), Rate: float64Ptr(rate)}
}

// collapseModelPrices 把单价表收敛为唯一一条 mergedModelPriceKey 条目：按
// modelPriceInheritOrder 取第一个存在的既有取值（含 0=不计费），都没有时回退
// 到默认价。旧档位键、一度单列的 search 键以及用户手改写入的其他模型键都会被
// 删除，保证设置页只出现一组单价输入框。
func collapseModelPrices(models, defaults map[string]ModelPrice) map[string]ModelPrice {
	out := make(map[string]ModelPrice, 1)
	price, ok := firstModelPrice(models, modelPriceInheritOrder)
	if !ok {
		price = defaults[mergedModelPriceKey]
	}
	out[mergedModelPriceKey] = clampPrice(price)
	return out
}

func firstModelPrice(models map[string]ModelPrice, keys []string) (ModelPrice, bool) {
	for _, key := range keys {
		if price, ok := models[key]; ok {
			return price, true
		}
	}
	return ModelPrice{}, false
}

func clampPrice(p ModelPrice) ModelPrice {
	if p.InputPrice < 0 {
		p.InputPrice = 0
	}
	if p.OutputPrice < 0 {
		p.OutputPrice = 0
	}
	if p.InputMissPrice == nil {
		// 旧设置文件没有未命中单价，回落默认值而不是当成 0（免费）。
		p.InputMissPrice = float64Ptr(defaultInputMissPrice)
	} else if *p.InputMissPrice < 0 {
		p.InputMissPrice = float64Ptr(0)
	}
	return p
}

// validateModelPrices 拒绝负单价，避免手改请求写出负价条目。
func validateModelPrices(models map[string]ModelPrice) error {
	for key, price := range models {
		if price.InputPrice < 0 || price.OutputPrice < 0 {
			return fmt.Errorf("%w: model %q prices must be >= 0", ErrInvalidUsageSettings, key)
		}
		if price.InputMissPrice != nil && *price.InputMissPrice < 0 {
			return fmt.Errorf("%w: model %q cache-miss input price must be >= 0", ErrInvalidUsageSettings, key)
		}
	}
	return nil
}
