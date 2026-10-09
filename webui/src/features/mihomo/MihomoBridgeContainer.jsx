import { Loader2 } from 'lucide-react'

import { useI18n } from '../../i18n'
import useMihomoBridge from './useMihomoBridge'
import MihomoStatusCard from './MihomoStatusCard'
import MihomoSubscriptions from './MihomoSubscriptions'
import MihomoNodesTable from './MihomoNodesTable'

export default function MihomoBridgeContainer({ config, onRefresh, onMessage, authFetch }) {
    const { t } = useI18n()
    const bridge = useMihomoBridge({
        authFetch,
        onMessage,
        onConfigChanged: onRefresh,
        t,
    })

    if (bridge.loading && !bridge.status) {
        return (
            <div className="min-h-[320px] rounded-xl border border-border bg-card/60 flex items-center justify-center">
                <Loader2 className="w-4 h-4 animate-spin text-primary" />
            </div>
        )
    }

    const confirmDelete = (sub) => {
        if (!confirm(t('mihomoBridge.deleteSubConfirm', { name: sub.name || sub.url }))) return
        bridge.deleteSubscription(sub.id)
    }

    const confirmAssignAll = () => {
        if (!confirm(t('mihomoBridge.assignAllConfirm'))) return
        bridge.assignAll()
    }

    return (
        <div className="space-y-6">
            <MihomoStatusCard
                status={bridge.status}
                busy={bridge.busy}
                onSaveSettings={bridge.saveSettings}
                onApply={bridge.applyNow}
                onDownloadBinary={bridge.downloadBinary}
            />

            <MihomoSubscriptions
                subscriptions={bridge.subscriptions}
                busy={bridge.busy}
                onAdd={bridge.addSubscription}
                onRefresh={(sub) => bridge.refreshSubscription(sub.id)}
                onDelete={confirmDelete}
            />

            <MihomoNodesTable
                nodes={bridge.nodes}
                latency={bridge.latency}
                accounts={config?.accounts || []}
                busy={bridge.busy}
                canTest={Boolean(bridge.status?.running)}
                testing={Boolean(bridge.busy?.delayTest)}
                assigning={Boolean(bridge.busy?.assignAll)}
                onBind={bridge.bindAccount}
                onUnbind={(identifier) => bridge.bindAccount(identifier, '')}
                onTestLatency={bridge.testLatency}
                onAssignAll={confirmAssignAll}
            />
        </div>
    )
}
