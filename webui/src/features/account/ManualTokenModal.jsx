import { useState, useEffect } from 'react'
import { X, Copy, Check } from 'lucide-react'

export default function ManualTokenModal({
    show,
    t,
    account,
    token,
    setToken,
    loading,
    onClose,
    onConfirm,
    onMessage,
}) {
    const [copied, setCopied] = useState(false)

    useEffect(() => {
        if (!show) {
            setCopied(false)
        }
    }, [show])

    if (!show || !account) {
        return null
    }

    const handleCopy = async () => {
        const username = account.username || account.identifier || ''
        const password = account.password || ''
        const textToCopy = `${username} ${password}`.trim()
        try {
            if (navigator?.clipboard?.writeText) {
                await navigator.clipboard.writeText(textToCopy)
            } else {
                const textarea = document.createElement('textarea')
                textarea.value = textToCopy
                document.body.appendChild(textarea)
                textarea.select()
                document.execCommand('copy')
                document.body.removeChild(textarea)
            }
            setCopied(true)
            if (onMessage) onMessage('success', t('accountManager.copyAccountAndPasswordSuccess'))
            setTimeout(() => setCopied(false), 2000)
        } catch {
            if (onMessage) onMessage('error', t('messages.copyFailed'))
        }
    }

    return (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm p-4 animate-in fade-in">
            <div className="bg-card w-full max-w-lg rounded-xl border border-border shadow-2xl overflow-hidden animate-in zoom-in-95">
                <div className="p-4 border-b border-border flex justify-between items-center gap-4">
                    <div className="min-w-0">
                        <h3 className="font-semibold text-foreground">{t('accountManager.manualTokenTitle')}</h3>
                        <p className="mt-0.5 text-xs text-muted-foreground truncate">
                            {t('accountManager.manualTokenAccountLabel', { account: account.username || account.identifier })}
                        </p>
                    </div>
                    <button onClick={onClose} className="text-muted-foreground hover:text-foreground transition-colors">
                        <X className="w-5 h-5" />
                    </button>
                </div>
                <div className="p-6 space-y-4">
                    <div>
                        <label className="block text-sm font-medium mb-1.5 text-foreground">
                            Token <span className="text-destructive">*</span>
                        </label>
                        <textarea
                            className="input-field min-h-[120px] font-mono text-xs w-full resize-y"
                            placeholder={t('accountManager.manualTokenPlaceholder')}
                            value={token}
                            onChange={e => setToken(e.target.value)}
                            autoFocus
                        />
                    </div>
                    <div className="flex items-center justify-between gap-2 pt-2">
                        <button
                            type="button"
                            onClick={handleCopy}
                            className="px-3 py-2 rounded-lg border border-border hover:bg-secondary transition-colors text-sm font-medium flex items-center gap-1.5 text-foreground"
                            title={t('accountManager.copyAccountAndPasswordTitle')}
                        >
                            {copied ? <Check className="w-4 h-4 text-emerald-500" /> : <Copy className="w-4 h-4 text-muted-foreground" />}
                            <span>{copied ? t('accountManager.copied') : t('accountManager.copyAccountAndPassword')}</span>
                        </button>
                        <div className="flex items-center gap-2">
                            <button
                                type="button"
                                onClick={onClose}
                                className="px-4 py-2 rounded-lg border border-border hover:bg-secondary transition-colors text-sm font-medium text-foreground"
                            >
                                {t('actions.cancel')}
                            </button>
                            <button
                                type="button"
                                onClick={onConfirm}
                                disabled={loading || !token.trim()}
                                className="px-4 py-2 bg-primary text-primary-foreground rounded-lg hover:bg-primary/90 transition-colors text-sm font-medium disabled:opacity-50"
                            >
                                {loading ? t('accountManager.manualTokenSubmitting') : t('actions.confirm')}
                            </button>
                        </div>
                    </div>
                </div>
            </div>
        </div>
    )
}
