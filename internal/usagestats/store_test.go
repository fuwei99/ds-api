package usagestats

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordAndSummary(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "usage.json"))
	defer store.Close()

	at := time.Date(2026, 8, 18, 2, 30, 0, 0, time.UTC) // GMT+8: 2026-08-18
	store.Record("deepseek-v4.1-flash", "caller-a", map[string]any{"prompt_tokens": 100, "completion_tokens": 20}, at)
	store.Record("DeepSeek-V4.1-Flash", "caller-b", map[string]any{"prompt_tokens": 50, "completion_tokens": 30}, at)
	store.Record("deepseek-v4.1-flash-search", "", map[string]any{"input_tokens": int64(10), "output_tokens": int64(5)}, at.Add(24*time.Hour))
	store.Record("deepseek-v4.1-flash", "caller-a", nil, at)

	entries := store.Summary()
	if len(entries) != 3 {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	first := entries[0]
	if first.Date != "2026-08-19" || first.Model != "deepseek-v4.1-flash-search" || first.CallerID != "unknown" {
		t.Fatalf("unexpected newest entry: %+v", first)
	}
	second := entries[1]
	if second.Model != "deepseek-v4.1-flash" || second.CallerID != "caller-a" {
		t.Fatalf("unexpected caller split: %+v", second)
	}
	if second.PromptTokens != 100 || second.CompletionTokens != 20 || second.TotalTokens != 120 || second.Calls != 1 {
		t.Fatalf("unexpected aggregation: %+v", second)
	}
	third := entries[2]
	if third.CallerID != "caller-b" || third.PromptTokens != 50 || third.CompletionTokens != 30 {
		t.Fatalf("unexpected caller-b entry: %+v", third)
	}
}

func TestRecordReasoningTokens(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "usage.json"))
	defer store.Close()

	at := time.Date(2026, 8, 18, 2, 30, 0, 0, time.UTC)
	store.Record("deepseek-v4.1-flash", "caller-a", map[string]any{
		"prompt_tokens":     100,
		"completion_tokens": 60,
		"completion_tokens_details": map[string]any{
			"reasoning_tokens": 40,
		},
		"total_tokens": 160,
	}, at)

	entries := store.Summary()
	if len(entries) != 1 {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	entry := entries[0]
	if entry.ReasoningTokens != 40 {
		t.Fatalf("unexpected reasoning tokens: %+v", entry)
	}
	if entry.TotalTokens != 160 {
		t.Fatalf("unexpected total tokens: %+v", entry)
	}
}

func TestPersistAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	store := New(path)
	store.Record("deepseek-v4.1-flash", "caller-a", map[string]any{"prompt_tokens": 7, "completion_tokens": 3}, time.Now())
	store.Close()

	reloaded := New(path)
	defer reloaded.Close()
	entries := reloaded.Summary()
	if len(entries) != 1 {
		t.Fatalf("unexpected reloaded entries: %+v", entries)
	}
	entry := entries[0]
	if entry.PromptTokens != 7 || entry.CompletionTokens != 3 || entry.Calls != 1 {
		t.Fatalf("unexpected reloaded entry: %+v", entry)
	}
}

func TestLoadCorruptFileStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := New(path)
	defer store.Close()
	if entries := store.Summary(); len(entries) != 0 {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}

func TestFileShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	store := New(path)
	store.Record("gpt-4o", "caller-a", map[string]any{"prompt_tokens": 12, "completion_tokens": 4}, time.Now())
	store.Close()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["version"] != float64(FileVersion) {
		t.Fatalf("unexpected version: %v", payload["version"])
	}
	entries, ok := payload["entries"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("unexpected entries: %v", payload["entries"])
	}
}

func TestLoadLegacyV1DayModelFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	legacy := `{
  "version": 1,
  "updated_at": 1788784141151,
  "totals": {"requests": 1, "input_tokens": 19, "output_tokens": 54},
  "days": {
    "2026-09-07": {
      "date": "2026-09-07",
      "requests": 1,
      "input_tokens": 19,
      "output_tokens": 54,
      "models": {
        "deepseek-v4-flash": {"requests": 1, "input_tokens": 19, "output_tokens": 54}
      }
    }
  }
}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	store := New(path)
	defer store.Close()
	entries := store.Summary()
	if len(entries) != 1 {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	entry := entries[0]
	if entry.Date != "2026-09-07" || entry.Model != "deepseek-v4-flash" || entry.CallerID != "unknown" {
		t.Fatalf("unexpected migrated entry: %+v", entry)
	}
	if entry.PromptTokens != 19 || entry.CompletionTokens != 54 || entry.TotalTokens != 73 || entry.Calls != 1 {
		t.Fatalf("unexpected migrated totals: %+v", entry)
	}
}

func TestLoadLegacyArrayFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	legacy := `[{"date":"2026-09-07","model":"deepseek-v4-pro","caller_id":"caller-a","prompt_tokens":10,"completion_tokens":5,"reasoning_tokens":2,"total_tokens":15,"cost":0.01,"calls":1}]`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	store := New(path)
	defer store.Close()
	entries := store.Summary()
	if len(entries) != 1 {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	entry := entries[0]
	if entry.Model != "deepseek-v4-pro" || entry.CallerID != "caller-a" || entry.ReasoningTokens != 2 || entry.Cost != 0.01 {
		t.Fatalf("unexpected legacy entry: %+v", entry)
	}
}

func TestBackfillOnlyWhenEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	store := New(path)
	defer store.Close()

	at := time.Date(2026, 8, 18, 2, 30, 0, 0, time.UTC)
	items := []BackfillItem{{
		Timestamp:  at.UnixMilli(),
		Model:      "deepseek-v4.1-flash",
		CallerID:   "caller-a",
		Prompt:     100,
		Completion: 20,
		Reasoning:  5,
		Total:      120,
	}}
	applied, err := store.Backfill(items)
	if err != nil || !applied {
		t.Fatalf("backfill failed: applied=%v err=%v", applied, err)
	}
	entries := store.Summary()
	if len(entries) != 1 || entries[0].Calls != 1 || entries[0].ReasoningTokens != 5 {
		t.Fatalf("unexpected backfilled entries: %+v", entries)
	}

	// 账本非空时再次回填必须被拒绝，避免重复计数。
	store.Record("deepseek-v4.1-flash", "caller-a", map[string]any{"prompt_tokens": 1, "completion_tokens": 1}, at)
	applied, err = store.Backfill(items)
	if err != nil || applied {
		t.Fatalf("backfill should be skipped: applied=%v err=%v", applied, err)
	}
}

func TestClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	store := New(path)
	store.Record("deepseek-v4.1-flash", "caller-a", map[string]any{"prompt_tokens": 7, "completion_tokens": 3}, time.Now())
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if entries := store.Summary(); len(entries) != 0 {
		t.Fatalf("unexpected entries after clear: %+v", entries)
	}
	store.Close()
	reloaded := New(path)
	defer reloaded.Close()
	if entries := reloaded.Summary(); len(entries) != 0 {
		t.Fatalf("unexpected entries after reload: %+v", entries)
	}
}

// defaultInputCostPerMillion 是默认设置下每 100 万输入 tokens 的费用：默认
// 「模拟缓存命中」开启、命中率 98%，其中 98% 按命中价 0.02、2% 按未命中价 1
// 计价。写成字面量而不是调用 inputCost，避免用被测实现推导期望值。
const defaultInputCostPerMillion = 0.98*0.02 + 0.02*1

func TestCostComputation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	store := New(path)
	defer store.Close()

	// 模型合并后默认只有 deepseek-v4.1-flash 系列，单价 input 0.02 /
	// input_miss 1 / output 4 per 1M tokens（默认波峰 09:00-12:00、14:00-18:00，
	// 倍率 2，周末与法定节假日按空闲价计价）。
	peak := time.Date(2026, 8, 19, 10, 0, 0, 0, gmt8)    // 周三 10:00 GMT+8，波峰
	offPeak := time.Date(2026, 8, 19, 13, 0, 0, 0, gmt8) // 周三 13:00 GMT+8，非波峰

	store.Record("deepseek-v4.1-flash", "caller-a", map[string]any{"prompt_tokens": 1_000_000, "completion_tokens": 1_000_000}, offPeak)
	store.Record("deepseek-v4.1-flash", "caller-b", map[string]any{"prompt_tokens": 1_000_000, "completion_tokens": 1_000_000}, peak)

	entries := store.Summary()
	if len(entries) != 2 {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	byCaller := map[string]Entry{}
	for _, entry := range entries {
		byCaller[entry.CallerID] = entry
	}
	wantOffPeak := defaultInputCostPerMillion + 4
	wantPeak := wantOffPeak * 2
	if got := byCaller["caller-a"].Cost; got < wantOffPeak-0.001 || got > wantOffPeak+0.001 {
		t.Fatalf("unexpected off-peak cost: %v", got)
	}
	if got := byCaller["caller-b"].Cost; got < wantPeak-0.001 || got > wantPeak+0.001 {
		t.Fatalf("unexpected peak cost: %v", got)
	}
}

// TestMergedModelVariantsSharePrice 覆盖模型合并后的全部公开模型 ID：-search
// 与 -nothinking 变体都必须能匹配到默认单价，且 -search 命中自己的键。
func TestMergedModelVariantsSharePrice(t *testing.T) {
	models := DefaultSettings().Models
	for _, model := range []string{
		"deepseek-v4.1-flash",
		"deepseek-v4.1-flash-nothinking",
		"deepseek-v4.1-flash-search",
		"deepseek-v4.1-flash-search-nothinking",
	} {
		price, ok := matchModelPrice(models, model)
		if !ok {
			t.Fatalf("%s did not match any default price", model)
		}
		if price.InputPrice != 0.02 || price.OutputPrice != 4 {
			t.Fatalf("%s matched unexpected price: %+v", model, price)
		}
	}
	// 单价表只保留合并后的那一条，不再出现模型合并前的档位键。
	if len(models) != 1 {
		t.Fatalf("price table must hold a single entry: %+v", models)
	}
	if _, ok := models[mergedModelPriceKey]; !ok {
		t.Fatalf("price table must be keyed by %q: %+v", mergedModelPriceKey, models)
	}
}

func TestMatchModelPrice(t *testing.T) {
	models := map[string]ModelPrice{
		"v4-pro": {InputPrice: 1, OutputPrice: 2},
	}
	if price, ok := matchModelPrice(models, "deepseek-v4-pro"); !ok || price.InputPrice != 1 {
		t.Fatalf("unexpected prefix-suffix match: %+v %v", price, ok)
	}
	if _, ok := matchModelPrice(models, "deepseek-v4"); ok {
		t.Fatal("v4 without pro should not match v4-pro")
	}
	if price, ok := matchModelPrice(models, "V4-PRO"); !ok || price.OutputPrice != 2 {
		t.Fatalf("unexpected case-insensitive match: %+v %v", price, ok)
	}
}

func TestSaveSettingsValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	store := New(path)
	defer store.Close()

	if err := store.SaveSettings(UsageSettings{Peak: PeakSettings{Enabled: true, Start1: "12:00", End1: "09:00"}}); !errors.Is(err, ErrInvalidUsageSettings) {
		t.Fatalf("expected ErrInvalidUsageSettings, got %v", err)
	}
	if err := store.SaveSettings(UsageSettings{Peak: PeakSettings{Enabled: true, Start1: "25:00", End1: "26:00"}}); !errors.Is(err, ErrInvalidUsageSettings) {
		t.Fatalf("expected ErrInvalidUsageSettings for invalid clock, got %v", err)
	}

	settings := UsageSettings{
		Models: map[string]ModelPrice{mergedModelPriceKey: {InputPrice: 0.2, OutputPrice: 14}},
	}
	if err := store.SaveSettings(settings); err != nil {
		t.Fatalf("save settings failed: %v", err)
	}
	loaded := store.Settings()
	if loaded.Models[mergedModelPriceKey].InputPrice != 0.2 {
		t.Fatalf("unexpected saved settings: %+v", loaded)
	}
	if len(loaded.Models) != 1 {
		t.Fatalf("price table must hold a single entry: %+v", loaded.Models)
	}

	reloaded := New(path)
	defer reloaded.Close()
	if reloaded.Settings().Models[mergedModelPriceKey].OutputPrice != 14 {
		t.Fatalf("settings not persisted: %+v", reloaded.Settings())
	}
}

// TestSaveSettingsBackfillsDefaultPrice 覆盖未提供单价时的默认值补齐。
func TestSaveSettingsBackfillsDefaultPrice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	store := New(path)
	defer store.Close()

	if err := store.SaveSettings(UsageSettings{Enabled: true}); err != nil {
		t.Fatalf("save settings failed: %v", err)
	}
	loaded := store.Settings()
	if len(loaded.Models) != 1 {
		t.Fatalf("price table must hold a single entry: %+v", loaded.Models)
	}
	if got := loaded.Models[mergedModelPriceKey]; got.InputPrice != 0.02 || got.OutputPrice != 4 {
		t.Fatalf("expected default price, got %+v", got)
	}
}

// TestSettingsCollapseToSingleModelPrice 覆盖单价表收敛为唯一一条目：合并后
// 所有模型共用同一套单价，因此旧档位键、一度单列的 search 键以及手改写入的
// 其他模型键都会被折叠掉，设置页只剩一组输入/输出输入框。
func TestSettingsCollapseToSingleModelPrice(t *testing.T) {
	peak := `"peak":{"enabled":true,"start1":"09:00","end1":"12:00","start2":"14:00","end2":"18:00","multiplier":2,"weekend_normal":true}`

	cases := []struct {
		name     string
		models   string
		wantIn   float64
		wantOut  float64
		checkKey func(*testing.T, UsageSettings)
	}{
		{
			name:    "flash wins over pro",
			models:  `{"v4-pro":{"input_price":0.9,"output_price":9},"flash":{"input_price":0.03,"output_price":5}}`,
			wantIn:  0.03,
			wantOut: 5,
			checkKey: func(t *testing.T, s UsageSettings) {
				for _, legacyKey := range []string{"v4-pro", "flash", "pro", "vision", "deepseek-v4.1-flash-search"} {
					if _, ok := s.Models[legacyKey]; ok {
						t.Fatalf("legacy key %q survived collapsing: %+v", legacyKey, s.Models)
					}
				}
			},
		},
		{
			name:    "pro is inherited when it is the only legacy key",
			models:  `{"pro":{"input_price":0.2,"output_price":14}}`,
			wantIn:  0.2,
			wantOut: 14,
		},
		{
			name:    "canonical wins over the standalone search key",
			models:  `{"deepseek-v4.1-flash":{"input_price":0.04,"output_price":6},"deepseek-v4.1-flash-search":{"input_price":0.99,"output_price":99}}`,
			wantIn:  0.04,
			wantOut: 6,
			checkKey: func(t *testing.T, s UsageSettings) {
				if _, ok := s.Models["deepseek-v4.1-flash-search"]; ok {
					t.Fatalf("standalone search key survived collapsing: %+v", s.Models)
				}
			},
		},
		{
			name:    "standalone search key is inherited when canonical is absent",
			models:  `{"deepseek-v4.1-flash-search":{"input_price":0.07,"output_price":7}}`,
			wantIn:  0.07,
			wantOut: 7,
		},
		{
			name:    "unknown hand-edited keys are dropped",
			models:  `{"gpt-4o":{"input_price":9,"output_price":9}}`,
			wantIn:  0.02,
			wantOut: 4,
			checkKey: func(t *testing.T, s UsageSettings) {
				if _, ok := s.Models["gpt-4o"]; ok {
					t.Fatalf("unknown key survived collapsing: %+v", s.Models)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "usage.json")
			raw := `{"enabled":true,"models":` + tc.models + `,` + peak + `}`
			if err := os.WriteFile(path+".settings", []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			store := New(path)
			defer store.Close()

			settings := store.Settings()
			if len(settings.Models) != 1 {
				t.Fatalf("price table must hold a single entry: %+v", settings.Models)
			}
			got := settings.Models[mergedModelPriceKey]
			if got.InputPrice != tc.wantIn || got.OutputPrice != tc.wantOut {
				t.Fatalf("expected %v/%v, got %+v", tc.wantIn, tc.wantOut, got)
			}
			if tc.checkKey != nil {
				tc.checkKey(t, settings)
			}
		})
	}
}

// TestCollapsedPriceAppliesToEveryMergedVariant 确认收敛后的单一条目仍覆盖全部
// 公开模型变体，避免因为只剩一条而漏计价。
func TestCollapsedPriceAppliesToEveryMergedVariant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	legacy := `{"enabled":true,"models":{"flash":{"input_price":0.03,"output_price":5}},"peak":{"enabled":false,"start1":"09:00","end1":"12:00","start2":"14:00","end2":"18:00","multiplier":2,"weekend_normal":true}}`
	if err := os.WriteFile(path+".settings", []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	store := New(path)
	defer store.Close()

	at := time.Date(2026, 8, 19, 13, 0, 0, 0, gmt8) // 非波峰
	// 单价 0.03 / 5 且默认开启「模拟缓存命中」（命中率 98%、未命中价 1）。
	want := 0.98*0.03 + 0.02*1 + 5
	for _, model := range []string{
		"deepseek-v4.1-flash",
		"deepseek-v4.1-flash-search",
		"deepseek-v4.1-flash-nothinking",
		"deepseek-v4.1-flash-search-nothinking",
	} {
		store.Record(model, "caller-"+model, map[string]any{"prompt_tokens": 1_000_000, "completion_tokens": 1_000_000}, at)
	}
	entries := store.Summary()
	if len(entries) != 4 {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	for _, entry := range entries {
		if entry.Cost < want-0.001 || entry.Cost > want+0.001 {
			t.Fatalf("%s billed %v, want %v", entry.Model, entry.Cost, want)
		}
	}
}

func TestSummaryDoesNotRepriceLegacyEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	legacy := `[{"date":"2026-09-07","model":"deepseek-v4-pro","caller_id":"caller-a","prompt_tokens":1000000,"completion_tokens":1000000,"total_tokens":2000000,"calls":1}]`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	store := New(path)
	defer store.Close()
	entries := store.Summary()
	if len(entries) != 1 {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	// 费用只在记录时刻估算（含波峰），历史条目不做追溯重算。
	if entries[0].Cost != 0 {
		t.Fatalf("cost must stay as recorded, got %v", entries[0].Cost)
	}
}

func TestZeroPriceMeansFree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	store := New(path)
	defer store.Close()

	settings := store.Settings()
	// 「单价 0 表示不计费」现在要同时把命中价与未命中价都设为 0；只设命中价时
	// 未命中部分仍按默认的 1 元计价。
	settings.Models[mergedModelPriceKey] = ModelPrice{
		InputPrice:     0,
		InputMissPrice: float64Ptr(0),
		OutputPrice:    0,
	}
	if err := store.SaveSettings(settings); err != nil {
		t.Fatalf("save settings failed: %v", err)
	}
	if got := store.Settings().Models[mergedModelPriceKey]; got.InputPrice != 0 || got.OutputPrice != 0 || got.MissInputPrice() != 0 {
		t.Fatalf("zero price must be preserved: %+v", got)
	}

	at := time.Date(2026, 8, 19, 10, 0, 0, 0, gmt8) // 波峰时段
	store.Record("deepseek-v4.1-flash", "caller-a", map[string]any{"prompt_tokens": 1_000_000, "completion_tokens": 1_000_000}, at)
	entries := store.Summary()
	if len(entries) != 1 || entries[0].Cost != 0 {
		t.Fatalf("zero price must not be billed: %+v", entries)
	}
}

func TestSaveSettingsRejectsNegativePrices(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "usage.json"))
	defer store.Close()

	settings := store.Settings()
	settings.Models["pro"] = ModelPrice{InputPrice: -1, OutputPrice: 13.5}
	if err := store.SaveSettings(settings); !errors.Is(err, ErrInvalidUsageSettings) {
		t.Fatalf("expected ErrInvalidUsageSettings, got %v", err)
	}

	settings.Models["pro"] = ModelPrice{InputPrice: 1, OutputPrice: 13.5, InputMissPrice: float64Ptr(-0.5)}
	if err := store.SaveSettings(settings); !errors.Is(err, ErrInvalidUsageSettings) {
		t.Fatalf("expected ErrInvalidUsageSettings for negative cache-miss price, got %v", err)
	}
}

func TestPeakWindowUsesGMT8(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "usage.json"))
	defer store.Close()

	// UTC 02:00 = GMT+8 10:00（周三，默认波峰时段）。无论服务器本地时区
	// 如何都必须按波峰计价。
	at := time.Date(2026, 8, 19, 2, 0, 0, 0, time.UTC)
	store.Record("deepseek-v4.1-flash", "caller-a", map[string]any{"prompt_tokens": 1_000_000, "completion_tokens": 1_000_000}, at)
	entries := store.Summary()
	if len(entries) != 1 {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	want := (defaultInputCostPerMillion + 4) * 2
	if got := entries[0].Cost; got < want-0.001 || got > want+0.001 {
		t.Fatalf("expected peak cost resolved in GMT+8, got %v", got)
	}
}

// TestCacheHitPricing 覆盖「模拟缓存命中」的计价：开启后输入 tokens 按命中率
// 拆成命中 / 未命中两部分分别计价，关闭时退化为改造前的单一输入单价。
func TestCacheHitPricing(t *testing.T) {
	offPeak := time.Date(2026, 8, 19, 13, 0, 0, 0, gmt8) // 周三 13:00 GMT+8，非波峰

	cases := []struct {
		name     string
		cacheHit CacheHitSettings
		want     float64
	}{
		{"unset falls back to 98% hit", CacheHitSettings{}, 0.98*0.02 + 0.02*2},
		{"disabled bills the whole input at the hit price", CacheHitSettings{Enabled: boolPtr(false)}, 0.02},
		{"50% hit", CacheHitSettings{Enabled: boolPtr(true), Rate: float64Ptr(50)}, 0.5*0.02 + 0.5*2},
		{"0% hit bills everything as a miss", CacheHitSettings{Enabled: boolPtr(true), Rate: float64Ptr(0)}, 2},
		{"100% hit bills everything as a hit", CacheHitSettings{Enabled: boolPtr(true), Rate: float64Ptr(100)}, 0.02},
		{"rate above 100 is clamped", CacheHitSettings{Enabled: boolPtr(true), Rate: float64Ptr(150)}, 0.02},
		{"negative rate is clamped to 0", CacheHitSettings{Enabled: boolPtr(true), Rate: float64Ptr(-10)}, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := New(filepath.Join(t.TempDir(), "usage.json"))
			defer store.Close()

			// 输出单价设为 0，让期望值只反映输入的拆分口径。
			settings := DefaultSettings()
			settings.Models[mergedModelPriceKey] = ModelPrice{
				InputPrice:     0.02,
				InputMissPrice: float64Ptr(2),
				OutputPrice:    0,
			}
			settings.CacheHit = tc.cacheHit
			if err := store.SaveSettings(settings); err != nil {
				t.Fatalf("save settings failed: %v", err)
			}
			store.Record("deepseek-v4.1-flash", "caller-a", map[string]any{"prompt_tokens": 1_000_000}, offPeak)

			entries := store.Summary()
			if len(entries) != 1 {
				t.Fatalf("unexpected entries: %+v", entries)
			}
			if got := entries[0].Cost; got < tc.want-0.001 || got > tc.want+0.001 {
				t.Fatalf("cost = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCacheHitSettingsDefaultsAndPersistence 覆盖「模拟缓存命中」的默认值与
// 持久化：旧设置文件（没有 cache_hit 段）必须回落默认（开启 + 98%），显式
// 关闭与显式填 0 必须原样保留，而不是被默认值覆盖。
func TestCacheHitSettingsDefaultsAndPersistence(t *testing.T) {
	t.Run("legacy file without cache_hit falls back to defaults", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "usage.json")
		legacy := `{"enabled":true,"models":{"deepseek-v4.1-flash":{"input_price":0.02,"output_price":4}},` +
			`"peak":{"enabled":false,"start1":"09:00","end1":"12:00","start2":"14:00","end2":"18:00","multiplier":2,"weekend_normal":true}}`
		if err := os.WriteFile(path+".settings", []byte(legacy), 0o644); err != nil {
			t.Fatal(err)
		}
		store := New(path)
		defer store.Close()

		settings := store.Settings()
		if !settings.CacheHit.IsEnabled() || settings.CacheHit.HitRate() != defaultCacheHitRate {
			t.Fatalf("expected default cache hit settings, got %+v", settings.CacheHit)
		}
		// 旧文件没有未命中单价，必须回落默认的 1 元而不是当成 0（免费）。
		if got := settings.Models[mergedModelPriceKey].MissInputPrice(); got != defaultInputMissPrice {
			t.Fatalf("expected default cache-miss price, got %v", got)
		}
	})

	t.Run("explicit disable and zero values survive a reload", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "usage.json")
		store := New(path)
		settings := DefaultSettings()
		settings.CacheHit = CacheHitSettings{Enabled: boolPtr(false), Rate: float64Ptr(0)}
		settings.Models[mergedModelPriceKey] = ModelPrice{
			InputPrice:     0.02,
			InputMissPrice: float64Ptr(0),
			OutputPrice:    4,
		}
		if err := store.SaveSettings(settings); err != nil {
			t.Fatalf("save settings failed: %v", err)
		}
		store.Close()

		reloaded := New(path)
		defer reloaded.Close()
		got := reloaded.Settings()
		if got.CacheHit.IsEnabled() {
			t.Fatalf("expected cache hit disabled after reload: %+v", got.CacheHit)
		}
		if got.CacheHit.HitRate() != 0 {
			t.Fatalf("expected hit rate 0 after reload, got %v", got.CacheHit.HitRate())
		}
		if price := got.Models[mergedModelPriceKey]; price.MissInputPrice() != 0 {
			t.Fatalf("expected explicit zero cache-miss price to survive, got %+v", price)
		}
	})
}

// TestSettingsReturnsDeepCopy 确认 Settings() 返回的指针字段是副本，调用方
// 修改它不会污染 Store 内部状态。
func TestSettingsReturnsDeepCopy(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "usage.json"))
	defer store.Close()

	settings := store.Settings()
	*settings.CacheHit.Enabled = false
	*settings.CacheHit.Rate = 0
	price := settings.Models[mergedModelPriceKey]
	*price.InputMissPrice = 999

	got := store.Settings()
	if !got.CacheHit.IsEnabled() || got.CacheHit.HitRate() != defaultCacheHitRate {
		t.Fatalf("cache hit settings were mutated through the returned copy: %+v", got.CacheHit)
	}
	if p := got.Models[mergedModelPriceKey]; p.MissInputPrice() != defaultInputMissPrice {
		t.Fatalf("model price was mutated through the returned copy: %+v", p)
	}
}

func TestRecordUsageDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	store := New(path)
	defer store.Close()

	// 关闭「记录用量」必须被保存与加载流程尊重，不能被强制改回 true。
	settings := store.Settings()
	settings.Enabled = false
	if err := store.SaveSettings(settings); err != nil {
		t.Fatalf("save settings failed: %v", err)
	}
	if store.Settings().Enabled {
		t.Fatal("expected enabled=false after SaveSettings")
	}

	at := time.Date(2026, 8, 18, 2, 30, 0, 0, time.UTC)
	store.Record("deepseek-v4-pro", "caller-a", map[string]any{"prompt_tokens": 10, "completion_tokens": 5}, at)
	if entries := store.Summary(); len(entries) != 0 {
		t.Fatalf("expected no entries while usage recording is disabled: %+v", entries)
	}

	settings.Enabled = true
	if err := store.SaveSettings(settings); err != nil {
		t.Fatalf("re-enable settings failed: %v", err)
	}
	store.Record("deepseek-v4-pro", "caller-a", map[string]any{"prompt_tokens": 10, "completion_tokens": 5}, at)
	if entries := store.Summary(); len(entries) != 1 {
		t.Fatalf("expected entries after re-enabling: %+v", entries)
	}
}

func TestSettingsEnabledPersistsAcrossReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	store := New(path)
	settings := store.Settings()
	settings.Enabled = false
	if err := store.SaveSettings(settings); err != nil {
		t.Fatalf("save settings failed: %v", err)
	}
	store.Close()

	reloaded := New(path)
	defer reloaded.Close()
	if reloaded.Settings().Enabled {
		t.Fatal("expected enabled=false after reload")
	}
}

// TestImportMergesExportedEntries 覆盖导入合并：同日期 + 模型 + 调用方累加，
// 费用沿用导出端数值（不重新计价），坏日期 / 空模型的条目被跳过。
func TestImportMergesExportedEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	store := New(path)
	defer store.Close()

	at := time.Date(2026, 8, 19, 13, 0, 0, 0, gmt8)
	store.Record("deepseek-v4.1-flash", "caller-a", map[string]any{"prompt_tokens": 100, "completion_tokens": 20}, at)

	merged, err := store.Import([]Entry{
		{Date: "2026-08-19", Model: "DeepSeek-V4.1-Flash", CallerID: "caller-a", PromptTokens: 50, CompletionTokens: 10, TotalTokens: 60, Calls: 2, Cost: 1.25},
		{Date: "2026-08-20", Model: "deepseek-v4.1-flash", CallerID: "caller-b", PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10, Calls: 1, Cost: 0.5},
		{Date: "not-a-date", Model: "deepseek-v4.1-flash", PromptTokens: 1, TotalTokens: 1, Calls: 1},
		{Date: "2026-08-20", Model: "  ", PromptTokens: 1, TotalTokens: 1, Calls: 1},
	})
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if merged != 2 {
		t.Fatalf("expected 2 merged entries, got %d", merged)
	}

	entries := store.Summary()
	if len(entries) != 2 {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	var mergedEntry, newEntry Entry
	for _, entry := range entries {
		if entry.Date == "2026-08-19" {
			mergedEntry = entry
		}
		if entry.Date == "2026-08-20" {
			newEntry = entry
		}
	}
	if mergedEntry.PromptTokens != 150 || mergedEntry.CompletionTokens != 30 || mergedEntry.Calls != 3 {
		t.Fatalf("imported entry was not merged: %+v", mergedEntry)
	}
	// 费用 = 原记录的计价 + 导入条目自带的 1.25，不重算。
	if mergedEntry.Cost <= 1.25 {
		t.Fatalf("imported cost was not preserved: %+v", mergedEntry)
	}
	if newEntry.CallerID != "caller-b" || newEntry.TotalTokens != 10 || newEntry.Cost != 0.5 {
		t.Fatalf("unexpected new entry: %+v", newEntry)
	}

	// 导入必须落盘：重载后仍能看到合并结果。
	store.Close()
	reloaded := New(path)
	defer reloaded.Close()
	if entries := reloaded.Summary(); len(entries) != 2 {
		t.Fatalf("import was not persisted: %+v", entries)
	}
}
