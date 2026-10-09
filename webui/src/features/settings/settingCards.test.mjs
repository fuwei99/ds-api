// Guards the settings page's card layout.
//
// The page's unit is a card, and which settings share one — plus where each card
// sits — is declared in the Go registries and reported as `setting_cards` /
// `setting_specs` / `section_specs`. This module only arranges what the server
// declares, so these tests pin that arrangement: the grouping, the shared order
// sequence across both registries, and the fallback that keeps a setting with no
// card on the page.
//
// Run with the rest of the Node unit tests via tests/scripts/run-unit-node.sh.
import assert from 'node:assert/strict'
import test from 'node:test'

import { buildSettingCards } from './settingCards.js'

const SECTION_SPECS = [
    { key: 'admin', title_key: 'settings.securityTitle', bespoke: true, order: 1, fields: [] },
    { key: 'runtime', title_key: 'settings.runtimeTitle', order: 2, fields: [] },
    { key: 'auto_delete', title_key: 'settings.autoDeleteTitle', order: 6, fields: [] },
]

const SETTING_SPECS = [
    { key: 'responses', card: 'behavior', title_key: 'settings.responsesTitle', fields: [] },
    { key: 'thinking_injection', card: 'behavior', title_key: 'settings.thinkingInjectionTitle', fields: [] },
    { key: 'input_file', card: 'input_file', title_key: 'settings.inputFileEnabled', fields: [] },
    { key: 'loose', title_key: 'settings.looseTitle', fields: [] },
]

const SETTING_CARDS = [
    { key: 'behavior', title_key: 'settings.behaviorTitle', order: 3 },
    { key: 'input_file', title_key: 'settings.inputFileTitle', description_key: 'settings.inputFileDesc', order: 4 },
]

function build(overrides = {}) {
    return buildSettingCards({
        sectionSpecs: SECTION_SPECS,
        settingSpecs: SETTING_SPECS,
        settingCards: SETTING_CARDS,
        ...overrides,
    })
}

test('buildSettingCards puts the settings a card names in one card', () => {
    const cards = build()
    const behavior = cards.find((card) => card.kind === 'settings' && card.card.key === 'behavior')
    assert.ok(behavior, 'the declared card must render')
    assert.deepEqual(behavior.settings.map((spec) => spec.key), ['responses', 'thinking_injection'])
    assert.equal(behavior.card.title_key, 'settings.behaviorTitle')
    // A card that holds several settings labels each switch with the setting's
    // own name, so the switch says which one it turns on.
    assert.equal(behavior.ownCard, false)
})

test('buildSettingCards places every card by the order its declaration gives', () => {
    const cards = build()
    assert.deepEqual(cards.map((card) => (card.kind === 'section' ? card.spec.key : card.card.key)), [
        'runtime',
        'behavior',
        'input_file',
        'auto_delete',
        'loose',
    ])
})

test('buildSettingCards leaves a bespoke section to its own component', () => {
    const cards = build()
    assert.equal(cards.some((card) => card.kind === 'section' && card.spec.key === 'admin'), false)
})

test('buildSettingCards gives a setting no card names a card of its own, last', () => {
    const cards = build()
    const loose = cards[cards.length - 1]
    assert.equal(loose.kind, 'settings')
    assert.deepEqual(loose.settings.map((spec) => spec.key), ['loose'])
    // The card is titled by the setting itself, so the switch inside keeps its
    // field's own label instead of repeating the title.
    assert.equal(loose.card.title_key, 'settings.looseTitle')
    assert.equal(loose.ownCard, true)
})

test('buildSettingCards keeps a setting whose card the payload does not describe', () => {
    const cards = build({ settingCards: [SETTING_CARDS[0]] })
    const keys = cards.filter((card) => card.kind === 'settings').map((card) => card.card.key)
    assert.deepEqual(keys, ['behavior', 'input_file', 'loose'])
    const inputFile = cards.find((card) => card.kind === 'settings' && card.card.key === 'input_file')
    assert.equal(inputFile.ownCard, true)
})

test('buildSettingCards tolerates a response without declarations', () => {
    assert.deepEqual(buildSettingCards(), [])
    assert.deepEqual(buildSettingCards({ sectionSpecs: null, settingSpecs: null, settingCards: null }), [])
})
