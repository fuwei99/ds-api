import { useEffect, useRef } from 'react'
// Registration lives in echartsSetup.js so the chart-option tests can assert
// against the exact same module set the app ships.
import { init } from './echartsSetup'

// Dark-mode colors are applied explicitly in chartOptions, so charts are
// initialized without the echarts built-in theme to avoid double styling.
export default function EChart({ option, height = 272, ariaLabel }) {
    const ref = useRef(null)
    const chartRef = useRef(null)

    useEffect(() => {
        if (!ref.current) return undefined
        if (!chartRef.current) {
            chartRef.current = init(ref.current)
        }
        chartRef.current.setOption(option, true)
        return undefined
    }, [option])

    useEffect(() => {
        const resize = () => {
            if (chartRef.current) chartRef.current.resize()
        }
        window.addEventListener('resize', resize)
        // Container resizes (e.g. sidebar collapse) don't fire window resize.
        let observer
        if (ref.current && typeof ResizeObserver !== 'undefined') {
            observer = new ResizeObserver(resize)
            observer.observe(ref.current)
        }
        return () => {
            window.removeEventListener('resize', resize)
            if (observer) observer.disconnect()
            if (chartRef.current) {
                chartRef.current.dispose()
                chartRef.current = null
            }
        }
    }, [])

    return <div ref={ref} role="img" aria-label={ariaLabel} style={{ width: '100%', height: `${height}px` }} />
}
