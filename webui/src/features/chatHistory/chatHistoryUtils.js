export const LIMIT_OPTIONS = [0, 10, 20, 50]
export const DISABLED_LIMIT = 0
export const MESSAGE_COLLAPSE_AT = 700
export const VIEW_MODE_KEY = 'ds2api_chat_history_view_mode'

const SYSTEM_MARKER = 'System'
const USER_MARKER = 'User'
const ASSISTANT_MARKER = 'Assistant'
const TOOL_MARKER = 'Tool'
const CURRENT_INPUT_FILE_PROMPT = '继续会话'
// The tools variant appends a fixed suffix; both shapes are matched exactly so a
// real user message that merely starts with 继续会话 is never mistaken for it.
const CURRENT_INPUT_FILE_PROMPT_WITH_TOOLS = `${CURRENT_INPUT_FILE_PROMPT} 使用工具时请参照说明与格式要求，仅使用所列出的工具`
const LEGACY_CURRENT_INPUT_FILE_PROMPTS = new Set([
    'The current request and prior conversation context have already been provided. Answer the latest user request directly.',
    'Continue from the latest state in the attached HISTORY.txt context. Treat it as the current working state and answer the latest user request directly.Do not mention HISTORY.txt in the main text.',
])
const HISTORY_TRANSCRIPT_TITLE = '# HISTORY.txt'
const HISTORY_TRANSCRIPT_ENTRY_RE = /^===\s+\d+\.\s+([A-Z][A-Z_ -]*)\s+===\s*$/gm

function isCurrentInputFilePrompt(value) {
    const text = String(value || '').trim()
    return text === CURRENT_INPUT_FILE_PROMPT
        || text === CURRENT_INPUT_FILE_PROMPT_WITH_TOOLS
        || LEGACY_CURRENT_INPUT_FILE_PROMPTS.has(text)
}

function normalizeHistoryRole(role) {
    const normalized = String(role || '').trim().toLowerCase()
    if (normalized === 'function') return 'tool'
    if (normalized === 'developer') return 'system'
    if (normalized === 'system' || normalized === 'user' || normalized === 'assistant' || normalized === 'tool') {
        return normalized
    }
    return normalized || 'system'
}

export function formatDateTime(value, lang) {
    if (!value) return '-'
    try {
        return new Intl.DateTimeFormat(lang === 'zh' ? 'zh-CN' : 'en-US', {
            year: 'numeric',
            month: '2-digit',
            day: '2-digit',
            hour: '2-digit',
            minute: '2-digit',
            second: '2-digit',
        }).format(new Date(value))
    } catch {
        return '-'
    }
}

export function formatElapsed(ms, t) {
    if (!ms) return t('chatHistory.metaUnknown')
    if (ms < 1000) return `${ms}ms`
    return `${(ms / 1000).toFixed(ms < 10_000 ? 2 : 1)}s`
}

export function previewText(item) {
    return item?.preview || item?.content || item?.reasoning_content || item?.error || item?.user_input || ''
}

export function statusTone(status) {
    switch (status) {
        case 'success':
            return 'border-emerald-500/20 bg-emerald-500/10 text-emerald-600'
        case 'error':
            return 'border-destructive/20 bg-destructive/10 text-destructive'
        case 'stopped':
            return 'border-amber-500/20 bg-amber-500/10 text-amber-600'
        default:
            return 'border-border bg-secondary/60 text-muted-foreground'
    }
}

export function downloadTextFile(filename, text) {
    const blob = new Blob([text], { type: 'text/plain;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = filename
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
    URL.revokeObjectURL(url)
}

function fallbackCopyText(text) {
    const textArea = document.createElement('textarea')
    textArea.value = text
    textArea.setAttribute('readonly', '')
    textArea.style.position = 'fixed'
    textArea.style.top = '-9999px'
    textArea.style.left = '-9999px'

    document.body.appendChild(textArea)
    textArea.focus()
    textArea.select()

    let copied = false
    try {
        copied = document.execCommand('copy')
    } finally {
        document.body.removeChild(textArea)
    }

    if (!copied) {
        throw new Error('copy failed')
    }
}

export async function copyTextWithFallback(text) {
    try {
        if (navigator.clipboard?.writeText) {
            await navigator.clipboard.writeText(text)
            return
        }
    } catch {
        // Fall through to execCommand fallback.
    }
    fallbackCopyText(text)
}

const ROLE_MARKERS = [SYSTEM_MARKER, USER_MARKER, ASSISTANT_MARKER, TOOL_MARKER]

function findNextMarker(text, start) {
    let earliest = -1
    let markerLen = 0
    for (const marker of ROLE_MARKERS) {
        const idx = text.indexOf(marker, start)
        if (idx >= 0 && (earliest < 0 || idx < earliest)) {
            earliest = idx
            markerLen = marker.length
        }
    }
    return earliest >= 0 ? { index: earliest, length: markerLen } : null
}

export function parseStrictHistoryMessages(historyText) {
    const rawText = String(historyText || '')
    if (!rawText) return null

    const parsed = []
    let cursor = 0
    let trailingAssistantPromptOnly = false

    while (cursor < rawText.length) {
        const next = findNextMarker(rawText, cursor)
        if (!next) break

        const marker = rawText.slice(next.index, next.index + next.length)
        const contentStart = next.index + next.length

        if (marker === SYSTEM_MARKER) {
            const after = findNextMarker(rawText, contentStart)
            const end = after ? after.index : rawText.length
            const content = rawText.slice(contentStart, end)
            if (!content.trim()) return null
            parsed.push({ role: 'system', content })
            cursor = end
            continue
        }

        if (marker === USER_MARKER) {
            const after = findNextMarker(rawText, contentStart)
            const end = after ? after.index : rawText.length
            const content = rawText.slice(contentStart, end)
            if (!content.trim()) return null
            parsed.push({ role: 'user', content })
            cursor = end
            continue
        }

        if (marker === ASSISTANT_MARKER) {
            const after = findNextMarker(rawText, contentStart)
            const end = after ? after.index : rawText.length
            const content = rawText.slice(contentStart, end)
            if (!content.trim()) {
                trailingAssistantPromptOnly = true
                break
            }
            parsed.push({ role: 'assistant', content })
            cursor = end
            continue
        }

        if (marker === TOOL_MARKER) {
            const after = findNextMarker(rawText, contentStart)
            const end = after ? after.index : rawText.length
            const content = rawText.slice(contentStart, end)
            if (!content.trim()) return null
            parsed.push({ role: 'tool', content })
            cursor = end
            continue
        }

        break
    }

    if (!parsed.length) return null
    if (!trailingAssistantPromptOnly && parsed[parsed.length - 1]?.role !== 'assistant') return null
    return parsed
}

export function parseTranscriptHistoryMessages(historyText) {
    const rawText = String(historyText || '')
    const titleIndex = rawText.indexOf(HISTORY_TRANSCRIPT_TITLE)
    const transcript = titleIndex >= 0 ? rawText.slice(titleIndex) : rawText
    const matches = [...transcript.matchAll(HISTORY_TRANSCRIPT_ENTRY_RE)]
    if (!matches.length) return null

    const parsed = []
    for (let i = 0; i < matches.length; i += 1) {
        const match = matches[i]
        const next = matches[i + 1]
        const role = normalizeHistoryRole(match[1])
        const start = (match.index || 0) + match[0].length
        const end = next ? next.index : transcript.length
        const content = transcript.slice(start, end).replace(/^\r?\n/, '').trim()
        if (!content) continue
        parsed.push({ role, content })
    }

    return parsed.length ? parsed : null
}

export function parseHistoryMessages(historyText) {
    return parseStrictHistoryMessages(historyText) || parseTranscriptHistoryMessages(historyText)
}

export function buildListModeMessages(item, t) {
    const liveMessages = Array.isArray(item?.messages) && item.messages.length > 0
        ? item.messages
        : [{ role: 'user', content: item?.user_input || t('chatHistory.emptyUserInput') }]
    const historyMessages = parseHistoryMessages(item?.history_text)

    if (!historyMessages?.length) {
        return { messages: liveMessages, historyMerged: false }
    }

    // The continuation placeholder carries no user-visible content, so it is
    // dropped once the uploaded history is merged back in. It is injected as a
    // system turn; older archives recorded it as a user turn, so both roles are
    // accepted here. When the newest user turn stayed inline in the live prompt,
    // it follows the placeholder and must be kept after the merged history.
    const withoutPlaceholder = liveMessages.filter(message => !(
        ['system', 'developer', 'user'].includes(String(message?.role || '').trim().toLowerCase())
        && isCurrentInputFilePrompt(message?.content)
    ))

    if (!withoutPlaceholder.length) {
        return { messages: historyMessages, historyMerged: true }
    }

    const insertAt = withoutPlaceholder.findIndex(message => {
        const role = String(message?.role || '').trim().toLowerCase()
        return role !== 'system' && role !== 'developer'
    })
    const mergedMessages = [...withoutPlaceholder]
    mergedMessages.splice(insertAt < 0 ? mergedMessages.length : insertAt, 0, ...historyMessages)

    return { messages: mergedMessages, historyMerged: true }
}
