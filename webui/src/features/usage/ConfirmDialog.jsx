import { useCallback, useRef, useState } from 'react'
import { useI18n } from '../../i18n'
import { useDialogA11y } from './useDialogA11y'

// ConfirmDialog is a controlled dialog: the parent mounts it and unmounts via
// onClose. It replaces window.confirm so the prompt matches the app theme and
// is keyboard-friendly (focus trap + Escape cancels).
export default function ConfirmDialog({ title, message, confirmText, cancelText, danger = false, onConfirm, onClose }) {
    const { t } = useI18n()
    const panelRef = useRef(null)
    const [busy, setBusy] = useState(false)

    const cancel = useCallback(() => {
        if (busy) return
        onClose()
    }, [busy, onClose])

    const confirm = useCallback(async () => {
        if (busy) return
        setBusy(true)
        try {
            await onConfirm()
        } finally {
            setBusy(false)
            onClose()
        }
    }, [busy, onConfirm, onClose])

    useDialogA11y(panelRef, cancel)

    return (
        <div className="usage-confirm-overlay" role="dialog" aria-modal="true" aria-label={title}>
            <div className="usage-confirm-panel" ref={panelRef}>
                <div className="usage-confirm-title">{title}</div>
                <div className="usage-confirm-message">{message}</div>
                <div className="usage-confirm-actions">
                    <button type="button" className="usage-btn" onClick={cancel} disabled={busy}>
                        {cancelText || t('actions.cancel')}
                    </button>
                    <button type="button" className={danger ? 'usage-btn usage-btn-danger' : 'usage-btn usage-btn-primary'} onClick={confirm} disabled={busy}>
                        {confirmText || t('usage.settingsDone')}
                    </button>
                </div>
            </div>
        </div>
    )
}
