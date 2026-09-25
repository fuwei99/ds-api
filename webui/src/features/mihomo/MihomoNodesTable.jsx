import { memo, useMemo, useState } from 'react'
import { ChevronLeft, ChevronRight, Gauge, Link2, Loader2, Plus, Search, UserCheck, X } from 'lucide-react'
import clsx from 'clsx'

import { useI18n } from '../../i18n'
import BindAccountModal from './BindAccountModal'

import { accountOptions } from './useMihomoBridge'
export { accountOptions }

function PortBadge({ t, port }) {
    if (!port) {
        return (
            <span className="inline-flex items-center rounded-full border border-border bg-muted/20 px-2 py-1 text-[10px] font-medium text-muted-foreground">
                {t('mihomoBridge.portUnassigned')}
            </span>
        )
    }
    return (
        <span className="inline-flex items-center rounded-full border border-emerald-500/20 bg-emerald-500/10 px-2 py-1 text-[10px] font-mono font-medium text-emerald-500">
            127.0.0.1:{port}
        </span>
    )
}

function LatencyBadge({ t, latency, node }) {
    if (latency) {
        if (latency.error) {
            return (
                <span
                    className="inline-flex items-center rounded-full border border-red-500/25 bg-red-500/10 px-2 py-1 text-[10px] font-medium text-red-500"
                    title={latency.error}
                >
                    {t('mihomoBridge.latencyFailed')}
                </span>
            )
        }
        return (
            <span className="inline-flex items-center rounded-full border border-emerald-500/20 bg-emerald-500/10 px-2 py-1 text-[10px] font-mono font-medium text-emerald-500">
                {latency.delay_ms} ms
            </span>
        )
    }
    if (node?.health === 'fail') {
        return (
            <span
                className="inline-flex items-center rounded-full border border-red-500/25 bg-red-500/10 px-2 py-1 text-[10px] font-medium text-red-500"
                title={node.health_error || ''}
            >
                {t('mihomoBridge.latencyFailed')}
            </span>
        )
    }
    return null
}

const NodeRow = memo(function NodeRow({ t, node, latency, busy, onOpenBind, onUnbind }) {
    const bound = Array.isArray(node.accounts) ? node.accounts : []
    const binding = Boolean(busy?.[`bind:${node.node_key}`])

    return (
        <div className="p-4 md:p-5 space-y-3 hover:bg-muted/40 transition-colors">
            <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-3">
                <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                        <div className="font-medium text-foreground">{node.name}</div>
                        <span className="inline-flex items-center rounded-full border border-primary/20 bg-primary/10 px-2 py-1 text-[10px] font-medium uppercase tracking-wide text-primary">
                            {node.type || '?'}
                        </span>
                        <PortBadge t={t} port={node.local_port} />
                        <LatencyBadge t={t} latency={latency} node={node} />
                    </div>
                    <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                        {node.server && (
                            <span className="font-mono bg-muted/30 px-2 py-1 rounded border border-border">{node.server}</span>
                        )}
                        <span>{node.subscription}</span>
                    </div>
                </div>

                <div className="flex items-center gap-2 self-start lg:self-auto">
                    <button
                        onClick={() => onOpenBind(node)}
                        disabled={binding}
                        className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border hover:bg-secondary text-xs font-medium transition-colors disabled:opacity-50 shrink-0"
                    >
                        {binding ? (
                            <Loader2 className="w-3.5 h-3.5 animate-spin text-primary" />
                        ) : (
                            <Plus className="w-3.5 h-3.5 text-primary" />
                        )}
                        <span>{t('mihomoBridge.bindAction')}</span>
                    </button>
                </div>
            </div>

            {bound.length > 0 && (
                <div className="flex flex-wrap items-center gap-2">
                    <span className="text-[10px] font-bold uppercase tracking-wider text-muted-foreground">
                        {t('mihomoBridge.boundAccounts')}
                    </span>
                    {bound.map(acc => {
                        const unbinding = Boolean(busy?.[`bind:${acc.identifier}`])
                        return (
                            <span
                                key={acc.identifier}
                                className={clsx(
                                    'inline-flex items-center gap-1.5 rounded-full border border-emerald-500/25 bg-emerald-500/10 pl-3 pr-1.5 py-1 text-[11px] font-medium text-emerald-500',
                                    unbinding && 'opacity-60'
                                )}
                            >
                                {acc.label}
                                <button
                                    onClick={() => onUnbind(acc.identifier)}
                                    disabled={unbinding}
                                    className="rounded-full p-0.5 hover:bg-emerald-500/20 transition-colors disabled:opacity-50"
                                    title={t('mihomoBridge.unbindAction')}
                                >
                                    {unbinding ? <Loader2 className="w-3 h-3 animate-spin" /> : <X className="w-3 h-3" />}
                                </button>
                            </span>
                        )
                    })}
                </div>
            )}
        </div>
    )
})

export default function MihomoNodesTable({
    nodes,
    latency,
    accounts,
    busy,
    canTest,
    testing,
    assigning,
    onBind,
    onUnbind,
    onTestLatency,
    onAssignAll,
}) {
    const { t } = useI18n()
    const [page, setPage] = useState(1)
    const [pageSize, setPageSize] = useState(20)
    const [searchQuery, setSearchQuery] = useState('')
    const [filterStatus, setFilterStatus] = useState('all')
    const [bindingTarget, setBindingTarget] = useState(null)

    const options = useMemo(() => accountOptions(accounts), [accounts])

    // 测过延迟后按延迟升序展示：成功项在前（按 delay_ms），失败/未测项保持原序垫底。
    // 未测延迟时：已绑定账号的节点排到最前，让绑定情况一眼可见。
    const sortedNodes = useMemo(() => {
        const withLatency = latency && Object.keys(latency).length > 0
        if (withLatency) {
            return [...nodes].sort((a, b) => {
                const la = latency[a.node_key]
                const lb = latency[b.node_key]
                const aOk = Boolean(la && !la.error && la.delay_ms > 0)
                const bOk = Boolean(lb && !lb.error && lb.delay_ms > 0)
                if (aOk !== bOk) return aOk ? -1 : 1
                if (aOk && bOk) return la.delay_ms - lb.delay_ms
                return 0
            })
        }
        return [...nodes].sort((a, b) => {
            const aBound = Array.isArray(a.accounts) && a.accounts.length > 0
            const bBound = Array.isArray(b.accounts) && b.accounts.length > 0
            if (aBound !== bBound) return aBound ? -1 : 1
            return 0
        })
    }, [nodes, latency])

    // 各状态统计计数
    const counts = useMemo(() => {
        let bound = 0
        let unbound = 0
        let healthy = 0
        let failed = 0
        for (const n of nodes) {
            const hasBound = Array.isArray(n.accounts) && n.accounts.length > 0
            if (hasBound) bound++
            else unbound++

            const lat = latency?.[n.node_key]
            const isHealthy = Boolean((lat && !lat.error && lat.delay_ms > 0) || (n.latency_ms > 0 && n.health !== 'fail'))
            const isFailed = Boolean(lat?.error || n.health === 'fail')
            if (isHealthy) healthy++
            if (isFailed) failed++
        }
        return { total: nodes.length, bound, unbound, healthy, failed }
    }, [nodes, latency])

    // 组合搜索与状态过滤
    const filteredNodes = useMemo(() => {
        const query = searchQuery.trim().toLowerCase()
        return sortedNodes.filter(node => {
            // 状态过滤
            if (filterStatus === 'bound') {
                if (!Array.isArray(node.accounts) || node.accounts.length === 0) return false
            } else if (filterStatus === 'unbound') {
                if (Array.isArray(node.accounts) && node.accounts.length > 0) return false
            } else if (filterStatus === 'healthy') {
                const lat = latency?.[node.node_key]
                const isHealthy = Boolean((lat && !lat.error && lat.delay_ms > 0) || (node.latency_ms > 0 && node.health !== 'fail'))
                if (!isHealthy) return false
            } else if (filterStatus === 'failed') {
                const lat = latency?.[node.node_key]
                const isFailed = Boolean(lat?.error || node.health === 'fail')
                if (!isFailed) return false
            }

            // 搜索过滤
            if (!query) return true
            if ((node.name || '').toLowerCase().includes(query)) return true
            if ((node.server || '').toLowerCase().includes(query)) return true
            if ((node.subscription || '').toLowerCase().includes(query)) return true
            if (Array.isArray(node.accounts)) {
                for (const acc of node.accounts) {
                    if ((acc.identifier || '').toLowerCase().includes(query)) return true
                    if ((acc.label || '').toLowerCase().includes(query)) return true
                }
            }
            return false
        })
    }, [sortedNodes, filterStatus, searchQuery, latency])

    // 分页计算
    const totalPages = Math.max(1, Math.ceil(filteredNodes.length / pageSize))
    const currentPage = Math.min(page, totalPages)
    const displayedNodes = useMemo(() => {
        const start = (currentPage - 1) * pageSize
        return filteredNodes.slice(start, start + pageSize)
    }, [filteredNodes, currentPage, pageSize])

    const handleSearchChange = (val) => {
        setSearchQuery(val)
        setPage(1)
    }

    const handleFilterChange = (status) => {
        setFilterStatus(status)
        setPage(1)
    }

    const handlePageSizeChange = (size) => {
        setPageSize(size)
        setPage(1)
    }

    // 计算弹窗中当前节点的候选账号（排除已绑定此节点的账号）
    const candidateAccounts = useMemo(() => {
        if (!bindingTarget) return []
        const bound = Array.isArray(bindingTarget.accounts) ? bindingTarget.accounts : []
        const boundIds = new Set(bound.map(acc => acc.identifier))
        return options.filter(opt => !boundIds.has(opt.identifier))
    }, [bindingTarget, options])

    return (
        <div className="bg-card border border-border rounded-xl overflow-hidden shadow-sm">
            <div className="p-6 border-b border-border flex flex-col md:flex-row md:items-center justify-between gap-4">
                <div>
                    <h2 className="text-lg font-semibold">{t('mihomoBridge.nodesTitle')}</h2>
                    <p className="text-sm text-muted-foreground mt-1">{t('mihomoBridge.nodesDesc')}</p>
                </div>
                <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-2 shrink-0">
                    <button
                        onClick={onTestLatency}
                        disabled={testing || assigning || !canTest || nodes.length === 0}
                        className="inline-flex items-center justify-center gap-2 px-4 py-2 rounded-lg border border-border hover:bg-secondary transition-colors font-medium text-sm disabled:opacity-50"
                        title={!canTest ? t('mihomoBridge.latencyTestDisabledHint') : ''}
                    >
                        {testing ? <Loader2 className="w-4 h-4 animate-spin" /> : <Gauge className="w-4 h-4" />}
                        {testing ? t('mihomoBridge.testingLatency') : t('mihomoBridge.testLatency')}
                    </button>
                    <button
                        onClick={onAssignAll}
                        disabled={assigning || testing || nodes.length === 0}
                        className="inline-flex items-center justify-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground hover:bg-primary/90 transition-colors font-medium text-sm shadow-sm disabled:opacity-50"
                    >
                        {assigning ? <Loader2 className="w-4 h-4 animate-spin" /> : <UserCheck className="w-4 h-4" />}
                        {assigning ? t('mihomoBridge.assigningAll') : t('mihomoBridge.assignAll')}
                    </button>
                </div>
            </div>

            {/* 搜索与筛选工具栏 */}
            {nodes.length > 0 && (
                <div className="p-4 border-b border-border bg-muted/20 flex flex-col lg:flex-row lg:items-center justify-between gap-3">
                    <div className="relative flex-1 max-w-md">
                        <Search className="w-4 h-4 text-muted-foreground absolute left-3 top-1/2 -translate-y-1/2" />
                        <input
                            type="text"
                            value={searchQuery}
                            onChange={e => handleSearchChange(e.target.value)}
                            placeholder={t('mihomoBridge.searchPlaceholder')}
                            className="input-field pl-9 text-xs"
                        />
                        {searchQuery && (
                            <button
                                onClick={() => handleSearchChange('')}
                                className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                            >
                                <X className="w-3.5 h-3.5" />
                            </button>
                        )}
                    </div>

                    <div className="flex flex-wrap items-center gap-1.5">
                        <button
                            onClick={() => handleFilterChange('all')}
                            className={clsx(
                                'px-2.5 py-1.5 rounded-lg text-xs font-medium transition-colors border',
                                filterStatus === 'all'
                                    ? 'bg-primary text-primary-foreground border-primary'
                                    : 'bg-card text-muted-foreground border-border hover:bg-secondary'
                            )}
                        >
                            {t('mihomoBridge.filterAll', { count: counts.total })}
                        </button>
                        <button
                            onClick={() => handleFilterChange('bound')}
                            className={clsx(
                                'px-2.5 py-1.5 rounded-lg text-xs font-medium transition-colors border',
                                filterStatus === 'bound'
                                    ? 'bg-primary text-primary-foreground border-primary'
                                    : 'bg-card text-muted-foreground border-border hover:bg-secondary'
                            )}
                        >
                            {t('mihomoBridge.filterBound', { count: counts.bound })}
                        </button>
                        <button
                            onClick={() => handleFilterChange('unbound')}
                            className={clsx(
                                'px-2.5 py-1.5 rounded-lg text-xs font-medium transition-colors border',
                                filterStatus === 'unbound'
                                    ? 'bg-primary text-primary-foreground border-primary'
                                    : 'bg-card text-muted-foreground border-border hover:bg-secondary'
                            )}
                        >
                            {t('mihomoBridge.filterUnbound', { count: counts.unbound })}
                        </button>
                        <button
                            onClick={() => handleFilterChange('healthy')}
                            className={clsx(
                                'px-2.5 py-1.5 rounded-lg text-xs font-medium transition-colors border',
                                filterStatus === 'healthy'
                                    ? 'bg-primary text-primary-foreground border-primary'
                                    : 'bg-card text-muted-foreground border-border hover:bg-secondary'
                            )}
                        >
                            {t('mihomoBridge.filterHealthy', { count: counts.healthy })}
                        </button>
                        {counts.failed > 0 && (
                            <button
                                onClick={() => handleFilterChange('failed')}
                                className={clsx(
                                    'px-2.5 py-1.5 rounded-lg text-xs font-medium transition-colors border',
                                    filterStatus === 'failed'
                                        ? 'bg-destructive text-destructive-foreground border-destructive'
                                        : 'bg-card text-destructive border-border hover:bg-destructive/10'
                                )}
                            >
                                {t('mihomoBridge.filterFailed', { count: counts.failed })}
                            </button>
                        )}
                    </div>
                </div>
            )}

            {nodes.length === 0 ? (
                <div className="p-10 text-center text-muted-foreground">{t('mihomoBridge.noNodes')}</div>
            ) : filteredNodes.length === 0 ? (
                <div className="p-10 text-center text-muted-foreground">{t('mihomoBridge.noNodesFound')}</div>
            ) : (
                <div className="divide-y divide-border">
                    {displayedNodes.map(node => (
                        <NodeRow
                            key={node.node_key}
                            t={t}
                            node={node}
                            latency={latency?.[node.node_key]}
                            busy={busy}
                            onOpenBind={setBindingTarget}
                            onUnbind={onUnbind}
                        />
                    ))}
                </div>
            )}

            {/* 分页控制栏 */}
            {filteredNodes.length > 0 && (
                <div className="p-4 border-t border-border flex flex-col sm:flex-row items-center justify-between gap-3">
                    <div className="flex items-center gap-3">
                        <div className="text-xs text-muted-foreground">
                            {t('mihomoBridge.pageInfo', {
                                current: currentPage,
                                total: totalPages,
                                count: filteredNodes.length,
                                bound: counts.bound,
                            })}
                        </div>
                        <select
                            value={pageSize}
                            onChange={e => handlePageSizeChange(Number(e.target.value))}
                            className="text-xs border border-border rounded-md px-2 py-1 bg-background text-foreground"
                        >
                            {[20, 50, 100, 200].map(s => (
                                <option key={s} value={s}>{s} / 页</option>
                            ))}
                        </select>
                    </div>

                    {totalPages > 1 && (
                        <div className="flex items-center gap-1">
                            <button
                                onClick={() => setPage(p => Math.max(1, p - 1))}
                                disabled={currentPage <= 1}
                                className="p-1.5 border border-border rounded-md hover:bg-secondary transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
                            >
                                <ChevronLeft className="w-4 h-4" />
                            </button>
                            <span className="text-xs font-medium px-2">
                                {currentPage} / {totalPages}
                            </span>
                            <button
                                onClick={() => setPage(p => Math.min(totalPages, p + 1))}
                                disabled={currentPage >= totalPages}
                                className="p-1.5 border border-border rounded-md hover:bg-secondary transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
                            >
                                <ChevronRight className="w-4 h-4" />
                            </button>
                        </div>
                    )}
                </div>
            )}

            {/* 可搜索账号绑定弹窗 */}
            <BindAccountModal
                node={bindingTarget}
                candidates={candidateAccounts}
                busy={Boolean(bindingTarget && busy?.[`bind:${bindingTarget.node_key}`])}
                onBind={onBind}
                onClose={() => setBindingTarget(null)}
                t={t}
            />
        </div>
    )
}
