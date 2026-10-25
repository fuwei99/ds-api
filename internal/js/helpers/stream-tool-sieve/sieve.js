'use strict';
const {
  resetIncrementalToolState,
  noteText,
  insideCodeFenceWithState,
} = require('./state');
const { trimWrappingJSONFence } = require('./jsonscan');
const {
  findToolMarkupTagOutsideIgnored,
  findDSMLTagOutsideIgnored,
  findMatchingDSMLClose,
  sanitizeLooseCDATA,
} = require('./parse_payload');
const {
  consumeXMLToolCapture: consumeXMLToolCaptureImpl,
  hasOpenXMLToolTag,
  shouldKeepBareInvokeCapture,
  findPartialXMLToolTagStart,
} = require('./sieve-xml');
function processToolSieveChunk(state, chunk, toolNames) {
  if (!state) {
    return [];
  }
  if (chunk) {
    state.pending += chunk;
  }
  const events = [];
  while (true) {
    if (Array.isArray(state.pendingToolCalls) && state.pendingToolCalls.length > 0) {
      events.push({ type: 'tool_calls', calls: state.pendingToolCalls });
      state.pendingToolRaw = '';
      state.pendingToolCalls = [];
      continue;
    }
    if (state.capturing) {
      if (state.pending) {
        state.capture += state.pending;
        state.pending = '';
      }
      const consumed = consumeToolCapture(state, toolNames, false);
      if (!consumed.ready) {
        break;
      }
      state.capture = '';
      state.capturing = false;
      resetIncrementalToolState(state);

      if (Array.isArray(consumed.calls) && consumed.calls.length > 0) {
        if (consumed.prefix) {
          noteText(state, consumed.prefix);
          events.push({ type: 'text', text: consumed.prefix });
        }
        // Emit the completed tool call immediately so the runtime can mark the
        // turn as tool-call emitting before the capture suffix (tail prose after
        // the tool block) is released as content. Deferring to the next
        // processToolSieveChunk call would let same-chunk tail text slip through
        // as visible content in the same stream pass.
        events.push({ type: 'tool_calls', calls: consumed.calls });
        state.pendingToolRaw = '';
        if (consumed.suffix) {
          state.pending = consumed.suffix + state.pending;
        }
        continue;
      }
      if (consumed.prefix) {
        noteText(state, consumed.prefix);
        events.push({ type: 'text', text: consumed.prefix });
      }
      if (consumed.suffix) {
        state.pending += consumed.suffix;
      }
      continue;
    }
    const pending = state.pending || '';
    if (!pending) {
      break;
    }
    const start = findToolSegmentStart(state, pending);
    if (start === HOLD_TOOL_SEGMENT_START) {
      break;
    }
    if (start >= 0) {
      const prefix = pending.slice(0, start);
      if (prefix) {
        const resetMarkdownSpan = shouldResetUnclosedMarkdownPrefix(state, prefix, pending.slice(start));
        noteText(state, prefix);
        if (resetMarkdownSpan) {
          state.markdownCodeSpanTicks = 0;
        }
        events.push({ type: 'text', text: prefix });
      }
      state.pending = '';
      state.capture += pending.slice(start);
      state.capturing = true;
      resetIncrementalToolState(state);
      continue;
    }
    const [safe, hold] = splitSafeContentForToolDetection(state, pending);
    if (!safe) {
      break;
    }
    state.pending = hold;
    noteText(state, safe);
    events.push({ type: 'text', text: safe });
  }
  return events;
}

function flushToolSieve(state, toolNames) {
  if (!state) {
    return [];
  }
  const events = processToolSieveChunk(state, '', toolNames);
  if (state.pending && Number.isInteger(state.markdownCodeSpanTicks) && state.markdownCodeSpanTicks > 0) {
    state.markdownCodeSpanTicks = 0;
    events.push(...processToolSieveChunk(state, '', toolNames));
  }
  if (Array.isArray(state.pendingToolCalls) && state.pendingToolCalls.length > 0) {
    events.push({ type: 'tool_calls', calls: state.pendingToolCalls });
    state.pendingToolRaw = '';
    state.pendingToolCalls = [];
  }
  if (state.capturing) {
    const consumed = consumeToolCapture(state, toolNames, true);
    if (consumed.ready) {
      if (consumed.prefix) {
        noteText(state, consumed.prefix);
        events.push({ type: 'text', text: consumed.prefix });
      }
      if (Array.isArray(consumed.calls) && consumed.calls.length > 0) {
        events.push({ type: 'tool_calls', calls: consumed.calls });
      }
      if (consumed.suffix) {
        noteText(state, consumed.suffix);
        events.push({ type: 'text', text: consumed.suffix });
      }
    } else if (state.capture) {
      const content = state.capture;
      const recovered = sanitizeLooseCDATA(content);
      if (recovered !== content) {
        const recoveredResult = consumeXMLToolCaptureImpl(recovered, toolNames, trimWrappingJSONFence);
        if (recoveredResult.ready && Array.isArray(recoveredResult.calls) && recoveredResult.calls.length > 0) {
          if (recoveredResult.prefix) {
            noteText(state, recoveredResult.prefix);
            events.push({ type: 'text', text: recoveredResult.prefix });
          }
          events.push({ type: 'tool_calls', calls: recoveredResult.calls });
          if (recoveredResult.suffix) {
            noteText(state, recoveredResult.suffix);
            events.push({ type: 'text', text: recoveredResult.suffix });
          }
        } else {
          noteText(state, content);
          events.push({ type: 'text', text: content });
        }
      } else {
        noteText(state, content);
        events.push({ type: 'text', text: content });
      }
    }
    state.capture = '';
    state.capturing = false;
    resetIncrementalToolState(state);
  }
  if (state.pending) {
    noteText(state, state.pending);
    events.push({ type: 'text', text: state.pending });
    state.pending = '';
  }
  return events;
}

function splitSafeContentForToolDetection(state, s) {
  const text = s || '';
  if (!text) {
    return ['', ''];
  }
  // Only hold back partial XML tool tags.
  const xmlIdx = findPartialXMLToolTagStart(text);
  if (xmlIdx >= 0) {
    if (insideCodeFenceWithState(state, text.slice(0, xmlIdx))) {
      return [text, ''];
    }
    const markdown = markdownCodeSpanStateAt(state, text.slice(0, xmlIdx));
    if (markdown.ticks > 0) {
      if (markdownCodeSpanCloses(text.slice(xmlIdx), markdown.ticks)) {
        return [text, ''];
      }
      if (markdown.fromPrior) {
        return ['', text];
      }
    }
    if (xmlIdx > 0) {
      return [text.slice(0, xmlIdx), text.slice(xmlIdx)];
    }
    return ['', text];
  }
  return [text, ''];
}

const HOLD_TOOL_SEGMENT_START = -2;

function findToolSegmentStart(state, s) {
  if (!s) {
    return -1;
  }
  let offset = 0;
  while (true) {
    const hit = findNextToolInterceptTag(s, offset);
    if (!hit) {
      return -1;
    }
    if (insideCodeFenceWithState(state, s.slice(0, hit.start))) {
      offset = hit.end + 1;
      continue;
    }
    const markdown = markdownCodeSpanStateAt(state, s.slice(0, hit.start));
    if (markdown.ticks === 0) {
      return hit.start;
    }
    if (markdownCodeSpanCloses(s.slice(hit.start), markdown.ticks)) {
      offset = hit.end + 1;
      continue;
    }
    if (markdown.fromPrior) {
      return HOLD_TOOL_SEGMENT_START;
    }
    return hit.start;
  }
}

// findNextToolInterceptTag returns the earliest streaming interception entry at
// or after offset: a recognized EPSE / canonical tool markup tag, or a
// DSML-prefixed tag. DSML output never parses into a tool call, but its wrapper
// still has to be captured so the raw alien markup is not leaked piecemeal.
function findNextToolInterceptTag(s, offset) {
  const tag = findToolMarkupTagOutsideIgnored(s, offset);
  const dsml = findDSMLTagOutsideIgnored(s, offset);
  if (tag && dsml) {
    return tag.start <= dsml.start
      ? { start: tag.start, end: tag.end }
      : { start: dsml.start, end: dsml.end };
  }
  if (tag) {
    return { start: tag.start, end: tag.end };
  }
  if (dsml) {
    return { start: dsml.start, end: dsml.end };
  }
  return null;
}

function markdownCodeSpanStateAt(state, text) {
  const raw = typeof text === 'string' ? text : '';
  let ticks = state && Number.isInteger(state.markdownCodeSpanTicks) ? state.markdownCodeSpanTicks : 0;
  let fromPrior = ticks > 0;
  for (let i = 0; i < raw.length;) {
    if (raw[i] !== '`') {
      i += 1;
      continue;
    }
    const run = countBacktickRun(raw, i);
    if (ticks === 0) {
      if (run >= 3 && atMarkdownFenceLineStart(raw, i)) {
        i += run;
        continue;
      }
      if (state && insideCodeFenceWithState(state, raw.slice(0, i))) {
        i += run;
        continue;
      }
      ticks = run;
      fromPrior = false;
    } else if (run === ticks) {
      ticks = 0;
      fromPrior = false;
    }
    i += run;
  }
  return { ticks, fromPrior };
}

function markdownCodeSpanCloses(text, ticks) {
  const raw = typeof text === 'string' ? text : '';
  if (!Number.isInteger(ticks) || ticks <= 0) {
    return false;
  }
  for (let i = 0; i < raw.length;) {
    if (raw[i] !== '`') {
      i += 1;
      continue;
    }
    const run = countBacktickRun(raw, i);
    if (run === ticks) {
      return true;
    }
    i += run;
  }
  return false;
}

function shouldResetUnclosedMarkdownPrefix(state, prefix, suffix) {
  const markdown = markdownCodeSpanStateAt(state, prefix);
  return markdown.ticks > 0 && !markdown.fromPrior && !markdownCodeSpanCloses(suffix, markdown.ticks);
}

function countBacktickRun(text, start) {
  let count = 0;
  while (start + count < text.length && text[start + count] === '`') {
    count += 1;
  }
  return count;
}

function atMarkdownFenceLineStart(text, idx) {
  for (let i = idx - 1; i >= 0; i -= 1) {
    const ch = text[i];
    if (ch === ' ' || ch === '\t') {
      continue;
    }
    return ch === '\n' || ch === '\r';
  }
  return true;
}

function consumeToolCapture(state, toolNames, final) {
  const captured = state.capture || '';
  if (!captured) {
    return { ready: false, prefix: '', calls: [], suffix: '' };
  }

  // XML-only tool call extraction.
  const xmlResult = consumeXMLToolCaptureImpl(captured, toolNames, trimWrappingJSONFence);
  if (xmlResult.ready) {
    return xmlResult;
  }
  // If XML tags are present but block is incomplete, keep buffering.
  if (hasOpenXMLToolTag(captured)) {
    return { ready: false, prefix: '', calls: [], suffix: '' };
  }
  // DSML never parses into a tool call: hide it from visible content while
  // keeping the raw text available for the finalize repair path.
  const dsml = consumeDSMLHiddenCapture(captured, final);
  if (dsml.handled) {
    if (dsml.hold) {
      return { ready: false, prefix: '', calls: [], suffix: '' };
    }
    return { ready: true, prefix: dsml.prefix, calls: [], suffix: dsml.suffix };
  }
  if (shouldKeepBareInvokeCapture(captured)) {
    return { ready: false, prefix: '', calls: [], suffix: '' };
  }

  // No XML tool tags detected — release captured content as text.
  return {
    ready: true,
    prefix: captured,
    calls: [],
    suffix: '',
  };
}

// consumeDSMLHiddenCapture hides a DSML block from visible output: prefix is
// the text before the first DSML tag; suffix is the text after the matched
// closing tag. handled=true means a DSML tag was found; hold=true means the
// outer wrapper has not closed yet and the caller should keep buffering. When
// final is true an unclosed tail is discarded as well. Mirrors the Go side
// (internal/toolstream/tool_sieve_core.go) consumeDSMLHiddenCapture.
function consumeDSMLHiddenCapture(captured, final) {
  const open = findDSMLTagOutsideIgnored(captured, 0);
  if (!open) {
    return { prefix: '', suffix: '', handled: false, hold: false };
  }
  const prefix = captured.slice(0, open.start);
  const closeTag = findMatchingDSMLClose(captured, open);
  if (!closeTag) {
    if (final) {
      return { prefix, suffix: '', handled: true, hold: false };
    }
    return { prefix: '', suffix: '', handled: true, hold: true };
  }
  return { prefix, suffix: captured.slice(closeTag.end + 1), handled: true, hold: false };
}

module.exports = {
  processToolSieveChunk,
  flushToolSieve,
};
