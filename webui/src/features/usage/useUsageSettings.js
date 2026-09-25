import { useCallback, useEffect, useRef, useState } from 'react'
import { useI18n } from '../../i18n'
import {
    DEFAULT_CACHE_HIT_RATE,
    DEFAULT_INPUT_MISS_PRICE,
    applyServerSettings,
    loadLocalPricing,
    peakWindowError,
    persistLocalPricing,
} from './usageUtils'

export function useUsageSettings({ authFetch, onMessage }) {
    const { t } = useI18n()
    const [settingsOpen, setSettingsOpen] = useState(false)
    const [pricingOpen, setPricingOpen] = useState(false)
    const [settingsLoadState, setSettingsLoadState] = useState('loading')
    const [usageEnabled, setUsageEnabled] = useState(false)
    const [pricing, setPricing] = useState(() => loadLocalPricing())
    const [saving, setSaving] = useState(false)
    const savingPromiseRef = useRef(null)
    const loadSettingsIdRef = useRef(0)
    const mountedRef = useRef(true)
    // Abort the in-flight settings load on unmount / refetch; the requestId
    // guard only discards stale state, the request itself keeps running.
    const abortRef = useRef(null)

    useEffect(() => {
        mountedRef.current = true
        return () => {
            mountedRef.current = false
            if (abortRef.current) abortRef.current.abort()
        }
    }, [])

    // Debounce localStorage writes: pricing changes on every keystroke in the
    // price inputs, and each write serializes the whole table.
    useEffect(() => {
        const timer = setTimeout(() => persistLocalPricing(pricing), 300)
        return () => clearTimeout(timer)
    }, [pricing])

    const loadUsageSettings = useCallback(async () => {
        const requestId = ++loadSettingsIdRef.current
        if (abortRef.current) abortRef.current.abort()
        const controller = new AbortController()
        abortRef.current = controller
        setSettingsLoadState('loading')
        try {
            const res = await authFetch('/admin/usage/settings', { signal: controller.signal })
            const data = await res.json().catch(() => ({}))
            if (requestId !== loadSettingsIdRef.current || !mountedRef.current) return
            if (res.ok && data.settings) {
                const s = data.settings
                setUsageEnabled(!!s.enabled)
                setSettingsLoadState('ready')
                setPricing((prev) => applyServerSettings(prev, s))
            } else {
                setSettingsLoadState('error')
                onMessage?.('error', t('usage.settingsLoadFailed'))
            }
        } catch (e) {
            if (e?.name === 'AbortError') return
            if (requestId === loadSettingsIdRef.current && mountedRef.current) {
                setSettingsLoadState('error')
                onMessage?.('error', t('usage.settingsLoadFailed'))
            }
        }
    }, [authFetch, onMessage, t])

    useEffect(() => {
        loadUsageSettings()
    }, [loadUsageSettings])

    // saveUsageSettings pushes the full pricing set to the server and reports
    // whether the save succeeded so the caller can decide whether to close.
    const saveUsageSettings = useCallback(async (nextPricing, nextEnabled) => {
        if (savingPromiseRef.current) {
            return savingPromiseRef.current
        }
        const promise = (async () => {
            setSaving(true)
            try {
                const settings = {
                    enabled: !!nextEnabled,
                    models: Object.fromEntries(
                        Object.entries(nextPricing.models || {}).map(([name, p]) => [
                            name,
                            {
                                input_price: Number(p?.inputPrice || 0),
                                // 显式下发未命中单价：服务端把缺省字段当作「旧设置
                                // 文件」并回落默认值，只有写出来才能保存用户填的 0。
                                input_miss_price: Number(p?.inputMissPrice ?? DEFAULT_INPUT_MISS_PRICE),
                                output_price: Number(p?.outputPrice || 0),
                            },
                        ])
                    ),
                    peak: {
                        enabled: !!nextPricing.peakEnabled,
                        start1: nextPricing.peak1Start,
                        end1: nextPricing.peak1End,
                        start2: nextPricing.peak2Start,
                        end2: nextPricing.peak2End,
                        multiplier: Number(nextPricing.peakMultiplier || 0),
                        weekend_normal: !!nextPricing.weekendNormal,
                    },
                    cache_hit: {
                        enabled: !!nextPricing.cacheHitEnabled,
                        rate: Number(nextPricing.cacheHitRate ?? DEFAULT_CACHE_HIT_RATE),
                    },
                }
                const res = await authFetch('/admin/usage/settings', {
                    method: 'PUT',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ settings }),
                })
                const data = await res.json().catch(() => ({}))
                if (!res.ok) throw new Error(data?.detail || t('usage.saveFailed'))
                if (data.settings) {
                    setUsageEnabled(!!data.settings.enabled)
                    setPricing((prev) => applyServerSettings(prev, data.settings))
                }
                onMessage?.('success', t('usage.settingsSaved'))
                return true
            } catch (err) {
                onMessage?.('error', err.message || t('usage.saveFailed'))
                return false
            } finally {
                savingPromiseRef.current = null
                if (mountedRef.current) setSaving(false)
            }
        })()
        savingPromiseRef.current = promise
        return promise
    }, [authFetch, onMessage, t])

    // closeSettings(attemptSave) never dead-locks: × / Esc call it with
    // attemptSave=false and always dismiss; "Done" attempts the save and only
    // closes on success (or closes without saving when settings failed to load).
    const closeSettings = useCallback(async (attemptSave) => {
        if (attemptSave) {
            if (settingsLoadState === 'loading') {
                onMessage?.('error', t('usage.settingsNotLoaded'))
                return
            }
            if (settingsLoadState === 'error') {
                setPricingOpen(false)
                setSettingsOpen(false)
                onMessage?.('error', t('usage.settingsNotSaved'))
                return
            }
            const peakError = peakWindowError(pricing)
            if (peakError) {
                setPricingOpen(true)
                onMessage?.('error', t(peakError))
                return
            }
            const ok = await saveUsageSettings(pricing, usageEnabled)
            if (!ok) return
        }
        setPricingOpen(false)
        setSettingsOpen(false)
    }, [settingsLoadState, pricing, usageEnabled, saveUsageSettings, onMessage, t])

    const dismissSettings = useCallback(() => {
        setPricingOpen(false)
        setSettingsOpen(false)
    }, [])

    return {
        settingsOpen,
        setSettingsOpen,
        pricingOpen,
        setPricingOpen,
        settingsLoadState,
        saving,
        usageEnabled,
        setUsageEnabled,
        pricing,
        setPricing,
        loadUsageSettings,
        saveUsageSettings,
        closeSettings,
        dismissSettings,
    }
}
