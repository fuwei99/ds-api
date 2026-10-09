import { Languages } from 'lucide-react'

import { useI18n } from '../i18n'

export default function LanguageToggle({ className = '', compact = false }) {
    const { lang, setLang, t } = useI18n()
    const nextLang = lang === 'zh' ? 'en' : 'zh'
    const label = nextLang === 'zh' ? t('language.chinese') : t('language.english')
    const title = nextLang === 'zh' ? t('language.switchToChinese') : t('language.switchToEnglish')

    return (
        <button
            type="button"
            onClick={() => setLang(nextLang)}
            className={`inline-flex items-center justify-center gap-1.5 rounded-lg border border-border bg-card/80 text-xs font-medium text-muted-foreground shadow-sm backdrop-blur transition-colors hover:border-primary/40 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 focus-visible:ring-offset-background ${compact ? 'h-8 px-2' : 'h-9 px-3'} ${className}`}
            title={title}
            aria-label={title}
        >
            <Languages className="h-3.5 w-3.5" />
            <span>{nextLang === 'zh' ? '中' : 'EN'}</span>
            {!compact && <span className="hidden sm:inline text-muted-foreground/70">· {label}</span>}
        </button>
    )
}
