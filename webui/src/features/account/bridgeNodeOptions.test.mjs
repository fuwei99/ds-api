// 账号管理「账号代理」下拉的选项规则。
//
// 这条规则决定了用户在账号行里能看到哪些代理桥节点：默认只列出最近一次测速
// 连通的节点（手动测速与自动调度测速写的是同一份持久化字段），一个节点都没
// 测过速时才放开为全部节点。这些判断只写在 bridgeNodeOptions.js 里，界面按它
// 渲染，所以这里把每条规则钉住。
//
// Run with the rest of the Node unit tests via tests/scripts/run-unit-node.sh.
import assert from 'node:assert/strict'
import test from 'node:test'

import {
    AUTO_VALUE,
    NODE_PREFIX,
    NO_PROXY_VALUE,
    PROXY_PREFIX,
    buildProxyOptions,
    currentBinding,
    filterProxyOptions,
    nodeTested,
    parseBindingValue,
    selectableNodes,
} from './bridgeNodeOptions.js'

const node = (name, extra = {}) => ({
    node_key: `sub-1::${name}`,
    name,
    proxy_id: `mihomo-${name}`,
    health: 'unknown',
    latency_ms: 0,
    tested_at: 0,
    accounts: [],
    ...extra,
})

test('selectableNodes 未测速时显示全部节点', () => {
    const nodes = [node('香港'), node('日本'), node('美国')]
    assert.equal(nodes.some(n => nodeTested(n)), false, '固件本身不应带测速结果')
    assert.equal(selectableNodes(nodes).length, 3)
})

test('selectableNodes 测过速后只保留连通的节点', () => {
    const nodes = [
        node('香港', { health: 'ok', latency_ms: 120, tested_at: 100 }),
        node('日本', { health: 'fail', tested_at: 100, health_error: 'timeout' }),
        node('美国', { health: 'unknown' }),
    ]
    const kept = selectableNodes(nodes)
    assert.deepEqual(kept.map(n => n.name), ['香港'])
})

test('selectableNodes 连通节点按延迟升序', () => {
    const nodes = [
        node('慢节点', { health: 'ok', latency_ms: 300, tested_at: 1 }),
        node('快节点', { health: 'ok', latency_ms: 50, tested_at: 1 }),
    ]
    assert.deepEqual(selectableNodes(nodes).map(n => n.name), ['快节点', '慢节点'])
})

test('selectableNodes 未测速时按节点名排序', () => {
    const nodes = [node('香港'), node('美国'), node('日本')]
    assert.deepEqual(selectableNodes(nodes).map(n => n.name), ['日本', '美国', '香港'])
})

test('取值编码可往返', () => {
    assert.deepEqual(parseBindingValue(NO_PROXY_VALUE), { kind: 'none', id: '' })
    assert.deepEqual(parseBindingValue(AUTO_VALUE), { kind: 'auto', id: '' })
    assert.deepEqual(parseBindingValue(NODE_PREFIX + 'sub-1::香港'), { kind: 'node', id: 'sub-1::香港' })
    assert.deepEqual(parseBindingValue(PROXY_PREFIX + 'proxy-1'), { kind: 'proxy', id: 'proxy-1' })
    // 其它前缀（含空值）一律按"不走代理"处理，避免提交出未知绑定。
    assert.equal(parseBindingValue('garbage').kind, 'none')
})

test('currentBinding 区分自动调度、手动绑定与不走代理', () => {
    const nodes = [node('香港', { proxy_id: 'mihomo-hk' })]
    const proxies = [
        { id: 'mihomo-hk', name: '香港', type: 'socks5', host: '127.0.0.1', port: 10801 },
        { id: 'mihomo-jp', name: '日本', type: 'socks5', host: '127.0.0.1', port: 10802 },
        { id: 'manual-1', name: '手动代理', type: 'socks5', host: '10.0.0.1', port: 1080 },
    ]
    // 手动绑定的节点：用户钉住的选择，按钮与高亮都是这个节点。
    const pinned = currentBinding({ proxyID: 'mihomo-hk', manualBinding: true, nodes, proxies })
    assert.deepEqual(pinned, {
        value: NODE_PREFIX + 'sub-1::香港',
        kind: 'node',
        mode: NODE_PREFIX + 'sub-1::香港',
        manual: true,
        nodeKey: 'sub-1::香港',
        label: '香港',
        health: 'unknown',
    })
    assert.equal(currentBinding({ proxyID: 'manual-1', manualBinding: true, nodes, proxies }).value, PROXY_PREFIX + 'manual-1')
    // 没有绑定代理时：显式直连（no_proxy）与"等桥分配"是两种状态。
    assert.equal(currentBinding({ proxyID: '', noProxy: true, nodes, proxies }).value, NO_PROXY_VALUE)
    assert.equal(currentBinding({ proxyID: '', noProxy: false, nodes, proxies }).value, AUTO_VALUE)
    assert.equal(currentBinding({ proxyID: '', nodes, proxies }).value, AUTO_VALUE)
    // 自动调度分配来的托管节点：按钮上照常显示当前节点（用户不点开也知道走哪个出口），
    // 但面板里高亮的是「自动调度」——节点不是用户的选择，桥可以随时把它挪走。
    const auto = currentBinding({ proxyID: 'mihomo-hk', manualBinding: false, nodes, proxies })
    assert.equal(auto.value, NODE_PREFIX + 'sub-1::香港')
    assert.equal(auto.label, '香港')
    assert.equal(auto.mode, AUTO_VALUE)
    assert.equal(auto.manual, false)
    // 节点已从订阅消失（列表里没有）时按代理 ID 原样回显，不能显示空白。
    assert.equal(currentBinding({ proxyID: 'mihomo-gone', manualBinding: true, nodes, proxies }).value, PROXY_PREFIX + 'mihomo-gone')
})

test('buildProxyOptions 不会把自动调度塞进手动代理组', () => {
    const nodes = [node('香港', { health: 'ok', latency_ms: 80, tested_at: 1 })]
    const proxies = [{ id: 'manual-1', name: '手动代理', type: 'socks5', host: '10.0.0.1', port: 1080 }]
    const { current, nodeOptions, proxyOptions } = buildProxyOptions({ proxyID: '', noProxy: false, nodes, proxies })
    assert.equal(current.value, AUTO_VALUE)
    assert.deepEqual(nodeOptions.map(o => o.value), [NODE_PREFIX + 'sub-1::香港'])
    assert.deepEqual(proxyOptions.map(o => o.value), [PROXY_PREFIX + 'manual-1'])
})

test('buildProxyOptions 分开代理桥节点与手动代理', () => {
    const nodes = [
        node('香港', { health: 'ok', latency_ms: 80, tested_at: 1, accounts: [{ identifier: 'a@x.com' }] }),
        node('日本', { health: 'fail', tested_at: 1 }),
    ]
    const proxies = [
        { id: 'mihomo-hk', name: '香港', type: 'socks5', host: '127.0.0.1', port: 10801 },
        { id: 'manual-1', name: '手动代理', type: 'socks5', host: '10.0.0.1', port: 1080 },
    ]
    const { nodeOptions, proxyOptions } = buildProxyOptions({ proxyID: '', nodes, proxies })
    assert.deepEqual(nodeOptions.map(o => o.value), [NODE_PREFIX + 'sub-1::香港'])
    assert.equal(nodeOptions[0].bound, 1)
    // 托管代理不作为手动代理重复列出。
    assert.deepEqual(proxyOptions.map(o => o.value), [PROXY_PREFIX + 'manual-1'])
})

test('buildProxyOptions 当前绑定不可用时仍保留在列表里', () => {
    const nodes = [
        node('香港', { health: 'ok', latency_ms: 80, tested_at: 1 }),
        node('日本', { health: 'fail', tested_at: 1, proxy_id: 'mihomo-jp' }),
    ]
    const proxies = [{ id: 'mihomo-jp', name: '日本', type: 'socks5', host: '127.0.0.1', port: 10802 }]
    const { current, nodeOptions } = buildProxyOptions({ proxyID: 'mihomo-jp', manualBinding: true, nodes, proxies })
    assert.equal(current.value, NODE_PREFIX + 'sub-1::日本')
    assert.equal(nodeOptions[0].value, current.value, '当前绑定必须排在节点组第一位')
    assert.equal(nodeOptions[0].stale, true)
    assert.equal(nodeOptions[0].health, 'fail')
})

test('buildProxyOptions 不回填自动调度分配来的节点', () => {
    const nodes = [
        node('香港', { health: 'ok', latency_ms: 80, tested_at: 1 }),
        node('日本', { health: 'fail', tested_at: 1, proxy_id: 'mihomo-jp' }),
    ]
    const proxies = [{ id: 'mihomo-jp', name: '日本', type: 'socks5', host: '127.0.0.1', port: 10802 }]
    // 节点由自动调度分配且当前测速失败：不是用户的选择，不该以"当前绑定"回填，
    // 面板里高亮的是「自动调度」（桥会在下一轮把账号挪到健康节点）。
    const { current, nodeOptions } = buildProxyOptions({ proxyID: 'mihomo-jp', manualBinding: false, nodes, proxies })
    assert.equal(current.value, NODE_PREFIX + 'sub-1::日本', '按钮仍显示当前节点')
    assert.equal(current.mode, AUTO_VALUE, '面板高亮自动调度')
    assert.deepEqual(nodeOptions.map(o => o.value), [NODE_PREFIX + 'sub-1::香港'])
})

test('filterProxyOptions 按名称/服务器/订阅匹配', () => {
    const options = [
        { value: NODE_PREFIX + 'a', label: '香港 01', server: 'hk.example.com', subscription: '机场 A' },
        { value: NODE_PREFIX + 'b', label: '日本 01', server: 'jp.example.com', subscription: '机场 B' },
    ]
    assert.equal(filterProxyOptions(options, '').length, 2)
    assert.deepEqual(filterProxyOptions(options, '香港').map(o => o.label), ['香港 01'])
    assert.deepEqual(filterProxyOptions(options, 'jp.example').map(o => o.label), ['日本 01'])
    assert.deepEqual(filterProxyOptions(options, '机场 B').map(o => o.label), ['日本 01'])
    assert.equal(filterProxyOptions(options, '不存在').length, 0)
})
