import { useCallback, useEffect, useRef, useState } from 'react'
import { useI18n } from '../../i18n'
import ConfirmDialog from './ConfirmDialog'
import { DEFAULT_INPUT_MISS_PRICE, MODEL_PRICE_KEY, peakWindowError } from './usageUtils'
import { useDialogA11y } from './useDialogA11y'

// NumberInput keeps its own text state so intermediate values like "0." or a
// temporarily empty field survive while typing; only parseable values within
// [min, max] are propagated, and blur restores the last valid number.
function NumberInput({ value, onChange, min = 0, max, step = 0.01, ...rest }) {
    const [text, setText] = useState(() => String(value))
    const lastValidRef = useRef(value)
    const lastRenderedRef = useRef(value)

    const inRange = useCallback((n) => {
        if (!Number.isFinite(n) || n < min) return false
        if (max != null && n > max) return false
        return true
    }, [min, max])

    useEffect(() => {
        const n = Number(value)
        if (inRange(n)) {
            lastValidRef.current = n
        }
        if (lastRenderedRef.current !== value) {
            lastRenderedRef.current = value
            setText(String(value))
        }
    }, [value, inRange])

    const handleChange = useCallback((e) => {
        const v = e.target.value
        setText(v)
        const n = Number(v)
        if (v.trim() !== '' && inRange(n)) {
            lastValidRef.current = n
            onChange(n)
        }
    }, [onChange, inRange])

    const handleBlur = useCallback(() => {
        setText(String(lastValidRef.current))
    }, [])

    return (
        <input
            type="number"
            inputMode="decimal"
            min={min}
            max={max}
            step={step}
            value={text}
            onChange={handleChange}
            onBlur={handleBlur}
            {...rest}
        />
    )
}

export default function SettingsPanel({
    pricingOpen,
    setPricingOpen,
    settingsLoadState,
    saving,
    usageEnabled,
    setUsageEnabled,
    pricing,
    setPricing,
    closeSettings,
    dismissSettings,
    summaryMode,
    setSummaryMode,
    onClear,
    onImport,
    importing,
}) {
    const { t } = useI18n()
    const panelRef = useRef(null)
    const fileInputRef = useRef(null)
    const [confirmClear, setConfirmClear] = useState(false)

    // 导入按钮只负责选文件；真正的解析与合并请求由上层容器处理（它持有
    // authFetch / loadRecords）。选完立即清空 input.value，保证同一个文件
    // 可以重复选择导入。
    const handleImportChange = useCallback((e) => {
        const file = e.target.files && e.target.files[0]
        e.target.value = ''
        if (file && onImport) onImport(file)
    }, [onImport])

    // 模型合并后所有模型共用同一套单价，单价表固定只有一条共享条目，因此这里
    // 只渲染一组输入/输出输入框，也不再显示模型名。开启「模拟缓存命中」后
    // 输入被拆成命中 / 未命中两个单价，并额外出现命中率输入框。
    const price = pricing.models[MODEL_PRICE_KEY] || {
        inputPrice: 0,
        inputMissPrice: DEFAULT_INPUT_MISS_PRICE,
        outputPrice: 0,
    }
    const cacheHitOn = pricing.cacheHitEnabled !== false
    const peakError = peakWindowError(pricing)
    const setPriceField = useCallback((field, value) => {
        setPricing((prev) => {
            const current = prev.models[MODEL_PRICE_KEY] || {
                inputPrice: 0,
                inputMissPrice: DEFAULT_INPUT_MISS_PRICE,
                outputPrice: 0,
            }
            return {
                ...prev,
                models: { ...prev.models, [MODEL_PRICE_KEY]: { ...current, [field]: value } },
            }
        })
    }, [setPricing])
    const setCacheHitEnabled = useCallback((enabled) => {
        setPricing((prev) => ({ ...prev, cacheHitEnabled: enabled }))
    }, [setPricing])

    // Escape dismisses the panel directly; while the nested confirm dialog is
    // open, useDialogA11y yields the keyboard to it.
    useDialogA11y(panelRef, dismissSettings)

    return (
        <div className="usage-settings-overlay" role="dialog" aria-modal="true">
            <div className="usage-settings-panel" ref={panelRef}>
                <div className="usage-settings-head">
                    {pricingOpen && (
                        <button type="button" className="usage-settings-back" onClick={() => setPricingOpen(false)} aria-label={t('usage.settingsBack')}>‹</button>
                    )}
                    <span className="usage-settings-title">{pricingOpen ? t('usage.settingsPricingTitle') : t('usage.settingsTitle')}</span>
                    <button type="button" className="usage-settings-close" onClick={dismissSettings} aria-label={t('usage.settingsClose')}>×</button>
                </div>
                <div className="usage-settings-body">
                    {pricingOpen ? (
                        <>
                            <div className="usage-settings-group-title">{t('usage.settingsIdlePriceTitle')}</div>
                            <div className="usage-settings-row">
                                <span>{cacheHitOn ? t('usage.settingsInputHit') : t('usage.settingsInput')}</span>
                                <NumberInput
                                    value={price.inputPrice}
                                    min={0}
                                    step={0.01}
                                    onChange={(n) => setPriceField('inputPrice', n)}
                                />
                            </div>
                            {cacheHitOn && (
                                <div className="usage-settings-row">
                                    <span>{t('usage.settingsInputMiss')}</span>
                                    <NumberInput
                                        value={price.inputMissPrice}
                                        min={0}
                                        step={0.01}
                                        onChange={(n) => setPriceField('inputMissPrice', n)}
                                    />
                                </div>
                            )}
                            <div className="usage-settings-row">
                                <span>{t('usage.settingsOutput')}</span>
                                <NumberInput
                                    value={price.outputPrice}
                                    min={0}
                                    step={0.01}
                                    onChange={(n) => setPriceField('outputPrice', n)}
                                />
                            </div>
                            <div className="usage-settings-row">
                                <span>{t('usage.settingsPeakBilling')}</span>
                                <label className="usage-settings-check">
                                    <input type="checkbox" checked={pricing.peakEnabled} onChange={(e) => setPricing((p) => ({ ...p, peakEnabled: e.target.checked }))} />
                                    {t('usage.settingsEnable')}
                                </label>
                            </div>
                            {pricing.peakEnabled && (
                                <>
                                    <div className="usage-settings-row">
                                        <span>{t('usage.settingsPeak1')}</span>
                                        <div className="usage-settings-two">
                                            <input type="time" value={pricing.peak1Start} onChange={(e) => setPricing((p) => ({ ...p, peak1Start: e.target.value }))} />
                                            <input type="time" value={pricing.peak1End} onChange={(e) => setPricing((p) => ({ ...p, peak1End: e.target.value }))} />
                                        </div>
                                    </div>
                                    <div className="usage-settings-row">
                                        <span>{t('usage.settingsPeak2')}</span>
                                        <div className="usage-settings-two">
                                            <input type="time" value={pricing.peak2Start} onChange={(e) => setPricing((p) => ({ ...p, peak2Start: e.target.value }))} />
                                            <input type="time" value={pricing.peak2End} onChange={(e) => setPricing((p) => ({ ...p, peak2End: e.target.value }))} />
                                        </div>
                                    </div>
                                    {peakError && (
                                        <div className="usage-settings-warning" role="alert">{t(peakError)}</div>
                                    )}
                                    <div className="usage-settings-row">
                                        <span>{t('usage.settingsPeakMultiplierLabel')}</span>
                                        <NumberInput
                                            value={pricing.peakMultiplier}
                                            min={0}
                                            step={0.1}
                                            onChange={(n) => setPricing((p) => ({ ...p, peakMultiplier: n }))}
                                        />
                                    </div>
                                    {/* 周末/节假日豁免只在波峰计价开启时才有意义，因此随波峰开关一起显示。 */}
                                    <div className="usage-settings-row">
                                        <span>{t('usage.settingsExcludeWeekendHoliday')}</span>
                                        <input type="checkbox" checked={pricing.weekendNormal} onChange={(e) => setPricing((p) => ({ ...p, weekendNormal: e.target.checked }))} />
                                    </div>
                                </>
                            )}
                            {/* 「模拟缓存命中」放在价格设置最下方，开关样式与上面的
                                「波峰计价」保持一致：上游不返回缓存命中信息，开启后按
                                命中率把输入 tokens 拆成命中 / 未命中两部分分别计价，
                                并拆开显示在下方各模型的 Token 用量图里。 */}
                            <div className="usage-settings-row">
                                <span>{t('usage.settingsCacheHit')}</span>
                                <label className="usage-settings-check">
                                    <input type="checkbox" checked={cacheHitOn} onChange={(e) => setCacheHitEnabled(e.target.checked)} />
                                    {t('usage.settingsEnable')}
                                </label>
                            </div>
                            {cacheHitOn && (
                                <div className="usage-settings-row">
                                    <span>{t('usage.settingsCacheHitRate')}</span>
                                    <span className="usage-settings-percent">
                                        <NumberInput
                                            value={pricing.cacheHitRate}
                                            min={0}
                                            max={100}
                                            step={1}
                                            onChange={(n) => setPricing((p) => ({ ...p, cacheHitRate: n }))}
                                        />
                                        <span className="usage-settings-percent-sign" aria-hidden="true">%</span>
                                    </span>
                                </div>
                            )}
                        </>
                    ) : (
                        <>
                            {settingsLoadState === 'error' && (
                                <div className="usage-settings-warning" role="alert">{t('usage.settingsCacheWarning')}</div>
                            )}
                            <div className="usage-settings-row usage-settings-row-switch">
                                <div className="usage-settings-row-text">
                                    <span className="usage-settings-row-label">{t('usage.settingsRecordUsage')}</span>
                                    <span className="usage-settings-row-hint">{t('usage.settingsRecordUsageHint')}</span>
                                </div>
                                <label className="usage-switch" title={t('usage.settingsRecordUsage')}>
                                    <input type="checkbox" role="switch" checked={usageEnabled} onChange={(e) => setUsageEnabled(e.target.checked)} />
                                    <span className="usage-switch-track"><span className="usage-switch-thumb" /></span>
                                </label>
                            </div>
                            <div className="usage-settings-row">
                                <span>{t('usage.settingsDefaultView')}</span>
                                <select value={summaryMode} onChange={(e) => setSummaryMode(e.target.value)}>
                                    <option value="tokens">{t('usage.totalTokens')}</option>
                                    <option value="cost">{t('usage.cost')}</option>
                                </select>
                            </div>
                            <div className="usage-settings-row">
                                <span>{t('usage.settingsPricingLabel')}</span>
                                <button type="button" className="usage-btn usage-settings-entry" onClick={() => setPricingOpen(true)}>
                                    <span>{t('usage.settingsPricingEntry')}</span>
                                    <span className="usage-settings-entry-arrow" aria-hidden="true">›</span>
                                </button>
                            </div>
                            <div className="usage-settings-row">
                                <span>{t('usage.settingsClearAllTitle')}</span>
                                <button type="button" className="usage-btn usage-btn-danger" onClick={() => setConfirmClear(true)}>{t('usage.settingsClearButton')}</button>
                            </div>
                        </>
                    )}
                </div>
                <div className="usage-settings-foot">
                    {!pricingOpen && (
                        <button
                            type="button"
                            className="usage-btn usage-settings-import"
                            onClick={() => fileInputRef.current && fileInputRef.current.click()}
                            disabled={importing}
                        >
                            {importing ? t('usage.settingsImporting') : t('usage.settingsImportButton')}
                        </button>
                    )}
                    <button type="button" className="usage-btn usage-btn-primary" onClick={() => closeSettings(true)} disabled={settingsLoadState === 'loading' || saving}>
                        {t('usage.settingsDone')}
                    </button>
                </div>
                {!pricingOpen && (
                    <input
                        ref={fileInputRef}
                        type="file"
                        accept=".csv,text/csv"
                        className="usage-settings-file"
                        onChange={handleImportChange}
                    />
                )}
                {confirmClear && (
                    <ConfirmDialog
                        title={t('usage.clearConfirmTitle')}
                        message={t('usage.clearConfirm')}
                        confirmText={t('usage.clearConfirmAction')}
                        danger
                        onConfirm={onClear}
                        onClose={() => setConfirmClear(false)}
                    />
                )}
            </div>
        </div>
    )
}
