import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { ChevronDown, RefreshCw, Search, X } from 'lucide-react'
import clsx from 'clsx'

import { AUTO_VALUE, NO_PROXY_VALUE, buildProxyOptions, filterProxyOptions } from './bridgeNodeOptions'

// 面板尺寸：宽度固定，高度有上限——节点再多也只占这么高，内部滚动，不铺满整屏。
const PANEL_WIDTH = 256
const PANEL_MAX_HEIGHT = 260
const PANEL_MIN_HEIGHT = 140
// 触发按钮上方/下方至少要有这么多空间，否则换一边弹出。
const PANEL_MIN_SPACE = 200
// 面板固定装饰（搜索框 + 底栏 + 边框）的估算高度，用于把限高留给选项列表。
const PANEL_CHROME_HEIGHT = 66
// 面板与视口边缘、与触发按钮的最小间距。
const VIEWPORT_MARGIN = 8
const TRIGGER_GAP = 4

// accountProxyLabel 计算按钮上的当前值文案（含节点延迟），
// 面板里的每行也复用同一套文案规则。
function accountProxyLabel(t, option) {
    if (!option || option.kind === 'none') return t('accountManager.proxyNone')
    if (option.kind === 'auto') return t('accountManager.proxyAuto')
    if (option.group !== 'node') {
        return option.stale ? `${option.label} ${t('accountManager.proxyOptionStale')}` : option.label
    }
    const parts = [option.label]
    if (option.latency > 0) parts.push(`${option.latency}ms`)
    if (option.stale) parts.push(t('accountManager.proxyOptionStale'))
    return parts.join(' · ')
}

// AccountProxySelect 是账号行里的「账号代理」下拉：列出全部可用的代理桥节点
// 与手动代理。用面板而不是原生 <select> 是因为节点可能有上百个，需要限高滚动
// （原生下拉由系统绘制，无法限高）。
//
// 面板通过 portal 挂在 body 上并用 fixed 定位：列表容器带 overflow-hidden（窄屏
// 收窄下拉宽度用），面板留在行内会被裁掉；fixed 定位同时让"按剩余空间决定向上/
// 向下弹出并限高"成为可能。
export default function AccountProxySelect({
    t, selectRef, width, disabled, proxyID, manualBinding, noProxy, proxies, nodes, nodesAvailable,
    loadingNodes, onReloadNodes, onSelect,
}) {
    const rootRef = useRef(null)
    const panelRef = useRef(null)
    const [open, setOpen] = useState(false)
    const [query, setQuery] = useState('')
    const [panel, setPanel] = useState(null)

    const { current, nodeOptions, proxyOptions } = useMemo(
        () => buildProxyOptions({ proxyID, manualBinding, noProxy, nodes, proxies }),
        [proxyID, manualBinding, noProxy, nodes, proxies]
    )
    const visibleNodes = useMemo(() => filterProxyOptions(nodeOptions, query), [nodeOptions, query])
    const visibleProxies = useMemo(() => filterProxyOptions(proxyOptions, query), [proxyOptions, query])
    const hasOptions = visibleNodes.length > 0 || visibleProxies.length > 0

    // measurePanel 按触发按钮的位置与视口剩余空间算出面板的 fixed 坐标：
    // 下方空间不足时改为向上弹出。向上时把面板底边锚在按钮上沿
    // （translateY(-100%)），面板整体高度随内容变化也始终贴着按钮；
    // 列表限高取可用空间的较小值并封顶，节点再多也只在面板内部滚动。
    const measurePanel = useCallback(() => {
        const rect = rootRef.current?.getBoundingClientRect()
        if (!rect) return
        const viewportWidth = window.innerWidth
        const viewportHeight = window.innerHeight
        const width = Math.min(PANEL_WIDTH, viewportWidth - VIEWPORT_MARGIN * 2)
        const below = viewportHeight - rect.bottom - TRIGGER_GAP - VIEWPORT_MARGIN
        const above = rect.top - TRIGGER_GAP - VIEWPORT_MARGIN
        const up = below < PANEL_MIN_SPACE && above > below
        const room = Math.max((up ? above : below) - PANEL_CHROME_HEIGHT, PANEL_MIN_HEIGHT)
        const height = Math.min(PANEL_MAX_HEIGHT, room)
        const top = up
            ? Math.max(VIEWPORT_MARGIN + PANEL_CHROME_HEIGHT + height, rect.top - TRIGGER_GAP)
            : Math.min(rect.bottom + TRIGGER_GAP, viewportHeight - VIEWPORT_MARGIN - PANEL_CHROME_HEIGHT - height)
        const left = Math.min(Math.max(VIEWPORT_MARGIN, rect.left), Math.max(VIEWPORT_MARGIN, viewportWidth - VIEWPORT_MARGIN - width))
        setPanel({ top, left, width, height, up })
    }, [])

    useEffect(() => {
        if (!open || typeof window === 'undefined') return
        measurePanel()
        window.addEventListener('resize', measurePanel)
        // 页面滚动会让 fixed 面板错位：滚动时重新定位（capture 以覆盖内层滚动容器）。
        window.addEventListener('scroll', measurePanel, true)
        return () => {
            window.removeEventListener('resize', measurePanel)
            window.removeEventListener('scroll', measurePanel, true)
        }
    }, [open, measurePanel])

    useEffect(() => {
        if (!open) return
        const onPointerDown = (event) => {
            // 面板在 portal 里（不在 rootRef 内），必须一并判断，
            // 否则点击选项会先被当成"点击外部"而提前关闭面板。
            if (rootRef.current?.contains(event.target)) return
            if (panelRef.current?.contains(event.target)) return
            setOpen(false)
        }
        const onKeyDown = (event) => {
            if (event.key === 'Escape') setOpen(false)
        }
        document.addEventListener('pointerdown', onPointerDown)
        document.addEventListener('keydown', onKeyDown)
        return () => {
            document.removeEventListener('pointerdown', onPointerDown)
            document.removeEventListener('keydown', onKeyDown)
        }
    }, [open])

    const toggleOpen = () => {
        if (disabled) return
        const next = !open
        setOpen(next)
        if (!next) return
        setQuery('')
        // 打开面板时顺带把节点列表刷新到最新（节流在 hook 里，不会每次打开都请求）。
        onReloadNodes?.()
    }

    const choose = (value) => {
        setOpen(false)
        if (value === current.mode) return
        onSelect?.(value)
    }

    const renderOption = (option) => (
        <button
            key={option.value}
            type="button"
            role="option"
            aria-selected={option.value === current.mode}
            onClick={() => choose(option.value)}
            className={clsx(
                'w-full text-left px-2.5 py-1.5 text-xs flex items-center justify-between gap-2 hover:bg-muted transition-colors',
                option.value === current.mode && 'bg-primary/10 text-primary'
            )}
        >
            <span className="truncate">{accountProxyLabel(t, option)}</span>
            <span className="shrink-0 text-[10px] text-muted-foreground flex items-center gap-1.5">
                {option.group === 'node' && option.bound > 0 && (
                    <span>{t('accountManager.proxyNodeBound', { count: option.bound })}</span>
                )}
                {option.stale && <span className="text-amber-500">{t('accountManager.proxyNodeStale')}</span>}
            </span>
        </button>
    )

    // renderFixedOption 渲染"不绑定具体节点"的两个固定选项：自动调度与不走代理。
    // 这两个选项容易混淆（交回自动调度 vs. 永久直连），但说明文字放在右侧会把
    // 选项名挤成省略号，因此行内只留名字，解释放到悬停提示（title）里。
    const renderFixedOption = (value, labelKey, hintKey) => (
        <button
            key={value}
            type="button"
            role="option"
            aria-selected={value === current.mode}
            title={t(hintKey)}
            onClick={() => choose(value)}
            className={clsx(
                'w-full text-left px-2.5 py-1.5 text-xs flex items-center justify-between gap-2 hover:bg-muted transition-colors',
                value === current.mode && 'bg-primary/10 text-primary'
            )}
        >
            <span className="truncate">{t(labelKey)}</span>
        </button>
    )

    return (
        <div ref={(node) => { rootRef.current = node; if (selectRef) selectRef.current = node }} className="relative flex-none min-w-0" style={{ width: `${width}px` }}>
            <button
                type="button"
                onClick={toggleOpen}
                disabled={disabled}
                title={current.mode === AUTO_VALUE
                    ? (current.label
                        ? t('accountManager.proxyAutoCurrentNode', { name: current.label })
                        : t('accountManager.proxyAutoHint'))
                    : current.kind === 'none'
                        ? t('accountManager.proxyNoneHint')
                        : current.label}
                className={clsx(
                    'w-full flex items-center justify-between gap-1 px-1.5 sm:px-2.5 py-1.5 text-[10px] lg:text-xs bg-secondary border border-border rounded-md focus:outline-none focus:ring-1 focus:ring-ring disabled:opacity-50',
                    open && 'ring-1 ring-ring'
                )}
            >
                <span className={clsx('truncate', current.value === NO_PROXY_VALUE && 'text-muted-foreground')}>
                    {accountProxyLabel(t, current)}
                </span>
                {loadingNodes
                    ? <RefreshCw className="w-3 h-3 shrink-0 animate-spin opacity-60" />
                    : <ChevronDown className={clsx('w-3 h-3 shrink-0 opacity-60 transition-transform', open && 'rotate-180')} />}
            </button>

            {open && panel && createPortal((
                <div
                    ref={panelRef}
                    className="fixed z-50 bg-card border border-border rounded-lg shadow-lg overflow-hidden"
                    style={{
                        top: `${panel.top}px`,
                        left: `${panel.left}px`,
                        width: `${panel.width}px`,
                        transform: panel.up ? 'translateY(-100%)' : undefined,
                    }}
                >
                    <div className="flex items-center gap-1.5 px-2 py-1.5 border-b border-border">
                        <Search className="w-3 h-3 shrink-0 text-muted-foreground" />
                        <input
                            autoFocus
                            value={query}
                            onChange={e => setQuery(e.target.value)}
                            placeholder={t('accountManager.proxySearchPlaceholder')}
                            className="flex-1 min-w-0 bg-transparent text-xs focus:outline-none placeholder:text-muted-foreground"
                        />
                        {query && (
                            <button type="button" onClick={() => setQuery('')} className="shrink-0 text-muted-foreground hover:text-foreground">
                                <X className="w-3 h-3" />
                            </button>
                        )}
                    </div>
                    <div className="overflow-y-auto custom-scrollbar py-1" style={{ maxHeight: `${panel.height}px` }} role="listbox">
                        {renderFixedOption(AUTO_VALUE, 'accountManager.proxyAuto', 'accountManager.proxyAutoHint')}
                        {renderFixedOption(NO_PROXY_VALUE, 'accountManager.proxyNone', 'accountManager.proxyNoneHint')}
                        {visibleNodes.length > 0 && (
                            <div className="px-2.5 pt-2 pb-1 text-[10px] font-medium text-muted-foreground">
                                {t('accountManager.proxyGroupNodes', { count: visibleNodes.length })}
                            </div>
                        )}
                        {visibleNodes.map(renderOption)}
                        {visibleProxies.length > 0 && (
                            <div className="px-2.5 pt-2 pb-1 text-[10px] font-medium text-muted-foreground">
                                {t('accountManager.proxyGroupManual', { count: visibleProxies.length })}
                            </div>
                        )}
                        {visibleProxies.map(renderOption)}
                        {!hasOptions && (
                            <div className="px-2.5 py-2 text-[11px] text-muted-foreground">
                                {proxyOptions.length + nodeOptions.length > 0
                                    ? t('accountManager.proxyNoMatch')
                                    : nodesAvailable
                                        ? t('accountManager.proxyNoAvailableNodes')
                                        : t('accountManager.proxyNodesUnavailable')}
                            </div>
                        )}
                    </div>
                    <div className="flex items-center justify-between gap-2 px-2 py-1.5 border-t border-border text-[10px] text-muted-foreground">
                        <span className="truncate">{t('accountManager.proxyNodesHint')}</span>
                        <button
                            type="button"
                            onClick={() => onReloadNodes?.(true)}
                            disabled={loadingNodes}
                            className="shrink-0 flex items-center gap-1 hover:text-primary transition-colors disabled:opacity-50"
                        >
                            <RefreshCw className={clsx('w-3 h-3', loadingNodes && 'animate-spin')} />
                            {t('accountManager.proxyRefreshNodes')}
                        </button>
                    </div>
                </div>
            ), document.body)}
        </div>
    )
}
