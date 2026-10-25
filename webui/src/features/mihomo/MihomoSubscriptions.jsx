import { useState } from 'react'
import { Loader2, Plus, RefreshCw, Trash2 } from 'lucide-react'

import { useI18n } from '../../i18n'

function formatUpdatedAt(t, ts) {
    if (!ts) {
        return t('mihomoBridge.updatedAt', { time: t('mihomoBridge.never') })
    }
    return t('mihomoBridge.updatedAt', { time: new Date(ts * 1000).toLocaleString() })
}

function SubscriptionRow({ t, sub, busy, onRefresh, onDelete }) {
    const refreshing = Boolean(busy?.[`refresh:${sub.id}`])
    const deleting = Boolean(busy?.[`delete:${sub.id}`])
    return (
        <div className="p-4 md:p-5 flex flex-col lg:flex-row lg:items-center justify-between gap-4 hover:bg-muted/40 transition-colors">
            <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                    <div className="font-medium text-foreground">{sub.name || sub.url}</div>
                    <span className="inline-flex items-center rounded-full border border-primary/20 bg-primary/10 px-2 py-1 text-[10px] font-medium text-primary">
                        {t('mihomoBridge.nodeCount', { count: sub.node_count ?? 0 })}
                    </span>
                </div>
                <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                    <span className="font-mono bg-muted/30 px-2 py-1 rounded border border-border truncate max-w-md" title={sub.url}>
                        {sub.url}
                    </span>
                    <span>{formatUpdatedAt(t, sub.updated_at)}</span>
                </div>
            </div>

            <div className="flex items-center gap-2 self-start lg:self-auto">
                <button
                    onClick={() => onRefresh(sub)}
                    disabled={refreshing || deleting}
                    className="inline-flex items-center gap-1 px-3 py-1.5 rounded-md border border-border hover:bg-secondary transition-colors text-xs font-medium disabled:opacity-50"
                >
                    {refreshing ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <RefreshCw className="w-3.5 h-3.5" />}
                    {t('mihomoBridge.refreshAction')}
                </button>
                <button
                    onClick={() => onDelete(sub)}
                    disabled={refreshing || deleting}
                    className="p-2 text-muted-foreground hover:text-destructive hover:bg-destructive/10 rounded-md transition-colors disabled:opacity-50"
                    title={t('actions.delete')}
                >
                    {deleting ? <Loader2 className="w-4 h-4 animate-spin" /> : <Trash2 className="w-4 h-4" />}
                </button>
            </div>
        </div>
    )
}

export default function MihomoSubscriptions({ subscriptions, busy, onAdd, onRefresh, onDelete }) {
    const { t } = useI18n()
    const [name, setName] = useState('')
    const [url, setUrl] = useState('')
    const adding = Boolean(busy?.addSub)

    const submit = async () => {
        if (!url.trim()) return
        const ok = await onAdd(name.trim(), url.trim())
        if (ok) {
            setName('')
            setUrl('')
        }
    }

    return (
        <div className="bg-card border border-border rounded-xl overflow-hidden shadow-sm">
            <div className="p-6 border-b border-border">
                <h2 className="text-lg font-semibold">{t('mihomoBridge.subsTitle')}</h2>
                <p className="text-sm text-muted-foreground mt-1">{t('mihomoBridge.subsDesc')}</p>

                <div className="mt-4 grid gap-3 md:grid-cols-[220px_1fr_auto]">
                    <input
                        type="text"
                        className="input-field"
                        placeholder={t('mihomoBridge.subNamePlaceholder')}
                        aria-label={t('mihomoBridge.subNameLabel')}
                        value={name}
                        onChange={e => setName(e.target.value)}
                    />
                    <input
                        type="text"
                        className="input-field font-mono"
                        placeholder={t('mihomoBridge.subUrlPlaceholder')}
                        aria-label={t('mihomoBridge.subUrlLabel')}
                        value={url}
                        onChange={e => setUrl(e.target.value)}
                        onKeyDown={e => { if (e.key === 'Enter') submit() }}
                    />
                    <button
                        onClick={submit}
                        disabled={adding || !url.trim()}
                        className="flex items-center justify-center gap-2 px-4 py-2 bg-primary text-primary-foreground rounded-lg hover:bg-primary/90 transition-colors font-medium text-sm shadow-sm disabled:opacity-50"
                    >
                        {adding ? <Loader2 className="w-4 h-4 animate-spin" /> : <Plus className="w-4 h-4" />}
                        {adding ? t('mihomoBridge.adding') : t('mihomoBridge.addSub')}
                    </button>
                </div>
            </div>

            {subscriptions.length === 0 ? (
                <div className="p-10 text-center text-muted-foreground">{t('mihomoBridge.noSubs')}</div>
            ) : (
                <div className="divide-y divide-border">
                    {subscriptions.map(sub => (
                        <SubscriptionRow
                            key={sub.id}
                            t={t}
                            sub={sub}
                            busy={busy}
                            onRefresh={onRefresh}
                            onDelete={onDelete}
                        />
                    ))}
                </div>
            )}
        </div>
    )
}
