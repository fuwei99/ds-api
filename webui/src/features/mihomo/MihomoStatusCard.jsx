import { useEffect, useState } from 'react'
import { AlertTriangle, Download, Loader2, Power, RefreshCw } from 'lucide-react'
import clsx from 'clsx'

import { useI18n } from '../../i18n'

function StatusBadge({ t, status }) {
    if (!status?.supported) {
        return (
            <span className="inline-flex items-center rounded-full border border-border bg-muted/20 px-2 py-1 text-[10px] font-medium text-muted-foreground">
                {t('mihomoBridge.unsupported')}
            </span>
        )
    }
    const running = Boolean(status?.running)
    return (
        <span
            className={clsx(
                'inline-flex items-center gap-1.5 rounded-full border px-2 py-1 text-[10px] font-medium',
                running
                    ? 'border-emerald-500/20 bg-emerald-500/10 text-emerald-500'
                    : 'border-border bg-muted/20 text-muted-foreground'
            )}
        >
            <span className={clsx('w-1.5 h-1.5 rounded-full', running ? 'bg-emerald-500 animate-pulse' : 'bg-muted-foreground/50')} />
            {running ? t('mihomoBridge.running') : t('mihomoBridge.stopped')}
        </span>
    )
}

function InfoRow({ label, value, mono = true }) {
    return (
        <div className="flex items-center justify-between gap-3 text-xs">
            <span className="text-muted-foreground shrink-0">{label}</span>
            <span className={clsx('truncate text-foreground', mono && 'font-mono')} title={value || ''}>
                {value || '-'}
            </span>
        </div>
    )
}

export default function MihomoStatusCard({ status, busy, onSaveSettings, onApply, onDownloadBinary }) {
    const { t } = useI18n()
    const [form, setForm] = useState({ enabled: false, binary_path: '', base_port: 0, api_port: 0, auto_bind: false })

    useEffect(() => {
        if (!status) return
        setForm({
            enabled: Boolean(status.enabled),
            binary_path: status.binary_path || '',
            base_port: status.base_port || 0,
            api_port: status.api_port || 0,
            auto_bind: Boolean(status.auto_bind),
        })
    }, [status])

    const saving = Boolean(busy?.settings)
    const applying = Boolean(busy?.apply)
    const downloading = Boolean(busy?.binary)
    const binaryFound = Boolean(status?.binary_found)
    const download = status?.download || {}
    const downloadProgress = download.progress || 0
    const startedAt = status?.started_at > 0 ? new Date(status.started_at * 1000).toLocaleString() : ''

    return (
        <div className="bg-card border border-border rounded-xl overflow-hidden shadow-sm">
            <div className="p-6 border-b border-border flex flex-col md:flex-row md:items-center justify-between gap-4">
                <div>
                    <div className="flex items-center gap-3">
                        <h2 className="text-lg font-semibold">{t('mihomoBridge.statusTitle')}</h2>
                        <StatusBadge t={t} status={status} />
                    </div>
                    <p className="text-sm text-muted-foreground mt-1">{t('mihomoBridge.statusDesc')}</p>
                </div>
                <div className="flex items-center gap-2 shrink-0">
                    {!binaryFound && (
                        <button
                            onClick={onDownloadBinary}
                            disabled={downloading || !status?.supported}
                            className="flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground hover:bg-primary/90 transition-colors font-medium text-sm disabled:opacity-50"
                            title={t('mihomoBridge.downloadHint')}
                        >
                            {downloading ? <Loader2 className="w-4 h-4 animate-spin" /> : <Download className="w-4 h-4" />}
                            {downloading
                                ? t('mihomoBridge.downloading', { progress: downloadProgress })
                                : t('mihomoBridge.downloadAction')}
                        </button>
                    )}
                    <button
                        onClick={onApply}
                        disabled={applying || !status?.supported}
                        className="flex items-center gap-2 px-4 py-2 rounded-lg border border-border hover:bg-secondary transition-colors font-medium text-sm disabled:opacity-50"
                    >
                        {applying ? <Loader2 className="w-4 h-4 animate-spin" /> : <RefreshCw className="w-4 h-4" />}
                        {applying ? t('mihomoBridge.applying') : t('mihomoBridge.applyAction')}
                    </button>
                </div>
            </div>

            <div className="p-6 grid gap-6 lg:grid-cols-[1fr_280px]">
                <div className="space-y-4">
                    <label className="flex items-center gap-2.5 text-sm font-medium cursor-pointer select-none">
                        <input
                            type="checkbox"
                            className="w-4 h-4 accent-primary"
                            checked={form.enabled}
                            onChange={e => setForm({ ...form, enabled: e.target.checked })}
                        />
                        <Power className="w-4 h-4 text-muted-foreground" />
                        {t('mihomoBridge.enabledLabel')}
                    </label>

                    <label className="flex items-start gap-2.5 text-sm font-medium cursor-pointer select-none">
                        <input
                            type="checkbox"
                            className="w-4 h-4 accent-primary mt-0.5"
                            checked={form.auto_bind}
                            onChange={e => setForm({ ...form, auto_bind: e.target.checked })}
                        />
                        <span>
                            <span>{t('mihomoBridge.autoBindLabel')}</span>
                            <span className="block text-xs font-normal text-muted-foreground mt-0.5">
                                {t('mihomoBridge.autoBindHint')}
                            </span>
                        </span>
                    </label>

                    <div>
                        <label className="block text-sm font-medium mb-1.5">{t('mihomoBridge.binaryLabel')}</label>
                        <input
                            type="text"
                            className="input-field font-mono"
                            placeholder={t('mihomoBridge.binaryPlaceholder')}
                            value={form.binary_path}
                            onChange={e => setForm({ ...form, binary_path: e.target.value })}
                        />
                    </div>

                    <div className="grid grid-cols-2 gap-4">
                        <div>
                            <label className="block text-sm font-medium mb-1.5">{t('mihomoBridge.basePortLabel')}</label>
                            <input
                                type="number"
                                min="1"
                                max="65535"
                                className="input-field"
                                value={form.base_port || ''}
                                onChange={e => setForm({ ...form, base_port: Number(e.target.value) || 0 })}
                            />
                        </div>
                        <div>
                            <label className="block text-sm font-medium mb-1.5">{t('mihomoBridge.apiPortLabel')}</label>
                            <input
                                type="number"
                                min="1"
                                max="65535"
                                className="input-field"
                                value={form.api_port || ''}
                                onChange={e => setForm({ ...form, api_port: Number(e.target.value) || 0 })}
                            />
                        </div>
                    </div>

                    <div className="flex items-center gap-3 pt-1">
                        <button
                            onClick={() => onSaveSettings(form)}
                            disabled={saving || !status?.supported}
                            className="px-4 py-2 bg-primary text-primary-foreground rounded-lg hover:bg-primary/90 transition-colors text-sm font-medium disabled:opacity-50 flex items-center gap-2"
                        >
                            {saving && <Loader2 className="w-4 h-4 animate-spin" />}
                            {t('mihomoBridge.saveSettings')}
                        </button>
                        {form.enabled && !status?.binary && (
                            <span className="text-xs text-amber-500">{t('mihomoBridge.confirmEnableHint')}</span>
                        )}
                    </div>

                    {download.state === 'error' && download.error && (
                        <div className="rounded-lg border border-destructive/25 bg-destructive/10 px-3 py-2 text-xs text-destructive flex items-start gap-2">
                            <AlertTriangle className="w-3.5 h-3.5 mt-0.5 shrink-0" />
                            <div>
                                <div className="font-semibold">{t('mihomoBridge.downloadError')}</div>
                                <div className="mt-0.5 break-all">{download.error}</div>
                            </div>
                        </div>
                    )}

                    {status?.last_error && (
                        <div className="rounded-lg border border-destructive/25 bg-destructive/10 px-3 py-2 text-xs text-destructive flex items-start gap-2">
                            <AlertTriangle className="w-3.5 h-3.5 mt-0.5 shrink-0" />
                            <div>
                                <div className="font-semibold">{t('mihomoBridge.lastError')}</div>
                                <div className="mt-0.5 break-all">{status.last_error}</div>
                            </div>
                        </div>
                    )}
                </div>

                <div className="rounded-lg border border-border/60 bg-background/60 p-4 space-y-2.5 h-fit">
                    <InfoRow label={t('mihomoBridge.binaryPath')} value={status?.binary || t('mihomoBridge.notConfigured')} />
                    <InfoRow label={t('mihomoBridge.workDir')} value={status?.work_dir} />
                    <InfoRow label={t('mihomoBridge.apiAddr')} value={status?.api_addr} />
                    <InfoRow label={t('mihomoBridge.listenersCount')} value={String(status?.listeners?.length ?? 0)} mono={false} />
                    {status?.health && (
                        <InfoRow
                            label={t('mihomoBridge.healthLabel')}
                            value={t('mihomoBridge.healthSummary', {
                                ok: status.health.available ?? 0,
                                dead: status.health.dead ?? 0,
                            })}
                            mono={false}
                        />
                    )}
                    {status?.running && startedAt && (
                        <InfoRow label={t('mihomoBridge.startedAt')} value={startedAt} mono={false} />
                    )}
                </div>
            </div>
        </div>
    )
}
