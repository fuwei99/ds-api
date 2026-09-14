import { useMemo } from 'react'
import { Gauge, Link2, Loader2, UserCheck, X } from 'lucide-react'
import clsx from 'clsx'

import { useI18n } from '../../i18n'

// accountOptions 把 config.accounts 归一化为 {identifier, label}，
// identifier 与后端 Account.Identifier() 规则一致（email 优先，其次 mobile）。
export function accountOptions(accounts) {
    return (accounts || [])
        .map(acc => {
            const identifier = String(acc?.email || acc?.mobile || '').trim()
            if (!identifier) return null
            const name = String(acc?.name || '').trim()
            return { identifier, label: name ? `${name} (${identifier})` : identifier }
        })
        .filter(Boolean)
}

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

function NodeRow({ t, node, latency, options, busy, onBind, onUnbind }) {
    const bound = Array.isArray(node.accounts) ? node.accounts : []
    const boundIds = new Set(bound.map(acc => acc.identifier))
    const candidates = options.filter(opt => !boundIds.has(opt.identifier))
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
                    <Link2 className="w-3.5 h-3.5 text-muted-foreground shrink-0" />
                    <select
                        className="input-field !w-auto min-w-[200px] text-xs"
                        value=""
                        disabled={binding || candidates.length === 0}
                        onChange={e => {
                            const identifier = e.target.value
                            if (identifier) onBind(identifier, node.node_key)
                        }}
                    >
                        <option value="">{t('mihomoBridge.bindPlaceholder')}</option>
                        {candidates.map(opt => (
                            <option key={opt.identifier} value={opt.identifier}>{opt.label}</option>
                        ))}
                    </select>
                    {binding && <Loader2 className="w-4 h-4 animate-spin text-primary shrink-0" />}
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
}

export default function MihomoNodesTable({ nodes, latency, accounts, busy, canTest, testing, assigning, onBind, onUnbind, onTestLatency, onAssignAll }) {
    const { t } = useI18n()
    const options = accountOptions(accounts)

    // 测过延迟后按延迟升序展示：成功项在前（按 delay_ms），失败/未测项保持原序垫底。
    // 未测延迟时：已绑定账号的节点排到最前，让绑定情况一眼可见（绑定可由账号反推）。
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

            {nodes.length === 0 ? (
                <div className="p-10 text-center text-muted-foreground">{t('mihomoBridge.noNodes')}</div>
            ) : (
                <div className="divide-y divide-border">
                    {sortedNodes.map(node => (
                        <NodeRow
                            key={node.node_key}
                            t={t}
                            node={node}
                            latency={latency?.[node.node_key]}
                            options={options}
                            busy={busy}
                            onBind={onBind}
                            onUnbind={onUnbind}
                        />
                    ))}
                </div>
            )}
        </div>
    )
}
