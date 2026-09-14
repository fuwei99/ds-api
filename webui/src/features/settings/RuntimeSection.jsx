export default function RuntimeSection({ t, form, setForm }) {
    return (
        <div className="bg-card border border-border rounded-xl p-5 space-y-4">
            <h3 className="font-semibold">{t('settings.runtimeTitle')}</h3>
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
                <label className="text-sm space-y-2">
                    <span className="text-muted-foreground">{t('settings.accountMaxInflight')}</span>
                    <input
                        type="number"
                        min={1}
                        value={form.runtime.account_max_inflight}
                        onChange={(e) => setForm((prev) => ({
                            ...prev,
                            runtime: { ...prev.runtime, account_max_inflight: Number(e.target.value || 1) },
                        }))}
                        className="w-full bg-background border border-border rounded-lg px-3 py-2"
                    />
                </label>
                <label className="text-sm space-y-2">
                    <span className="text-muted-foreground">{t('settings.accountMaxQueue')}</span>
                    <input
                        type="number"
                        min={1}
                        value={form.runtime.account_max_queue}
                        onChange={(e) => setForm((prev) => ({
                            ...prev,
                            runtime: { ...prev.runtime, account_max_queue: Number(e.target.value || 1) },
                        }))}
                        className="w-full bg-background border border-border rounded-lg px-3 py-2"
                    />
                </label>
                <label className="text-sm space-y-2">
                    <span className="text-muted-foreground">{t('settings.globalMaxInflight')}</span>
                    <input
                        type="number"
                        min={1}
                        value={form.runtime.global_max_inflight}
                        onChange={(e) => setForm((prev) => ({
                            ...prev,
                            runtime: { ...prev.runtime, global_max_inflight: Number(e.target.value || 1) },
                        }))}
                        className="w-full bg-background border border-border rounded-lg px-3 py-2"
                    />
                </label>
                <label className="text-sm space-y-2">
                    <span className="text-muted-foreground">{t('settings.tokenRefreshIntervalHours')}</span>
                    <input
                        type="number"
                        min={1}
                        max={720}
                        step={1}
                        value={form.runtime.token_refresh_interval_hours}
                        onChange={(e) => setForm((prev) => ({
                            ...prev,
                            runtime: { ...prev.runtime, token_refresh_interval_hours: Number(e.target.value || 1) },
                        }))}
                        className="w-full bg-background border border-border rounded-lg px-3 py-2"
                    />
                </label>
                <label className="text-sm space-y-2 md:col-span-2 lg:col-span-4">
                    <span className="text-muted-foreground">{t('settings.deviceIdMode')}</span>
                    <select
                        value={form.runtime.device_id_mode || 'random'}
                        onChange={(e) => setForm((prev) => ({
                            ...prev,
                            runtime: { ...prev.runtime, device_id_mode: e.target.value },
                        }))}
                        className="w-full bg-background border border-border rounded-lg px-3 py-2"
                    >
                        <option value="random">{t('settings.deviceIdModeRandom')}</option>
                        <option value="real">{t('settings.deviceIdModeReal')}</option>
                    </select>
                    <span className="block text-xs text-muted-foreground leading-relaxed">
                        {t('settings.deviceIdModeDesc')}
                    </span>
                </label>
                <label className="flex items-start gap-3 rounded-lg border border-border bg-background/60 p-4 md:col-span-1 lg:col-span-2">
                    <input
                        type="checkbox"
                        checked={Boolean(form.runtime?.mute_report?.enabled)}
                        onChange={(e) => setForm((prev) => ({
                            ...prev,
                            runtime: {
                                ...prev.runtime,
                                mute_report: { ...prev.runtime?.mute_report, enabled: e.target.checked },
                            },
                        }))}
                        className="mt-1 h-4 w-4 rounded border-border"
                    />
                    <div className="space-y-1">
                        <span className="text-sm font-medium block">{t('settings.muteReportEnabled')}</span>
                        <span className="text-xs text-muted-foreground block">{t('settings.muteReportDesc')}</span>
                    </div>
                </label>
                <label className="text-sm space-y-2 md:col-span-1 lg:col-span-2">
                    <span className="text-muted-foreground">{t('settings.muteReportUrl')}</span>
                    <input
                        type="url"
                        value={form.runtime?.mute_report?.url ?? ''}
                        placeholder="http://127.0.0.1:8100"
                        onChange={(e) => setForm((prev) => ({
                            ...prev,
                            runtime: {
                                ...prev.runtime,
                                mute_report: { ...prev.runtime?.mute_report, url: e.target.value },
                            },
                        }))}
                        className="w-full bg-background border border-border rounded-lg px-3 py-2"
                    />
                    <span className="block text-xs text-muted-foreground leading-relaxed">
                        {t('settings.muteReportUrlDesc')}
                    </span>
                </label>
                <label className="flex items-start gap-3 rounded-lg border border-border bg-background/60 p-4 md:col-span-1 lg:col-span-2">
                    <input
                        type="checkbox"
                        checked={Boolean(form.runtime?.account_rate_limit?.enabled)}
                        onChange={(e) => setForm((prev) => ({
                            ...prev,
                            runtime: {
                                ...prev.runtime,
                                account_rate_limit: {
                                    ...prev.runtime?.account_rate_limit,
                                    enabled: e.target.checked,
                                },
                            },
                        }))}
                        className="mt-1 h-4 w-4 rounded border-border"
                    />
                    <div className="space-y-1">
                        <span className="text-sm font-medium block">{t('settings.accountRateLimitEnabled')}</span>
                        <span className="text-xs text-muted-foreground block">{t('settings.accountRateLimitDesc')}</span>
                    </div>
                </label>
                <label className="text-sm space-y-2 md:col-span-1 lg:col-span-2">
                    <span className="text-muted-foreground">{t('settings.accountRateLimitLimit')}</span>
                    <input
                        type="number"
                        min={1}
                        value={form.runtime?.account_rate_limit?.max_per_minute ?? 5}
                        onChange={(e) => setForm((prev) => ({
                            ...prev,
                            runtime: {
                                ...prev.runtime,
                                account_rate_limit: {
                                    ...prev.runtime?.account_rate_limit,
                                    max_per_minute: Math.max(1, Number(e.target.value || 1)),
                                },
                            },
                        }))}
                        className="w-full bg-background border border-border rounded-lg px-3 py-2"
                    />
                    <span className="block text-xs text-muted-foreground leading-relaxed">
                        {t('settings.accountRateLimitHelp')}
                    </span>
                </label>
            </div>
        </div>
    )
}
