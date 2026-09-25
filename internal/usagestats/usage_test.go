package usagestats

import (
	"path/filepath"
	"testing"
	"time"
)

// gmt8Date 构造一个 GMT+8 的日期（时刻固定为当天 10:00，落在默认波峰窗口内）。
func gmt8Date(year int, month time.Month, day, hour int) time.Time {
	return time.Date(year, month, day, hour, 0, 0, 0, gmt8)
}

// TestIsNonWorkday 覆盖「排除周末及节假日」的日期判定：周末、落在工作日的
// 法定节假日都算非工作日；调休上班的周末仍按周末豁免（保持历史口径）；
// 超出 chinesecalendar 覆盖范围的年份退化为「仅周末」。
func TestIsNonWorkday(t *testing.T) {
	cases := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"spring festival on friday", gmt8Date(2026, time.February, 20, 10), true},
		{"spring festival on monday", gmt8Date(2026, time.February, 23, 10), true},
		{"labour day on tuesday", gmt8Date(2026, time.May, 5, 10), true},
		{"national day on tuesday", gmt8Date(2026, time.October, 6, 10), true},
		{"new year on friday", gmt8Date(2026, time.January, 2, 10), true},
		{"mid-autumn festival on friday", gmt8Date(2026, time.September, 25, 10), true},
		{"ordinary tuesday", gmt8Date(2026, time.February, 24, 10), false},
		{"ordinary tuesday in march", gmt8Date(2026, time.March, 10, 10), false},
		{"ordinary saturday", gmt8Date(2026, time.January, 3, 10), true},
		// 2026-01-04 与 2026-02-28 是官方调休上班日，但「排除周末」按星期判定，
		// 与改造前保持一致，仍然算非工作日。
		{"in-lieu workday on sunday", gmt8Date(2026, time.January, 4, 10), true},
		{"in-lieu workday on saturday", gmt8Date(2026, time.February, 28, 10), true},
		// chinesecalendar 只覆盖到 2026-10-10，之后的日期回退到星期判断。
		{"unsupported year weekday", gmt8Date(2027, time.March, 10, 10), false},
		{"unsupported year saturday", gmt8Date(2027, time.March, 13, 10), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNonWorkday(tc.at.In(gmt8)); got != tc.want {
				t.Fatalf("isNonWorkday(%s %s) = %v, want %v",
					tc.at.Format("2006-01-02"), tc.at.Weekday(), got, tc.want)
			}
		})
	}
}

// TestIsPeakHourExcludesWeekendHoliday 覆盖波峰判定在「排除周末及节假日」
// 开关两种状态下的表现。
func TestIsPeakHourExcludesWeekendHoliday(t *testing.T) {
	peak := DefaultSettings().Peak // 09:00-12:00 / 14:00-18:00，倍率 2
	peakOff := peak
	peakOff.WeekendNormal = false

	cases := []struct {
		name string
		at   time.Time
		peak PeakSettings
		want bool
	}{
		{"holiday weekday inside window", gmt8Date(2026, time.February, 20, 10), peak, false},
		{"holiday weekday second window", gmt8Date(2026, time.October, 1, 15), peak, false},
		{"holiday weekday when exemption off", gmt8Date(2026, time.February, 20, 10), peakOff, true},
		{"ordinary weekday inside window", gmt8Date(2026, time.February, 24, 10), peak, true},
		{"ordinary weekday outside window", gmt8Date(2026, time.February, 24, 13), peak, false},
		{"weekend inside window", gmt8Date(2026, time.January, 3, 10), peak, false},
		{"weekend when exemption off", gmt8Date(2026, time.January, 3, 10), peakOff, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPeakHour(tc.peak, tc.at); got != tc.want {
				t.Fatalf("isPeakHour(%s %s) = %v, want %v",
					tc.at.Format("2006-01-02"), tc.at.Weekday(), got, tc.want)
			}
		})
	}
}

// TestRecordBillsHolidayAtIdlePrice 覆盖端到端计价：法定节假日即使落在默认
// 波峰窗口内，也按空闲价结算。
func TestRecordBillsHolidayAtIdlePrice(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "usage.json"))
	defer store.Close()

	usage := map[string]any{"prompt_tokens": 1_000_000, "completion_tokens": 1_000_000}
	// 2026-02-20（周五）是春节假期，10:00 落在波峰窗口内。
	store.Record("deepseek-v4.1-flash", "caller-holiday", usage, gmt8Date(2026, time.February, 20, 10))
	// 2026-02-24（周二）是普通工作日，同一时刻按波峰价结算。
	store.Record("deepseek-v4.1-flash", "caller-workday", usage, gmt8Date(2026, time.February, 24, 10))

	entries := store.Summary()
	if len(entries) != 2 {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	byCaller := map[string]Entry{}
	for _, entry := range entries {
		byCaller[entry.CallerID] = entry
	}
	idle := defaultInputCostPerMillion + 4
	if got := byCaller["caller-holiday"].Cost; got < idle-0.001 || got > idle+0.001 {
		t.Fatalf("holiday cost = %v, want idle %v", got, idle)
	}
	if got := byCaller["caller-workday"].Cost; got < idle*2-0.001 || got > idle*2+0.001 {
		t.Fatalf("workday cost = %v, want peak %v", got, idle*2)
	}
}
