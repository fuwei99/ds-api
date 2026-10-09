import assert from 'node:assert/strict'
import test from 'node:test'

import {
    DARK_THEME,
    getInitialTheme,
    LIGHT_THEME,
    THEME_STORAGE_KEY,
    toggleTheme,
} from './theme.js'

test('uses the saved theme', () => {
    assert.equal(getInitialTheme({ getItem: (key) => key === THEME_STORAGE_KEY ? LIGHT_THEME : null }), LIGHT_THEME)
    assert.equal(getInitialTheme({ getItem: (key) => key === THEME_STORAGE_KEY ? DARK_THEME : null }), DARK_THEME)
})

test('defaults to the dark theme for a missing or invalid saved value', () => {
    assert.equal(getInitialTheme({ getItem: () => null }), DARK_THEME)
    assert.equal(getInitialTheme({ getItem: () => 'system' }), DARK_THEME)
})

test('toggles between light and dark themes', () => {
    assert.equal(toggleTheme(DARK_THEME), LIGHT_THEME)
    assert.equal(toggleTheme(LIGHT_THEME), DARK_THEME)
})
