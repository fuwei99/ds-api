// Renders the settings page's cards: one per declared setting card
// (internal/config/settings.go) and one per declared section form
// (internal/config/sections.go), in the order the declarations give them.
//
// Everything a card needs — which settings it holds, the card title, each
// field's label, its help text, its input kind, its bounds, an enum's options —
// comes from the server's `setting_cards` / `setting_specs` / `section_specs`
// payload, which is generated from the Go registries. Adding or changing a
// setting, or moving one to another card, is therefore a registry entry plus the
// locale keys it names: no component, no per-field JSX, and no new file here.
//
// A section that needs a bespoke layout keeps its own component and is marked
// `bespoke` in its declaration; its values still travel through the same read
// and write path.
import { useState } from 'react'

import { buildSettingCards } from './settingCards'
import { parseIntFieldInput } from './useSettingsForm'

export default function RegistrySettingsSection({ t, form, setForm, specs, sectionSpecs = [], cardSpecs = [] }) {
    const cards = buildSettingCards({
        sectionSpecs,
        settingSpecs: specs,
        settingCards: cardSpecs,
    })
    if (cards.length === 0) {
        return null
    }
    return (
        <>
            {cards.map((card) => (card.kind === 'section'
                ? <SectionCard key={`section:${card.spec.key}`} t={t} spec={card.spec} form={form} setForm={setForm} />
                : (
                    <SettingsCard
                        key={`settings:${card.card.key}`}
                        t={t}
                        form={form}
                        setForm={setForm}
                        card={card.card}
                        settings={card.settings}
                        ownCard={card.ownCard}
                    />
                )))}
        </>
    )
}

function CardShell({ title, description, children }) {
    return (
        <div className="bg-card border border-border rounded-xl p-5 space-y-4">
            <div className="space-y-1">
                <h3 className="font-semibold">{title}</h3>
                {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
            </div>
            {children}
        </div>
    )
}

function SectionCard({ t, spec, form, setForm }) {
    const key = String(spec?.key || '')
    const fields = Array.isArray(spec?.fields) ? spec.fields : []
    const titleKey = String(spec?.title_key || '')
    const descriptionKey = String(spec?.description_key || '')
    const section = form?.[key] || {}
    const setField = (name, value) => setForm((prev) => ({
        ...prev,
        [key]: { ...prev?.[key], [name]: value },
    }))
    return (
        <CardShell title={titleKey ? t(titleKey) : key} description={descriptionKey ? t(descriptionKey) : ''}>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                {fields.flatMap((field) => (
                    <SectionFieldControl
                        key={String(field?.name || '')}
                        t={t}
                        field={field}
                        value={section?.[field?.name]}
                        onChange={(value) => setField(String(field?.name || ''), value)}
                    />
                ))}
            </div>
        </CardShell>
    )
}

// IntFieldInput renders one declared int field. The text in the box is local
// state so the field stays usable while it is being edited — cleared, half
// typed, out of range — while the form keeps the last value the declaration
// accepts. The box reports a value only when what it holds is one the backend
// will take, so clearing it leaves the stored value alone instead of rewriting
// it to 0, which a min-1 field rejects (and the whole save with it); leaving
// the box shows the value that will be saved again.
function IntFieldInput({ field, value, onChange }) {
    const [draft, setDraft] = useState(null)
    const min = Number.isFinite(field?.min) ? field.min : undefined
    const max = Number.isFinite(field?.max) ? field.max : undefined
    const committed = typeof value === 'number' && Number.isFinite(value) ? String(value) : ''
    return (
        <input
            type="number"
            min={min}
            max={max}
            value={draft !== null ? draft : committed}
            onChange={(e) => {
                setDraft(e.target.value)
                const next = parseIntFieldInput(field, e.target.value)
                if (next !== undefined) {
                    onChange(next)
                }
            }}
            onBlur={() => setDraft(null)}
            className="w-full bg-background border border-border rounded-lg px-3 py-2"
        />
    )
}

// SectionFieldControl renders one declared section field: a leaf control
// (checkbox / number / text / textarea / select) or, for a group, the block of
// the same controls it holds.
function SectionFieldControl({ t, field, value, onChange }) {
    const name = String(field?.name || '')
    if (!name) {
        return null
    }
    const labelKey = String(field?.label_key || '')
    const label = labelKey ? t(labelKey) : ''
    const helpKey = String(field?.help_key || '')
    const help = helpKey ? <p className="text-xs text-muted-foreground">{t(helpKey)}</p> : null

    if (field?.kind === 'group') {
        const group = value && typeof value === 'object' ? value : {}
        const controls = (Array.isArray(field?.fields) ? field.fields : []).map((nested) => (
            <SectionFieldControl
                key={name + '.' + String(nested?.name || '')}
                t={t}
                field={nested}
                value={group?.[nested?.name]}
                onChange={(nestedValue) => onChange({ ...group, [nested?.name]: nestedValue })}
            />
        ))
        // A group that declares no label has no name of its own: it only binds a
        // sub-feature's switch to the settings that switch governs, so its fields
        // render straight into the card. A header here would repeat the switch's
        // own label, which is the one the user reads.
        if (!labelKey) {
            return controls
        }
        return (
            <div className="md:col-span-2 rounded-lg border border-border bg-background/40 p-4 space-y-3">
                <span className="text-sm font-medium block">{label}</span>
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">{controls}</div>
            </div>
        )
    }

    if (field?.kind === 'bool') {
        return (
            <label className="flex items-start gap-3 rounded-lg border border-border bg-background/60 p-4">
                <input
                    type="checkbox"
                    checked={Boolean(value)}
                    onChange={(e) => onChange(e.target.checked)}
                    className="mt-1 h-4 w-4 rounded border-border"
                />
                <div className="space-y-1">
                    <span className="text-sm font-medium block">{label}</span>
                    {helpKey ? (
                        <span className="text-xs text-muted-foreground block">{t(helpKey)}</span>
                    ) : null}
                </div>
            </label>
        )
    }

    if (field?.kind === 'int') {
        return (
            <label className="text-sm space-y-2">
                <span className="text-muted-foreground">{label}</span>
                <IntFieldInput field={field} value={value} onChange={onChange} />
                {help}
            </label>
        )
    }

    if (field?.kind === 'enum') {
        const options = Array.isArray(field?.options) ? field.options : []
        const selected = String(value ?? '')
        const current = options.find((opt) => String(opt?.value) === selected)
        const descriptionKey = String(current?.description_key || '')
        const warningKey = String(current?.warning_key || '')
        return (
            <label className="text-sm space-y-2 md:col-span-2">
                <span className="text-muted-foreground">{label}</span>
                <select
                    value={selected}
                    onChange={(e) => onChange(e.target.value)}
                    className="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm leading-5 focus:outline-none focus:ring-1 focus:ring-ring"
                >
                    {options.map((opt) => (
                        <option key={String(opt?.value)} value={String(opt?.value)}>
                            {t(String(opt?.label_key || ''))}
                        </option>
                    ))}
                </select>
                {descriptionKey ? (
                    <span className="block text-xs text-muted-foreground">{t(descriptionKey)}</span>
                ) : null}
                {warningKey ? (
                    <span className="block text-xs text-amber-500">{t(warningKey)}</span>
                ) : null}
                {help}
            </label>
        )
    }

    const text = String(value ?? '')
    if (field?.multiline) {
        return (
            <label className="text-sm space-y-2 md:col-span-2">
                <span className="text-muted-foreground">{label}</span>
                <textarea
                    rows={5}
                    value={text}
                    onChange={(e) => onChange(e.target.value)}
                    className="w-full bg-background border border-border rounded-lg px-3 py-2 resize-y min-h-32 font-mono text-xs"
                />
                {help}
            </label>
        )
    }
    return (
        <label className="text-sm space-y-2">
            <span className="text-muted-foreground">{label}</span>
            <input
                type="text"
                value={text}
                onChange={(e) => onChange(e.target.value)}
                className="w-full bg-background border border-border rounded-lg px-3 py-2"
            />
            {help}
        </label>
    )
}

// SettingsCard renders one card of the settings page: the settings the registry
// groups under it, in declaration order, as one grid of controls.
function SettingsCard({ t, form, setForm, card, settings, ownCard }) {
    const titleKey = String(card?.title_key || '')
    const descriptionKey = String(card?.description_key || '')
    const setSettingField = (key, name, value) => setForm((prev) => ({
        ...prev,
        settings: {
            ...prev.settings,
            [key]: { ...prev?.settings?.[key], [name]: value },
        },
    }))

    return (
        <CardShell
            title={titleKey ? t(titleKey) : String(card?.key || '')}
            description={descriptionKey ? t(descriptionKey) : ''}
        >
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                {settings.flatMap((spec) => {
                    const key = String(spec?.key || '')
                    if (!key) {
                        return []
                    }
                    const section = form?.settings?.[key] || {}
                    return (Array.isArray(spec?.fields) ? spec.fields : []).map((field) => (
                        <SettingField
                            key={key + '.' + String(field?.name || '')}
                            t={t}
                            field={field}
                            section={section}
                            setField={(name, value) => setSettingField(key, name, value)}
                            // Inside a card that holds several settings the switch
                            // has to name which one it turns on: the card title
                            // names the subject, not the switch. In a card that is
                            // the setting itself the title already says it, so the
                            // switch keeps its field's own label.
                            namedBy={ownCard ? null : spec}
                        />
                    ))
                })}
            </div>
        </CardShell>
    )
}

function SettingField({ t, field, section, setField, namedBy = null }) {
    const name = String(field?.name || '')
    if (!name) {
        return null
    }
    const label = t(String(field?.label_key || ''))
    const helpKey = String(field?.help_key || '')
    const help = helpKey ? (
        <p className="text-xs text-muted-foreground">{t(helpKey)}</p>
    ) : null
    // The server may attach `default_<field>` for a field whose built-in text is
    // worth showing: as a placeholder, or prefilled when the field asks for it.
    const defaultText = String(section?.[`default_${name}`] || '')
    // The name and description of the setting this field belongs to, for the
    // switch of a setting that shares its card with others.
    const settingTitleKey = String(namedBy?.title_key || '')
    const switchLabel = settingTitleKey ? t(settingTitleKey) : label
    const switchHelpKey = settingTitleKey ? String(namedBy?.description_key || '') : helpKey

    if (field?.kind === 'bool') {
        return (
            <label className="flex items-start gap-3 rounded-lg border border-border bg-background/60 p-4">
                <input
                    type="checkbox"
                    checked={Boolean(section?.[name])}
                    onChange={(e) => setField(name, e.target.checked)}
                    className="mt-1 h-4 w-4 rounded border-border"
                />
                <div className="space-y-1">
                    <span className="text-sm font-medium block">{switchLabel}</span>
                    {switchHelpKey ? (
                        <span className="text-xs text-muted-foreground block">{t(switchHelpKey)}</span>
                    ) : null}
                </div>
            </label>
        )
    }

    if (field?.kind === 'int') {
        return (
            <label className="text-sm space-y-2">
                <span className="text-muted-foreground">{label}</span>
                <IntFieldInput
                    field={field}
                    value={section?.[name]}
                    onChange={(next) => setField(name, next)}
                />
                {help}
            </label>
        )
    }

    const value = String(section?.[name] ?? '')
    // A field that asks for a prefill starts with the built-in text so editing
    // saves a copy of it; every other field shows that text as a placeholder,
    // which keeps "empty means use the built-in" true.
    const shown = field?.prefill_default && !value ? defaultText : value
    const placeholder = field?.prefill_default ? undefined : defaultText || undefined
    if (field?.multiline) {
        return (
            <label className="text-sm space-y-2 md:col-span-2">
                <span className="text-muted-foreground">{label}</span>
                <textarea
                    rows={5}
                    value={shown}
                    placeholder={placeholder}
                    onChange={(e) => setField(name, e.target.value)}
                    className="w-full bg-background border border-border rounded-lg px-3 py-2 resize-y min-h-32 font-mono text-xs"
                />
                {help}
            </label>
        )
    }
    return (
        <label className="text-sm space-y-2">
            <span className="text-muted-foreground">{label}</span>
            <input
                type="text"
                value={shown}
                placeholder={placeholder}
                onChange={(e) => setField(name, e.target.value)}
                className="w-full bg-background border border-border rounded-lg px-3 py-2"
            />
            {help}
        </label>
    )
}
