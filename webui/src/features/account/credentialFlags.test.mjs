// Guards the registry-driven credential-flag mapping in the account page.
//
// The promise of the credential-flag registry is that a new per-key switch needs
// no web UI component: the server describes the flags and the page renders them.
// That only holds if the page reads each flag through its declaration and sends
// it back under the same name, so these tests pin the mapping in both directions.
//
// Run with the rest of the Node unit tests via tests/scripts/run-unit-node.sh.
import assert from 'node:assert/strict'
import test from 'node:test'

import {
    credentialFlagFormValues,
    credentialFlagPayload,
    credentialFlagSpecs,
    credentialFlagValue,
} from './credentialFlags.js'

const SPECS = [
    {
        name: 'flag_off_by_default',
        default: false,
        label_key: 'accountManager.aLabel',
        help_key: 'accountManager.aHint',
        badge_key: 'accountManager.aOn',
    },
    {
        name: 'flag_on_by_default',
        default: true,
        label_key: 'accountManager.bLabel',
        help_key: '',
        badge_key: '',
    },
]

test('credentialFlagSpecs tolerates a response without specs', () => {
    assert.deepEqual(credentialFlagSpecs({}), [])
    assert.deepEqual(credentialFlagSpecs(undefined), [])
    assert.deepEqual(credentialFlagSpecs({ credential_flag_specs: 'not-an-array' }), [])
    assert.deepEqual(credentialFlagSpecs({ credential_flag_specs: [{ label_key: 'x' }] }), [])
})

test('credentialFlagValue reads a stored flag', () => {
    assert.equal(credentialFlagValue({ flag_off_by_default: true }, SPECS[0]), true)
    assert.equal(credentialFlagValue({ flag_on_by_default: false }, SPECS[1]), false)
})

test('credentialFlagValue falls back to the declared default', () => {
    // The API only reports a flag that differs from its default, so an absent
    // field must read as the declared default — otherwise a default-on switch
    // would render as off while the backend behaves as on.
    assert.equal(credentialFlagValue({}, SPECS[0]), false)
    assert.equal(credentialFlagValue({}, SPECS[1]), true)
    assert.equal(credentialFlagValue(null, SPECS[1]), true)
    assert.equal(credentialFlagValue({ flag_on_by_default: null }, SPECS[1]), true)
})

test('credentialFlagFormValues builds one entry per declared flag', () => {
    const config = { credential_flag_specs: SPECS }
    assert.deepEqual(credentialFlagFormValues(config, null), {
        flag_off_by_default: false,
        flag_on_by_default: true,
    })
    assert.deepEqual(credentialFlagFormValues(config, { flag_off_by_default: true }), {
        flag_off_by_default: true,
        flag_on_by_default: true,
    })
    assert.deepEqual(credentialFlagFormValues({}, { flag_off_by_default: true }), {})
})

test('credentialFlagPayload sends flags under their own names', () => {
    const payload = credentialFlagPayload({ credential_flag_specs: SPECS }, {
        flag_off_by_default: true,
        flag_on_by_default: false,
    })
    assert.deepEqual(payload, { flag_off_by_default: true, flag_on_by_default: false })
})

test('credentialFlagPayload omits flags the page does not know about', () => {
    // A page loaded before a switch existed must not reset it: only the flags
    // the response declared are sent back.
    const stale = { credential_flag_specs: [SPECS[0]] }
    assert.deepEqual(credentialFlagPayload(stale, { flag_off_by_default: true }), {
        flag_off_by_default: true,
    })
    assert.deepEqual(credentialFlagPayload({}, { flag_off_by_default: true }), {})
})
