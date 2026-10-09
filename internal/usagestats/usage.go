package usagestats

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/wangzeping722/chinesecalendar"
)

// computeCostLocked 按当前定价计算一次请求的费用；波峰倍率基于请求发生
// 时刻判断。单价 0 表示不计费，不会回退到默认价。调用方必须持有 s.mu。
func (s *Store) computeCostLocked(model string, prompt, completion int64, at time.Time) float64 {
	if !s.settings.Enabled {
		return 0
	}
	price, ok := matchModelPrice(s.settings.Models, model)
	if !ok {
		return 0
	}
	base := inputCost(price, s.settings.CacheHit, prompt) + float64(completion)/1_000_000*price.OutputPrice
	if s.settings.Peak.Enabled && isPeakHour(s.settings.Peak, at) {
		base *= s.settings.Peak.Multiplier
	}
	return base
}

// inputCost 计算输入 tokens 的费用。开启「模拟缓存命中」后，上游不返回的
// 缓存命中信息按配置的命中率模拟：输入被拆成命中部分（InputPrice）与未命中
// 部分（MissInputPrice）分别计价；关闭时输入整体按 InputPrice 计价，与改造前
// 完全一致。拆分口径与前端图表一致：命中 tokens = 输入 tokens × 命中率。
func inputCost(price ModelPrice, cacheHit CacheHitSettings, prompt int64) float64 {
	total := float64(prompt)
	if !cacheHit.IsEnabled() {
		return total / 1_000_000 * price.InputPrice
	}
	hit := total * cacheHit.HitRate() / 100
	miss := total - hit
	return hit/1_000_000*price.InputPrice + miss/1_000_000*price.MissInputPrice()
}

// matchModelPrice 按模型名匹配单价：先精确匹配（忽略大小写），否则取
// 「以-开头 / 以-结尾 / 夹在两个-之间」的最长候选，例如 "v4-pro" 可以
// 匹配 "deepseek-v4-pro"。
func matchModelPrice(models map[string]ModelPrice, model string) (ModelPrice, bool) {
	lowered := strings.ToLower(strings.TrimSpace(model))
	if price, ok := models[lowered]; ok {
		return price, true
	}

	type candidate struct {
		key    string
		price  ModelPrice
		length int
	}
	var candidates []candidate
	for key, price := range models {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized == "" {
			continue
		}
		if strings.HasPrefix(lowered, normalized+"-") ||
			strings.HasSuffix(lowered, "-"+normalized) ||
			strings.Contains(lowered, "-"+normalized+"-") {
			candidates = append(candidates, candidate{key: key, price: price, length: len(normalized)})
		}
	}
	if len(candidates) == 0 {
		return ModelPrice{}, false
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].length != candidates[j].length {
			return candidates[i].length > candidates[j].length
		}
		return candidates[i].key < candidates[j].key
	})
	return candidates[0].price, true
}

// isPeakHour 判断请求时刻是否落在波峰时段。波峰时段与日期分桶、用量页
// 展示口径一致，统一按 GMT+8 解释，避免服务器本地时区影响计价。
// 「排除周末及节假日」开启时，周末与法定节假日全程按空闲价计价。
func isPeakHour(peak PeakSettings, at time.Time) bool {
	gmt8Time := at.In(gmt8)
	if peak.WeekendNormal && isNonWorkday(gmt8Time) {
		return false
	}
	minutes := gmt8Time.Hour()*60 + gmt8Time.Minute()
	return timeRangeContains(peak.Start1, peak.End1, minutes) || timeRangeContains(peak.Start2, peak.End2, minutes)
}

// isNonWorkday 判断 GMT+8 的日历日是否属于「周末及节假日」。周末固定算空闲，
// 周末之外的法定节假日（例如落在工作日的春节、国庆）同样全程空闲。
// chinesecalendar 的节假日数据只覆盖到 2026-10-10，超出覆盖范围时 IsHoliday
// 恒为 false，于是自动退化为「仅周末空闲」的历史行为，不会把周末重新算成波峰。
func isNonWorkday(t time.Time) bool {
	switch t.Weekday() {
	case time.Saturday, time.Sunday:
		return true
	default:
		return chinesecalendar.IsHoliday(chineseCalendarDate(t))
	}
}

// chineseCalendarDate 把日期重新落到 time.Local：chinesecalendar 的日期索引按
// time.Local 构建，直接传 GMT+8 的 time.Time 会因 Location 不同导致内部 map
// 查找全部落空，落在工作日的法定节假日会被误判成普通工作日。这里只取年月日，
// 时刻与时区偏移都不参与判断。
func chineseCalendarDate(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
}

func timeRangeContains(start, end string, minutes int) bool {
	startMinutes, err := clockMinutes(start)
	if err != nil {
		return false
	}
	endMinutes, err := clockMinutes(end)
	if err != nil {
		return false
	}
	return minutes >= startMinutes && minutes < endMinutes
}

// ParseUsage 从 OpenAI/Claude/Gemini 风格的 usage map 中提取
// prompt/completion/reasoning/total tokens。
func ParseUsage(usage map[string]any) (prompt, completion, reasoning, total int64) {
	if usage == nil {
		return 0, 0, 0, 0
	}
	prompt = firstInt(usage, "prompt_tokens", "input_tokens")
	completion = firstInt(usage, "completion_tokens", "output_tokens")
	reasoning = firstInt(usage, "reasoning_tokens")

	if details, ok := usage["completion_tokens_details"].(map[string]any); ok {
		reasoning = firstInt(details, "reasoning_tokens")
		if reasoning == 0 {
			reasoning = firstInt(usage, "reasoning_tokens")
		}
	}

	total = firstInt(usage, "total_tokens")
	if total == 0 {
		total = prompt + completion
	}
	if total < prompt+completion {
		total = prompt + completion
	}
	return prompt, completion, reasoning, total
}

func firstInt(source map[string]any, keys ...string) int64 {
	for _, key := range keys {
		value, ok := source[key]
		if !ok || value == nil {
			continue
		}
		if n := toInt64(value); n != 0 {
			return n
		}
	}
	return 0
}

func toInt64(value any) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case int:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	case json.Number:
		n, _ := v.Int64()
		return n
	case string:
		var n int64
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n); err == nil {
			return n
		}
	}
	return 0
}
