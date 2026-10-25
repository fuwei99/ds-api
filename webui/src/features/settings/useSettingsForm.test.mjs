// Guards the registry-driven settings and sections mapping in the settings form.
//
// The promise of both registries is that a new setting or a new config section
// needs no web UI component: the server describes the fields and the form
// renders them. That only holds if the form reads each field from its own key
// and sends it back under the same key, so these tests pin the mapping in both
// directions.
//
// Run with the rest of the Node unit tests via tests/scripts/run-unit-node.sh.
import assert from 'node:assert/strict'
import test from 'node:test'

import { fromServerSections, fromServerSettings, intFieldAccepts, parseIntFieldInput, toServerPayload } from './useSettingsForm.js'

const SPECS = [
    {
        key: 'setting_a',
        title_key: 'settings.aTitle',
        fields: [
            { name: 'enabled', kind: 'bool', default: false, label_key: 'settings.fieldEnabled' },
            { name: 'limit', kind: 'int', default: 900, min: 30, max: 86400, label_key: 'settings.aLimit' },
        ],
    },
    {
        key: 'setting_b',
        title_key: 'settings.bTitle',
        fields: [
            { name: 'prompt', kind: 'string', default: '', multiline: true, label_key: 'settings.bPrompt' },
        ],
    },
]

const SECTION_SPECS = [
    {
        key: 'runtime',
        title_key: 'settings.runtimeTitle',
        bespoke: false,
        fields: [
            { name: 'account_max_inflight', kind: 'int', min: 1, max: 256, label_key: 'settings.accountMaxInflight' },
            { name: 'device_id_mode', kind: 'enum', label_key: 'settings.deviceIdMode', options: [] },
            {
                name: 'mute_report',
                kind: 'group',
                label_key: 'settings.muteReportEnabled',
                fields: [
                    { name: 'enabled', kind: 'bool', label_key: 'settings.muteReportEnabled' },
                    { name: 'url', kind: 'string', label_key: 'settings.muteReportUrl' },
                ],
            },
            {
                name: 'account_rate_limit',
                kind: 'group',
                label_key: 'settings.accountRateLimitEnabled',
                fields: [
                    { name: 'enabled', kind: 'bool', label_key: 'settings.accountRateLimitEnabled' },
                    { name: 'max_per_minute', kind: 'int', min: 1, max: 100000, label_key: 'settings.accountRateLimitLimit' },
                ],
            },
        ],
    },
    {
        key: 'admin',
        title_key: 'settings.securityTitle',
        bespoke: true,
        fields: [{ name: 'jwt_expire_hours', kind: 'int', min: 1, max: 720, label_key: 'settings.jwtExpireHours' }],
    },
]

// The registered settings' declared bounds, as internal/config/settings.go
// declares them: responses.store_ttl_seconds starts at 30, while
// input_file.min_chars legitimately starts at 0.
const INT_SETTING_SPECS = [
    {
        key: 'responses',
        title_key: 'settings.responsesTitle',
        fields: [
            { name: 'store_ttl_seconds', kind: 'int', default: 900, min: 30, max: 86400, label_key: 'settings.responsesTTL' },
        ],
    },
    {
        key: 'input_file',
        title_key: 'settings.inputFileTitle',
        fields: [
            { name: 'enabled', kind: 'bool', default: false, label_key: 'settings.fieldEnabled' },
            { name: 'min_chars', kind: 'int', default: 0, min: 0, max: 100000000, label_key: 'settings.inputFileMinChars' },
        ],
    },
]

test('fromServerSettings reads every declared field from its own key', () => {
    const got = fromServerSettings({
        setting_specs: SPECS,
        setting_a: { enabled: true, limit: 600 },
        setting_b: { prompt: 'hello' },
    })
    assert.deepEqual(got, {
        setting_a: { enabled: true, limit: 600 },
        setting_b: { prompt: 'hello' },
    })
})

test('fromServerSettings fills unset fields with the declared default', () => {
    // A field the server does not report must render as what the backend would
    // actually do, not as undefined.
    const got = fromServerSettings({ setting_specs: SPECS, setting_a: {} })
    assert.deepEqual(got, {
        setting_a: { enabled: false, limit: 900 },
        setting_b: { prompt: '' },
    })
})

test('fromServerSettings keeps server-side extras such as default_prompt', () => {
    // The two prompt settings show their built-in text, which the server
    // attaches next to the values rather than declaring as a field.
    const got = fromServerSettings({
        setting_specs: SPECS,
        setting_b: { prompt: '', default_prompt: 'built-in text' },
    })
    assert.equal(got.setting_b.default_prompt, 'built-in text')
    assert.equal(got.setting_b.prompt, '')
})

test('fromServerSettings tolerates a response without specs', () => {
    assert.deepEqual(fromServerSettings({}), {})
    assert.deepEqual(fromServerSettings(undefined), {})
    assert.deepEqual(fromServerSettings({ setting_specs: 'not-an-array' }), {})
})

test('fromServerSections reads each section under its config key', () => {
    // The values live under the section's own key — the same key the config file
    // uses — including the fields of a nested group.
    const got = fromServerSections(
        {
            runtime: {
                account_max_inflight: 4,
                device_id_mode: 'manual',
                mute_report: { enabled: true, url: 'http://127.0.0.1:8100' },
            },
            admin: { jwt_expire_hours: 48, has_password_hash: true },
        },
        SECTION_SPECS,
    )
    assert.equal(got.runtime.account_max_inflight, 4)
    assert.equal(got.runtime.mute_report.url, 'http://127.0.0.1:8100')
    assert.equal(got.admin.jwt_expire_hours, 48)
    // Server-side metadata travels with the section, not as a field.
    assert.equal(got.admin.has_password_hash, true)
})

test('fromServerSections tolerates a section the response omits', () => {
    const got = fromServerSections({}, SECTION_SPECS)
    assert.deepEqual(got, { runtime: {}, admin: {} })
    assert.deepEqual(fromServerSections({ runtime: {} }, undefined), {})
})

test('toServerPayload sends sections under their own keys', () => {
    const payload = toServerPayload(
        {
            runtime: {
                account_max_inflight: 4,
                device_id_mode: 'auto',
                mute_report: { enabled: true, url: 'http://127.0.0.1:8100' },
            },
            admin: { jwt_expire_hours: 48 },
            settings: {},
        },
        SECTION_SPECS,
    )
    assert.deepEqual(payload.runtime, {
        account_max_inflight: 4,
        device_id_mode: 'auto',
        mute_report: { enabled: true, url: 'http://127.0.0.1:8100' },
    })
    // A bespoke section's fields travel the same way as any other section's.
    assert.deepEqual(payload.admin, { jwt_expire_hours: 48 })
})

test('toServerPayload never sends section display extras back', () => {
    const payload = toServerPayload(
        { runtime: {}, admin: { jwt_expire_hours: 24, default_jwt_expire_hours: '24' }, settings: {} },
        SECTION_SPECS,
    )
    assert.deepEqual(payload.admin, { jwt_expire_hours: 24 })
})

test('toServerPayload omits sections the form does not know about', () => {
    // A page loaded before a section existed must not reset it.
    const payload = toServerPayload({ runtime: {}, admin: {}, settings: {} }, SECTION_SPECS)
    assert.deepEqual(payload, { runtime: {}, admin: {} })
    assert.equal('auto_delete' in payload, false)
})

test('toServerPayload sends settings under their own keys in section shape', () => {
    const payload = toServerPayload({
        settings: {
            setting_a: { enabled: true, limit: 600 },
            setting_b: { prompt: 'hello' },
        },
    })
    assert.deepEqual(payload.setting_a, { enabled: true, limit: 600 })
    assert.deepEqual(payload.setting_b, { prompt: 'hello' })
})

test('toServerPayload never sends setting display extras back', () => {
    // `default_<field>` entries are text the server showed the user, not values
    // the user edited: sending them would freeze a copy of the built-in text
    // into the config.
    const payload = toServerPayload({
        settings: { setting_b: { prompt: '', default_prompt: 'built-in text' } },
    })
    assert.deepEqual(payload.setting_b, { prompt: '' })
})

test('toServerPayload omits settings the form does not know about', () => {
    // A page loaded before a setting existed must not reset it.
    const payload = toServerPayload({ settings: {} })
    assert.equal('setting_a' in payload, false)
    assert.equal('setting_b' in payload, false)
})

test('parseIntFieldInput reports nothing for a box the declaration would not accept', () => {
    // The int control reports a value only when the box holds one the backend
    // will take. An empty box, a half-typed number and a value outside the
    // declared bounds all report nothing, so the form keeps the field's
    // previous value instead of being rewritten to 0 — which a min-1 section
    // field rejects, and discards the whole save with it.
    const field = { name: 'account_max_inflight', kind: 'int', min: 1, max: 256 }
    assert.equal(parseIntFieldInput(field, ''), undefined)
    assert.equal(parseIntFieldInput(field, '   '), undefined)
    assert.equal(parseIntFieldInput(field, '0'), undefined)
    assert.equal(parseIntFieldInput(field, '257'), undefined)
    assert.equal(parseIntFieldInput(field, 'abc'), undefined)
    assert.equal(parseIntFieldInput(field, '4.5'), undefined)
    assert.equal(parseIntFieldInput(field, '4'), 4)
    assert.equal(parseIntFieldInput(field, ' 4 '), 4)
})

test('parseIntFieldInput accepts 0 where the declaration allows it', () => {
    // The rule is the declared bounds, not a hard-coded minimum: input_file's
    // min_chars starts at 0, so 0 is a value the user may set there while the
    // same 0 is refused for a field declared from 1.
    const field = { name: 'min_chars', kind: 'int', min: 0, max: 100000000 }
    assert.equal(parseIntFieldInput(field, '0'), 0)
    assert.equal(intFieldAccepts(field, 0), true)
    assert.equal(intFieldAccepts({ min: 1, max: 256 }, 0), false)
    assert.equal(intFieldAccepts({ min: 1, max: 256 }, 5.5), false)
})

test('clearing an int box leaves the previous value in the payload', () => {
    // What the control reports for a cleared box is nothing, so the value the
    // form already held is what travels — not the 0 the old coercion sent.
    const field = { name: 'account_max_inflight', kind: 'int', min: 1, max: 256 }
    assert.equal(parseIntFieldInput(field, ''), undefined)
    const payload = toServerPayload({ runtime: { account_max_inflight: 4 } }, SECTION_SPECS)
    assert.deepEqual(payload.runtime, { account_max_inflight: 4 })
})

test('toServerPayload leaves out a loaded section int outside its declared bounds', () => {
    // DS2API_ACCOUNT_MAX_QUEUE=0 makes the page open with 0 in a min-1 box.
    // Echoing it back would make the backend reject the request — and every
    // other edit in it — so the field is left out and the stored value stays.
    const payload = toServerPayload(
        { runtime: { account_max_inflight: 0, device_id_mode: 'manual' } },
        SECTION_SPECS,
    )
    assert.deepEqual(payload.runtime, { device_id_mode: 'manual' })
    // A number the backend could not decode as an int is left out the same way.
    assert.deepEqual(
        toServerPayload({ runtime: { account_max_inflight: 4.5 } }, SECTION_SPECS).runtime,
        {},
    )
})

test('toServerPayload applies the declared bounds inside a group too', () => {
    const payload = toServerPayload(
        { runtime: { account_rate_limit: { enabled: true, max_per_minute: 0 } } },
        SECTION_SPECS,
    )
    assert.deepEqual(payload.runtime, { account_rate_limit: { enabled: true } })
})

test('toServerPayload applies the declared bounds to registered settings', () => {
    // Registered settings follow the same rule, from their own bounds: 0 is
    // outside responses.store_ttl_seconds' [30, 86400] and is left out, while
    // input_file.min_chars' 0 is a value its declaration allows.
    const payload = toServerPayload(
        { settings: { responses: { store_ttl_seconds: 0 }, input_file: { enabled: true, min_chars: 0 } } },
        [],
        INT_SETTING_SPECS,
    )
    assert.deepEqual(payload.responses, {})
    assert.deepEqual(payload.input_file, { enabled: true, min_chars: 0 })
})

test('toServerPayload bounds a bespoke section field as well', () => {
    // A bespoke section's card is its own component, but its values travel the
    // same read and write path, so the declaration bounds them too.
    const payload = toServerPayload({ admin: { jwt_expire_hours: 9999 } }, SECTION_SPECS)
    assert.deepEqual(payload.admin, {})
})
