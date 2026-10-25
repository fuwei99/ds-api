import { CheckCircle2, Server, ShieldCheck } from 'lucide-react'

function StatCard({ icon: Icon, label, value, unit, tone }) {
    return (
        <div className="group relative overflow-hidden rounded-xl border border-border bg-card p-5 transition-all duration-200 hover:border-primary/30 hover:shadow-md">
            <div className="pointer-events-none absolute -right-3 -top-3 opacity-[0.06] transition-opacity group-hover:opacity-[0.12]">
                <Icon className="h-20 w-20" />
            </div>
            <div className="flex items-center gap-2">
                <span className={`inline-flex h-1.5 w-1.5 rounded-full ${tone}`} />
                <p className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
                    {label}
                </p>
            </div>
            <div className="mt-3 flex items-baseline gap-2">
                <span className="text-3xl font-bold tabular-nums tracking-tight text-foreground">
                    {value}
                </span>
                <span className="text-xs text-muted-foreground">{unit}</span>
            </div>
        </div>
    )
}

export default function QueueCards({ queueStatus, t }) {
    if (!queueStatus) {
        return null
    }

    return (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
            <StatCard
                icon={CheckCircle2}
                label={t('accountManager.available')}
                value={queueStatus.available}
                unit={t('accountManager.accountsUnit')}
                tone="bg-emerald-500"
            />
            <StatCard
                icon={Server}
                label={t('accountManager.inUse')}
                value={queueStatus.in_use}
                unit={t('accountManager.threadsUnit')}
                tone="bg-primary"
            />
            <StatCard
                icon={ShieldCheck}
                label={t('accountManager.totalPool')}
                value={queueStatus.total}
                unit={t('accountManager.accountsUnit')}
                tone="bg-sky-500"
            />
        </div>
    )
}
