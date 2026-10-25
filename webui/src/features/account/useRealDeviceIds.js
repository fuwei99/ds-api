import { useCallback, useState } from 'react'

// useRealDeviceIds 管理 real 模式下绑定真实设备 id 的账号：拉取清单、按账号刷新、
// 批量刷新。两个动作的区别只在设备档案：
//   刷新 id      —— 复用现有设备档案重新生成：设备身份不变，但数美每次上报都会
//                   换回一个新的 deviceId；
//   重置设备档案  —— 先删除设备档案再生成，等于给该账号换一台新设备。
// 两者都会写回新 id，区别只在设备身份是否保留，因此重置需要二次确认、刷新不需要。
export function useRealDeviceIds({ apiFetch, t, onMessage, onRefresh, fetchAccounts }) {
    const [showRealDeviceIds, setShowRealDeviceIds] = useState(false)
    const [realDeviceIdItems, setRealDeviceIdItems] = useState([])
    const [loadingRealDeviceIds, setLoadingRealDeviceIds] = useState(false)
    const [realDevicePage, setRealDevicePage] = useState(1)
    const [realDevicePageSize, setRealDevicePageSize] = useState(10)
    const [realDeviceTotal, setRealDeviceTotal] = useState(0)
    // realDeviceTotalAll 是过滤前的 real 账号数：批量刷新作用于全量，
    // 界面用它决定批量入口是否可用（搜索筛空不等于没有账号可刷）。
    const [realDeviceTotalAll, setRealDeviceTotalAll] = useState(0)
    const [realDeviceTotalPages, setRealDeviceTotalPages] = useState(1)
    const [realDeviceQuery, setRealDeviceQuery] = useState('')
    const [refreshingRealDeviceId, setRefreshingRealDeviceId] = useState({})
    // bulkRefreshing 为空表示没有批量任务，否则为 'refresh' / 'reset'，用于按钮与行内禁用。
    const [bulkRefreshing, setBulkRefreshing] = useState('')
    // bulkErrors 保存批量刷新的逐账号失败原因，就地显示在对应行下方。
    const [bulkErrors, setBulkErrors] = useState({})

    // fetchRealDeviceIds 按页拉取。请求页超出范围时（清单被重置/解绑变短、
    // 搜索把结果筛少）自动回落到最后一页，避免停在空页上。
    const fetchRealDeviceIds = useCallback(async (targetPage, targetPageSize = realDevicePageSize, targetQuery = realDeviceQuery) => {
        setLoadingRealDeviceIds(true)
        try {
            const load = async (page) => {
                let url = `/admin/device-ids/real?page=${page}&page_size=${targetPageSize}`
                const q = String(targetQuery || '').trim()
                if (q) url += `&q=${encodeURIComponent(q)}`
                const res = await apiFetch(url)
                if (!res.ok) return null
                return await res.json()
            }
            let data = await load(targetPage)
            if (!data) return
            let page = targetPage
            const totalPages = Math.max(1, Number(data.total_pages) || 1)
            if (page > totalPages) {
                page = totalPages
                data = await load(page)
                if (!data) return
            }
            setRealDeviceIdItems(Array.isArray(data.items) ? data.items : [])
            setRealDevicePage(page)
            setRealDevicePageSize(targetPageSize)
            setRealDeviceQuery(targetQuery)
            setRealDeviceTotal(Number(data.total) || 0)
            setRealDeviceTotalAll(Number(data.total_all) || 0)
            setRealDeviceTotalPages(Math.max(1, Number(data.total_pages) || 1))
        } catch (e) {
            console.error('Failed to fetch real device ids:', e)
        } finally {
            setLoadingRealDeviceIds(false)
        }
    }, [apiFetch, realDevicePageSize, realDeviceQuery])

    const openRealDeviceIds = useCallback(() => {
        setBulkErrors({})
        setShowRealDeviceIds(true)
        fetchRealDeviceIds(1)
    }, [fetchRealDeviceIds])

    const closeRealDeviceIds = useCallback(() => {
        setShowRealDeviceIds(false)
    }, [])

    const changeRealDevicePageSize = useCallback((size) => {
        const next = Number(size) || 10
        setRealDevicePageSize(next)
        fetchRealDeviceIds(1, next)
    }, [fetchRealDeviceIds])

    // searchRealDeviceIds 与账号列表一致：每次输入都直接查（服务端过滤 + 回到第一页）。
    const searchRealDeviceIds = useCallback((query) => {
        setRealDeviceQuery(query)
        fetchRealDeviceIds(1, realDevicePageSize, query)
    }, [fetchRealDeviceIds, realDevicePageSize])

    const prevRealDevicePage = useCallback(() => {
        fetchRealDeviceIds(Math.max(1, realDevicePage - 1))
    }, [fetchRealDeviceIds, realDevicePage])

    const nextRealDevicePage = useCallback(() => {
        fetchRealDeviceIds(Math.min(realDeviceTotalPages, realDevicePage + 1))
    }, [fetchRealDeviceIds, realDevicePage, realDeviceTotalPages])

    // afterRefresh 在任一次刷新成功后统一收口：列表（当前页与当前搜索）、账号、
    // 上层统计都要跟着变。
    const afterRefresh = useCallback(async () => {
        await fetchRealDeviceIds(realDevicePage, realDevicePageSize, realDeviceQuery)
        await fetchAccounts?.()
        onRefresh?.()
    }, [fetchAccounts, fetchRealDeviceIds, onRefresh, realDevicePage, realDevicePageSize, realDeviceQuery])

    const refreshRealDeviceId = useCallback(async (identifier, resetProfile) => {
        if (resetProfile && !confirm(t('accountManager.realDeviceIdResetConfirm', { account: identifier }))) {
            return
        }
        setRefreshingRealDeviceId(prev => ({ ...prev, [identifier]: true }))
        setBulkErrors(prev => {
            if (!prev[identifier]) return prev
            const next = { ...prev }
            delete next[identifier]
            return next
        })
        try {
            const res = await apiFetch('/admin/device-ids/real/refresh', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ identifier, reset_profile: Boolean(resetProfile) }),
            })
            const data = await res.json().catch(() => ({}))
            if (!res.ok) {
                onMessage('error', data.detail || t('messages.requestFailed'))
                return
            }
            onMessage('success', t(
                resetProfile
                    ? 'accountManager.realDeviceIdResetSuccess'
                    : 'accountManager.realDeviceIdRefreshSuccess',
                { account: identifier },
            ))
            await afterRefresh()
        } catch (_err) {
            onMessage('error', t('messages.networkError'))
        } finally {
            setRefreshingRealDeviceId(prev => ({ ...prev, [identifier]: false }))
        }
    }, [afterRefresh, apiFetch, onMessage, t])

    const refreshAllRealDeviceIds = useCallback(async (resetProfile) => {
        if (resetProfile && !confirm(t('accountManager.realDeviceIdResetAllConfirm'))) {
            return
        }
        setBulkRefreshing(resetProfile ? 'reset' : 'refresh')
        setBulkErrors({})
        try {
            const res = await apiFetch('/admin/device-ids/real/refresh-all', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ reset_profile: Boolean(resetProfile) }),
            })
            const data = await res.json().catch(() => ({}))
            if (!res.ok) {
                onMessage('error', data.detail || t('messages.requestFailed'))
                return
            }
            const refreshed = Number(data.refreshed || 0)
            const failed = Number(data.failed || 0)
            if (failed > 0) {
                const failures = {}
                for (const item of Array.isArray(data.items) ? data.items : []) {
                    if (item?.error) failures[item.identifier] = item.error
                }
                setBulkErrors(failures)
                onMessage('error', t('accountManager.realDeviceIdBulkFailed', { refreshed, failed }))
            } else {
                onMessage('success', t(
                    resetProfile
                        ? 'accountManager.realDeviceIdResetAllSuccess'
                        : 'accountManager.realDeviceIdRefreshAllSuccess',
                    { count: refreshed },
                ))
            }
            await afterRefresh()
        } catch (_err) {
            onMessage('error', t('messages.networkError'))
        } finally {
            setBulkRefreshing('')
        }
    }, [afterRefresh, apiFetch, onMessage, t])

    return {
        showRealDeviceIds,
        openRealDeviceIds,
        closeRealDeviceIds,
        realDeviceIdItems,
        loadingRealDeviceIds,
        realDevicePage,
        realDevicePageSize,
        realDeviceTotal,
        realDeviceTotalAll,
        realDeviceTotalPages,
        realDeviceQuery,
        changeRealDevicePageSize,
        prevRealDevicePage,
        nextRealDevicePage,
        searchRealDeviceIds,
        refreshingRealDeviceId,
        bulkRefreshing,
        bulkErrors,
        fetchRealDeviceIds,
        refreshRealDeviceId,
        refreshAllRealDeviceIds,
    }
}
