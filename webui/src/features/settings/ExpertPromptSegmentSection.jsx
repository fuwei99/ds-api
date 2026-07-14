export default function ExpertPromptSegmentSection({ t, form, setForm }) {
    return (
        <div className="bg-card border border-border rounded-xl p-5 space-y-4">
            <div className="space-y-1">
                <h3 className="font-semibold">{t('settings.expertPromptSegmentTitle')}</h3>
                <p className="text-sm text-muted-foreground">{t('settings.expertPromptSegmentDesc')}</p>
                <p className="text-xs text-amber-600 dark:text-amber-400">{t('settings.expertPromptSegmentWarning')}</p>
            </div>
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <label className="flex items-start gap-3 rounded-lg border border-border bg-background/60 p-4">
                    <input
                        type="checkbox"
                        checked={Boolean(form.expert_prompt_segment?.enabled)}
                        onChange={(e) => setForm((prev) => ({
                            ...prev,
                            expert_prompt_segment: {
                                ...prev.expert_prompt_segment,
                                enabled: e.target.checked,
                            },
                        }))}
                        className="mt-1 h-4 w-4 rounded border-border"
                    />
                    <div className="space-y-1">
                        <span className="text-sm font-medium block">{t('settings.expertPromptSegmentEnabled')}</span>
                        <span className="text-xs text-muted-foreground block">{t('settings.expertPromptSegmentDesc')}</span>
                    </div>
                </label>
                <label className="text-sm space-y-2">
                    <span className="text-muted-foreground">{t('settings.expertPromptSegmentMaxChars')}</span>
                    <input
                        type="number"
                        min={1000}
                        max={100000000}
                        value={form.expert_prompt_segment?.max_chars ?? 90000}
                        onChange={(e) => setForm((prev) => ({
                            ...prev,
                            expert_prompt_segment: {
                                ...prev.expert_prompt_segment,
                                max_chars: Number(e.target.value || 0),
                            },
                        }))}
                        className="w-full bg-background border border-border rounded-lg px-3 py-2"
                    />
                    <p className="text-xs text-muted-foreground">{t('settings.expertPromptSegmentMaxCharsHelp')}</p>
                </label>
                <label className="text-sm space-y-2">
                    <span className="text-muted-foreground">{t('settings.expertPromptSegmentStopDelayMs')}</span>
                    <input
                        type="number"
                        min={0}
                        max={60000}
                        value={form.expert_prompt_segment?.stop_delay_ms ?? 1500}
                        onChange={(e) => setForm((prev) => ({
                            ...prev,
                            expert_prompt_segment: {
                                ...prev.expert_prompt_segment,
                                stop_delay_ms: Number(e.target.value || 0),
                            },
                        }))}
                        className="w-full bg-background border border-border rounded-lg px-3 py-2"
                    />
                    <p className="text-xs text-muted-foreground">{t('settings.expertPromptSegmentStopDelayMsHelp')}</p>
                </label>
            </div>
        </div>
    )
}
