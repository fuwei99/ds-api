import { ChevronLeft, ChevronRight, X } from 'lucide-react'

// RealDeviceIdModal 是 real 模式下的 device_id 管理弹窗：列出绑定真实设备 id 的
// 账号（服务端分页），按账号或整批刷新 id（可先重置设备档案）。
// 批量按钮作用于全量 real 账号，不受当前页影响。
export default function RealDeviceIdModal({
    show,
    t,
    items,
    loading,
    page,
    pageSize,
    totalPages,
    totalItems,
    totalAll,
    query,
    refreshing,
    bulkRefreshing,
    errors,
    onClose,
    onRefresh,
    onReset,
    onRefreshAll,
    onResetAll,
    onPrevPage,
    onNextPage,
    onPageSizeChange,
    onSearchChange,
}) {
    if (!show) {
        return null
    }
    const bulkBusy = Boolean(bulkRefreshing)
    const bulkButtonClass = "flex items-center px-3 py-1.5 rounded-lg transition-colors text-xs font-medium border disabled:opacity-50"
    const rowButtonClass = "px-2.5 py-1 rounded-md transition-colors text-xs font-medium border disabled:opacity-50"
    const pagerButtonClass = "p-1.5 border border-border rounded-md hover:bg-secondary transition-colors disabled:opacity-50 disabled:cursor-not-allowed"

    return (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm p-4 animate-in fade-in">
            <div className="bg-card w-full max-w-3xl rounded-xl border border-border shadow-2xl overflow-hidden animate-in zoom-in-95">
                <div className="px-4 py-3 sm:px-6 border-b border-border flex items-center justify-between gap-3">
                    <h3 className="font-semibold">{t('accountManager.realDeviceIdModalTitle')}</h3>
                    <button
                        onClick={onClose}
                        aria-label={t('accountManager.realDeviceIdClose')}
                        className="text-muted-foreground hover:text-foreground"
                    >
                        <X className="w-5 h-5" />
                    </button>
                </div>
                <div className="p-4 sm:p-6 space-y-4">
                    {/* 表格外的工具行：搜索在左，批量入口在右 */}
                    <div className="flex flex-wrap items-center justify-between gap-3">
                        <input
                            type="text"
                            value={query}
                            onChange={e => onSearchChange(e.target.value)}
                            placeholder={t('accountManager.searchPlaceholder')}
                            aria-label={t('accountManager.realDeviceIdSearchLabel')}
                            className="w-full sm:w-64 px-3 py-1.5 text-sm bg-muted border border-border rounded-lg focus:outline-none focus:ring-1 focus:ring-ring placeholder:text-muted-foreground"
                        />
                        <div className="flex flex-wrap gap-2">
                            <button
                                onClick={onRefreshAll}
                                disabled={bulkBusy || totalAll === 0}
                                className={`${bulkButtonClass} bg-secondary text-secondary-foreground border-border hover:bg-secondary/80`}
                            >
                                {bulkRefreshing === 'refresh' && <span className="animate-spin mr-1.5">⟳</span>}
                                {t('accountManager.realDeviceIdRefreshAll')}
                            </button>
                            <button
                                onClick={onResetAll}
                                disabled={bulkBusy || totalAll === 0}
                                className={`${bulkButtonClass} bg-destructive/10 text-destructive border-destructive/20 hover:bg-destructive/20`}
                            >
                                {bulkRefreshing === 'reset' && <span className="animate-spin mr-1.5">⟳</span>}
                                {t('accountManager.realDeviceIdResetAll')}
                            </button>
                        </div>
                    </div>
                    <div className="border border-border rounded-lg overflow-hidden">
                        <table className="w-full text-sm">
                            <thead className="bg-muted/50">
                                <tr className="text-left text-xs text-muted-foreground">
                                    <th className="px-3 py-2 font-medium align-middle">{t('accountManager.realDeviceIdColumnAccount')}</th>
                                    <th className="px-3 py-2 font-medium w-64 sm:w-80 text-right">{t('accountManager.realDeviceIdColumnAction')}</th>
                                </tr>
                            </thead>
                            <tbody className="divide-y divide-border">
                                {loading ? (
                                    <tr>
                                        <td colSpan={2} className="px-3 py-6 text-center text-xs text-muted-foreground">
                                            {t('actions.loading')}
                                        </td>
                                    </tr>
                                ) : items.length > 0 ? items.map(item => {
                                    const busy = Boolean(refreshing?.[item.identifier]) || bulkBusy
                                    return (
                                        <tr key={item.identifier} className="hover:bg-muted/40 transition-colors align-top">
                                            <td className="px-3 py-2">
                                                <div className="text-xs break-all">{item.identifier}</div>
                                                {errors?.[item.identifier] && (
                                                    <div className="mt-1 text-[11px] text-destructive break-all whitespace-pre-line">
                                                        {errors[item.identifier]}
                                                    </div>
                                                )}
                                            </td>
                                            <td className="px-3 py-2">
                                                <div className="flex flex-wrap items-center gap-2 justify-end">
                                                    <button
                                                        onClick={() => onRefresh(item.identifier)}
                                                        disabled={busy}
                                                        className={`${rowButtonClass} bg-secondary text-secondary-foreground border-border hover:bg-secondary/80`}
                                                    >
                                                        {refreshing?.[item.identifier] && <span className="animate-spin mr-1">⟳</span>}
                                                        {t('accountManager.realDeviceIdRefresh')}
                                                    </button>
                                                    <button
                                                        onClick={() => onReset(item.identifier)}
                                                        disabled={busy}
                                                        className={`${rowButtonClass} bg-destructive/10 text-destructive border-destructive/20 hover:bg-destructive/20`}
                                                    >
                                                        {t('accountManager.realDeviceIdReset')}
                                                    </button>
                                                </div>
                                            </td>
                                        </tr>
                                    )
                                }) : (
                                    <tr>
                                        <td colSpan={2} className="px-3 py-6 text-center text-xs text-muted-foreground">
                                            {String(query || '').trim()
                                                ? t('accountManager.searchNoResults')
                                                : t('accountManager.realDeviceIdEmpty')}
                                        </td>
                                    </tr>
                                )}
                            </tbody>
                        </table>
                        {totalItems > 0 && (
                            <div className="px-3 py-2 border-t border-border flex items-center justify-between gap-2">
                                <div className="flex items-center gap-3">
                                    <div className="text-xs text-muted-foreground">
                                        {t('accountManager.pageInfo', { current: page, total: totalPages, count: totalItems })}
                                    </div>
                                    <select
                                        value={pageSize}
                                        onChange={e => onPageSizeChange(Number(e.target.value))}
                                        aria-label={t('accountManager.realDeviceIdPageSize')}
                                        className="text-xs border border-border rounded-md px-2 py-1 bg-background text-foreground"
                                    >
                                        {[10, 20, 50, 100, 500, 1000].map(s => (
                                            <option key={s} value={s}>{s}</option>
                                        ))}
                                    </select>
                                </div>
                                <div className="flex items-center gap-2">
                                    <button
                                        onClick={onPrevPage}
                                        disabled={page <= 1 || loading}
                                        aria-label={t('accountManager.realDeviceIdPrevPage')}
                                        title={t('accountManager.realDeviceIdPrevPage')}
                                        className={pagerButtonClass}
                                    >
                                        <ChevronLeft className="w-4 h-4" />
                                    </button>
                                    <span className="text-xs font-medium px-1">{page} / {totalPages}</span>
                                    <button
                                        onClick={onNextPage}
                                        disabled={page >= totalPages || loading}
                                        aria-label={t('accountManager.realDeviceIdNextPage')}
                                        title={t('accountManager.realDeviceIdNextPage')}
                                        className={pagerButtonClass}
                                    >
                                        <ChevronRight className="w-4 h-4" />
                                    </button>
                                </div>
                            </div>
                        )}
                    </div>
                </div>
            </div>
        </div>
    )
}
