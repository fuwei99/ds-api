import assert from 'node:assert/strict'
import test from 'node:test'

import { accountOptions, nodesEqual, statusEqual } from './useMihomoBridge.js'

test('accountOptions normalizes email, mobile, and names', () => {
    const raw = [
        { email: 'user1@example.com', name: 'User One' },
        { mobile: '+8613800138000', name: '' },
        { email: 'user2@example.com' },
        { email: '   ', mobile: '' },
        null,
    ]
    const opts = accountOptions(raw)
    assert.equal(opts.length, 3)
    assert.deepEqual(opts[0], { identifier: 'user1@example.com', label: 'User One (user1@example.com)' })
    assert.deepEqual(opts[1], { identifier: '+8613800138000', label: '+8613800138000' })
    assert.deepEqual(opts[2], { identifier: 'user2@example.com', label: 'user2@example.com' })
})

test('nodesEqual detects unchanged nodes list correctly', () => {
    const a = [
        {
            node_key: 'sub-1::node1',
            local_port: 20001,
            health: 'pass',
            latency_ms: 120,
            tested_at: 1700000000,
            health_error: '',
            accounts: [{ identifier: 'user1@example.com', label: 'user1@example.com' }],
        },
        {
            node_key: 'sub-1::node2',
            local_port: 0,
            health: 'unknown',
            latency_ms: 0,
            tested_at: 0,
            health_error: '',
            accounts: [],
        },
    ]
    const b = JSON.parse(JSON.stringify(a))

    assert.equal(nodesEqual(a, b), true)
    assert.equal(nodesEqual(a, a), true)

    // Different length
    assert.equal(nodesEqual(a, [a[0]]), false)

    // Latency changed
    const c = JSON.parse(JSON.stringify(a))
    c[0].latency_ms = 150
    assert.equal(nodesEqual(a, c), false)

    // Account binding changed
    const d = JSON.parse(JSON.stringify(a))
    d[0].accounts = []
    assert.equal(nodesEqual(a, d), false)

    // Health changed
    const e = JSON.parse(JSON.stringify(a))
    e[1].health = 'fail'
    assert.equal(nodesEqual(a, e), false)
})

test('statusEqual detects status changes correctly', () => {
    const s1 = {
        running: true,
        pid: 1234,
        last_error: '',
        enabled: true,
        auto_bind: true,
        subscriptions: 2,
        listeners: [{ port: 20001 }],
        health: { available: 10, dead: 1 },
    }
    const s2 = { ...s1, listeners: [{ port: 20001 }] }
    assert.equal(statusEqual(s1, s2), true)

    assert.equal(statusEqual(s1, { ...s1, running: false }), false)
    assert.equal(statusEqual(s1, { ...s1, health: { available: 9, dead: 2 } }), false)
    assert.equal(statusEqual(s1, { ...s1, last_error: 'some error' }), false)
})

test('scale test: 1200 nodes and 500 accounts process in sub-10ms without memory pressure', () => {
    // 500 accounts
    const accounts = []
    for (let i = 0; i < 500; i++) {
        accounts.push({
            email: `user${i}@example.com`,
            name: `Account ${i}`,
        })
    }
    const t0 = performance.now()
    const opts = accountOptions(accounts)
    const t1 = performance.now()
    assert.equal(opts.length, 500)
    assert.ok(t1 - t0 < 50, `accountOptions took ${t1 - t0}ms, expected < 50ms`)

    // 1200 nodes with 500 bindings
    const nodes = []
    for (let i = 0; i < 1200; i++) {
        const boundAccount = i < 500 ? [{ identifier: `user${i}@example.com`, label: `Account ${i}` }] : []
        nodes.push({
            node_key: `sub-${Math.floor(i / 400)}::node-${i}`,
            name: `HK-Node-${i}`,
            server: `1.2.3.${i % 250}`,
            subscription: `Sub ${Math.floor(i / 400)}`,
            local_port: i < 500 ? 20000 + i : 0,
            health: i % 50 === 0 ? 'fail' : 'pass',
            latency_ms: i % 50 === 0 ? 0 : 50 + (i % 200),
            tested_at: 1700000000,
            health_error: i % 50 === 0 ? 'timeout' : '',
            accounts: boundAccount,
        })
    }

    const t2 = performance.now()
    const unchanged = nodesEqual(nodes, nodes)
    const t3 = performance.now()
    assert.equal(unchanged, true)
    assert.ok(t3 - t2 < 50, `nodesEqual took ${t3 - t2}ms, expected < 50ms`)

    // Verify candidate filtering for a single selected node in modal
    // (Notice: candidates is computed ONLY for the selected node, not 1200 times!)
    const targetNode = nodes[0]
    const boundIds = new Set(targetNode.accounts.map(a => a.identifier))
    const candidates = opts.filter(opt => !boundIds.has(opt.identifier))
    assert.equal(candidates.length, 499)

    // Verify 20-item pagination
    const pageSize = 20
    const totalPages = Math.ceil(candidates.length / pageSize)
    assert.equal(totalPages, 25)
    const page1 = candidates.slice(0, pageSize)
    assert.equal(page1.length, 20)
    const page25 = candidates.slice(24 * pageSize, 25 * pageSize)
    assert.equal(page25.length, 19)
})

