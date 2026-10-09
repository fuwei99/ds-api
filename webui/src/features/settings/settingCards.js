// Builds the settings page's cards from the server's declarations.
//
// The page's unit is a card: a titled block holding one or more registered
// settings, or one config section. Which settings share a card, what the card is
// called and where it sits come from the registries in internal/config — the
// settings API reports them as `setting_cards` / `section_specs` — so moving a
// setting to another card is a registry edit and not a change here.
//
// A setting no card names still reaches the page: it renders in a card of its
// own, titled by the setting itself. That is what keeps "add a setting" one
// registry entry.

// A card's position is declared (SettingCardSpec.Order, SectionForm.Order). A
// setting that belongs to no card has no declared position either, so it goes
// last: a cardless setting must never displace a card that was placed on
// purpose.
const NO_DECLARED_ORDER = Number.MAX_SAFE_INTEGER

function specList(value) {
    return Array.isArray(value) ? value : []
}

function declaredOrder(value) {
    return Number.isFinite(value) ? value : 0
}

export function buildSettingCards({ sectionSpecs, settingSpecs, settingCards } = {}) {
    const cards = []

    // Section cards. A bespoke section keeps its own component, so the generic
    // renderer leaves it out; its values still travel through the same form.
    for (const spec of specList(sectionSpecs)) {
        if (!spec || spec.bespoke || !String(spec.key || '')) {
            continue
        }
        cards.push({ kind: 'section', order: declaredOrder(spec.order), spec })
    }

    // Group the settings by the card each one declares.
    const grouped = new Map()
    const cardless = []
    for (const spec of specList(settingSpecs)) {
        const key = String(spec?.key || '')
        if (!key) {
            continue
        }
        const cardKey = String(spec?.card || '')
        if (!cardKey) {
            cardless.push(spec)
            continue
        }
        if (!grouped.has(cardKey)) {
            grouped.set(cardKey, [])
        }
        grouped.get(cardKey).push(spec)
    }

    for (const card of specList(settingCards)) {
        const key = String(card?.key || '')
        const held = grouped.get(key)
        if (!key || !held || held.length === 0) {
            continue
        }
        grouped.delete(key)
        cards.push({
            kind: 'settings',
            order: declaredOrder(card.order),
            card,
            settings: held,
            ownCard: false,
        })
    }

    // A setting that named a card the payload does not describe (a stale page)
    // falls back to a card of its own rather than disappearing from the page.
    for (const held of grouped.values()) {
        for (const spec of held) {
            cards.push(ownCard(spec))
        }
    }
    for (const spec of cardless) {
        cards.push(ownCard(spec))
    }

    // Stable sort: cards that declare the same order keep the order they arrived
    // in, which is declaration order on both sides and puts section cards before
    // setting cards.
    return cards.sort((a, b) => a.order - b.order)
}

// ownCard wraps one setting as a card of its own. The switch inside it then
// falls back to its field's own label: the card title already names the setting,
// so repeating it on the switch would say the same thing twice.
function ownCard(spec) {
    return {
        kind: 'settings',
        order: NO_DECLARED_ORDER,
        card: {
            key: String(spec.key),
            title_key: String(spec.title_key || ''),
            description_key: String(spec.description_key || ''),
        },
        settings: [spec],
        ownCard: true,
    }
}
