import { useState } from 'react'
import { Trash2, X } from 'lucide-react'

export default function DeviceIdModal({
    show,
    t,
    items,
    loading,
    deleting,
    onClose,
    onAdd,
    onDelete,
}) {
    const [value, setValue] = useState('')
    const [error, setError] = useState('')

    if (!show) {
        return null
    }

    const submit = async () => {
        const raw = String(value || '').trim()
        if (!raw) {
            setError(t('accountManager.deviceIdEmptyInput'))
            return
        }
        const message = await onAdd(raw)
        if (message) {
            setError(message)
            return
        }
        setError('')
        setValue('')
    }

    const close = () => {
        setError('')
        setValue('')
        onClose()
    }

    return (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm p-4 animate-in fade-in">
            <div className="bg-card w-full max-w-2xl rounded-xl border border-border shadow-2xl overflow-hidden animate-in zoom-in-95">
                <div className="p-4 border-b border-border flex justify-between items-center">
                    <h3 className="font-semibold">{t('accountManager.deviceIdModalTitle')}</h3>
                    <button onClick={close} className="text-muted-foreground hover:text-foreground">
                        <X className="w-5 h-5" />
                    </button>
                </div>
                <div className="p-4 sm:p-6 space-y-4">
                    <p className="text-xs text-muted-foreground leading-relaxed">
                        {t('accountManager.deviceIdModalDesc')}
                    </p>
                    <div>
                        <div className="flex items-start gap-2">
                            <textarea
                                className="input-field font-mono text-xs flex-1 min-w-0 resize-y"
                                rows={4}
                                placeholder={t('accountManager.deviceIdPlaceholder')}
                                value={value}
                                onChange={e => {
                                    setValue(e.target.value)
                                    if (error) setError('')
                                }}
                                onKeyDown={e => {
                                    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
                                        e.preventDefault()
                                        submit()
                                    }
                                }}
                            />
                            <button
                                onClick={submit}
                                disabled={loading}
                                className="px-4 py-2 bg-amber-400 text-amber-950 rounded-lg hover:bg-amber-500 transition-colors text-sm font-medium shrink-0 disabled:opacity-50"
                            >
                                {loading ? t('actions.saving') : t('accountManager.deviceIdSubmit')}
                            </button>
                        </div>
                        <p className="mt-1.5 text-xs text-muted-foreground">{t('accountManager.deviceIdHint')}</p>
                        {error && <p className="mt-1 text-xs text-destructive break-all whitespace-pre-line">{error}</p>}
                    </div>

                    <div className="border border-border rounded-lg overflow-hidden">
                        <table className="w-full text-sm">
                            <thead className="bg-muted/50">
                                <tr className="text-left text-xs text-muted-foreground">
                                    <th className="px-3 py-2 font-medium">{t('accountManager.deviceIdColumnId')}</th>
                                    <th className="px-3 py-2 font-medium w-20 sm:w-28">{t('accountManager.deviceIdColumnBound')}</th>
                                    <th className="px-3 py-2 font-medium w-12 sm:w-16">{t('accountManager.deviceIdColumnAction')}</th>
                                </tr>
                            </thead>
                            <tbody className="divide-y divide-border">
                                {items.length > 0 ? items.map(item => (
                                    <tr key={item.id} className="hover:bg-muted/40 transition-colors">
                                        <td className="px-3 py-2 font-mono text-xs align-top">
                                            {/* 窄屏（手机）下 device_id 最长占两行，超出部分省略 */}
                                            <div className="line-clamp-2 break-all" title={item.id}>{item.id}</div>
                                        </td>
                                        <td className="px-3 py-2 text-xs align-top">{item.bound_count ?? 0}</td>
                                        <td className="px-3 py-2 align-top">
                                            <button
                                                onClick={() => onDelete(item.id)}
                                                disabled={Boolean(deleting?.[item.id])}
                                                title={t('accountManager.deviceIdDeleteTitle')}
                                                className="p-1 text-muted-foreground hover:text-destructive hover:bg-destructive/10 rounded-md transition-colors disabled:opacity-50"
                                            >
                                                {deleting?.[item.id]
                                                    ? <span className="animate-spin inline-block w-3.5 h-3.5 text-center">⟳</span>
                                                    : <Trash2 className="w-3.5 h-3.5" />}
                                            </button>
                                        </td>
                                    </tr>
                                )) : (
                                    <tr>
                                        <td colSpan={3} className="px-3 py-6 text-center text-xs text-muted-foreground">
                                            {t('accountManager.deviceIdEmpty')}
                                        </td>
                                    </tr>
                                )}
                            </tbody>
                        </table>
                    </div>
                </div>
            </div>
        </div>
    )
}
