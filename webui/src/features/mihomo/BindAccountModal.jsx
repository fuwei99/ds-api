import { useEffect, useMemo, useRef, useState } from 'react'
import { ChevronLeft, ChevronRight, Link2, Search, User, X } from 'lucide-react'

const PAGE_SIZE = 20

export default function BindAccountModal({
    node,
    candidates = [],
    busy = false,
    onBind,
    onClose,
    t,
}) {
    const [search, setSearch] = useState('')
    const [page, setPage] = useState(1)
    const inputRef = useRef(null)

    useEffect(() => {
        if (!node) return
        setSearch('')
        setPage(1)
        const timer = setTimeout(() => {
            inputRef.current?.focus()
        }, 50)
        return () => clearTimeout(timer)
    }, [node])

    useEffect(() => {
        if (!node) return
        const onKeyDown = (e) => {
            if (e.key === 'Escape') {
                onClose()
            }
        }
        window.addEventListener('keydown', onKeyDown)
        return () => window.removeEventListener('keydown', onKeyDown)
    }, [node, onClose])

    const filtered = useMemo(() => {
        const q = search.trim().toLowerCase()
        if (!q) return candidates
        return candidates.filter(acc => {
            const id = (acc.identifier || '').toLowerCase()
            const label = (acc.label || '').toLowerCase()
            return id.includes(q) || label.includes(q)
        })
    }, [candidates, search])

    const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE))
    const currentPage = Math.min(page, totalPages)
    const displayed = useMemo(() => {
        const start = (currentPage - 1) * PAGE_SIZE
        return filtered.slice(start, start + PAGE_SIZE)
    }, [filtered, currentPage])

    if (!node) return null

    const handleSearchChange = (val) => {
        setSearch(val)
        setPage(1)
    }

    const handleSelect = (identifier) => {
        if (busy) return
        onBind(identifier, node.node_key)
        onClose()
    }

    return (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm p-4 animate-in fade-in">
            <div className="bg-card w-full max-w-lg rounded-xl border border-border shadow-2xl overflow-hidden animate-in zoom-in-95 flex flex-col max-h-[85vh]">
                <div className="p-4 border-b border-border flex justify-between items-center shrink-0">
                    <div className="min-w-0 pr-2">
                        <div className="flex items-center gap-2">
                            <Link2 className="w-4 h-4 text-primary shrink-0" />
                            <h3 className="font-semibold text-base truncate">{t('mihomoBridge.bindModalTitle')}</h3>
                        </div>
                        <p className="text-xs text-muted-foreground mt-1 truncate">
                            {t('mihomoBridge.bindModalDesc', { node: node.name })}
                        </p>
                    </div>
                    <button
                        onClick={onClose}
                        className="p-1 rounded-md text-muted-foreground hover:text-foreground hover:bg-muted transition-colors shrink-0"
                    >
                        <X className="w-5 h-5" />
                    </button>
                </div>

                <div className="p-4 border-b border-border shrink-0 bg-muted/20">
                    <div className="relative">
                        <Search className="w-4 h-4 text-muted-foreground absolute left-3 top-1/2 -translate-y-1/2" />
                        <input
                            ref={inputRef}
                            type="text"
                            value={search}
                            onChange={e => handleSearchChange(e.target.value)}
                            placeholder={t('mihomoBridge.accountSearchPlaceholder')}
                            className="input-field pl-9 text-xs"
                        />
                        {search && (
                            <button
                                onClick={() => handleSearchChange('')}
                                className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                            >
                                <X className="w-3.5 h-3.5" />
                            </button>
                        )}
                    </div>
                </div>

                <div className="p-2 overflow-y-auto flex-1 divide-y divide-border/50">
                    {candidates.length === 0 ? (
                        <div className="p-8 text-center text-xs text-muted-foreground">
                            {t('mihomoBridge.noAvailableAccounts')}
                        </div>
                    ) : filtered.length === 0 ? (
                        <div className="p-8 text-center text-xs text-muted-foreground">
                            {t('mihomoBridge.noAccountsFound')}
                        </div>
                    ) : (
                        displayed.map(acc => (
                            <button
                                key={acc.identifier}
                                onClick={() => handleSelect(acc.identifier)}
                                disabled={busy}
                                className="w-full text-left p-3 rounded-lg hover:bg-secondary/70 transition-colors flex items-center justify-between gap-3 group"
                            >
                                <div className="flex items-center gap-2.5 min-w-0">
                                    <div className="w-7 h-7 rounded-full bg-primary/10 text-primary flex items-center justify-center shrink-0">
                                        <User className="w-3.5 h-3.5" />
                                    </div>
                                    <div className="min-w-0">
                                        <div className="text-xs font-medium text-foreground truncate group-hover:text-primary transition-colors">
                                            {acc.label}
                                        </div>
                                        <div className="text-[11px] font-mono text-muted-foreground truncate">
                                            {acc.identifier}
                                        </div>
                                    </div>
                                </div>
                                <span className="text-[11px] text-muted-foreground group-hover:text-primary font-medium shrink-0">
                                    {t('mihomoBridge.bindAction')} &rarr;
                                </span>
                            </button>
                        ))
                    )}
                </div>

                <div className="p-3 border-t border-border flex flex-wrap justify-between items-center gap-2 text-xs text-muted-foreground shrink-0 bg-muted/10">
                    <span>
                        {filtered.length > 0 && t('mihomoBridge.modalPageInfo', {
                            current: currentPage,
                            total: totalPages,
                            count: filtered.length,
                        })}
                    </span>
                    <div className="flex items-center gap-2">
                        {totalPages > 1 && (
                            <div className="flex items-center gap-1">
                                <button
                                    onClick={() => setPage(p => Math.max(1, p - 1))}
                                    disabled={currentPage <= 1}
                                    className="p-1 border border-border rounded hover:bg-secondary transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
                                >
                                    <ChevronLeft className="w-3.5 h-3.5" />
                                </button>
                                <span className="text-xs font-medium px-1.5">
                                    {currentPage} / {totalPages}
                                </span>
                                <button
                                    onClick={() => setPage(p => Math.min(totalPages, p + 1))}
                                    disabled={currentPage >= totalPages}
                                    className="p-1 border border-border rounded hover:bg-secondary transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
                                >
                                    <ChevronRight className="w-3.5 h-3.5" />
                                </button>
                            </div>
                        )}
                        <button
                            onClick={onClose}
                            className="px-3 py-1.5 rounded-md border border-border hover:bg-secondary text-foreground text-xs font-medium transition-colors"
                        >
                            {t('actions.cancel')}
                        </button>
                    </div>
                </div>
            </div>
        </div>
    )
}
