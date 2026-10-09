'use strict';

const {
  toStringSafe,
} = require('./state');
const {
  parseMarkupToolCalls,
  stripFencedCodeBlocks,
  containsToolCallWrapperSyntaxOutsideIgnored,
  normalizeEPSEToolCallMarkup,
  hasRepairableXMLToolCallsWrapper,
  indexToolCDATAOpen,
  sanitizeLooseCDATA,
  repairSwallowedCDATARanges,
} = require('./parse_payload');

function extractToolNames(tools) {
  if (!Array.isArray(tools) || tools.length === 0) {
    return [];
  }
  const out = [];
  const seen = new Set();
  for (const t of tools) {
    if (!t || typeof t !== 'object') {
      continue;
    }
    const fn = t.function && typeof t.function === 'object' ? t.function : t;
    const name = toStringSafe(fn.name);
    if (!name || seen.has(name)) {
      continue;
    }
    seen.add(name);
    out.push(name);
  }
  return out;
}

function parseToolCalls(text, toolNames) {
  return parseToolCallsDetailed(text, toolNames).calls;
}

function parseToolCallsDetailed(text, toolNames) {
  const result = emptyParseResult();
  const raw = toStringSafe(text);
  if (!raw) {
    return result;
  }
  if (shouldSkipToolCallParsingForCodeFenceExample(raw)) {
    return result;
  }
  const original = stripFencedCodeBlocks(raw).trim();
  const normalized = normalizeEPSEToolCallMarkup(original);
  if (!normalized.ok || !normalized.text) {
    return result;
  }
  result.sawToolCallSyntax = looksLikeToolCallSyntax(normalized.text) || hasRepairableXMLToolCallsWrapper(normalized.text);
  // XML markup parsing only.
  let parsed = parseMarkupToolCalls(normalized.text);
  if (parsed.length === 0 && indexToolCDATAOpen(normalized.text, 0) >= 0) {
    const recovered = sanitizeLooseCDATA(normalized.text);
    if (recovered !== normalized.text) {
      parsed = parseMarkupToolCalls(recovered);
    }
  }
  parsed = reparseWithCDATAElementBoundaries(parsed, original);
  if (parsed.length === 0) {
    return result;
  }
  result.sawToolCallSyntax = true;
  const filtered = filterToolCallsDetailed(parsed, toolNames);
  result.calls = filtered.calls;
  result.rejectedToolNames = filtered.rejectedToolNames;
  result.rejectedByPolicy = filtered.rejectedToolNames.length > 0 && filtered.calls.length === 0;
  return result;
}

function parseStandaloneToolCalls(text, toolNames) {
  return parseStandaloneToolCallsDetailed(text, toolNames).calls;
}

function parseStandaloneToolCallsDetailed(text, toolNames) {
  const result = emptyParseResult();
  const raw = toStringSafe(text);
  if (!raw) {
    return result;
  }
  if (shouldSkipToolCallParsingForCodeFenceExample(raw)) {
    return result;
  }
  const original = stripFencedCodeBlocks(raw).trim();
  const normalized = normalizeEPSEToolCallMarkup(original);
  if (!normalized.ok || !normalized.text) {
    return result;
  }
  result.sawToolCallSyntax = looksLikeToolCallSyntax(normalized.text) || hasRepairableXMLToolCallsWrapper(normalized.text);
  // XML markup parsing only.
  let parsed = parseMarkupToolCalls(normalized.text);
  if (parsed.length === 0 && indexToolCDATAOpen(normalized.text, 0) >= 0) {
    const recovered = sanitizeLooseCDATA(normalized.text);
    if (recovered !== normalized.text) {
      parsed = parseMarkupToolCalls(recovered);
    }
  }
  parsed = reparseWithCDATAElementBoundaries(parsed, original);
  if (parsed.length === 0) {
    return result;
  }

  result.sawToolCallSyntax = true;
  const filtered = filterToolCallsDetailed(parsed, toolNames);
  result.calls = filtered.calls;
  result.rejectedToolNames = filtered.rejectedToolNames;
  result.rejectedByPolicy = filtered.rejectedToolNames.length > 0 && filtered.calls.length === 0;
  return result;
}

// reparseWithCDATAElementBoundaries retries parsing with swallowed CDATA ranges
// closed at their element boundary (Go parity:
// internal/toolcall/toolcalls_cdata_boundary_repair.go). The greedy result is
// kept unless the boundary interpretation parses strictly better, so every input
// greedy handles correctly is left alone.
//
// The repair runs on the pre-normalization original: the tail a greedy range
// swallowed was inside a CDATA body when normalizeEPSEToolCallMarkup ran, so its
// EPSE tags survived as parameter content. Releasing that tail is only useful if
// it is normalized afterwards, which means repairing first and normalizing
// second.
function reparseWithCDATAElementBoundaries(parsed, original) {
  const repaired = repairSwallowedCDATARanges(original);
  if (repaired === original) {
    return parsed;
  }
  const candidate = parseMarkupToolCalls(repaired);
  return boundaryRepairOutranks(candidate, parsed) ? candidate : parsed;
}

// boundaryRepairOutranks applies the same arbitration as the LLM repair path:
// more tool calls wins, then more populated parameters. Ties keep greedy.
function boundaryRepairOutranks(candidate, current) {
  if (candidate.length === 0) {
    return false;
  }
  if (candidate.length !== current.length) {
    return candidate.length > current.length;
  }
  return countToolCallInputs(candidate) > countToolCallInputs(current);
}

function countToolCallInputs(calls) {
  let total = 0;
  for (const call of calls) {
    if (call && call.input && typeof call.input === 'object') {
      total += Object.keys(call.input).length;
    }
  }
  return total;
}

function emptyParseResult() {
  return {
    calls: [],
    sawToolCallSyntax: false,
    rejectedByPolicy: false,
    rejectedToolNames: [],
  };
}

function filterToolCallsDetailed(parsed, toolNames) {
  const calls = [];
  for (const tc of parsed) {
    if (!tc || !tc.name) {
      continue;
    }
    const input = tc.input && typeof tc.input === 'object' && !Array.isArray(tc.input) ? tc.input : {};
    calls.push({
      name: tc.name,
      input,
    });
  }
  return { calls, rejectedToolNames: [] };
}

function looksLikeToolCallSyntax(text) {
  const styles = containsToolCallWrapperSyntaxOutsideIgnored(text);
  return styles.epse || styles.canonical;
}

function shouldSkipToolCallParsingForCodeFenceExample(text) {
  if (!looksLikeToolCallSyntax(text)) {
    return false;
  }
  const stripped = stripFencedCodeBlocks(text);
  return !looksLikeToolCallSyntax(stripped);
}

module.exports = {
  extractToolNames,
  parseToolCalls,
  parseToolCallsDetailed,
  parseStandaloneToolCalls,
  parseStandaloneToolCallsDetailed,
};
