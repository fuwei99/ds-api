// 账号管理里「账号代理」下拉的选项模型。
//
// 下拉里同时存在四类可选值，它们语义不同、提交的字段也不同，因此用一个前缀/
// 常量编码区分：
//
//   'auto'        自动调度：由代理桥分配与故障转移（清空绑定 + 清除 no_proxy /
//                 manual_binding，走 POST /admin/accounts/{identifier}/proxy 的
//                 mode=auto），这是手动绑定之后回到自动分配的唯一入口；
//   ''            不走代理：直连，且禁止自动调度分配节点（no_proxy）；
//   'node:<key>'  代理桥节点（走 /admin/mihomo/bindings/{identifier}）；
//   'proxy:<id>'  手动配置的代理（走 /admin/accounts/{identifier}/proxy）。
//
// 代理桥节点必须先由桥为该账号分配本地 SOCKS5 端口才算绑定，所以不能当成
// 普通代理提交——这是把节点与手动代理分开编码的原因。

export const AUTO_VALUE = 'auto'
export const NO_PROXY_VALUE = ''
export const NODE_PREFIX = 'node:'
export const PROXY_PREFIX = 'proxy:'

// 与后端 config.MihomoManagedProxyPrefix 一致：托管代理由代理桥创建/回收，
// 不在下拉里作为「手动代理」出现（它们对应的节点以节点选项呈现）。
const MANAGED_PROXY_PREFIX = 'mihomo-'

export function isManagedProxyID(id) {
    return String(id || '').trim().startsWith(MANAGED_PROXY_PREFIX)
}

export function nodeValue(nodeKey) {
    return NODE_PREFIX + String(nodeKey || '')
}

export function proxyValue(proxyID) {
    return PROXY_PREFIX + String(proxyID || '')
}

export function parseBindingValue(value) {
    const raw = String(value ?? '')
    if (raw === AUTO_VALUE) return { kind: 'auto', id: '' }
    if (raw.startsWith(NODE_PREFIX)) return { kind: 'node', id: raw.slice(NODE_PREFIX.length) }
    if (raw.startsWith(PROXY_PREFIX)) return { kind: 'proxy', id: raw.slice(PROXY_PREFIX.length) }
    return { kind: 'none', id: '' }
}

// nodeTested 判断节点是否已有测速结论。手动测速与自动调度测速写的是同一份
// 持久化字段（health / latency_ms / tested_at），后写覆盖先写，因此「最后一次」
// 就是这里读到的值。
export function nodeTested(node) {
    if (!node) return false
    if (Number(node.tested_at) > 0) return true
    return node.health === 'ok' || node.health === 'fail'
}

// selectableNodes 返回下拉里可选的代理桥节点：
//   - 一个节点都没测过速（全部 unknown）→ 全部节点可选，还没测速时不该凭空少一批；
//   - 只要测过速 → 只保留最近一次测速连通的节点（health=ok），
//     测速失败与尚未测速的节点不再列出。
// 排序：有延迟数据的按延迟升序，其余按节点名（未测速时就是全部按名称排序）。
export function selectableNodes(nodes) {
    const list = Array.isArray(nodes) ? nodes.slice() : []
    const anyTested = list.some(nodeTested)
    const kept = anyTested ? list.filter(node => node.health === 'ok') : list
    return kept.sort((a, b) => {
        const la = Number(a.latency_ms) || 0
        const lb = Number(b.latency_ms) || 0
        if (la > 0 && lb > 0 && la !== lb) return la - lb
        return String(a.name || '').localeCompare(String(b.name || ''))
    })
}

// currentBinding 反推账号当前的绑定方式（下拉框的当前值与回显）。
//
// 返回两个不同的值，因为"显示什么"和"高亮哪一项"是两件事：
//   - value：触发按钮上显示的当前绑定（自动调度分配来的节点也照原样显示节点名，
//     用户不点开下拉就知道现在走哪个出口）；
//   - mode：下拉面板里应当高亮的选项。自动调度分配来的节点不算用户的选择，
//     此时高亮的是「自动调度」，节点只在按钮上体现。
//
// 其余判定：
//   - 没有绑定：no_proxy 为真 = 不走代理（直连），否则 = 自动调度（等桥分配）；
//   - 绑定到托管代理（mihomo- 前缀）且 manual_binding 为真：用户钉住的节点；
//   - 绑定到普通代理：用户手动指定（manual 恒为真，桥不会分配非托管代理）；
//   - 都找不到时按代理 ID 原样回显，保证界面不会显示成空白。
export function currentBinding({ proxyID, manualBinding, noProxy, nodes, proxies }) {
    const id = String(proxyID || '').trim()
    if (!id) {
        return noProxy
            ? { value: NO_PROXY_VALUE, kind: 'none', mode: NO_PROXY_VALUE, manual: false, label: '' }
            : { value: AUTO_VALUE, kind: 'auto', mode: AUTO_VALUE, manual: false, label: '' }
    }
    const manual = Boolean(manualBinding)
    if (isManagedProxyID(id)) {
        const node = (nodes || []).find(n => String(n.proxy_id || '') === id)
        if (node) {
            const value = nodeValue(node.node_key)
            return {
                value,
                kind: 'node',
                mode: manual ? value : AUTO_VALUE,
                manual,
                nodeKey: node.node_key,
                label: node.name || node.node_key,
                health: node.health || 'unknown',
            }
        }
        const proxy = (proxies || []).find(p => String(p.id || '') === id)
        return {
            value: proxyValue(id),
            kind: 'proxy',
            mode: manual ? proxyValue(id) : AUTO_VALUE,
            manual,
            label: proxy ? (proxy.name || `${proxy.host}:${proxy.port}`) : id,
        }
    }
    const proxy = (proxies || []).find(p => String(p.id || '') === id)
    return {
        value: proxyValue(id),
        kind: 'proxy',
        mode: proxyValue(id),
        manual: true,
        label: proxy ? (proxy.name || `${proxy.host}:${proxy.port}`) : id,
    }
}

// buildProxyOptions 组装下拉面板的两组选项：代理桥节点与手动代理。
// 节点按 selectableNodes 的可见性规则过滤；用户自己钉住的绑定若不在可见集合里
// （测速失败、订阅变更等），以 stale 形式补在对应组的第一位，
// 否则用户看不到自己绑的是什么，也改不回来。自动调度分配来的节点不回填：
// 它不是用户的选择，桥会在下一轮调和里把账号挪走。
export function buildProxyOptions({ proxyID, manualBinding, noProxy, nodes, proxies }) {
    const current = currentBinding({ proxyID, manualBinding, noProxy, nodes, proxies })
    const nodeOptions = selectableNodes(nodes).map(node => ({
        value: nodeValue(node.node_key),
        group: 'node',
        label: node.name || node.node_key,
        latency: Number(node.latency_ms) || 0,
        bound: (node.accounts || []).length,
        health: node.health || 'unknown',
        server: node.server || '',
        subscription: node.subscription || '',
    }))
    const proxyOptions = (proxies || [])
        .filter(proxy => !isManagedProxyID(proxy.id))
        .map(proxy => ({
            value: proxyValue(proxy.id),
            group: 'proxy',
            label: proxy.name || `${proxy.host}:${proxy.port}`,
        }))
    // 只有"用户自己钉住某个具体节点/代理"的当前值需要回填：auto / 直连 是固定
    // 选项，始终在面板里，不参与回填。
    if (current.manual && (current.kind === 'node' || current.kind === 'proxy')) {
        const visible = current.kind === 'node' ? nodeOptions : proxyOptions
        if (!visible.some(option => option.value === current.value)) {
            const fallback = { ...current, stale: true, latency: 0, bound: 0, server: '', subscription: '' }
            visible.unshift(fallback)
        }
    }
    return { current, nodeOptions, proxyOptions }
}

// filterProxyOptions 按关键字过滤选项（匹配名称/服务器/订阅）。
export function filterProxyOptions(options, query) {
    const q = String(query || '').trim().toLowerCase()
    if (!q) return options
    return options.filter(option => {
        const haystack = [option.label, option.server, option.subscription]
            .filter(Boolean)
            .join(' ')
            .toLowerCase()
        return haystack.includes(q)
    })
}
