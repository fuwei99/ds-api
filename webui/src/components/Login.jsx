import { useState } from 'react'
import { ArrowRight, Check, Key, LayoutDashboard, Lock, ShieldCheck } from 'lucide-react'

import { useI18n } from '../i18n'
import LanguageToggle from './LanguageToggle'
import ThemeToggle from './ThemeToggle'

export default function Login({ onLogin, onMessage }) {
    const { t } = useI18n()
    const [adminKey, setAdminKey] = useState('')
    const [loading, setLoading] = useState(false)
    const [remember, setRemember] = useState(true)

    const handleLogin = async (e) => {
        e.preventDefault()
        if (!adminKey.trim()) return

        setLoading(true)

        try {
            const res = await fetch('/admin/login', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ admin_key: adminKey }),
            })

            const data = await res.json()

            if (res.ok && data.success) {
                const storage = remember ? localStorage : sessionStorage
                storage.setItem('ds2api_token', data.token)
                storage.setItem('ds2api_token_expires', Date.now() + data.expires_in * 1000)

                onLogin(data.token)
                if (data.message) {
                    onMessage('warning', data.message)
                }
            } else {
                onMessage('error', data.detail || t('login.signInFailed'))
            }
        } catch (e) {
            onMessage('error', t('login.networkError', { error: e.message }))
        } finally {
            setLoading(false)
        }
    }

    return (
        <div className="app-backdrop relative flex min-h-screen w-full flex-col items-center justify-center p-4 text-foreground">
            <div className="absolute top-5 right-5 z-20 flex items-center gap-2">
                <ThemeToggle />
                <LanguageToggle />
            </div>

            <div className="relative z-10 w-full max-w-[400px] animate-in fade-in zoom-in-95 duration-300">
                <div className="rounded-2xl border border-border bg-card/80 p-8 shadow-xl shadow-black/5 backdrop-blur-xl">
                    <div className="mb-8 space-y-3 text-center">
                        <div className="inline-flex h-12 w-12 items-center justify-center rounded-xl bg-primary text-primary-foreground shadow-lg shadow-primary/25">
                            <LayoutDashboard className="h-6 w-6" />
                        </div>
                        <div>
                            <h1 className="text-2xl font-bold tracking-tight">{t('login.welcome')}</h1>
                            <p className="mt-1 text-sm text-muted-foreground">{t('login.subtitle')}</p>
                        </div>
                    </div>

                    <form onSubmit={handleLogin} className="space-y-5">
                        <div className="space-y-2">
                            <label className="ml-0.5 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                                {t('login.adminKeyLabel')}
                            </label>
                            <div className="flex items-stretch overflow-hidden rounded-lg border border-input bg-background transition-colors focus-within:border-ring focus-within:ring-2 focus-within:ring-primary/25">
                                <div className="flex items-center justify-center border-r border-border bg-muted/40 px-3 text-muted-foreground transition-colors group-focus-within:text-primary">
                                    <Key className="h-4 w-4" />
                                </div>
                                <input
                                    type="password"
                                    className="h-11 flex-1 bg-transparent px-3 text-sm text-foreground placeholder:text-muted-foreground/50 focus:outline-none"
                                    placeholder={t('login.adminKeyPlaceholder')}
                                    value={adminKey}
                                    onChange={e => setAdminKey(e.target.value)}
                                    autoFocus
                                />
                            </div>
                        </div>

                        <div className="flex items-center justify-between px-0.5">
                            <label className="group flex cursor-pointer items-center gap-2.5">
                                <div className="relative flex items-center">
                                    <input
                                        type="checkbox"
                                        className="peer sr-only"
                                        checked={remember}
                                        onChange={e => setRemember(e.target.checked)}
                                    />
                                    <div className="h-[18px] w-[18px] rounded-md border border-border bg-secondary shadow-sm transition-all peer-checked:border-primary peer-checked:bg-primary" />
                                    <Check className="absolute inset-0 m-auto h-3 w-3 stroke-[3] text-primary-foreground opacity-0 transition-opacity peer-checked:opacity-100" />
                                </div>
                                <span className="text-xs font-medium text-muted-foreground transition-colors group-hover:text-foreground">
                                    {t('login.rememberSession')}
                                </span>
                            </label>
                        </div>

                        <button
                            type="submit"
                            disabled={loading}
                            className="btn btn-primary h-11 w-full text-sm shadow-lg shadow-primary/20"
                        >
                            {loading ? (
                                <div className="h-4.5 w-4.5 animate-spin rounded-full border-2 border-primary-foreground/30 border-t-primary-foreground" />
                            ) : (
                                <>
                                    <span>{t('login.signIn')}</span>
                                    <ArrowRight className="h-4 w-4" />
                                </>
                            )}
                        </button>
                    </form>

                    <div className="mt-7 flex justify-center border-t border-border pt-5">
                        <div className="flex items-center gap-1.5 text-[10px] font-medium uppercase tracking-wider text-muted-foreground/70">
                            <ShieldCheck className="h-3 w-3" />
                            <span>{t('login.secureConnection')}</span>
                        </div>
                    </div>
                </div>

                <p className="mt-6 text-center font-mono text-[10px] text-muted-foreground/50">
                    {t('login.adminPortal')}
                </p>
            </div>
        </div>
    )
}
