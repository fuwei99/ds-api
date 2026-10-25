'use strict';

// Guard against the hallucinated `[Tool]:` role marker: the model occasionally
// finishes a tool call, prints the prompt's tool-result role marker itself,
// invents a tool result, and then keeps answering its own invented dialogue.
// `[Tool]:` is prompt structure and never reply text, so the stream is cut
// there — the text produced before the marker is delivered normally, the marker
// and everything after it is dropped.
//
// Mirrors internal/sse/tool_role_marker_guard.go; keep the marker in sync.

const TOOL_ROLE_MARKER = '[Tool]:';

// Longest proper prefix of the marker that is withheld while waiting for the
// rest of a marker split across SSE fragments.
const TOOL_ROLE_MARKER_HOLDBACK = TOOL_ROLE_MARKER.length - 1;

function createToolRoleMarkerGuard() {
  return { pending: '', tripped: false };
}

// indexToolRoleMarker returns the offset of the first case-insensitive
// occurrence of the role marker in text, or -1.
function indexToolRoleMarker(text) {
  if (typeof text !== 'string' || text.length < TOOL_ROLE_MARKER.length) {
    return -1;
  }
  return text.toLowerCase().indexOf(TOOL_ROLE_MARKER.toLowerCase());
}

// trailingToolRoleMarkerPrefixLen returns the length of the longest suffix of
// text that is a proper prefix of the role marker (case-insensitive), so a
// marker still arriving one fragment at a time can be withheld.
function trailingToolRoleMarkerPrefixLen(text) {
  const limit = Math.min(TOOL_ROLE_MARKER_HOLDBACK, text.length);
  for (let l = limit; l > 0; l -= 1) {
    if (text.slice(text.length - l).toLowerCase() === TOOL_ROLE_MARKER.slice(0, l).toLowerCase()) {
      return l;
    }
  }
  return 0;
}

// feedToolRoleMarkerGuard consumes the next raw text fragment and returns the
// prefix that is safe to forward downstream. A trailing fragment that could
// still grow into the marker is withheld until a later feed; once tripped the
// guard returns '' forever, so the caller ends the upstream read and finishes
// with the text it already forwarded.
//
// The withheld fragment is deliberately never released: by construction it is
// the beginning of a marker, and a reply that ends inside one ends inside
// fabricated text.
function feedToolRoleMarkerGuard(guard, text) {
  if (!guard || guard.tripped) {
    return '';
  }
  const data = guard.pending + (typeof text === 'string' ? text : '');
  guard.pending = '';
  if (!data) {
    return '';
  }
  const idx = indexToolRoleMarker(data);
  if (idx >= 0) {
    guard.tripped = true;
    return data.slice(0, idx);
  }
  const hold = trailingToolRoleMarkerPrefixLen(data);
  guard.pending = data.slice(data.length - hold);
  return data.slice(0, data.length - hold);
}

module.exports = {
  TOOL_ROLE_MARKER,
  createToolRoleMarkerGuard,
  feedToolRoleMarkerGuard,
  indexToolRoleMarker,
};
