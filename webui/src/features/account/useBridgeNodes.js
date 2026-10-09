import { useCallback, useEffect, useRef, useState } from 'react'

// 打开下拉后多久算数据过期：超过这个间隔再开面板会重新拉一次节点列表，
// 否则直接用缓存（节点健康由后台巡检更新，不需要每次开面板都请求）。
const FRESH_WINDOW_MS = 30000

// useBridgeNodes 读取代理桥节点清单（含最近一次测速结果），供账号管理里的
// 「账号代理」下拉展示全部可用节点。
//
// 代理桥未启用/未装配时接口会返回错误：这里视为「没有桥节点」，
// 下拉退回到只显示手动代理，不影响账号页其它功能。
export function useBridgeNodes({ apiFetch }) {
    const [nodes, setNodes] = useState([])
    const [available, setAvailable] = useState(false)
    const [loading, setLoading] = useState(false)
    const [error, setError] = useState('')
    const fetchedAtRef = useRef(0)
    const inFlightRef = useRef(null)

    const load = useCallback(async () => {
        if (inFlightRef.current) return inFlightRef.current
        const task = (async () => {
            setLoading(true)
            try {
                const res = await apiFetch('/admin/mihomo/nodes')
                if (!res.ok) {
                    const data = await res.json().catch(() => ({}))
                    setNodes([])
                    setAvailable(false)
                    setError(data?.detail || `HTTP ${res.status}`)
                    return
                }
                const data = await res.json()
                setNodes(Array.isArray(data?.items) ? data.items : [])
                setAvailable(true)
                setError('')
                fetchedAtRef.current = Date.now()
            } catch (err) {
                setNodes([])
                setAvailable(false)
                setError(err?.message || '')
            } finally {
                setLoading(false)
                inFlightRef.current = null
            }
        })()
        inFlightRef.current = task
        return task
    }, [apiFetch])

    // force 为真时无条件刷新（面板里的刷新按钮）；否则只在数据过期时刷新
    // （每次打开下拉都会调用，靠 FRESH_WINDOW_MS 节流）。
    const reload = useCallback((force = false) => {
        if (!force && fetchedAtRef.current && Date.now() - fetchedAtRef.current < FRESH_WINDOW_MS) {
            return Promise.resolve()
        }
        return load()
    }, [load])

    useEffect(() => {
        load()
    }, [load])

    return { nodes, available, loading, error, reload }
}
