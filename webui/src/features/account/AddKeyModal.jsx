import { X } from 'lucide-react'
import { v4 as uuidv4 } from 'uuid'

import { maskSecret } from '../../utils/maskSecret'

export default function AddKeyModal({ show, t, editingKey, newKey, setNewKey, loading, onClose, onAdd }) {
    if (!show) {
        return null
    }

    const isEditing = Boolean(editingKey?.key)
    const displayKey = isEditing ? maskSecret(editingKey?.key || newKey.key) : newKey.key

    return (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm p-4 animate-in fade-in">
            <div className="bg-card w-full max-w-md rounded-xl border border-border shadow-2xl overflow-hidden animate-in zoom-in-95">
                <div className="p-4 border-b border-border flex justify-between items-center">
                    <h3 className="font-semibold">{isEditing ? t('accountManager.modalEditKeyTitle') : t('accountManager.modalAddKeyTitle')}</h3>
                    <button onClick={onClose} className="text-muted-foreground hover:text-foreground">
                        <X className="w-5 h-5" />
                    </button>
                </div>
                <div className="p-6 space-y-4">
                    <div>
                        <label className="block text-sm font-medium mb-1.5">{isEditing ? t('accountManager.keyLabel') : t('accountManager.newKeyLabel')}</label>
                        <div className="flex gap-2">
                            <input
                                type="text"
                                className={isEditing ? "input-field bg-muted/30 flex-1 cursor-not-allowed" : "input-field bg-background flex-1"}
                                placeholder={isEditing ? t('accountManager.keyReadonlyPlaceholder') : t('accountManager.newKeyPlaceholder')}
                                value={displayKey}
                                onChange={e => setNewKey({ ...newKey, key: e.target.value })}
                                autoFocus={!isEditing}
                                readOnly={isEditing}
                            />
                            {!isEditing && (
                                <button
                                    type="button"
                                    onClick={() => setNewKey({ ...newKey, key: 'sk-' + uuidv4().replace(/-/g, '') })}
                                    className="px-3 py-2 bg-secondary text-secondary-foreground rounded-lg hover:bg-secondary/80 transition-colors text-sm font-medium border border-border whitespace-nowrap"
                                >
                                    {t('accountManager.generate')}
                                </button>
                            )}
                        </div>
                        <p className="text-xs text-muted-foreground mt-1.5">
                            {isEditing ? t('accountManager.keyReadonlyHint') : t('accountManager.generateHint')}
                        </p>
                    </div>
                    <div>
                        <label className="block text-sm font-medium mb-1.5">{t('accountManager.nameOptional')}</label>
                        <input
                            type="text"
                            className="input-field"
                            placeholder={t('accountManager.namePlaceholder')}
                            value={newKey.name}
                            onChange={e => setNewKey({ ...newKey, name: e.target.value })}
                            autoFocus={isEditing}
                        />
                    </div>
                    <div>
                        <label className="block text-sm font-medium mb-1.5">{t('accountManager.remarkOptional')}</label>
                        <input
                            type="text"
                            className="input-field"
                            placeholder={t('accountManager.remarkPlaceholder')}
                            value={newKey.remark}
                            onChange={e => setNewKey({ ...newKey, remark: e.target.value })}
                        />
                    </div>
                    <div className="pt-1">
                        <label className="flex items-center gap-3 cursor-pointer">
                            <button
                                type="button"
                                role="switch"
                                aria-checked={!!newKey.force_disable_tools}
                                onClick={() => setNewKey({ ...newKey, force_disable_tools: !newKey.force_disable_tools })}
                                className={`relative inline-flex h-6 w-11 shrink-0 items-center rounded-full border transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 focus-visible:ring-offset-card ${newKey.force_disable_tools ? 'border-amber-500 bg-amber-500' : 'border-border bg-muted'}`}
                            >
                                <span className={`inline-block h-4 w-4 transform rounded-full shadow-sm transition-transform ${newKey.force_disable_tools ? 'translate-x-6 bg-white' : 'translate-x-1 bg-muted-foreground'}`} />
                            </button>
                            <span className="text-sm font-medium">{t('accountManager.forceDisableToolsLabel')}</span>
                        </label>
                        <p className="text-xs text-amber-600 dark:text-amber-400 mt-1.5">{t('accountManager.forceDisableToolsHint')}</p>
                    </div>
                    <div className="flex justify-end gap-2 pt-2">
                        <button onClick={onClose} className="px-4 py-2 rounded-lg border border-border hover:bg-secondary transition-colors text-sm font-medium">{t('actions.cancel')}</button>
                        <button onClick={onAdd} disabled={loading} className="px-4 py-2 bg-primary text-primary-foreground rounded-lg hover:bg-primary/90 transition-colors text-sm font-medium disabled:opacity-50">
                            {loading
                                ? (isEditing ? t('accountManager.editKeyLoading') : t('accountManager.addKeyLoading'))
                                : (isEditing ? t('accountManager.editKeyAction') : t('accountManager.addKeyAction'))}
                        </button>
                    </div>
                </div>
            </div>
        </div>
    )
}
