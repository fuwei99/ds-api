import { useCallback, useEffect, useRef, useState } from 'react'
import { useI18n } from '../../i18n'
import { entryToRecord, sameUsageRecords } from './usageUtils'

export function useUsageRecords({ authFetch, onMessage }) {
    const { t } = useI18n()
    const [records, setRecords] = useState([])
    const [loading, setLoading] = useState(true)
    // `initialized` separates the first load from manual refreshes: after the
    // first load completes we keep the rendered data on screen while
    // re-fetching, instead of swapping the whole page for the loading block.
    const [initialized, setInitialized] = useState(false)
    const [error, setError] = useState('')
    const mountedRef = useRef(true)
    const loadRecordsIdRef = useRef(0)
    // Abort the in-flight request when a newer one starts or the hook unmounts;
    // the requestId guard only stops stale state writes, not the network work.
    const abortRef = useRef(null)
    // Keep the latest i18n translator / toast callback in refs so switching
    // language doesn't change loadRecords' identity and trigger a refetch.
    const tRef = useRef(t)
    const onMessageRef = useRef(onMessage)

    useEffect(() => {
        tRef.current = t
        onMessageRef.current = onMessage
    })

    useEffect(() => {
        mountedRef.current = true
        return () => {
            mountedRef.current = false
            if (abortRef.current) abortRef.current.abort()
        }
    }, [])

    const loadRecords = useCallback(async () => {
        const requestId = ++loadRecordsIdRef.current
        if (abortRef.current) abortRef.current.abort()
        const controller = new AbortController()
        abortRef.current = controller
        setLoading(true)
        setError('')
        try {
            const res = await authFetch('/admin/usage', { signal: controller.signal })
            const data = await res.json().catch(() => ({}))
            if (!res.ok) {
                throw new Error(data?.detail || tRef.current('usage.loadFailed'))
            }
            const items = Array.isArray(data.items) ? data.items : []
            const mapped = items.map(entryToRecord).filter((r) => r.created_at > 0)
            if (requestId === loadRecordsIdRef.current && mountedRef.current) {
                // Reuse the previous array when the polled ledger is unchanged so
                // downstream memos and charts are not invalidated every 30s.
                setRecords((prev) => (sameUsageRecords(prev, mapped) ? prev : mapped))
            }
        } catch (err) {
            if (err?.name === 'AbortError') return
            if (requestId === loadRecordsIdRef.current && mountedRef.current) {
                setError(err.message || tRef.current('usage.loadFailed'))
                onMessageRef.current?.('error', err.message || tRef.current('usage.loadFailed'))
            }
        } finally {
            if (requestId === loadRecordsIdRef.current && mountedRef.current) {
                setLoading(false)
                setInitialized(true)
            }
        }
    }, [authFetch])

    useEffect(() => {
        loadRecords()
    }, [loadRecords])

    const clearRecords = useCallback(() => {
        setRecords([])
    }, [])

    return { records, loading, initialized, error, loadRecords, clearRecords }
}
