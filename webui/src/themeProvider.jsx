import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'

import { applyTheme, DARK_THEME, getInitialTheme, LIGHT_THEME, THEME_STORAGE_KEY, toggleTheme } from './theme'

const ThemeContext = createContext({
    theme: DARK_THEME,
    setTheme: () => {},
    toggle: () => {},
})

export const ThemeProvider = ({ children }) => {
    const [theme, setTheme] = useState(() => getInitialTheme(typeof window === 'undefined' ? undefined : window.localStorage))
    const isFirstRun = useRef(true)

    useEffect(() => {
        applyTheme(theme, document.documentElement)
        if (isFirstRun.current) {
            isFirstRun.current = false
            return
        }
        try {
            window.localStorage.setItem(THEME_STORAGE_KEY, theme)
        } catch (_e) {}
    }, [theme])

    useEffect(() => {
        if (typeof window === 'undefined') return
        const onStorage = (e) => {
            if (e.key !== THEME_STORAGE_KEY) return
            if (e.newValue === LIGHT_THEME || e.newValue === DARK_THEME) {
                setTheme(prev => prev === e.newValue ? prev : e.newValue)
            }
        }
        window.addEventListener('storage', onStorage)
        return () => window.removeEventListener('storage', onStorage)
    }, [])

    const toggle = useCallback(() => {
        setTheme(prev => toggleTheme(prev))
    }, [])

    const value = useMemo(() => ({ theme, setTheme, toggle }), [theme, toggle])

    return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}

export const useTheme = () => useContext(ThemeContext)
