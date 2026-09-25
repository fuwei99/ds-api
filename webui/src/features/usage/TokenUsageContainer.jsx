import { useCallback, useEffect, useMemo, useState } from 'react'
import { useI18n } from '../../i18n'
import { useTheme } from '../../themeProvider'
import './usage.css'
import EChart from './EChart'
import ModelRow from './ModelRow'
import SettingsPanel from './SettingsPanel'
import { useUsageRecords } from './useUsageRecords'
import { useUsageSettings } from './useUsageSettings'
import { useEntranceAnimation } from './useEntranceAnimation'
import { buildStackedOption } from './chartOptions'
import {
    aggregateDaily,
    buildUsageCsv,
    dayKey,
    fillDateRange,
    fmtCompact,
    fmtMoney,
    fmtNumber,
    normalizeUsage,
    parseDayKey,
    parseUsageCsv,
    shortCaller,
} from './usageUtils'

const MODEL_PALETTE = ['#FF8800', '#FF5500', '#FFAA33', '#E64400', '#FFB84D', '#F97316', '#C2410C', '#FDBA74']
const API_KEY_PALETTE = ['#A0DCFD', '#60B3FE', '#0C70F3', '#4DA3FF', '#1F6FEB', '#38BDF8', '#7DD3FC', '#1D4ED8']
const OTHER_COLOR = '#94A3B8'
const TOP_N = 8

export default function TokenUsageContainer({ authFetch, onMessage }) {
    const { lang, t } = useI18n()
    const fmt = useCallback((n) => fmtNumber(n, lang), [lang])
    const fmtMoneyLocal = useCallback((n) => fmtMoney(n, lang), [lang])

    const { records, loading, initialized, error, loadRecords, clearRecords } = useUsageRecords({ authFetch, onMessage })
    // Charts only animate when data first arrives; refreshes and filter changes
    // update in place instead of replaying the entrance animation.
    const animateCharts = useEntranceAnimation(records.length > 0)
    const {
        settingsOpen,
        setSettingsOpen,
        pricingOpen,
        setPricingOpen,
        settingsLoadState,
        saving,
        usageEnabled,
        setUsageEnabled,
        pricing,
        setPricing,
        closeSettings,
        dismissSettings,
    } = useUsageSettings({ authFetch, onMessage })

    const [dateRange, setDateRange] = useState('30')
    const [modelFilter, setModelFilter] = useState('all')
    const [chartDimension, setChartDimension] = useState('model')
    const [importing, setImporting] = useState(false)
    const [summaryMode, setSummaryMode] = useState(() => {
        try {
            const stored = localStorage.getItem('ds2api_usage_summary_mode')
            return stored === 'tokens' ? 'tokens' : 'cost'
        } catch (e) {
            return 'cost'
        }
    })

    useEffect(() => {
        try { localStorage.setItem('ds2api_usage_summary_mode', summaryMode) } catch (e) { /* ignore */ }
    }, [summaryMode])

    const { theme } = useTheme()

    // Keep the dashboard near real-time: poll in the background while the tab
    // is visible; the hook keeps stale data on screen during refresh.
    useEffect(() => {
        const timer = setInterval(() => {
            if (document.visibilityState === 'visible') loadRecords()
        }, 30000)
        return () => clearInterval(timer)
    }, [loadRecords])

    const filtered = useMemo(() => {
        let list = records.slice()
        if (dateRange !== 'all') {
            const days = Math.max(1, Number(dateRange) || 30)
            const todayStart = parseDayKey(dayKey(Date.now())).getTime()
            const cutoff = todayStart - (days - 1) * 86400000
            list = list.filter((r) => {
                const ts = Number(r.created_at || r.createdAt || 0)
                return ts > 0 && ts >= cutoff
            })
        }
        if (modelFilter !== 'all') {
            list = list.filter((r) => (r.model || 'unknown') === modelFilter)
        }
        return list
    }, [records, dateRange, modelFilter])

    const summary = useMemo(() => {
        let savedTotal = 0
        let savedCost = 0
        records.forEach((r) => {
            const u = normalizeUsage(r.usage)
            if (u) savedTotal += u.total
            savedCost += Number(r.cost || 0)
        })

        let windowTotal = 0
        let windowCost = 0
        let promptSum = 0
        let completionSum = 0
        let calls = 0
        filtered.forEach((r) => {
            const u = normalizeUsage(r.usage)
            if (!u) return
            windowTotal += u.total
            windowCost += Number(r.cost || 0)
            promptSum += u.prompt
            completionSum += u.completion
            calls += (r.calls_count || 0)
        })

        return { savedTotal, savedCost, windowTotal, windowCost, promptSum, completionSum, calls }
    }, [records, filtered])

    const chartRecords = useMemo(() => {
        if (modelFilter === 'all') return records.slice()
        return records.filter((r) => (r.model || 'unknown') === modelFilter)
    }, [records, modelFilter])

    const dailyAll = useMemo(() => fillDateRange(aggregateDaily(chartRecords), dateRange), [chartRecords, dateRange])

    const modelOptions = useMemo(() => {
        const set = new Set()
        records.forEach((r) => set.add(r.model || 'unknown'))
        return Array.from(set).sort()
    }, [records])

    // Sorted descending so the largest model/caller leads the legend; only the
    // top TOP_N get their own series, everything else rolls up into "Other".
    const topSeries = useMemo(() => {
        const categories = new Set()
        dailyAll.forEach((d) => {
            const map = chartDimension === 'model' ? d.byModel : d.byCaller
            Object.keys(map).forEach((key) => categories.add(key))
        })
        const withTotals = Array.from(categories).map((name) => {
            let total = 0
            dailyAll.forEach((d) => {
                const map = chartDimension === 'model' ? d.byModel : d.byCaller
                total += map[name] || 0
            })
            return { name, total }
        })
        withTotals.sort((a, b) => b.total - a.total)

        const restNames = withTotals.slice(TOP_N).map((item) => item.name)
        const usedNames = new Set()
        const series = withTotals.slice(0, TOP_N).map((item, index) => {
            let name = chartDimension === 'model' ? item.name : shortCaller(item.name)
            if (chartDimension !== 'model') {
                const base = name
                let suffix = 2
                while (usedNames.has(name)) {
                    name = `${base} (${suffix})`
                    suffix++
                }
                usedNames.add(name)
            }
            return {
                name,
                fullName: item.name,
                color: chartDimension === 'model'
                    ? MODEL_PALETTE[index % MODEL_PALETTE.length]
                    : API_KEY_PALETTE[index % API_KEY_PALETTE.length],
                valueOf: (d) => {
                    const map = chartDimension === 'model' ? d.byModel : d.byCaller
                    return map[item.name] || 0
                },
                valueOfCost: (d) => {
                    const map = chartDimension === 'model' ? d.byModelCost : d.byCallerCost
                    return map[item.name] || 0
                },
            }
        })
        if (restNames.length > 0) {
            series.push({
                name: t('usage.other'),
                color: OTHER_COLOR,
                valueOf: (d) => {
                    const map = chartDimension === 'model' ? d.byModel : d.byCaller
                    let sum = 0
                    restNames.forEach((key) => { sum += map[key] || 0 })
                    return sum
                },
                valueOfCost: (d) => {
                    const map = chartDimension === 'model' ? d.byModelCost : d.byCallerCost
                    let sum = 0
                    restNames.forEach((key) => { sum += map[key] || 0 })
                    return sum
                },
            })
        }
        return series
    }, [dailyAll, chartDimension, t])

    const costMode = summaryMode === 'cost'
    const dark = theme === 'dark'

    // 「模拟缓存命中」开启后，下方各模型的 Token 用量图要把输入拆成命中 /
    // 未命中两条序列；这里做一次记忆化，避免每次渲染都换新对象触发重算。
    const cacheHit = useMemo(() => ({
        enabled: pricing.cacheHitEnabled !== false,
        rate: pricing.cacheHitRate,
    }), [pricing.cacheHitEnabled, pricing.cacheHitRate])

    const mainOption = useMemo(() => buildStackedOption({
        daily: dailyAll,
        series: topSeries,
        dark,
        costMode,
        fmt,
        fmtCompact,
        fmtMoney: fmtMoneyLocal,
        animate: animateCharts,
    }), [dailyAll, topSeries, dark, costMode, fmt, fmtMoneyLocal, animateCharts])

    const modelRows = useMemo(() => {
        const groups = new Map()
        filtered.forEach((r) => {
            const model = r.model || 'unknown'
            if (!groups.has(model)) groups.set(model, [])
            groups.get(model).push(r)
        })
        return Array.from(groups.entries()).map(([model, list]) => {
            const dailyRows = fillDateRange(aggregateDaily(list), dateRange)
            let total = 0
            list.forEach((r) => {
                const u = normalizeUsage(r.usage)
                if (u) total += u.total
            })
            let cost = 0
            list.forEach((r) => { cost += Number(r.cost || 0) })
            return { model, daily: dailyRows, total, cost, calls: list.reduce((sum, r) => sum + (r.calls_count || 0), 0) }
        }).sort((a, b) => b.total - a.total)
    }, [filtered, dateRange])

    const handleExport = useCallback(() => {
        if (!filtered.length) return
        const { csv, count } = buildUsageCsv(filtered)
        const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' })
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url
        a.download = 'ds2api-token-usage.csv'
        // The anchor must be in the DOM for some browsers, and the object URL
        // must outlive the click before it is revoked.
        document.body.appendChild(a)
        a.click()
        document.body.removeChild(a)
        // Give the browser time to start the download before releasing the URL;
        // a 0ms timeout can cancel it in Safari / Firefox.
        setTimeout(() => URL.revokeObjectURL(url), 1000)
        onMessage?.('success', t('usage.exportDone', { count: String(count) }))
    }, [filtered, onMessage, t])

    const handleClearUsage = useCallback(async () => {
        try {
            const res = await authFetch('/admin/usage', { method: 'DELETE' })
            const data = await res.json().catch(() => ({}))
            if (!res.ok) throw new Error(data?.detail || t('usage.clearFailed'))
            onMessage?.('success', t('usage.clearDone'))
            clearRecords()
        } catch (err) {
            onMessage?.('error', err.message || t('usage.clearFailed'))
        }
    }, [authFetch, onMessage, t, clearRecords])

    // 导入「导出」产生的 CSV：前端解析成账本条目后交给后端按日期 + 模型 +
    // 调用方合并，再刷新列表。表头错误等解析问题以 i18n key 形式返回。
    const handleImport = useCallback(async (file) => {
        if (!file) return
        setImporting(true)
        try {
            const { entries, skipped, error } = parseUsageCsv(await file.text())
            if (error) throw new Error(t(error))
            const res = await authFetch('/admin/usage/import', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ entries }),
            })
            const data = await res.json().catch(() => ({}))
            if (!res.ok) throw new Error(data?.detail || t('usage.importFailed'))
            const count = Number(data?.imported ?? entries.length)
            onMessage?.('success', t('usage.importDone', { count: String(count) }))
            if (skipped > 0) onMessage?.('error', t('usage.importSkipped', { count: String(skipped) }))
            await loadRecords()
        } catch (err) {
            onMessage?.('error', err.message || t('usage.importFailed'))
        } finally {
            setImporting(false)
        }
    }, [authFetch, loadRecords, onMessage, t])

    // Label clicks (prefix text / separator) don't natively open a <select>;
    // open the picker explicitly and fall back to focusing the control.
    const openSelectMenu = useCallback((e) => {
        if (e.target && e.target.tagName === 'SELECT') return
        const select = e.currentTarget.querySelector('select')
        if (!select) return
        if (typeof select.showPicker === 'function') {
            try {
                select.showPicker()
            } catch (err) {
                select.focus()
            }
        } else {
            select.focus()
        }
    }, [])

    const rootClassName = dark ? 'usage-root usage-theme-dark' : 'usage-root'

    // Full-screen loading only covers the very first load. Later refreshes
    // keep the rendered data on screen (the refresh button just disables) so
    // the page never flashes a bare loading block over existing content.
    if (loading && !initialized) {
        return (
            <div className={rootClassName} data-theme={theme}>
                <div className="usage-loading">
                    <div className="usage-spinner" />
                    <span>{t('usage.loading')}</span>
                </div>
            </div>
        )
    }

    // A failed refresh keeps the stale data visible (a toast is already
    // emitted); only a failure with nothing to show gets the error screen.
    if (error && records.length === 0) {
        return (
            <div className={rootClassName} data-theme={theme}>
                <div className="usage-loading">
                    <span>{error}</span>
                    <button type="button" className="usage-btn" onClick={loadRecords}>{t('usage.retry')}</button>
                </div>
            </div>
        )
    }

    return (
        <div className={rootClassName} data-theme={theme}>
            <div className="usage-page">
                <div className="usage-title" role="heading" aria-level="1">{t('usage.title')}</div>
                <div className="usage-banner-wrap">
                    <div className="usage-banner-text">{t('usage.banner')}</div>
                </div>

                <div className="usage-section">
                    <div className="usage-summary-row">
                        <div className="usage-card">
                            <div className="usage-card-inner">
                                <div className="usage-card-label-row">
                                    <span className="usage-card-label">{costMode ? t('usage.costTotal') : t('usage.savedTokens')}</span>
                                </div>
                                <div className="usage-card-value-row">
                                    <div className="usage-card-value-wrap">
                                        <span className="usage-card-value">
                                            <span className="usage-card-number">{costMode ? fmtMoneyLocal(summary.savedCost) : fmt(summary.savedTotal)}</span>
                                        </span>
                                    </div>
                                </div>
                            </div>
                        </div>
                        <div className="usage-card">
                            <div className="usage-card-inner">
                                <div className="usage-card-label-row">
                                    <span className="usage-card-label">{costMode ? t('usage.costWindow') : t('usage.windowTokens')}</span>
                                </div>
                                <div className="usage-card-value-row">
                                    <div className="usage-card-value-wrap">
                                        <span className="usage-card-value">
                                            <span className="usage-card-number">{costMode ? fmtMoneyLocal(summary.windowCost) : fmt(summary.windowTotal)}</span>
                                        </span>
                                    </div>
                                </div>
                            </div>
                        </div>
                    </div>
                </div>

                <div className="usage-divider" />

                <div className="usage-toolbar">
                    <label className="usage-select-wrap" onClick={openSelectMenu}>
                        <span className="usage-select-label">{t('usage.dateRange')}</span>
                        <span className="usage-select-sep" />
                        <span className="usage-select-value">
                            <select className="usage-select" value={dateRange} onChange={(e) => setDateRange(e.target.value)}>
                                <option value="7">{t('usage.days7')}</option>
                                <option value="30">{t('usage.days30')}</option>
                                <option value="all">{t('usage.all')}</option>
                            </select>
                            <span className="usage-select-caret" aria-hidden="true">▾</span>
                        </span>
                    </label>

                    <label className="usage-select-wrap" onClick={openSelectMenu}>
                        <span className="usage-select-label">{t('usage.model')}</span>
                        <span className="usage-select-sep" />
                        <span className="usage-select-value">
                            <select className="usage-select" value={modelFilter} onChange={(e) => setModelFilter(e.target.value)}>
                                <option value="all">{t('usage.all')}</option>
                                {modelOptions.map((m) => (
                                    <option key={m} value={m}>{m}</option>
                                ))}
                            </select>
                            <span className="usage-select-caret" aria-hidden="true">▾</span>
                        </span>
                    </label>

                    <div className="usage-toolbar-actions">
                        <button type="button" className="usage-btn" onClick={loadRecords} disabled={loading}>
                            {t('usage.refresh')}
                        </button>
                        <button type="button" className="usage-btn usage-btn-primary" onClick={handleExport}>
                            {t('usage.export')}
                        </button>
                        <button type="button" className="usage-btn usage-settings-btn" onClick={() => { setPricingOpen(false); setSettingsOpen(true) }} aria-label={t('usage.settingsAria')} disabled={settingsLoadState === 'loading'}>
                            ⚙
                        </button>
                    </div>
                </div>

                <div className="usage-section">
                    <div className="usage-metrics-row">
                        {costMode ? (
                            <>
                                <div className="usage-card usage-card-wide">
                                    <div className="usage-card-inner">
                                        <div className="usage-card-label-row">
                                            <span className="usage-card-label">{t('usage.cost')}</span>
                                        </div>
                                        <div className="usage-card-value-row">
                                            <div className="usage-card-value-wrap">
                                                <span className="usage-card-value">
                                                    <span className="usage-card-number">{fmtMoneyLocal(summary.windowCost)}</span>
                                                </span>
                                            </div>
                                        </div>
                                    </div>
                                </div>
                                <div className="usage-card usage-card-output">
                                    <div className="usage-card-inner">
                                        <div className="usage-card-label-row">
                                            <span className="usage-card-label">{t('usage.apiRequests')}</span>
                                        </div>
                                        <div className="usage-card-value-row">
                                            <div className="usage-card-value-wrap">
                                                <span className="usage-card-value">
                                                    <span className="usage-card-number">{fmt(summary.calls)}</span>
                                                </span>
                                            </div>
                                        </div>
                                    </div>
                                </div>
                                <div className="usage-card usage-card-mid">
                                    <div className="usage-card-inner">
                                        <div className="usage-card-label-row">
                                            <span className="usage-card-label">{t('usage.totalTokens')}</span>
                                        </div>
                                        <div className="usage-card-value-row">
                                            <div className="usage-card-value-wrap">
                                                <span className="usage-card-value">
                                                    <span className="usage-card-number">{fmt(summary.windowTotal)}</span>
                                                </span>
                                            </div>
                                        </div>
                                    </div>
                                </div>
                            </>
                        ) : (
                            <>
                                <div className="usage-card usage-card-wide">
                                    <div className="usage-card-inner">
                                        <div className="usage-card-label-row">
                                            <span className="usage-card-label">{t('usage.inputTokens')}</span>
                                        </div>
                                        <div className="usage-card-value-row">
                                            <div className="usage-card-value-wrap">
                                                <span className="usage-card-value">
                                                    <span className="usage-card-number">{fmt(summary.promptSum)}</span>
                                                </span>
                                            </div>
                                        </div>
                                    </div>
                                </div>
                                <div className="usage-card usage-card-output">
                                    <div className="usage-card-inner">
                                        <div className="usage-card-label-row">
                                            <span className="usage-card-label">{t('usage.outputTokens')}</span>
                                        </div>
                                        <div className="usage-card-value-row">
                                            <div className="usage-card-value-wrap">
                                                <span className="usage-card-value">
                                                    <span className="usage-card-number">{fmt(summary.completionSum)}</span>
                                                </span>
                                            </div>
                                        </div>
                                    </div>
                                </div>
                                <div className="usage-card usage-card-mid">
                                    <div className="usage-card-inner">
                                        <div className="usage-card-label-row">
                                            <span className="usage-card-label">{t('usage.apiRequests')}</span>
                                        </div>
                                        <div className="usage-card-value-row">
                                            <div className="usage-card-value-wrap">
                                                <span className="usage-card-value">
                                                    <span className="usage-card-number">{fmt(summary.calls)}</span>
                                                </span>
                                            </div>
                                        </div>
                                    </div>
                                </div>
                            </>
                        )}
                    </div>

                    <div className="usage-chart-block">
                        <div className="usage-chart-card">
                            <div className="usage-chart-body">
                                <div className="usage-chart-head">
                                    <div className="usage-chart-title-row">
                                        <span className="usage-chart-title">{costMode ? t('usage.costUsage') : t('usage.tokenUsage')}</span>
                                        <span className="usage-chart-value">{costMode ? fmtMoneyLocal(summary.windowCost) : fmt(summary.windowTotal)}</span>
                                    </div>
                                    <div className="usage-chart-actions">
                                        <div className="usage-segmented">
                                            <button
                                                type="button"
                                                className={chartDimension === 'model' ? 'usage-seg-btn active' : 'usage-seg-btn'}
                                                onClick={() => setChartDimension('model')}
                                            >
                                                {t('usage.model')}
                                            </button>
                                            <button
                                                type="button"
                                                className={chartDimension === 'apikey' ? 'usage-seg-btn active' : 'usage-seg-btn'}
                                                onClick={() => setChartDimension('apikey')}
                                            >
                                                {t('usage.apiKey')}
                                            </button>
                                        </div>
                                    </div>
                                </div>
                                <EChart option={mainOption} height={272} />
                                <div className="usage-legend">
                                    {topSeries.map((s) => (
                                        <span
                                            className="usage-legend-item"
                                            key={s.name}
                                            title={s.fullName && s.fullName !== s.name ? s.fullName : undefined}
                                        >
                                            <span className="usage-swatch" style={{ background: s.color }} />
                                            <span>{s.name}</span>
                                        </span>
                                    ))}
                                </div>
                            </div>
                        </div>
                    </div>
                </div>

                {modelRows.map((row) => (
                    <ModelRow key={row.model} row={row} dark={dark} costMode={costMode} cacheHit={cacheHit} />
                ))}

                <div className="usage-status">
                    {t('usage.loaded', { count: String(filtered.length) })}
                </div>

                {settingsOpen && (
                    <SettingsPanel
                        pricingOpen={pricingOpen}
                        setPricingOpen={setPricingOpen}
                        settingsLoadState={settingsLoadState}
                        saving={saving}
                        usageEnabled={usageEnabled}
                        setUsageEnabled={setUsageEnabled}
                        pricing={pricing}
                        setPricing={setPricing}
                        closeSettings={closeSettings}
                        dismissSettings={dismissSettings}
                        summaryMode={summaryMode}
                        setSummaryMode={setSummaryMode}
                        onClear={handleClearUsage}
                        onImport={handleImport}
                        importing={importing}
                    />
                )}
            </div>
        </div>
    )
}
