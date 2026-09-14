import { useCallback, useEffect, useState } from 'react'

// latencyFromNodes 从 /nodes 返回的节点健康字段重建延迟映射。
// 测速结果已持久化在 mihomo_subscriptions.json，重启后进入页面即可展示，
// 无需先手动点"测试延迟"。
export function latencyFromNodes(nodes) {
    const map = {}
    for (const n of nodes || []) {
        if (n.latency_ms > 0) {
            map[n.node_key] = { delay_ms: n.latency_ms, error: '' }
        } else if (n.health === 'fail') {
            map[n.node_key] = { delay_ms: 0, error: n.health_error || 'fail' }
        }
    }
    return map
}

// useMihomoBridge 封装 Mihomo 代理桥管理接口的全部数据访问。
// config 变更（账号 proxy_id、托管代理列表）通过 onConfigChanged 通知上层刷新。
export default function useMihomoBridge({ authFetch, onMessage, onConfigChanged, t }) {
    const apiFetch = authFetch || fetch
    const [status, setStatus] = useState(null)
    const [subscriptions, setSubscriptions] = useState([])
    const [nodes, setNodes] = useState([])
    const [latency, setLatency] = useState({})
    const [loading, setLoading] = useState(true)
    const [busy, setBusy] = useState({})

    const readApiResponse = useCallback(async (res) => {
        const contentType = String(res.headers.get('content-type') || '').toLowerCase()
        const raw = (await res.text()).trim()
        if (!raw) return {}
        if (contentType.includes('application/json')) {
            try {
                return JSON.parse(raw)
            } catch (_err) {
                return { detail: raw }
            }
        }
        return { detail: raw }
    }, [])

    const report = useCallback((res, data, successText) => {
        if (!res.ok) {
            onMessage('error', data.detail || t('messages.requestFailed'))
            return false
        }
        if (successText) onMessage('success', successText)
        return true
    }, [onMessage, t])

    const loadStatus = useCallback(async () => {
        try {
            const res = await apiFetch('/admin/mihomo/status')
            setStatus(await readApiResponse(res))
        } catch (_err) {
            setStatus(null)
        }
    }, [apiFetch, readApiResponse])

    const loadSubscriptions = useCallback(async () => {
        try {
            const res = await apiFetch('/admin/mihomo/subscriptions')
            const data = await readApiResponse(res)
            setSubscriptions(data.items || [])
        } catch (_err) {
            setSubscriptions([])
        }
    }, [apiFetch, readApiResponse])

    const loadNodes = useCallback(async () => {
        try {
            const res = await apiFetch('/admin/mihomo/nodes')
            const data = await readApiResponse(res)
            const items = data.items || []
            setNodes(items)
            // 用持久化/最新健康数据重建延迟映射，进入页面即可看到历史测速结果。
            setLatency(latencyFromNodes(items))
        } catch (_err) {
            setNodes([])
        }
    }, [apiFetch, readApiResponse])

    const loadAll = useCallback(async () => {
        setLoading(true)
        await Promise.all([loadStatus(), loadSubscriptions(), loadNodes()])
        setLoading(false)
    }, [loadStatus, loadSubscriptions, loadNodes])

    useEffect(() => {
        loadAll()
    }, [loadAll])

    // 定时/实时刷新：后台自动巡检（默认 60s）会更新节点延迟并落盘，
    // 前端定时轮询让面板上的延迟/健康状态保持最新。
    useEffect(() => {
        const interval = setInterval(() => {
            loadStatus()
            loadNodes()
        }, 10000)
        return () => clearInterval(interval)
    }, [loadStatus, loadNodes])

    const withBusy = useCallback(async (key, fn) => {
        setBusy(prev => ({ ...prev, [key]: true }))
        try {
            return await fn()
        } catch (err) {
            onMessage('error', err?.message || t('messages.networkError'))
            return false
        } finally {
            setBusy(prev => ({ ...prev, [key]: false }))
        }
    }, [onMessage, t])

    const saveSettings = useCallback((form) => withBusy('settings', async () => {
        const res = await apiFetch('/admin/mihomo/settings', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                enabled: Boolean(form.enabled),
                binary_path: form.binary_path || '',
                base_port: Number(form.base_port) || 0,
                api_port: Number(form.api_port) || 0,
                auto_bind: Boolean(form.auto_bind),
            }),
        })
        const data = await readApiResponse(res)
        if (!report(res, data, t('mihomoBridge.settingsSaved'))) return false
        if (data.status) setStatus(data.status)
        await onConfigChanged?.()
        return true
    }), [apiFetch, withBusy, readApiResponse, report, t, onConfigChanged])

    const applyNow = useCallback(() => withBusy('apply', async () => {
        const res = await apiFetch('/admin/mihomo/apply', { method: 'POST' })
        const data = await readApiResponse(res)
        if (!report(res, data, t('mihomoBridge.applySuccess'))) return false
        if (data.status) setStatus(data.status)
        return true
    }), [apiFetch, withBusy, readApiResponse, report, t])

    // pollDownloadFinished 轮询 /admin/mihomo/binary 直到下载完成/失败。
    const pollDownloadFinished = useCallback(async () => {
        const deadline = Date.now() + 10 * 60 * 1000
        while (Date.now() < deadline) {
            await new Promise(resolve => setTimeout(resolve, 2000))
            let info = {}
            try {
                const res = await apiFetch('/admin/mihomo/binary')
                info = await readApiResponse(res)
            } catch (_err) {
                continue
            }
            if (info?.state === 'done') {
                return true
            }
            if (info?.state === 'error') {
                onMessage('error', info.error || t('mihomoBridge.downloadFailed'))
                return false
            }
            setStatus(prev => (prev ? { ...prev, download: info, binary_found: Boolean(info.found) } : prev))
        }
        onMessage('error', t('mihomoBridge.downloadTimeout'))
        return false
    }, [apiFetch, readApiResponse, onMessage, t])

    const downloadBinary = useCallback(() => withBusy('binary', async () => {
        const res = await apiFetch('/admin/mihomo/binary/download', { method: 'POST' })
        const data = await readApiResponse(res)
        if (!report(res, data, t('mihomoBridge.downloadStarted'))) return false
        const ok = await pollDownloadFinished()
        if (ok) {
            onMessage('success', t('mihomoBridge.downloadSuccess'))
        }
        await loadStatus()
        return ok
    }), [withBusy, apiFetch, readApiResponse, report, t, pollDownloadFinished, loadStatus, onMessage])

    const addSubscription = useCallback((name, url) => withBusy('addSub', async () => {
        const res = await apiFetch('/admin/mihomo/subscriptions', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name, url }),
        })
        const data = await readApiResponse(res)
        const count = data?.subscription?.node_count ?? 0
        if (!report(res, data, t('mihomoBridge.addSuccess', { count }))) return false
        await Promise.all([loadSubscriptions(), loadNodes(), loadStatus()])
        await onConfigChanged?.()
        return true
    }), [apiFetch, withBusy, readApiResponse, report, t, loadSubscriptions, loadNodes, loadStatus, onConfigChanged])

    const refreshSubscription = useCallback((subID) => withBusy(`refresh:${subID}`, async () => {
        const res = await apiFetch(`/admin/mihomo/subscriptions/${encodeURIComponent(subID)}/refresh`, { method: 'POST' })
        const data = await readApiResponse(res)
        if (!report(res, data, t('mihomoBridge.refreshSuccess', { count: data.node_count ?? 0 }))) return false
        await Promise.all([loadSubscriptions(), loadNodes(), loadStatus()])
        await onConfigChanged?.()
        return true
    }), [apiFetch, withBusy, readApiResponse, report, t, loadSubscriptions, loadNodes, loadStatus, onConfigChanged])

    const deleteSubscription = useCallback((subID) => withBusy(`delete:${subID}`, async () => {
        const res = await apiFetch(`/admin/mihomo/subscriptions/${encodeURIComponent(subID)}`, { method: 'DELETE' })
        const data = await readApiResponse(res)
        if (!report(res, data, t('mihomoBridge.deleteSuccess'))) return false
        await Promise.all([loadSubscriptions(), loadNodes(), loadStatus()])
        await onConfigChanged?.()
        return true
    }), [apiFetch, withBusy, readApiResponse, report, t, loadSubscriptions, loadNodes, loadStatus, onConfigChanged])

    const bindAccount = useCallback((identifier, nodeKey) => withBusy(`bind:${nodeKey || identifier}`, async () => {
        const res = await apiFetch(`/admin/mihomo/bindings/${encodeURIComponent(identifier)}`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ node: nodeKey || '' }),
        })
        const data = await readApiResponse(res)
        const okText = nodeKey ? t('mihomoBridge.bindSuccess') : t('mihomoBridge.unbindSuccess')
        if (!report(res, data, okText)) return false
        await Promise.all([loadNodes(), loadStatus()])
        await onConfigChanged?.()
        return true
    }), [apiFetch, withBusy, readApiResponse, report, t, loadNodes, loadStatus, onConfigChanged])

    // testLatency 批量测试全部节点延迟（后端按批次并发执行），
    // 结果存进 latency（node_key -> {delay_ms, error}），供列表排序与展示；
    // 同时刷新节点/状态，展示后端健康池的判定结果。
    const testLatency = useCallback(() => withBusy('delayTest', async () => {
        const res = await apiFetch('/admin/mihomo/delay-test', { method: 'POST' })
        const data = await readApiResponse(res)
        if (!report(res, data, t('mihomoBridge.latencyTestDone', { total: data.total ?? 0 }))) return false
        const map = {}
        for (const item of data.items || []) {
            map[item.node_key] = { delay_ms: item.delay_ms, error: item.error }
        }
        setLatency(map)
        await Promise.all([loadNodes(), loadStatus()])
        return true
    }), [apiFetch, withBusy, readApiResponse, report, t, loadNodes, loadStatus])

    // assignAll 一键为全部账号分配节点绑定：已测过延迟时只用测试成功的节点
    // （按延迟升序，超时/失败的节点不绑定）；未测过则全部节点按原顺序。
    // 节点不足时后端循环分配。返回绑定是否成功。
    const assignAll = useCallback(() => {
        const hasLatency = latency && Object.keys(latency).length > 0
        let ordered = nodes
        if (hasLatency) {
            ordered = nodes
                .filter(n => {
                    const l = latency[n.node_key]
                    return Boolean(l && !l.error && l.delay_ms > 0)
                })
                .sort((a, b) => (latency[a.node_key].delay_ms || 0) - (latency[b.node_key].delay_ms || 0))
        }
        const nodeKeys = ordered.map(n => n.node_key)
        if (nodeKeys.length === 0) {
            onMessage('error', t('mihomoBridge.assignNoNodes'))
            return Promise.resolve(false)
        }
        return withBusy('assignAll', async () => {
            const res = await apiFetch('/admin/mihomo/assign', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ node_keys: nodeKeys }),
            })
            const data = await readApiResponse(res)
            if (!report(res, data, t('mihomoBridge.assignAllSuccess', { count: data.bound ?? 0 }))) return false
            await Promise.all([loadNodes(), loadStatus()])
            await onConfigChanged?.()
            return true
        })
    }, [nodes, latency, apiFetch, withBusy, readApiResponse, report, t, loadNodes, loadStatus, onConfigChanged, onMessage])

    return {
        status, subscriptions, nodes, latency, loading, busy,
        reload: loadAll,
        saveSettings, applyNow, downloadBinary,
        addSubscription, refreshSubscription, deleteSubscription,
        bindAccount, testLatency, assignAll,
    }
}
