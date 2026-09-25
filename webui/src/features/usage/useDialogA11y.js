import { useEffect, useRef } from 'react'

// useDialogA11y focuses the first focusable element inside the dialog on
// mount, traps Tab navigation within it, and closes on Escape. While a nested
// dialog (any [role="dialog"] rendered inside this node) is open, the outer
// dialog yields the keyboard entirely to it. Focus is restored to whatever was
// focused before the dialog opened.
export function useDialogA11y(ref, onEscape) {
    const onEscapeRef = useRef(onEscape)
    useEffect(() => {
        onEscapeRef.current = onEscape
    })

    useEffect(() => {
        const node = ref.current
        if (!node) return undefined
        const previouslyFocused = document.activeElement
        // offsetParent is null for elements inside a position:fixed ancestor,
        // so use layout rects to test visibility instead.
        const focusables = () => Array.from(
            node.querySelectorAll('button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])')
        ).filter((el) => !el.disabled && el.getClientRects().length > 0)
        const first = focusables()[0]
        if (first) first.focus()
        const onKey = (e) => {
            if (node.querySelector('[role="dialog"]')) return
            if (e.key === 'Escape') {
                e.stopPropagation()
                onEscapeRef.current()
                return
            }
            if (e.key === 'Tab') {
                const list = focusables()
                if (list.length === 0) return
                const firstEl = list[0]
                const lastEl = list[list.length - 1]
                if (!node.contains(document.activeElement)) {
                    e.preventDefault()
                    firstEl.focus()
                } else if (e.shiftKey && document.activeElement === firstEl) {
                    e.preventDefault()
                    lastEl.focus()
                } else if (!e.shiftKey && document.activeElement === lastEl) {
                    e.preventDefault()
                    firstEl.focus()
                }
            }
        }
        document.addEventListener('keydown', onKey)
        return () => {
            document.removeEventListener('keydown', onKey)
            if (previouslyFocused && typeof previouslyFocused.focus === 'function' && document.contains(previouslyFocused)) {
                previouslyFocused.focus()
            }
        }
    }, [ref])
}
