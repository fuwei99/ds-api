import { useCallback, useState } from 'react'

// useDeviceIdPool 管理 manual 模式下手动录入的 device_id 号池：
// 拉取列表、新增（带格式校验，错误就地返回给弹窗）与删除（删除后原绑定账号
// 会自动换绑，因此需要刷新账号列表）。
export function useDeviceIdPool({ apiFetch, t, onMessage, onRefresh, fetchAccounts }) {
    const [showDeviceIds, setShowDeviceIds] = useState(false)
    const [deviceIdItems, setDeviceIdItems] = useState([])
    const [loadingDeviceIds, setLoadingDeviceIds] = useState(false)
    const [addingDeviceId, setAddingDeviceId] = useState(false)
    const [deletingDeviceId, setDeletingDeviceId] = useState({})

    const fetchDeviceIds = useCallback(async () => {
        setLoadingDeviceIds(true)
        try {
            const res = await apiFetch('/admin/device-ids')
            if (res.ok) {
                const data = await res.json()
                setDeviceIdItems(Array.isArray(data.items) ? data.items : [])
            }
        } catch (e) {
            console.error('Failed to fetch device ids:', e)
        } finally {
            setLoadingDeviceIds(false)
        }
    }, [apiFetch])

    const openDeviceIds = useCallback(() => {
        setShowDeviceIds(true)
        fetchDeviceIds()
    }, [fetchDeviceIds])

    const closeDeviceIds = useCallback(() => {
        setShowDeviceIds(false)
    }, [])

    // addDeviceId 支持一次提交多个 id（每行一个），返回错误文案（成功时返回 null），
    // 由弹窗就地展示。单行提交与批量提交走同一路径。
    const addDeviceId = useCallback(async (raw) => {
        const ids = [...new Set(
            String(raw || '')
                .split(/\r?\n/)
                .map(line => line.trim())
                .filter(Boolean),
        )]
        if (ids.length === 0) {
            return t('accountManager.deviceIdEmptyInput')
        }
        setAddingDeviceId(true)
        try {
            const failed = []
            let addedCount = 0
            for (const id of ids) {
                const res = await apiFetch('/admin/device-ids', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ id }),
                })
                const data = await res.json().catch(() => ({}))
                if (!res.ok) {
                    failed.push(`${id}: ${data.detail || t('messages.requestFailed')}`)
                } else {
                    addedCount += 1
                }
            }
            if (addedCount > 0) {
                onMessage('success', t('accountManager.deviceIdAddSuccess', { count: addedCount }))
                await fetchDeviceIds()
                onRefresh?.()
            }
            if (failed.length > 0) {
                return failed.join('\n')
            }
            return null
        } catch (_err) {
            return t('messages.networkError')
        } finally {
            setAddingDeviceId(false)
        }
    }, [apiFetch, fetchDeviceIds, onMessage, onRefresh, t])

    const deleteDeviceId = useCallback(async (id) => {
        if (!confirm(t('accountManager.deviceIdDeleteConfirm'))) return
        setDeletingDeviceId(prev => ({ ...prev, [id]: true }))
        try {
            const res = await apiFetch('/admin/device-ids/delete', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ id }),
            })
            const data = await res.json().catch(() => ({}))
            if (!res.ok) {
                onMessage('error', data.detail || t('messages.requestFailed'))
                return
            }
            onMessage('success', t('accountManager.deviceIdDeleteSuccess'))
            await fetchDeviceIds()
            await fetchAccounts?.()
            onRefresh?.()
        } catch (_err) {
            onMessage('error', t('messages.networkError'))
        } finally {
            setDeletingDeviceId(prev => ({ ...prev, [id]: false }))
        }
    }, [apiFetch, fetchAccounts, fetchDeviceIds, onMessage, onRefresh, t])

    return {
        showDeviceIds,
        openDeviceIds,
        closeDeviceIds,
        deviceIdItems,
        loadingDeviceIds,
        addingDeviceId,
        deletingDeviceId,
        addDeviceId,
        deleteDeviceId,
        fetchDeviceIds,
    }
}
