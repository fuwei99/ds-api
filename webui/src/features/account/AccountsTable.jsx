import { useCallback, useEffect, useRef, useState } from 'react'
import { ChevronLeft, ChevronRight, Check, Copy, Pencil, Play, Plus, Trash2, FolderX } from 'lucide-react'
import clsx from 'clsx'

// 代理下拉框默认最大宽度（与原有布局一致的视觉宽度上限）。
const PROXY_SELECT_MAX_WIDTH = 180
// 收窄后的最小可用宽度：窄屏到极限时仍要保证能显示一个省略号占位，
// 且不能把操作区的固定按钮挤出容器（320px 视口下约需 48px 以内）。
const PROXY_SELECT_MIN_WIDTH = 44

// 仅当账号行的操作区（开关 + 代理下拉框 + 编辑/刷新/删除按钮）在窄屏放不下时，
// 才把代理下拉框收窄；收窄到刚好放下全部按钮为止。宽屏或标签较少时保持原样。
function AccountActions({ children }) {
    const containerRef = useRef(null)
    const selectRef = useRef(null)
    const [proxyWidth, setProxyWidth] = useState(PROXY_SELECT_MAX_WIDTH)

    const measure = useCallback(() => {
        const container = containerRef.current
        const select = selectRef.current
        if (!container || !select) return

        // 操作区在当前布局中真正可用的宽度。
        const available = container.clientWidth
        if (available <= 0) return

        // 除下拉框外的固定元素（开关、三个按钮）占用宽度 + 元素间距。
        // 这些元素全部 shrink-0，因此宽度即 offsetWidth，不会随容器变化。
        const children = Array.from(container.children)
        let fixed = 0
        children.forEach((child) => {
            if (child === select) return
            fixed += child.offsetWidth
        })
        // 从计算样式读取真实的 gap（响应式下可能是 6px 或 8px），避免硬编码失配。
        const gap = parseFloat(getComputedStyle(container).columnGap) || 0
        const gaps = Math.max(0, children.length - 1) * gap

        const needed = available - fixed - gaps
        if (needed >= PROXY_SELECT_MAX_WIDTH) {
            setProxyWidth((prev) => (prev === PROXY_SELECT_MAX_WIDTH ? prev : PROXY_SELECT_MAX_WIDTH))
            return
        }

        // 收窄到刚好放下全部按钮；若已到下限仍放不下，保持下限。
        const target = Math.max(PROXY_SELECT_MIN_WIDTH, Math.ceil(needed))
        setProxyWidth((prev) => (prev === target ? prev : target))
    }, [])

    useEffect(() => {
        const container = containerRef.current
        if (!container) return

        measure()

        if (typeof ResizeObserver === 'undefined') {
            window.addEventListener('resize', measure)
            return () => window.removeEventListener('resize', measure)
        }

        const observer = new ResizeObserver(() => measure())
        observer.observe(container)
        return () => observer.disconnect()
    }, [measure])

    return children({ containerRef, selectRef, proxyWidth })
}

export default function AccountsTable({
    t,
    accounts,
    loadingAccounts,
    testing,
    testingAll,
    batchProgress,
    sessionCounts,
    deletingSessions,
    updatingProxy,
    togglingEnabled,
    togglingAllEnabled,
    deletingBanned,
    totalAccounts,
    page,
    pageSize,
    totalPages,
    resolveAccountIdentifier,
    proxies,
    elasticPoolEnabled,
    onOpenElasticPool,
    onTestAll,
    onShowAddAccount,
    onEditAccount,
    onTestAccount,
    onDeleteAccount,
    onDeleteAllSessions,
    onUpdateAccountProxy,
    onToggleAccountEnabled,
    onToggleAllAccountsEnabled,
    onDeleteBannedAccounts,
    onPrevPage,
    onNextPage,
    onPageSizeChange,
    searchQuery,
    onSearchChange,
    envBacked = false,
    deviceIdMode = 'manual',
    deviceIdRemaining,
    onOpenDeviceIds,
}) {
    const [copiedId, setCopiedId] = useState(null)
    // real 模式不使用手工号池，相关的入口与计数一并隐藏。
    const showDeviceIdControls = deviceIdMode !== 'real'
    const deviceIdRemainingKnown = deviceIdRemaining !== undefined && deviceIdRemaining !== null
    const deviceIdRemainingLow = deviceIdRemainingKnown && Number(deviceIdRemaining) < 3

    const copyId = (id) => {
        navigator.clipboard.writeText(id).then(() => {
            setCopiedId(id)
            setTimeout(() => setCopiedId(null), 1500)
        })
    }
    return (
        <div className="bg-card border border-border rounded-xl overflow-hidden shadow-sm">
            <div className="p-6 border-b border-border flex flex-col md:flex-row md:items-center justify-between gap-4">
                <div>
                    <h2 className="text-lg font-semibold">{t('accountManager.accountsTitle')}</h2>
                    <p className="text-sm text-muted-foreground">{t('accountManager.accountsDesc')}</p>
                </div>
                <div className="flex flex-wrap gap-2">
                    <input
                        type="text"
                        value={searchQuery}
                        onChange={e => onSearchChange(e.target.value)}
                        placeholder={t('accountManager.searchPlaceholder')}
                        className="px-3 py-1.5 text-sm bg-muted border border-border rounded-lg focus:outline-none focus:ring-1 focus:ring-ring placeholder:text-muted-foreground"
                    />
                    <button
                        onClick={onTestAll}
                        disabled={testingAll || totalAccounts === 0}
                        className="flex items-center px-3 py-2 bg-secondary text-secondary-foreground rounded-lg hover:bg-secondary/80 transition-colors text-xs font-medium border border-border disabled:opacity-50"
                    >
                        {testingAll ? <span className="animate-spin mr-2">⟳</span> : <Play className="w-3 h-3 mr-2" />}
                        {t('accountManager.testAll')}
                    </button>
                    {showDeviceIdControls && (
                        <button
                            onClick={onOpenDeviceIds}
                            className="flex items-center px-3 py-2 bg-secondary text-secondary-foreground rounded-lg hover:bg-secondary/80 transition-colors text-xs font-medium border border-border"
                        >
                            {t('accountManager.manageDeviceId')}
                        </button>
                    )}
                    <button
                        onClick={onShowAddAccount}
                        className="flex items-center gap-2 px-4 py-2 bg-primary text-primary-foreground rounded-lg hover:bg-primary/90 transition-colors font-medium text-sm shadow-sm"
                    >
                        <Plus className="w-4 h-4" />
                        {t('accountManager.addAccount')}
                    </button>
                </div>
            </div>

            <div className="px-6 py-3 border-b border-border flex items-center justify-between gap-2">
                <button
                    onClick={onOpenElasticPool}
                    className="flex items-center px-3 py-1.5 rounded-lg transition-colors text-xs font-medium border bg-primary text-primary-foreground border-primary hover:bg-primary/90"
                >
                    {t('accountManager.elasticPool')}
                </button>
                <div className="flex flex-wrap gap-2">
                    {showDeviceIdControls && (
                        <span
                            className={clsx(
                                "flex items-center px-2 py-1.5 rounded-lg text-xs font-medium border",
                                deviceIdRemainingLow
                                    ? "bg-red-500/15 text-red-600 border-red-500/40"
                                    : "bg-transparent text-muted-foreground border-transparent"
                            )}
                        >
                            {t('accountManager.deviceIdRemaining', { count: deviceIdRemainingKnown ? Number(deviceIdRemaining) : 0 })}
                        </span>
                    )}
                    <button
                        onClick={onDeleteBannedAccounts}
                        disabled={deletingBanned || testingAll || totalAccounts === 0}
                        className="flex items-center px-3 py-1.5 bg-destructive/10 text-destructive border border-destructive/20 rounded-lg hover:bg-destructive/20 transition-colors text-xs font-medium disabled:opacity-50"
                    >
                        {deletingBanned && <span className="animate-spin mr-2">⟳</span>}
                        {t('accountManager.deleteBannedAccounts')}
                    </button>
                    <button
                        onClick={() => onToggleAllAccountsEnabled(false)}
                        disabled={elasticPoolEnabled || togglingAllEnabled || testingAll || totalAccounts === 0}
                        className="flex items-center px-3 py-1.5 bg-destructive/10 text-destructive border border-destructive/20 rounded-lg hover:bg-destructive/20 transition-colors text-xs font-medium disabled:opacity-50"
                    >
                        {togglingAllEnabled && <span className="animate-spin mr-2">⟳</span>}
                        {t('accountManager.disableAllAccounts')}
                    </button>
                    <button
                        onClick={() => onToggleAllAccountsEnabled(true)}
                        disabled={elasticPoolEnabled || togglingAllEnabled || testingAll || totalAccounts === 0}
                        className="flex items-center px-3 py-1.5 bg-secondary text-secondary-foreground rounded-lg hover:bg-secondary/80 transition-colors text-xs font-medium border border-border disabled:opacity-50"
                    >
                        {t('accountManager.enableAllAccounts')}
                    </button>
                </div>
            </div>

            {testingAll && batchProgress.total > 0 && (
                <div className="p-4 border-b border-border bg-muted/30">
                    <div className="flex items-center justify-between text-sm mb-2">
                        <span className="font-medium">{t('accountManager.testingAllAccounts')}</span>
                        <span className="text-muted-foreground">{batchProgress.current} / {batchProgress.total}</span>
                    </div>
                    <div className="w-full bg-muted rounded-full h-2 overflow-hidden mb-4">
                        <div
                            className="bg-primary h-full transition-all duration-300"
                            style={{ width: `${(batchProgress.current / batchProgress.total) * 100}%` }}
                        />
                    </div>
                    {batchProgress.results.length > 0 && (
                        <div className="grid grid-cols-2 md:grid-cols-4 gap-2 max-h-32 overflow-y-auto custom-scrollbar">
                            {batchProgress.results.map((r, i) => (
                                <div key={i} className={clsx(
                                    "text-xs px-2 py-1 rounded border truncate",
                                    r.success ? "bg-emerald-500/10 border-emerald-500/20 text-emerald-500" : "bg-destructive/10 border-destructive/20 text-destructive"
                                )}>
                                    {r.success ? '✓' : '✗'} {r.id}
                                </div>
                            ))}
                        </div>
                    )}
                </div>
            )}

            <div className="divide-y divide-border">
                {loadingAccounts ? (
                    <div className="p-8 text-center text-muted-foreground">{t('actions.loading')}</div>
                ) : accounts.length > 0 ? (
                    accounts.map((acc, i) => {
                        const id = resolveAccountIdentifier(acc)
                        const assignedProxy = proxies.find(proxy => proxy.id === acc.proxy_id)
                        const runtimeUnknown = envBacked && !acc.test_status
                        const isDisabled = acc.enabled === false
                        const isBanned = acc.banned === true
                        const isMuted = acc.muted === true
                        const mutedRecoverAt = formatMuteUntil(acc.muted_until)
                        const isActive = !isDisabled && !isBanned && !isMuted && (acc.test_status === 'ok' || acc.has_token)
                        return (
                            <div key={i} className={clsx(
                                "p-4 flex flex-col md:flex-row md:items-center justify-between gap-4 hover:bg-muted/50 transition-colors min-w-0",
                                (isDisabled || isBanned || isMuted) && "opacity-60"
                            )}>
                                <div className="flex items-center gap-3 min-w-0">
                                    <div className={clsx(
                                        "w-2 h-2 rounded-full shrink-0",
                                        isDisabled ? "bg-red-500 shadow-[0_0_8px_rgba(239,68,68,0.5)]" :
                                        isBanned ? "bg-red-500 shadow-[0_0_8px_rgba(239,68,68,0.5)]" :
                                        isMuted ? "bg-orange-500 shadow-[0_0_8px_rgba(249,115,22,0.5)]" :
                                        acc.test_status === 'failed' ? "bg-red-500 shadow-[0_0_8px_rgba(239,68,68,0.5)]" :
                                        isActive ? "bg-emerald-500 shadow-[0_0_8px_rgba(16,185,129,0.5)]" :
                                        runtimeUnknown ? "bg-blue-500 shadow-[0_0_8px_rgba(59,130,246,0.5)]" : "bg-amber-500"
                                    )} />
                                    <div className="min-w-0">
                                        <div className="text-sm font-medium truncate">{acc.name || '-'}</div>
                                        <div
                                            className="font-medium truncate flex items-center gap-1.5 cursor-pointer hover:text-primary transition-colors group"
                                            onClick={() => copyId(id)}
                                        >
                                            <span className="truncate">{id || '-'}</span>
                                            {copiedId === id
                                                ? <Check className="w-3 h-3 text-emerald-500 shrink-0" />
                                                : <Copy className="w-3 h-3 opacity-0 group-hover:opacity-50 shrink-0 transition-opacity" />
                                            }
                                        </div>
                                        {acc.remark && (
                                            <div className="text-xs text-muted-foreground truncate mt-0.5">{acc.remark}</div>
                                        )}
                                        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground mt-0.5 min-w-0">
                                            <span>{isBanned ? (acc.disabled_reason || t('accountManager.accountBanned')) : isDisabled ? t('accountManager.accountDisabled') : isMuted ? t('accountManager.accountMuted') : acc.test_status === 'failed' ? t('accountManager.testStatusFailed') : isActive ? t('accountManager.sessionActive') : runtimeUnknown ? t('accountManager.runtimeStatusUnknown') : t('accountManager.reauthRequired')}</span>
                                            {isDisabled && !isBanned && (
                                                <span className="font-mono bg-red-500/10 text-red-500 px-1.5 py-0.5 rounded text-[10px]" title={t('accountManager.accountDisabledHint')}>
                                                    {t('accountManager.accountDisabled')}
                                                </span>
                                            )}
                                            {isBanned && (
                                                <span className="font-mono bg-red-500/10 text-red-500 px-1.5 py-0.5 rounded text-[10px]" title={acc.disabled_reason || t('accountManager.accountBannedHint')}>
                                                    {acc.disabled_reason || t('accountManager.accountBanned')}
                                                </span>
                                            )}
                                            {isMuted && (
                                                <span className="font-mono bg-orange-500/10 text-orange-500 px-1.5 py-0.5 rounded text-[10px]" title={t('accountManager.accountMutedHint')}>
                                                    {t('accountManager.accountMutedRecoverAt', { time: mutedRecoverAt })}
                                                </span>
                                            )}
                                            {acc.token_preview && (
                                                <span className="font-mono bg-muted px-1.5 py-0.5 rounded text-[10px]">
                                                    {acc.token_preview}
                                                </span>
                                            )}
                                            {sessionCounts && sessionCounts[id] !== undefined && (
                                                <span className="font-mono bg-blue-500/10 text-blue-500 px-1.5 py-0.5 rounded text-[10px]">
                                                    {t('accountManager.sessionCount', { count: sessionCounts[id] })}
                                                </span>
                                            )}
                                            {sessionCounts && sessionCounts[id] !== undefined && sessionCounts[id] > 0 && (
                                                <button
                                                    onClick={() => onDeleteAllSessions(id)}
                                                    disabled={deletingSessions && deletingSessions[id]}
                                                    className="flex items-center gap-1 font-mono bg-red-500/10 text-red-500 hover:bg-red-500/20 px-1.5 py-0.5 rounded text-[10px] transition-colors disabled:opacity-50"
                                                    title={t('accountManager.deleteAllSessions')}
                                                >
                                                    {deletingSessions && deletingSessions[id] ? (
                                                        <span className="animate-spin">⟳</span>
                                                    ) : (
                                                        <FolderX className="w-3 h-3" />
                                                    )}
                                                </button>
                                            )}
                                            {acc.proxy_id && (
                                                <span className="font-mono bg-amber-500/10 text-amber-500 px-1.5 py-0.5 rounded text-[10px]">
                                                    {t('accountManager.proxyBadge', { name: assignedProxy ? (assignedProxy.name || `${assignedProxy.host}:${assignedProxy.port}`) : acc.proxy_id })}
                                                </span>
                                            )}
                                            {acc.pool_type === 'no_tools' && (
                                                <span className="font-mono bg-blue-500/10 text-blue-500 px-1.5 py-0.5 rounded text-[10px]">
                                                    {t('accountManager.poolBadgeNoTools')}
                                                </span>
                                            )}
                                            {acc.pool_type === 'tools_only' && (
                                                <span className="font-mono bg-purple-500/10 text-purple-500 px-1.5 py-0.5 rounded text-[10px]">
                                                    {t('accountManager.poolBadgeToolsOnly')}
                                                </span>
                                            )}
                                            {acc.device_id_type === 'real' && (
                                                <span className="font-mono bg-emerald-500/10 text-emerald-500 px-1.5 py-0.5 rounded text-[10px]" title={t('accountManager.deviceBadgeRealTitle')}>
                                                    {t('accountManager.deviceBadgeReal')}
                                                </span>
                                            )}
                                        </div>
                                    </div>
                                </div>
                                <AccountActions key={i}>
                                    {({ containerRef, selectRef, proxyWidth }) => (
                                <div
                                    ref={containerRef}
                                    className="flex items-center gap-1.5 sm:gap-2 w-[calc(100%-1.25rem)] md:w-auto self-stretch md:self-auto ml-5 lg:ml-0 min-w-0 overflow-hidden"
                                >
                                    <button
                                        type="button"
                                        role="switch"
                                        aria-checked={!isDisabled}
                                        onClick={() => onToggleAccountEnabled(id, isDisabled)}
                                        disabled={elasticPoolEnabled || togglingEnabled?.[id]}
                                        title={isDisabled ? t('accountManager.enableAccount') : t('accountManager.disableAccount')}
                                        className={clsx(
                                            "relative inline-flex h-5 w-9 items-center rounded-full transition-colors disabled:opacity-50 disabled:cursor-not-allowed shrink-0",
                                            isDisabled ? "bg-muted-foreground/30" : "bg-primary"
                                        )}
                                    >
                                        <span className={clsx(
                                            "inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform shadow-sm",
                                            isDisabled ? "translate-x-1" : "translate-x-[18px]"
                                        )} />
                                    </button>
                                    <select
                                        ref={selectRef}
                                        value={acc.proxy_id || ''}
                                        onChange={e => onUpdateAccountProxy(id, e.target.value)}
                                        disabled={updatingProxy?.[id]}
                                        style={{ width: `${proxyWidth}px` }}
                                        className="flex-none px-1.5 sm:px-2.5 py-1.5 text-[10px] lg:text-xs bg-secondary border border-border rounded-md focus:outline-none focus:ring-1 focus:ring-ring disabled:opacity-50"
                                    >
                                        <option value="">{t('accountManager.proxyNone')}</option>
                                        {proxies.map(proxy => (
                                            <option key={proxy.id} value={proxy.id}>
                                                {proxy.name || `${proxy.host}:${proxy.port}`}
                                            </option>
                                        ))}
                                    </select>
                                    <button
                                        onClick={() => onEditAccount(acc)}
                                        disabled={!id}
                                        className="p-1 lg:p-1.5 text-muted-foreground hover:text-primary hover:bg-primary/10 rounded-md transition-colors disabled:opacity-40 disabled:cursor-not-allowed shrink-0"
                                        title={id ? t('accountManager.editAccountTitle') : t('accountManager.invalidIdentifier')}
                                    >
                                        <Pencil className="w-3.5 h-3.5 lg:w-4 lg:h-4" />
                                    </button>
                                    <button
                                        onClick={() => onTestAccount(id)}
                                        disabled={testing[id]}
                                        className="px-2 lg:px-3 py-1 lg:py-1.5 text-[10px] lg:text-xs font-medium border border-border rounded-md hover:bg-secondary transition-colors disabled:opacity-50 shrink-0 whitespace-nowrap"
                                    >
                                        {testing[id] ? t('actions.testing') : t('actions.test')}
                                    </button>
                                    <button
                                        onClick={() => onDeleteAccount(id)}
                                        className="p-1 lg:p-1.5 text-muted-foreground hover:text-destructive hover:bg-destructive/10 rounded-md transition-colors shrink-0"
                                    >
                                        <Trash2 className="w-3.5 h-3.5 lg:w-4 lg:h-4" />
                                    </button>
                                </div>
                                    )}
                                </AccountActions>
                            </div>
                        )
                    })
                ) : (
                    <div className="p-8 text-center text-muted-foreground">{searchQuery ? t('accountManager.searchNoResults') : t('accountManager.noAccounts')}</div>
                )}
            </div>

            {totalPages > 1 && (
                <div className="p-4 border-t border-border flex items-center justify-between">
                    <div className="flex items-center gap-3">
                        <div className="text-sm text-muted-foreground">
                            {t('accountManager.pageInfo', { current: page, total: totalPages, count: totalAccounts })}
                        </div>
                        <select
                            value={pageSize}
                            onChange={e => onPageSizeChange(Number(e.target.value))}
                            className="text-sm border border-border rounded-md px-2 py-1 bg-background text-foreground"
                        >
                            {[10, 20, 50, 100, 500, 1000, 2000, 5000].map(s => (
                                <option key={s} value={s}>{s}</option>
                            ))}
                        </select>
                    </div>
                    <div className="flex items-center gap-2">
                        <button
                            onClick={onPrevPage}
                            disabled={page <= 1 || loadingAccounts}
                            className="p-2 border border-border rounded-md hover:bg-secondary transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                        >
                            <ChevronLeft className="w-4 h-4" />
                        </button>
                        <span className="text-sm font-medium px-2">{page} / {totalPages}</span>
                        <button
                            onClick={onNextPage}
                            disabled={page >= totalPages || loadingAccounts}
                            className="p-2 border border-border rounded-md hover:bg-secondary transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                        >
                            <ChevronRight className="w-4 h-4" />
                        </button>
                    </div>
                </div>
            )}
        </div>
    )
}

function formatMuteUntil(muteUntil) {
    if (!muteUntil || muteUntil <= 0) return '--'
    const d = new Date(muteUntil * 1000)
    const month = String(d.getMonth() + 1).padStart(2, '0')
    const day = String(d.getDate()).padStart(2, '0')
    const hour = String(d.getHours()).padStart(2, '0')
    const min = String(d.getMinutes()).padStart(2, '0')
    return `${month}月${day}日 ${hour}:${min}`
}
