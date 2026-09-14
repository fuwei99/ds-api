import { Suspense, lazy, useCallback, useEffect, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import {
    LayoutDashboard,
    Upload,
    Cloud,
    Settings as SettingsIcon,
    LogOut,
    Menu,
    X,
    Server,
    Users,
    Globe,
    Network,
    History,
    Loader2,
    ChevronRight,
} from 'lucide-react'
import clsx from 'clsx'

import LanguageToggle from '../components/LanguageToggle'
import ThemeToggle from '../components/ThemeToggle'
import { useI18n } from '../i18n'

const AccountManagerContainer = lazy(() => import('../features/account/AccountManagerContainer'))
const ApiTesterContainer = lazy(() => import('../features/apiTester/ApiTesterContainer'))
const ChatHistoryContainer = lazy(() => import('../features/chatHistory/ChatHistoryContainer'))
const BatchImport = lazy(() => import('../components/BatchImport'))
const VercelSyncContainer = lazy(() => import('../features/vercel/VercelSyncContainer'))
const SettingsContainer = lazy(() => import('../features/settings/SettingsContainer'))
const ProxyManagerContainer = lazy(() => import('../features/proxy/ProxyManagerContainer'))
const MihomoBridgeContainer = lazy(() => import('../features/mihomo/MihomoBridgeContainer'))

function TabLoadingFallback({ label }) {
    return (
        <div className="min-h-[320px] rounded-xl border border-border bg-card/60 flex items-center justify-center">
            <div className="flex items-center gap-3 text-sm text-muted-foreground">
                <Loader2 className="w-4 h-4 animate-spin text-primary" />
                <span>{label}</span>
            </div>
        </div>
    )
}

function BrandMark({ compact = false }) {
    return (
        <div className="flex items-center gap-2.5">
            <div className={clsx(
                "rounded-xl bg-primary flex items-center justify-center text-primary-foreground shadow-lg shadow-primary/25",
                compact ? "w-7 h-7" : "w-9 h-9"
            )}>
                <LayoutDashboard className={compact ? "w-4 h-4" : "w-5 h-5"} />
            </div>
            {!compact && (
                <div className="leading-tight">
                    <div className="font-bold text-lg tracking-tight text-foreground">DS2API</div>
                </div>
            )}
        </div>
    )
}

export default function DashboardShell({ token, onLogout, config, fetchConfig, showMessage, message, onForceLogout, isVercel }) {
    const { t } = useI18n()
    const location = useLocation()
    const navigate = useNavigate()
    const [sidebarOpen, setSidebarOpen] = useState(false)

    const navItems = [
        { id: 'accounts', label: t('nav.accounts.label'), icon: Users, description: t('nav.accounts.desc') },
        { id: 'proxies', label: t('nav.proxies.label'), icon: Globe, description: t('nav.proxies.desc') },
        { id: 'mihomo', label: t('nav.mihomo.label'), icon: Network, description: t('nav.mihomo.desc') },
        { id: 'test', label: t('nav.test.label'), icon: Server, description: t('nav.test.desc') },
        { id: 'history', label: t('nav.history.label'), icon: History, description: t('nav.history.desc') },
        { id: 'import', label: t('nav.import.label'), icon: Upload, description: t('nav.import.desc') },
        { id: 'vercel', label: t('nav.vercel.label'), icon: Cloud, description: t('nav.vercel.desc') },
        { id: 'settings', label: t('nav.settings.label'), icon: SettingsIcon, description: t('nav.settings.desc') },
    ]

    const tabIds = new Set(navItems.map(item => item.id))
    const pathSegments = location.pathname.replace(/^\/+|\/+$/g, '').split('/').filter(Boolean)
    const routeSegments = pathSegments[0] === 'admin' ? pathSegments.slice(1) : pathSegments
    const pathTab = routeSegments[0] || ''
    const activeTab = tabIds.has(pathTab) ? pathTab : 'accounts'
    const adminBasePath = pathSegments[0] === 'admin' ? '/admin' : ''
    const activeNavItem = navItems.find(n => n.id === activeTab)

    const navigateToTab = useCallback((tabID) => {
        const nextPath = tabID === 'accounts'
            ? `${adminBasePath || ''}/`
            : `${adminBasePath}/${tabID}`
        navigate(nextPath)
        setSidebarOpen(false)
    }, [adminBasePath, navigate])

    const authFetch = useCallback(async (url, options = {}) => {
        const headers = {
            ...options.headers,
            'Authorization': `Bearer ${token}`
        }
        const res = await fetch(url, { ...options, headers })

        if (res.status === 401) {
            onLogout()
            throw new Error(t('auth.expired'))
        }
        return res
    }, [onLogout, t, token])


    const [versionInfo, setVersionInfo] = useState(null)

    useEffect(() => {
        let disposed = false
        async function loadVersion() {
            try {
                const res = await authFetch('/admin/version')
                const data = await res.json()
                if (!disposed) {
                    setVersionInfo(data)
                }
            } catch (_err) {
                if (!disposed) {
                    setVersionInfo(null)
                }
            }
        }
        loadVersion()
        return () => {
            disposed = true
        }
    }, [authFetch])

    const renderTab = () => {
        switch (activeTab) {
            case 'accounts':
                return <AccountManagerContainer config={config} onRefresh={fetchConfig} onMessage={showMessage} authFetch={authFetch} />
            case 'proxies':
                return <ProxyManagerContainer config={config} onRefresh={fetchConfig} onMessage={showMessage} authFetch={authFetch} />
            case 'mihomo':
                return <MihomoBridgeContainer config={config} onRefresh={fetchConfig} onMessage={showMessage} authFetch={authFetch} />
            case 'test':
                return <ApiTesterContainer config={config} onMessage={showMessage} authFetch={authFetch} />
            case 'history':
                return <ChatHistoryContainer onMessage={showMessage} authFetch={authFetch} />
            case 'import':
                return <BatchImport onRefresh={fetchConfig} onMessage={showMessage} authFetch={authFetch} />
            case 'vercel':
                return <VercelSyncContainer onMessage={showMessage} authFetch={authFetch} isVercel={isVercel} config={config} />
            case 'settings':
                return <SettingsContainer onRefresh={fetchConfig} onMessage={showMessage} authFetch={authFetch} onForceLogout={onForceLogout} isVercel={isVercel} />
            default:
                return null
        }
    }

    return (
        <div className="flex h-screen bg-background overflow-hidden text-foreground app-backdrop">
            {sidebarOpen && (
                <div
                    className="fixed inset-0 bg-background/70 backdrop-blur-sm z-40 lg:hidden"
                    onClick={() => setSidebarOpen(false)}
                />
            )}

            {/* Sidebar */}
            <aside className={clsx(
                "fixed lg:static inset-y-0 left-0 z-50 w-64 border-r border-border bg-card/70 backdrop-blur-xl transition-transform duration-300 ease-in-out lg:transform-none flex flex-col",
                sidebarOpen ? "translate-x-0 shadow-2xl" : "-translate-x-full"
            )}>
                <div className="px-5 pt-6 pb-5 border-b border-border/60">
                    <BrandMark />
                    <p className="mt-3 text-[10px] font-semibold tracking-[0.14em] uppercase text-muted-foreground">
                        {t('sidebar.onlineAdminConsole')}
                    </p>
                </div>

                <nav className="flex-1 px-3 py-4 space-y-1 overflow-y-auto custom-scrollbar">
                    {navItems.map((item) => {
                        const Icon = item.icon
                        const isActive = activeTab === item.id
                        return (
                            <button
                                key={item.id}
                                onClick={() => navigateToTab(item.id)}
                                className={clsx(
                                    "w-full flex items-center gap-3 px-3 py-2 rounded-lg text-sm font-medium transition-all duration-150 group relative",
                                    isActive
                                        ? "bg-primary/10 text-foreground"
                                        : "text-muted-foreground hover:bg-secondary/70 hover:text-foreground"
                                )}
                            >
                                {isActive && (
                                    <span className="absolute left-0 top-1/2 -translate-y-1/2 h-5 w-[3px] rounded-full bg-primary" />
                                )}
                                <Icon className={clsx(
                                    "w-4 h-4 shrink-0 transition-colors",
                                    isActive ? "text-primary" : "text-muted-foreground group-hover:text-foreground"
                                )} />
                                <span className="flex-1 text-left">{item.label}</span>
                                {isActive && <ChevronRight className="w-3.5 h-3.5 text-primary" />}
                            </button>
                        )
                    })}
                </nav>

                <div className="p-4 border-t border-border/60 space-y-4">
                    <div className="flex items-center justify-between">
                        <span className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                            {t('sidebar.systemStatus')}
                        </span>
                        <span className="inline-flex items-center gap-1.5 text-[10px] font-semibold text-emerald-500 bg-emerald-500/10 px-2 py-0.5 rounded-full border border-emerald-500/20">
                            <span className="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse" />
                            {t('sidebar.statusOnline')}
                        </span>
                    </div>

                    <div className="grid grid-cols-2 gap-2">
                        <div className="rounded-lg border border-border/60 bg-background/60 px-3 py-2.5">
                            <div className="text-[9px] font-bold uppercase tracking-wider text-muted-foreground">
                                {t('sidebar.accounts')}
                            </div>
                            <div className="text-lg font-bold text-foreground leading-tight">
                                {config.accounts?.length || 0}
                            </div>
                        </div>
                        <div className="rounded-lg border border-border/60 bg-background/60 px-3 py-2.5">
                            <div className="text-[9px] font-bold uppercase tracking-wider text-muted-foreground">
                                {t('sidebar.keys')}
                            </div>
                            <div className="text-lg font-bold text-foreground leading-tight">
                                {config.keys?.length || 0}
                            </div>
                        </div>
                    </div>

                    <div className="rounded-lg border border-border/60 bg-background/60 px-3 py-2.5">
                        <div className="text-[9px] font-bold uppercase tracking-wider text-muted-foreground mb-1">
                            {t('sidebar.version')}
                        </div>
                        <div className="text-xs font-semibold text-foreground">
                            {versionInfo?.current_tag || '-'}
                        </div>
                        {versionInfo?.has_update && (
                            <a
                                className="inline-flex mt-1 text-[10px] font-medium text-primary hover:underline"
                                href={versionInfo?.release_url || 'https://github.com/CJackHwang/ds2api/releases/latest'}
                                target="_blank"
                                rel="noreferrer"
                            >
                                {t('sidebar.updateAvailable', { latest: versionInfo.latest_tag || '' })}
                            </a>
                        )}
                    </div>

                    <button
                        onClick={onLogout}
                        className="w-full h-9 flex items-center justify-center gap-2 rounded-lg border border-border text-xs font-medium text-muted-foreground hover:bg-destructive/10 hover:text-destructive hover:border-destructive/30 transition-all"
                    >
                        <LogOut className="w-3.5 h-3.5" />
                        {t('sidebar.signOut')}
                    </button>
                </div>
            </aside>

            {/* Main column */}
            <main className="flex-1 flex flex-col min-w-0 overflow-hidden relative">
                {/* Mobile top bar */}
                <header className="lg:hidden h-14 flex items-center justify-between px-4 border-b border-border bg-card/70 backdrop-blur-xl">
                    <BrandMark compact />
                    <div className="flex items-center gap-2">
                        <ThemeToggle compact />
                        <LanguageToggle compact />
                        <button
                            onClick={() => setSidebarOpen(true)}
                            className="p-2 -mr-1 rounded-lg text-muted-foreground hover:text-foreground hover:bg-secondary/70"
                            aria-label="Open navigation"
                        >
                            <Menu className="w-5 h-5" />
                        </button>
                    </div>
                </header>

                <div className="flex-1 overflow-auto">
                    <div className="max-w-6xl mx-auto px-4 py-6 lg:px-10 lg:py-10 space-y-5 lg:space-y-7">
                        {/* Page header with controls pinned to the top-right */}
                        <div className="flex items-start justify-between gap-4">
                            <div className="min-w-0">
                                <h1 className="text-2xl lg:text-[1.75rem] font-bold tracking-tight">
                                    {activeNavItem?.label}
                                </h1>
                                <p className="mt-1 text-sm text-muted-foreground max-w-2xl">
                                    {activeNavItem?.description}
                                </p>
                            </div>
                            <div className="hidden lg:flex items-center gap-2 shrink-0">
                                <ThemeToggle />
                                <LanguageToggle />
                            </div>
                        </div>

                        {message && (
                            <div className={clsx(
                                "px-4 py-3 rounded-lg border flex items-center gap-3 text-sm animate-in fade-in slide-in-from-top-2",
                                message.type === 'error'
                                    ? "bg-destructive/10 border-destructive/25 text-destructive"
                                    : "bg-emerald-500/10 border-emerald-500/25 text-emerald-500"
                            )}>
                                {message.type === 'error'
                                    ? <X className="w-4 h-4 shrink-0" />
                                    : <div className="w-4 h-4 rounded-full border-2 border-emerald-500 flex items-center justify-center text-[9px] shrink-0">✓</div>}
                                <span>{message.text}</span>
                            </div>
                        )}

                        <div className="animate-in fade-in duration-300">
                            <Suspense fallback={<TabLoadingFallback label={activeNavItem?.label || 'DS2API'} />}>
                                {renderTab()}
                            </Suspense>
                        </div>
                    </div>
                </div>
            </main>
        </div>
    )
}
