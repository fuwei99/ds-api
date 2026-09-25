import { useEffect, useRef } from 'react'

// useEntranceAnimation returns true only for the first render in which
// `hasData` becomes true, so charts play their entrance animation once and
// stay still on later refreshes. Replaying the animation on every poll tick
// was the main source of jank on the usage page.
export function useEntranceAnimation(hasData) {
    const playedRef = useRef(false)
    const animate = !!hasData && !playedRef.current
    useEffect(() => {
        if (hasData) playedRef.current = true
    }, [hasData])
    return animate
}
