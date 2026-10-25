export const GMT8_OFFSET_MS = 8 * 60 * 60 * 1000

// parseDayKey turns a GMT+8 "YYYY-MM-DD" ledger key back into a Date at the
// start of that Beijing day. Centralised so every day-boundary calculation
// uses the exact same offset instead of scattering the literal around.
export function parseDayKey(key) {
    return new Date(key + 'T00:00:00+08:00')
}

export function fmtNumber(n, lang = 'zh') {
    return Math.round(Number(n || 0)).toLocaleString(lang === 'zh' ? 'zh-CN' : 'en-US')
}

// Keep one decimal for compact axis labels so 1.5K is not shown as "2K".
function trimCompact(value) {
    const rounded = Math.round(value * 10) / 10
    return Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1)
}

export function fmtCompact(n) {
    const value = Number(n || 0)
    const abs = Math.abs(value)
    if (abs >= 1e9) return trimCompact(value / 1e9) + 'B'
    if (abs >= 1e6) return trimCompact(value / 1e6) + 'M'
    if (abs >= 1e3) return trimCompact(value / 1e3) + 'K'
    return Math.round(value).toLocaleString('en-US')
}

export function fmtMoney(n, lang = 'zh') {
    const formatted = Number(n || 0).toLocaleString(lang === 'zh' ? 'zh-CN' : 'en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
    return (lang === 'zh' ? '¥' : 'CN¥ ') + formatted
}

export function normalizeUsage(usage) {
    if (!usage) return null
    const prompt = Number(usage.prompt_tokens || 0)
    const completion = Number(usage.completion_tokens || 0)
    const reasoning = Number(usage.completion_tokens_details ? usage.completion_tokens_details.reasoning_tokens : 0) || 0
    const total = Number(usage.total_tokens || 0) || (prompt + completion)
    return { prompt, completion, reasoning, total }
}

export function dayKey(ts) {
    const shifted = new Date(Number(ts) + GMT8_OFFSET_MS)
    if (Number.isNaN(shifted.getTime())) return 'unknown'
    const y = shifted.getUTCFullYear()
    const m = String(shifted.getUTCMonth() + 1).padStart(2, '0')
    const day = String(shifted.getUTCDate()).padStart(2, '0')
    return y + '-' + m + '-' + day
}

export function dayLabel(key) {
    if (!key || key === 'unknown') return key || ''
    const parts = key.split('-')
    return parts[1] + '/' + parts[2]
}

export function shortCaller(caller) {
    const value = String(caller || 'unknown')
    if (value.startsWith('caller:')) return value.slice(7, 13)
    return value.slice(0, 8)
}

export function entryToRecord(entry) {
    const date = String(entry.date || '')
    const usage = {
        prompt_tokens: Number(entry.prompt_tokens || 0),
        completion_tokens: Number(entry.completion_tokens || 0),
        total_tokens: Number(entry.total_tokens || 0),
        completion_tokens_details: { reasoning_tokens: Number(entry.reasoning_tokens || 0) },
    }
    return {
        model: entry.model || 'unknown',
        caller_id: entry.caller_id || '',
        date,
        created_at: date ? parseDayKey(date).getTime() : 0,
        calls_count: Number(entry.calls || 0),
        usage,
        cost: Number(entry.cost || 0),
    }
}

// sameUsageRecords reports whether two mapped record lists carry the same
// values. The 30s poll rebuilds the list on every tick even when the ledger
// did not change; comparing lets the hook keep the previous array reference so
// downstream memos and charts are not invalidated for nothing.
export function sameUsageRecords(a, b) {
    if (a === b) return true
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false
    for (let i = 0; i < a.length; i++) {
        const x = a[i]
        const y = b[i]
        if (x.model !== y.model || x.caller_id !== y.caller_id || x.date !== y.date ||
            x.created_at !== y.created_at || x.calls_count !== y.calls_count || x.cost !== y.cost) {
            return false
        }
        const xu = x.usage
        const yu = y.usage
        if (!xu || !yu) {
            if (xu !== yu) return false
            continue
        }
        if (xu.prompt !== yu.prompt || xu.completion !== yu.completion ||
            xu.reasoning !== yu.reasoning || xu.total !== yu.total) {
            return false
        }
    }
    return true
}

export function aggregateDaily(records) {
    const map = new Map()
    records.forEach((r) => {
        const usage = normalizeUsage(r.usage)
        if (!usage) return
        const key = dayKey(r.created_at || r.createdAt)
        const ts = parseDayKey(key).getTime()
        const entry = map.get(key) || {
            key,
            ts,
            calls: 0,
            prompt: 0,
            output: 0,
            reasoning: 0,
            total: 0,
            cost: 0,
            byModel: {},
            byModelCost: {},
            byCaller: {},
            byCallerCost: {},
        }
        entry.calls += (r.calls_count || 0)
        entry.prompt += usage.prompt
        entry.output += usage.completion
        entry.reasoning += usage.reasoning
        entry.total += usage.total
        entry.cost += Number(r.cost || 0)

        const model = r.model || 'unknown'
        const caller = String(r.caller_id || r.callerId || 'unknown')
        entry.byModel[model] = (entry.byModel[model] || 0) + usage.total
        entry.byModelCost[model] = (entry.byModelCost[model] || 0) + Number(r.cost || 0)
        entry.byCaller[caller] = (entry.byCaller[caller] || 0) + usage.total
        entry.byCallerCost[caller] = (entry.byCallerCost[caller] || 0) + Number(r.cost || 0)

        map.set(key, entry)
    })
    return Array.from(map.values()).sort((a, b) => a.key.localeCompare(b.key))
}

function emptyDayEntry(key, ts) {
    return {
        key,
        ts,
        calls: 0,
        prompt: 0,
        output: 0,
        reasoning: 0,
        total: 0,
        cost: 0,
        byModel: {},
        byModelCost: {},
        byCaller: {},
        byCallerCost: {},
    }
}

// mergeDayEntry folds a source day into a target bucket, summing scalar totals
// and every per-model / per-caller channel. Used when a long "all" range is
// bucketed into multi-day points.
function mergeDayEntry(target, source) {
    target.calls += source.calls || 0
    target.prompt += source.prompt || 0
    target.output += source.output || 0
    target.reasoning += source.reasoning || 0
    target.total += source.total || 0
    target.cost += source.cost || 0
    for (const field of ['byModel', 'byModelCost', 'byCaller', 'byCallerCost']) {
        const src = source[field] || {}
        for (const name of Object.keys(src)) {
            target[field][name] = (target[field][name] || 0) + src[name]
        }
    }
}

// MAX_ALL_POINTS caps how many points an "all" range renders. Beyond this the
// per-day series would grow unbounded with ledger age, so adjacent days are
// folded into buckets (the bucket keeps its first day as the key/label).
const MAX_ALL_POINTS = 180

export function fillDateRange(daily, dateRange) {
    const sorted = (daily || []).slice().sort((a, b) => a.key.localeCompare(b.key))
    const todayKey = dayKey(Date.now())
    const todayStart = parseDayKey(todayKey)
    let start = todayStart
    let end = todayStart

    if (dateRange === 'all') {
        if (sorted.length) {
            start = parseDayKey(sorted[0].key)
            end = parseDayKey(sorted[sorted.length - 1].key)
            if (end.getTime() < todayStart.getTime()) end = todayStart
        }
    } else {
        const days = Math.max(1, Number(dateRange) || 30)
        start = new Date(todayStart.getTime() - (days - 1) * 86400000)
        if (sorted.length) {
            const last = parseDayKey(sorted[sorted.length - 1].key)
            if (last.getTime() > end.getTime()) end = last
        }
    }

    if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime())) return sorted
    const byKey = new Map(sorted.map((d) => [d.key, d]))
    const out = []
    const last = new Date(end.getTime())

    if (dateRange === 'all') {
        const totalDays = Math.floor((last.getTime() - start.getTime()) / 86400000) + 1
        const step = Math.max(1, Math.ceil(totalDays / MAX_ALL_POINTS))
        const cursor = new Date(start.getTime())
        while (cursor <= last) {
            const key = dayKey(cursor.getTime())
            const bucket = emptyDayEntry(key, cursor.getTime())
            for (let offset = 0; offset < step; offset++) {
                const day = byKey.get(dayKey(cursor.getTime() + offset * 86400000))
                if (day) mergeDayEntry(bucket, day)
            }
            out.push(bucket)
            cursor.setUTCDate(cursor.getUTCDate() + step)
        }
        return out
    }

    const cursor = new Date(start.getTime())
    while (cursor <= last) {
        const key = dayKey(cursor.getTime())
        out.push(byKey.get(key) || emptyDayEntry(key, cursor.getTime()))
        cursor.setUTCDate(cursor.getUTCDate() + 1)
    }
    return out
}

export function applyServerSettings(prev, s) {
    // 单价表固定只有一条共享条目，直接按 canonical 键重建，避免历史缓存里的
    // 旧档位键（pro / vision / flash / v4-pro）残留成多余输入框。
    const models = {
        [MODEL_PRICE_KEY]: prev.models?.[MODEL_PRICE_KEY] || {
            inputPrice: 0,
            inputMissPrice: DEFAULT_INPUT_MISS_PRICE,
            outputPrice: 0,
        },
    }
    const server = collapseModelPrices(s?.models)[MODEL_PRICE_KEY]
    if (server) {
        const fallback = models[MODEL_PRICE_KEY]
        const rawInput = server.input_price
        const rawOutput = server.output_price
        // 未命中单价是本次新增字段：服务端为 null/undefined（旧设置文件）时
        // 必须回落到默认值 1，而不是被 Number(null)=0 覆盖成免费。
        const rawMiss = server.input_miss_price
        models[MODEL_PRICE_KEY] = {
            inputPrice: Number.isFinite(Number(rawInput)) && Number(rawInput) >= 0 ? Number(rawInput) : fallback.inputPrice,
            inputMissPrice: rawMiss == null
                ? fallback.inputMissPrice
                : (Number.isFinite(Number(rawMiss)) && Number(rawMiss) >= 0 ? Number(rawMiss) : fallback.inputMissPrice),
            outputPrice: Number.isFinite(Number(rawOutput)) && Number(rawOutput) >= 0 ? Number(rawOutput) : fallback.outputPrice,
        }
    }
    const rawMultiplier = s.peak?.multiplier ?? prev.peakMultiplier
    const peakMultiplier = Number.isFinite(Number(rawMultiplier)) && Number(rawMultiplier) >= 0 ? Number(rawMultiplier) : prev.peakMultiplier
    const rawCacheHitRate = s.cache_hit?.rate
    const cacheHitRate = Number.isFinite(Number(rawCacheHitRate)) && Number(rawCacheHitRate) >= 0
        ? clampCacheHitRate(Number(rawCacheHitRate))
        : prev.cacheHitRate
    return {
        ...prev,
        models,
        peakEnabled: s.peak?.enabled ?? prev.peakEnabled,
        peak1Start: s.peak?.start1 ?? prev.peak1Start,
        peak1End: s.peak?.end1 ?? prev.peak1End,
        peak2Start: s.peak?.start2 ?? prev.peak2Start,
        peak2End: s.peak?.end2 ?? prev.peak2End,
        peakMultiplier,
        weekendNormal: s.peak?.weekend_normal ?? prev.weekendNormal,
        cacheHitEnabled: s.cache_hit?.enabled ?? prev.cacheHitEnabled,
        cacheHitRate,
    }
}

const PRICING_STORAGE_KEY = 'ds2api_usage_pricing_v2'

// 模型合并后所有公开模型（deepseek-v4.1-flash 及其 -search / -nothinking 变体）
// 共用同一套单价，因此单价表只保留这一条；与后端 internal/usagestats 的
// mergedModelPriceKey 保持一致。
export const MODEL_PRICE_KEY = 'deepseek-v4.1-flash'

// 「模拟缓存命中」的默认值：默认开启，命中率 98%，缓存未命中的输入单价 1 元。
// 与后端 internal/usagestats 的 defaultCacheHitRate / defaultInputMissPrice 保持一致。
export const DEFAULT_CACHE_HIT_RATE = 98
export const DEFAULT_INPUT_MISS_PRICE = 1

export function clampCacheHitRate(rate) {
    const n = Number(rate)
    if (!Number.isFinite(n)) return DEFAULT_CACHE_HIT_RATE
    if (n < 0) return 0
    if (n > 100) return 100
    return n
}

// splitCacheHitTokens 按「模拟缓存命中」配置把输入 tokens 拆成命中 / 未命中
// 两部分：命中部分 = 输入 tokens × 命中率，其余算未命中。上游不返回缓存命中
// 信息，这里只是按设置里的命中率模拟，口径与后端 inputCost 的计价拆分一致。
export function splitCacheHitTokens(prompt, rate) {
    const total = Math.max(0, Math.round(Number(prompt) || 0))
    const hit = Math.round(total * clampCacheHitRate(rate) / 100)
    return { hit, miss: total - hit }
}

// peakWindowError validates the two peak windows when peak billing is on.
// Times are zero-padded "HH:MM" strings so lexicographic comparison matches
// wall-clock order; the windows are order-independent and must not overlap.
// Returns an i18n key describing the problem, or null when valid.
export function peakWindowError(pricing) {
    if (!pricing?.peakEnabled) return null
    const { peak1Start, peak1End, peak2Start, peak2End } = pricing
    if (peak1Start >= peak1End || peak2Start >= peak2End) return 'usage.settingsPeakOrder'
    if (peak1Start < peak2End && peak2Start < peak1End) return 'usage.settingsPeakOverlap'
    return null
}

// 收敛单价表时继承既有取值的顺序，与后端 modelPriceInheritOrder 保持一致：
// 合并后的基础键优先，其次是合并后一度单列的 search 键，最后才是合并前的档位键。
const MODEL_PRICE_INHERIT_ORDER = [
    MODEL_PRICE_KEY,
    'deepseek-v4.1-flash-search',
    'flash',
    'vision',
    'pro',
    'v4-pro',
]

// collapseModelPrices reduces the price table to the single shared entry so the
// settings page only ever renders one input/output pair. The first key present
// in MODEL_PRICE_INHERIT_ORDER wins; every other key (pre-merge tiers, the
// former standalone search key, hand-edited leftovers) is dropped.
function collapseModelPrices(models) {
    if (!models || typeof models !== 'object') {
        return {}
    }
    for (const key of MODEL_PRICE_INHERIT_ORDER) {
        if (Object.prototype.hasOwnProperty.call(models, key)) {
            return { [MODEL_PRICE_KEY]: models[key] }
        }
    }
    return {}
}

export function defaultPricing() {
    return {
        models: {
            [MODEL_PRICE_KEY]: { inputPrice: 0.02, inputMissPrice: DEFAULT_INPUT_MISS_PRICE, outputPrice: 4 },
        },
        peakEnabled: true,
        peak1Start: '09:00',
        peak1End: '12:00',
        peak2Start: '14:00',
        peak2End: '18:00',
        peakMultiplier: 2,
        weekendNormal: true,
        cacheHitEnabled: true,
        cacheHitRate: DEFAULT_CACHE_HIT_RATE,
    }
}

export function loadLocalPricing() {
    const defaults = defaultPricing()
    try {
        const raw = localStorage.getItem(PRICING_STORAGE_KEY)
        if (!raw) return defaults
        const parsed = JSON.parse(raw)
        const stored = collapseModelPrices(parsed.models)[MODEL_PRICE_KEY]
        const fallback = defaults.models[MODEL_PRICE_KEY]
        const rawInput = stored?.inputPrice ?? stored?.input_price
        const rawOutput = stored?.outputPrice ?? stored?.output_price
        // 本地缓存里的旧记录没有未命中单价，必须回落到默认值而不是当成 0。
        const rawMiss = stored?.inputMissPrice ?? stored?.input_miss_price
        const models = {
            [MODEL_PRICE_KEY]: {
                inputPrice: Number.isFinite(Number(rawInput)) && Number(rawInput) >= 0 ? Number(rawInput) : fallback.inputPrice,
                inputMissPrice: rawMiss == null
                    ? fallback.inputMissPrice
                    : (Number.isFinite(Number(rawMiss)) && Number(rawMiss) >= 0 ? Number(rawMiss) : fallback.inputMissPrice),
                outputPrice: Number.isFinite(Number(rawOutput)) && Number(rawOutput) >= 0 ? Number(rawOutput) : fallback.outputPrice,
            },
        }
        return {
            ...defaults,
            ...parsed,
            models,
            peakEnabled: typeof parsed.peakEnabled === 'boolean' ? parsed.peakEnabled : defaults.peakEnabled,
            weekendNormal: typeof parsed.weekendNormal === 'boolean' ? parsed.weekendNormal : defaults.weekendNormal,
            cacheHitEnabled: typeof parsed.cacheHitEnabled === 'boolean' ? parsed.cacheHitEnabled : defaults.cacheHitEnabled,
            cacheHitRate: Number.isFinite(Number(parsed.cacheHitRate)) && Number(parsed.cacheHitRate) >= 0
                ? clampCacheHitRate(Number(parsed.cacheHitRate))
                : defaults.cacheHitRate,
            peakMultiplier: Number.isFinite(Number(parsed.peakMultiplier)) && Number(parsed.peakMultiplier) >= 0
                ? Number(parsed.peakMultiplier)
                : defaults.peakMultiplier,
            peak1Start: parsed.peak1Start ?? defaults.peak1Start,
            peak1End: parsed.peak1End ?? defaults.peak1End,
            peak2Start: parsed.peak2Start ?? defaults.peak2Start,
            peak2End: parsed.peak2End ?? defaults.peak2End,
        }
    } catch (e) {
        return defaults
    }
}

export function persistLocalPricing(pricing) {
    try {
        localStorage.setItem(PRICING_STORAGE_KEY, JSON.stringify(pricing))
    } catch (e) {
        // ignore invalid localStorage values
    }
}

export function csvEscape(value) {
    const s = value == null ? '' : String(value)
    if (/[",\r\n]/.test(s)) return '"' + s.replace(/"/g, '""') + '"'
    return s
}

// buildUsageCsv renders aggregated usage entries as CSV. Dates are the GMT+8
// day keys straight from the ledger (not re-encoded through toISOString,
// which used to shift every row to the previous UTC day).
export function buildUsageCsv(records) {
    const header = ['model', 'caller_id', 'date', 'prompt_tokens', 'completion_tokens', 'reasoning_tokens', 'total_tokens', 'calls', 'cost']
    const rows = []
    for (const r of records) {
        const u = normalizeUsage(r.usage)
        if (!u) continue
        rows.push([
            r.model || 'unknown',
            r.caller_id || '',
            r.date || '',
            u.prompt,
            u.completion,
            u.reasoning,
            u.total,
            r.calls_count || 0,
            Number(r.cost || 0).toFixed(4),
        ].map(csvEscape).join(','))
    }
    const body = rows.length ? rows.join('\n') + '\n' : ''
    return { csv: '\uFEFF' + header.join(',') + '\n' + body, count: rows.length }
}

// parseCsvRows 是一个最小但符合 RFC 4180 的 CSV 读取器：支持引号包裹、字段内
// 的逗号/换行与 "" 转义，并吃掉导出的 UTF-8 BOM。只服务于导入「导出的 CSV」，
// 不追求支持所有方言。
function parseCsvRows(text) {
    const rows = []
    let row = []
    let field = ''
    let inQuotes = false
    const src = String(text || '').replace(/^\uFEFF/, '')
    for (let i = 0; i < src.length; i++) {
        const ch = src[i]
        if (inQuotes) {
            if (ch === '"') {
                if (src[i + 1] === '"') {
                    field += '"'
                    i++
                } else {
                    inQuotes = false
                }
            } else {
                field += ch
            }
        } else if (ch === '"') {
            inQuotes = true
        } else if (ch === ',') {
            row.push(field)
            field = ''
        } else if (ch === '\n') {
            row.push(field)
            field = ''
            rows.push(row)
            row = []
        } else if (ch !== '\r') {
            field += ch
        }
    }
    if (field !== '' || row.length) {
        row.push(field)
        rows.push(row)
    }
    return rows
}

function toIntCell(value) {
    const n = Number(String(value == null ? '' : value).replace(/,/g, '').trim())
    return Number.isFinite(n) ? Math.round(n) : 0
}

function toFloatCell(value) {
    const n = Number(String(value == null ? '' : value).replace(/,/g, '').trim())
    return Number.isFinite(n) ? n : 0
}

// parseUsageCsv 解析「导出」按钮产出的 CSV，得到可直接提交给
// /admin/usage/import 的条目数组。无法识别的表头/空文件返回 i18n key 形式的
// error；个别坏行（日期或模型缺失）会被跳过并通过 skipped 反馈。
export function parseUsageCsv(text) {
    const rows = parseCsvRows(text).filter((r) => r.some((c) => String(c).trim() !== ''))
    if (rows.length === 0) {
        return { entries: [], skipped: 0, error: 'usage.importEmpty' }
    }
    const header = rows[0].map((h) => String(h).trim().toLowerCase())
    const col = (name) => header.indexOf(name)
    const columns = {
        model: col('model'),
        caller_id: col('caller_id'),
        date: col('date'),
        prompt_tokens: col('prompt_tokens'),
        completion_tokens: col('completion_tokens'),
        reasoning_tokens: col('reasoning_tokens'),
        total_tokens: col('total_tokens'),
        calls: col('calls'),
        cost: col('cost'),
    }
    if (columns.model === -1 || columns.date === -1 || columns.prompt_tokens === -1 || columns.completion_tokens === -1) {
        return { entries: [], skipped: 0, error: 'usage.importInvalidHeader' }
    }
    const entries = []
    let skipped = 0
    for (const row of rows.slice(1)) {
        const cell = (index) => (index >= 0 && index < row.length ? String(row[index]).trim() : '')
        const date = cell(columns.date)
        const model = cell(columns.model)
        if (!/^\d{4}-\d{2}-\d{2}$/.test(date) || !model) {
            skipped++
            continue
        }
        const prompt = toIntCell(cell(columns.prompt_tokens))
        const completion = toIntCell(cell(columns.completion_tokens))
        const reasoning = toIntCell(cell(columns.reasoning_tokens))
        const totalRaw = toIntCell(cell(columns.total_tokens))
        entries.push({
            date,
            model,
            caller_id: cell(columns.caller_id),
            prompt_tokens: prompt,
            completion_tokens: completion,
            reasoning_tokens: reasoning,
            total_tokens: totalRaw > 0 ? totalRaw : prompt + completion,
            calls: toIntCell(cell(columns.calls)),
            cost: toFloatCell(cell(columns.cost)),
        })
    }
    if (entries.length === 0) {
        return { entries: [], skipped, error: 'usage.importNoRows' }
    }
    return { entries, skipped, error: null }
}
