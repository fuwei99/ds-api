import { ArrowRight, Boxes, Github, LayoutDashboard, Radio, Scale, ScanSearch, Sparkles, Workflow } from 'lucide-react'

import { useI18n } from '../i18n'
import LanguageToggle from './LanguageToggle'
import ThemeToggle from './ThemeToggle'

const FEATURES = [
    { icon: Boxes, key: 'compatibility' },
    { icon: Scale, key: 'loadBalancing' },
    { icon: Workflow, key: 'reasoning' },
    { icon: ScanSearch, key: 'search' },
]

export default function LandingPage({ onEnter }) {
    const { t } = useI18n()

    return (
        <div className="app-backdrop relative min-h-screen overflow-hidden text-foreground">
            <div className="absolute top-5 right-5 z-20 flex items-center gap-2">
                <ThemeToggle />
                <LanguageToggle />
            </div>

            <div className="relative z-10 mx-auto flex min-h-screen w-full max-w-5xl flex-col items-center justify-center px-6 py-20 text-center">
                <div className="inline-flex items-center gap-2 rounded-full border border-border bg-card/70 px-3.5 py-1.5 text-xs font-medium text-muted-foreground backdrop-blur">
                    <Sparkles className="h-3.5 w-3.5 text-primary" />
                    {t('landing.badge')}
                </div>

                <h1 className="mt-6 text-5xl sm:text-6xl font-bold tracking-tight">
                    DS2<span className="text-primary">API</span>
                </h1>
                <p className="mt-4 max-w-xl text-base sm:text-lg text-muted-foreground leading-relaxed">
                    {t('landing.features.compatibility.desc')}
                </p>

                <div className="mt-9 flex flex-wrap items-center justify-center gap-3">
                    <button
                        onClick={onEnter}
                        className="btn btn-primary h-11 px-6 text-sm shadow-lg shadow-primary/25"
                    >
                        <LayoutDashboard className="h-4 w-4" />
                        {t('landing.adminConsole')}
                        <ArrowRight className="h-4 w-4" />
                    </button>
                    <a
                        href="/v1/models"
                        target="_blank"
                        rel="noreferrer"
                        className="btn btn-secondary h-11 px-6 text-sm"
                    >
                        <Radio className="h-4 w-4" />
                        {t('landing.apiStatus')}
                    </a>
                    <a
                        href="https://github.com/CJackHwang/ds2api"
                        target="_blank"
                        rel="noreferrer"
                        className="btn btn-secondary h-11 px-6 text-sm"
                    >
                        <Github className="h-4 w-4" />
                        GitHub
                    </a>
                </div>

                <div className="mt-16 grid w-full grid-cols-1 gap-4 text-left sm:grid-cols-2 lg:grid-cols-4">
                    {FEATURES.map(({ icon: Icon, key }) => (
                        <div
                            key={key}
                            className="group rounded-xl border border-border bg-card/70 p-5 backdrop-blur transition-all duration-200 hover:-translate-y-1 hover:border-primary/40 hover:shadow-lg hover:shadow-primary/5"
                        >
                            <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/10 text-primary transition-colors group-hover:bg-primary group-hover:text-primary-foreground">
                                <Icon className="h-4.5 w-4.5" />
                            </div>
                            <h3 className="mt-4 text-sm font-semibold">
                                {t(`landing.features.${key}.title`)}
                            </h3>
                            <p className="mt-1.5 text-xs leading-relaxed text-muted-foreground">
                                {t(`landing.features.${key}.desc`)}
                            </p>
                        </div>
                    ))}
                </div>

                <footer className="mt-16 text-xs text-muted-foreground/60">
                    <p>{t('landing.footer')}</p>
                </footer>
            </div>
        </div>
    )
}
