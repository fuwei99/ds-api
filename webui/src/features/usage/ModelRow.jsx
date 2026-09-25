import { useCallback, useMemo } from 'react'
import { useI18n } from '../../i18n'
import EChart from './EChart'
import { buildLineOption, buildStackedOption } from './chartOptions'
import { fmtCompact, fmtMoney, fmtNumber, splitCacheHitTokens } from './usageUtils'
import { useEntranceAnimation } from './useEntranceAnimation'

// ModelRow renders the per-model breakdown: an API-requests line chart plus a
// stacked chart that shows tokens (tokens mode) or daily cost (cost mode).
// cacheHit 开启时（价格设置里的「模拟缓存命中」），输入 tokens 会按命中率
// 拆成「输入(命中缓存)」与「输入(未命中缓存)」两条堆叠序列。
export default function ModelRow({ row, dark, costMode, cacheHit }) {
    const { lang, t } = useI18n()
    const fmt = useCallback((n) => fmtNumber(n, lang), [lang])
    const fmtMoneyLocal = useCallback((n) => fmtMoney(n, lang), [lang])

    const cacheHitOn = !!cacheHit?.enabled
    const cacheHitRate = cacheHit?.rate
    const animate = useEntranceAnimation(row.daily.length > 0)

    // Pre-split each day's input once so the hit / miss series share the result
    // instead of recomputing splitCacheHitTokens twice per day per render.
    const cacheSplit = useMemo(() => {
        const map = new Map()
        if (cacheHitOn) {
            row.daily.forEach((d) => map.set(d, splitCacheHitTokens(d.prompt, cacheHitRate)))
        }
        return map
    }, [row.daily, cacheHitOn, cacheHitRate])

    const tokenSeries = useMemo(() => {
        const series = [
            { name: t('usage.outputTokens'), color: '#0C70F3', valueOf: (d) => d.output || 0 },
        ]
        if (cacheHitOn) {
            series.push(
                {
                    name: t('usage.inputHitTokens'),
                    color: '#A0DCFD',
                    valueOf: (d) => cacheSplit.get(d)?.hit || 0,
                },
                {
                    name: t('usage.inputMissTokens'),
                    color: '#F59E0B',
                    valueOf: (d) => cacheSplit.get(d)?.miss || 0,
                },
            )
        } else {
            series.push({ name: t('usage.inputTokens'), color: '#A0DCFD', valueOf: (d) => d.prompt || 0 })
        }
        return series.slice().sort((a, b) => {
            const ta = row.daily.reduce((sum, d) => sum + (a.valueOf(d) || 0), 0)
            const tb = row.daily.reduce((sum, d) => sum + (b.valueOf(d) || 0), 0)
            return ta - tb
        })
    }, [row.daily, t, cacheHitOn, cacheSplit])

    const tokensOption = useMemo(() => buildStackedOption({
        daily: row.daily,
        series: tokenSeries,
        dark,
        costMode: false,
        fmt,
        fmtCompact,
        fmtMoney: fmtMoneyLocal,
        animate,
    }), [row.daily, tokenSeries, dark, fmt, fmtMoneyLocal, animate])

    const costOption = useMemo(() => buildStackedOption({
        daily: row.daily,
        series: [{ name: t('usage.cost'), color: '#0C70F3', valueOf: () => 0, valueOfCost: (d) => d.cost || 0 }],
        dark,
        costMode: true,
        fmt,
        fmtCompact,
        fmtMoney: fmtMoneyLocal,
        animate,
    }), [row.daily, dark, fmt, fmtMoneyLocal, t, animate])

    const requestsOption = useMemo(() => buildLineOption({
        daily: row.daily,
        dark,
        name: t('usage.apiRequests'),
        fmt,
        animate,
    }), [row.daily, dark, t, fmt, animate])

    const chartOption = costMode ? costOption : tokensOption

    return (
        <div className="usage-model-block">
            <div className="usage-model-title">{row.model}</div>
            <div className="usage-model-body">
                <div className="usage-model-grid">
                    <div className="usage-model-card">
                        <div className="usage-model-card-head">
                            <span className="usage-model-card-label">{t('usage.apiRequests')}</span>
                            <span className="usage-model-card-value">{fmt(row.calls)}</span>
                        </div>
                        <div className="usage-model-card-chart">
                            <EChart option={requestsOption} height={272} />
                        </div>
                    </div>
                    <div className="usage-model-card">
                        <div className="usage-model-card-head">
                            <span className="usage-model-card-label">{costMode ? t('usage.cost') : t('usage.totalTokens')}</span>
                            <span className="usage-model-card-value">{costMode ? fmtMoneyLocal(row.cost) : fmt(row.total)}</span>
                        </div>
                        <div className="usage-model-card-chart">
                            <EChart option={chartOption} height={272} />
                        </div>
                    </div>
                </div>
            </div>
        </div>
    )
}
