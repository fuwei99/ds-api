export const THEME_STORAGE_KEY = 'ds2api_theme'
export const DARK_THEME = 'dark'
export const LIGHT_THEME = 'light'

export const getInitialTheme = (storage) => {
    const savedTheme = storage?.getItem(THEME_STORAGE_KEY)
    if (savedTheme === LIGHT_THEME || savedTheme === DARK_THEME) {
        return savedTheme
    }
    return DARK_THEME
}

export const applyTheme = (theme, root) => {
    if (root) {
        root.dataset.theme = theme
    }
}

export const toggleTheme = (theme) => (
    theme === LIGHT_THEME ? DARK_THEME : LIGHT_THEME
)
