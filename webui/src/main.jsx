import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import App from './App.jsx'
import { I18nProvider } from './i18n'
import './styles.css'
import { applyTheme, getInitialTheme } from './theme'
import { ThemeProvider } from './themeProvider'

const basename = import.meta.env.MODE === 'production' ? '/admin' : '/'

applyTheme(getInitialTheme(localStorage), document.documentElement)

ReactDOM.createRoot(document.getElementById('root')).render(
    <React.StrictMode>
        <ThemeProvider>
            <I18nProvider>
                <BrowserRouter basename={basename}>
                    <App />
                </BrowserRouter>
            </I18nProvider>
        </ThemeProvider>
    </React.StrictMode>,
)
