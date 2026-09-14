'use strict';

// Stateful tool-call marker normalizer. Mirrors internal/toolcall/marker.go
// (MarkerNormalizer / NormalizeMarkerText) so the Node stream mirror and the Go
// runtime agree byte-for-byte on how the per-API-key marker is folded back.
//
// The upstream model is instructed to use a per-caller marker instead of the
// canonical `EPSE` keyword, so the shared keyword never reaches it. Every
// consumer of the model output (tool sieve, standalone tool-call parser,
// repeat-loop guard) still speaks `EPSE`, so the marker has to be rewritten back
// before any of them run.
//
// SSE deltas can split the marker at any byte (`<|Q7Z` in one chunk and
// `K3M|tool_calls>` in the next), so the rewrite is stateful: a trailing
// fragment that could still grow into the marker is withheld until the next
// write (or flush) instead of being forwarded half-rewritten.

const EPSE_KEYWORD = 'EPSE';

function normalizeMarker(marker) {
  const text = asString(marker).toUpperCase();
  if (!text || text === EPSE_KEYWORD) {
    return '';
  }
  return text;
}

// createMarkerNormalizer builds a normalizer for marker. An empty or canonical
// ("EPSE") marker yields an inert normalizer, which is the legacy behaviour.
function createMarkerNormalizer(marker) {
  const active = normalizeMarker(marker);
  let buf = '';
  if (!active) {
    return {
      active: false,
      write(chunk) {
        return typeof chunk === 'string' ? chunk : '';
      },
      flush() {
        return '';
      },
    };
  }
  return {
    active: true,
    // write consumes the next stream chunk and returns the text that is safe to
    // forward to downstream consumers. A trailing fragment that could still grow
    // into the marker is withheld until the following write (or flush).
    write(chunk) {
      const data = buf + (typeof chunk === 'string' ? chunk : '');
      buf = '';
      const hold = trailingMarkerPrefixLen(data, active);
      if (hold > 0) {
        buf = data.slice(data.length - hold);
        return replaceMarkerKeyword(data.slice(0, data.length - hold), active);
      }
      return replaceMarkerKeyword(data, active);
    },
    // flush releases any withheld fragment at end of stream.
    flush() {
      const out = replaceMarkerKeyword(buf, active);
      buf = '';
      return out;
    },
  };
}

// trailingMarkerPrefixLen returns the length of the longest suffix of data that
// is a proper prefix of marker (case-insensitive ASCII), capped at
// len(marker) - 1. A complete marker is never held back.
function trailingMarkerPrefixLen(data, marker) {
  const max = Math.min(marker.length - 1, data.length);
  for (let l = max; l > 0; l -= 1) {
    if (equalsFold(data.slice(data.length - l), marker.slice(0, l))) {
      return l;
    }
  }
  return 0;
}

// replaceMarkerKeyword rewrites every complete marker occurrence in text back to
// the canonical EPSE keyword.
function replaceMarkerKeyword(text, marker) {
  if (!text || !marker) {
    return text;
  }
  if (!containsFold(text, marker)) {
    return text;
  }
  let out = '';
  let i = 0;
  while (i < text.length) {
    if (i + marker.length <= text.length && equalsFold(text.slice(i, i + marker.length), marker)) {
      out += EPSE_KEYWORD;
      i += marker.length;
      continue;
    }
    out += text[i];
    i += 1;
  }
  return out;
}

function containsFold(text, marker) {
  if (!marker || text.length < marker.length) {
    return false;
  }
  for (let i = 0; i + marker.length <= text.length; i += 1) {
    if (equalsFold(text.slice(i, i + marker.length), marker)) {
      return true;
    }
  }
  return false;
}

function equalsFold(a, b) {
  return a.length === b.length && a.toLowerCase() === b.toLowerCase();
}

function asString(v) {
  if (typeof v === 'string') {
    return v.trim();
  }
  if (v == null) {
    return '';
  }
  return String(v).trim();
}

module.exports = {
  EPSE_KEYWORD,
  createMarkerNormalizer,
  replaceMarkerKeyword,
};
