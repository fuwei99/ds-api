// Explicit .js keeps this module importable by Node's ESM resolver, which
// chartRegistration.test.mjs relies on to render the real chart options.
import { dayLabel } from './usageUtils.js'

export function makeXAxisLabelFormatter(daily) {
    const len = daily.length
    if (!len) return () => ''
    const showCount = len <= 7 ? 3 : 4
    const step = Math.max(1, Math.ceil((len - 1) / (showCount - 1)))
    const indexes = new Set()
    for (let i = 0; i < len; i += step) indexes.add(i)
    indexes.add(len - 1)
    return (value, index) => indexes.has(index) ? dayLabel(value) : ''
}

function axisMax(raw) {
    const value = Number(raw)
    if (!Number.isFinite(value) || !(value > 0)) return 1
    const lower = value / 0.8
    const upper = value / 0.65
    const pow = Math.pow(10, Math.floor(Math.log10(lower)))
    const stops = [1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6, 7, 8, 10]
    for (const multiplier of stops) {
        const candidate = multiplier * pow
        if (candidate >= lower && candidate <= upper) return candidate
    }
    return value * 1.35
}

function baseAxis(labelFormatter, dark = false) {
    return {
        axisLine: { lineStyle: { color: dark ? 'rgba(255,255,255,0.14)' : '#D2D8E5' } },
        axisTick: { show: false },
        axisLabel: {
            color: dark ? 'rgba(235,238,245,0.72)' : 'rgba(2,14,54,0.6)',
            fontSize: 12,
            fontFamily: 'Inter, "PingFang SC", "Microsoft YaHei", sans-serif',
            margin: 12,
            showMaxLabel: true,
            interval: 0,
            formatter: labelFormatter,
        },
        splitLine: { show: false },
    }
}

// buildStackedOption builds a stacked bar chart. `series` items provide
// { name, color, valueOf(dailyEntry), valueOfCost?(dailyEntry) }; pass
// costMode=true to chart the cost channels instead of token counts.
export function buildStackedOption({ daily, series, dark = false, costMode = false, fmt, fmtCompact, fmtMoney, animate = true }) {
    const rawMax = daily.reduce((max, d) => {
        const sum = series.reduce((total, s) => total + (costMode ? (s.valueOfCost ? s.valueOfCost(d) : 0) : (s.valueOf(d) || 0)), 0)
        return sum > max ? sum : max
    }, 0)
    const maxDaily = rawMax > 0 ? axisMax(rawMax) : 1

    return {
        animation: animate,
        animationDuration: animate ? 260 : 0,
        animationEasing: 'cubicOut',
        backgroundColor: 'transparent',
        tooltip: {
            trigger: 'axis',
            axisPointer: {
                type: 'line',
                snap: true,
                lineStyle: { color: dark ? 'rgba(255,255,255,0.35)' : 'rgba(2,14,54,0.28)', width: 1, type: 'dashed' },
                label: { show: false },
            },
            backgroundColor: dark ? 'rgba(18,22,28,0.96)' : '#fff',
            borderColor: dark ? 'rgba(255,255,255,0.12)' : '#E5E8EF',
            borderWidth: 1,
            padding: [14, 24],
            textStyle: { color: dark ? '#f2f4f8' : '#0f1115', fontSize: 14 },
            extraCssText: 'border-radius:12px;box-shadow:0 12px 34px rgba(0,0,0,0.16);',
            order: 'seriesDesc',
            valueFormatter: (v) => costMode ? fmtMoney(v) : fmt(v),
        },
        grid: { left: 50, right: 18, top: 18, bottom: 36 },
        xAxis: Object.assign(baseAxis(makeXAxisLabelFormatter(daily), dark), {
            type: 'category',
            data: daily.map((d) => d.key),
        }),
        yAxis: Object.assign(baseAxis(null, dark), {
            type: 'value',
            min: 0,
            max: maxDaily,
            interval: maxDaily / 2,
            splitLine: { show: true, lineStyle: { color: dark ? 'rgba(255,255,255,0.08)' : 'rgba(2,14,54,0.08)', type: 'solid' } },
            axisLabel: Object.assign(baseAxis(null, dark).axisLabel, {
                formatter: (v) => {
                    if (!costMode) return fmtCompact(v)
                    const n = Number(v || 0)
                    if (Number.isInteger(n)) return String(n)
                    const decimals = Math.abs(n) >= 1 ? 2 : 4
                    return n.toFixed(decimals).replace(/\.?0+$/, '')
                },
            }),
        }),
        series: series.map((s, idx) => ({
            name: s.name,
            type: 'bar',
            stack: 'total',
            barMaxWidth: 36,
            animationDelay: animate ? idx * 260 : 0,
            emphasis: { focus: 'series' },
            itemStyle: { color: s.color, borderRadius: idx === series.length - 1 ? [6, 6, 0, 0] : 0 },
            data: daily.map((d) => costMode ? (s.valueOfCost ? s.valueOfCost(d) : 0) : (s.valueOf(d) || 0)),
        })),
    }
}

export function buildLineOption({ daily, dark = false, name = 'API Requests', fmt, animate = true }) {
    const rawCalls = daily.reduce((max, d) => (d.calls > max ? d.calls : max), 0)
    const maxCalls = rawCalls > 0 ? axisMax(rawCalls) : 1
    return {
        animation: animate,
        animationDuration: animate ? 300 : 0,
        backgroundColor: 'transparent',
        tooltip: {
            trigger: 'axis',
            axisPointer: {
                type: 'line',
                snap: true,
                lineStyle: { color: dark ? 'rgba(255,255,255,0.35)' : 'rgba(2,14,54,0.28)', width: 1, type: 'dashed' },
                label: { show: false },
            },
            backgroundColor: dark ? 'rgba(18,22,28,0.96)' : '#fff',
            borderColor: dark ? 'rgba(255,255,255,0.12)' : '#E5E8EF',
            borderWidth: 1,
            padding: [14, 24],
            textStyle: { color: dark ? '#f2f4f8' : '#0f1115', fontSize: 14 },
            extraCssText: 'border-radius:12px;box-shadow:0 12px 34px rgba(0,0,0,0.16);',
            valueFormatter: (v) => fmt(v),
        },
        grid: { left: 50, right: 18, top: 18, bottom: 36 },
        xAxis: Object.assign(baseAxis(makeXAxisLabelFormatter(daily), dark), {
            type: 'category',
            data: daily.map((d) => d.key),
        }),
        yAxis: Object.assign(baseAxis(null, dark), {
            type: 'value',
            min: 0,
            max: maxCalls,
            interval: maxCalls / 2,
            splitLine: { show: true, lineStyle: { color: dark ? 'rgba(255,255,255,0.08)' : 'rgba(2,14,54,0.08)', type: 'solid' } },
            axisLabel: Object.assign(baseAxis(null, dark).axisLabel, { formatter: (v) => fmt(v) }),
        }),
        series: [{
            name,
            type: 'line',
            smooth: 0.85,
            smoothMonotone: 'none',
            showSymbol: false,
            symbol: 'circle',
            symbolSize: 7,
            itemStyle: { color: '#fff', borderColor: '#3964FE', borderWidth: 2 },
            emphasis: { showSymbol: true, itemStyle: { color: '#fff', borderColor: '#3964FE', borderWidth: 2 } },
            lineStyle: { color: '#3964FE', width: 1.6 },
            areaStyle: { color: 'rgba(57,100,254,0.14)' },
            data: daily.map((d) => d.calls),
        }],
    }
}
