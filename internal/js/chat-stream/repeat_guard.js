'use strict';

// Guard against the upstream repetition loop: DeepSeek occasionally degenerates
// at the end of a generation and emits protocol closing shells forever without
// ever sending a terminal status, so the stream only ends when the idle watchdog
// fires. The repeated shell is not always the same tag — observed loops include
// a plain `</|EPSE>` run, an alternating `</|EPSE|invoke>` /
// `</|EPSE|tool_calls>` run, and a run of the normalized canonical shells
// `</invoke>` / `</parameter>` — so the guard keys on "any closing tag `</…>`"
// rather than on one literal or one prefix.
//
// A well-formed tool call closes at most three shells back to back (the last
// parameter, then invoke, then tool_calls); any deeper structure is broken up by
// an opening tag. Measured worst cases for legitimate output: 3 for well-formed
// calls of every shape, 5 when a model over-closes, and 6 when a model explains
// the format by listing closing-shell variants one per line — that last shape
// has no structural upper bound, which is why the threshold keeps real headroom
// above it rather than sitting at the measured maximum.
//
// Ordinary HTML closing tags (see HTML_CLOSING_TAG_NAMES) are excluded: deeply
// nested markup legitimately ends with a long run of `</div>` / `</li>` /
// `</td>` lines, and those must not be mistaken for the degenerate protocol
// loop. The exclusion is by name, not by shape, so the canonical `</invoke>` /
// `</parameter>` shells still count.
//
// Mirrors internal/sse/repeat_guard.go; keep the threshold and HTML name list in
// sync.

const REPEAT_LOOP_THRESHOLD = 10;

// HTML_CLOSING_TAG_NAMES lists the common HTML/SVG element names whose closing
// tags can legitimately stack up in deeply nested markup. A closing tag whose
// name is in this set is skipped and breaks the run instead of being counted.
const HTML_CLOSING_TAG_NAMES = new Set([
  'a', 'article', 'aside', 'b', 'blockquote', 'body', 'button', 'code', 'dd',
  'div', 'dl', 'dt', 'em', 'footer', 'form', 'g', 'h1', 'h2', 'h3', 'h4', 'h5',
  'h6', 'head', 'header', 'html', 'i', 'label', 'li', 'main', 'nav', 'ol',
  'option', 'p', 'path', 'pre', 'script', 'section', 'select', 'span', 'strong',
  'style', 'svg', 'table', 'tbody', 'td', 'tfoot', 'th', 'thead', 'tr', 'ul',
]);

// Bounds how much text is retained while waiting for a closing tag's `>` to
// arrive in a later SSE fragment.
const MAX_PENDING_TAG_LEN = 512;

const CLOSING_TAG_NONE = 0;
const CLOSING_TAG_MATCHED = 1;
const CLOSING_TAG_NEED_MORE = 2;

function createRepeatLoopGuard(threshold = REPEAT_LOOP_THRESHOLD) {
  return {
    threshold: threshold > 0 ? threshold : REPEAT_LOOP_THRESHOLD,
    pending: '',
    run: 0,
    tripped: false,
  };
}

function isRepeatLoopSeparator(ch) {
  return ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r';
}

// isClosingTagBodyStart reports whether ch may appear directly after `</` in a
// closing tag. Whitespace, `<` and `>` are excluded so an unterminated `</` or
// a stray `</ >` is not mistaken for a tag; every other character is accepted,
// which keeps both XML element names (`</invoke>`) and separator-drift EPSE
// shells (`</|EPSE>`) eligible.
function isClosingTagBodyStart(ch) {
  return ch !== ' ' && ch !== '\t' && ch !== '\n' && ch !== '\r' && ch !== '<' && ch !== '>';
}

// scanProtocolClosingTag reports whether a closing tag starts at buf[i]. On a
// match `next` is the index just past the tag's `>`; otherwise `next` is an
// index the caller may safely resume from (never inside a region already proven
// free of `<`).
//
// A closing tag is `</`, at least one non-whitespace body character, then `>`.
// The body may be a normal XML element name (`</invoke>`, `</parameter>`,
// `</tool_calls>`) or a separator-drift EPSE shell (`</|EPSE>`,
// `</|EPSE|invoke>`, `</、EPSE、tool_calls>`). Matching every closing tag — not
// just the protocol-shaped ones — is required because the degenerate upstream
// loop also emits the normalized canonical shells `</invoke>` / `</parameter>`,
// which look exactly like ordinary XML closing tags. The only names skipped are
// the common HTML/SVG elements in HTML_CLOSING_TAG_NAMES, whose closing tags
// legitimately stack in deeply nested markup.
//
// Only the ASCII `>` terminates a tag; a drifted terminator simply leaves the
// run at zero, which loses detection but never produces a false positive.
function scanProtocolClosingTag(buf, i) {
  if (buf[i] !== '<') {
    return { next: i, status: CLOSING_TAG_NONE };
  }
  if (i + 1 >= buf.length) {
    return { next: i, status: CLOSING_TAG_NEED_MORE };
  }
  if (buf[i + 1] !== '/') {
    return { next: i, status: CLOSING_TAG_NONE };
  }
  if (i + 2 >= buf.length) {
    return { next: i, status: CLOSING_TAG_NEED_MORE };
  }
  if (!isClosingTagBodyStart(buf[i + 2])) {
    return { next: i, status: CLOSING_TAG_NONE };
  }
  for (let j = i + 3; j < buf.length; j += 1) {
    if (buf[j] === '>') {
      if (isHTMLClosingTag(buf, i, j + 1)) {
        // Ordinary HTML closing tag: not part of a protocol-shell loop. Return
        // it as a non-match so the run resets and scanning resumes past it.
        return { next: j + 1, status: CLOSING_TAG_NONE };
      }
      return { next: j + 1, status: CLOSING_TAG_MATCHED };
    }
    if (buf[j] === '<') {
      // A new tag starts before this one closed, so `</…` was not a closing tag
      // after all. Resuming at j is safe: no `<` was skipped.
      return { next: j, status: CLOSING_TAG_NONE };
    }
    if (j - i >= MAX_PENDING_TAG_LEN) {
      return { next: j, status: CLOSING_TAG_NONE };
    }
  }
  if (buf.length - i >= MAX_PENDING_TAG_LEN) {
    return { next: buf.length, status: CLOSING_TAG_NONE };
  }
  return { next: i, status: CLOSING_TAG_NEED_MORE };
}

// isHTMLClosingTag reports whether the closing tag spanning buf[start:end]
// (start at `<`, end just past `>`) names an ordinary HTML/SVG element from
// HTML_CLOSING_TAG_NAMES. The tag name runs from after `</` to the first
// whitespace or `>`, so `</div>`, `</div >` and `</DIV>` all match, while
// separator-drift shells (`</|EPSE|invoke>`) and protocol shells (`</invoke>`,
// `</parameter>`) do not.
function isHTMLClosingTag(buf, start, end) {
  let nameEnd = start + 2;
  while (nameEnd < end - 1 && !isRepeatLoopSeparator(buf[nameEnd])) {
    nameEnd += 1;
  }
  if (nameEnd === start + 2) {
    return false;
  }
  return HTML_CLOSING_TAG_NAMES.has(buf.slice(start + 2, nameEnd).toLowerCase());
}

// feedRepeatLoopGuard consumes the next raw text fragment and reports whether a
// repetition loop has been detected. Fragments may split a tag across SSE
// frames, so a short tail is retained between calls. Whitespace between two
// closing tags does not break the run; anything else resets it to zero.
function feedRepeatLoopGuard(guard, text) {
  if (!guard) {
    return false;
  }
  if (guard.tripped) {
    return true;
  }
  if (!text) {
    return false;
  }
  const buf = guard.pending + text;
  guard.pending = '';
  let i = 0;
  while (i < buf.length) {
    const { next, status } = scanProtocolClosingTag(buf, i);
    if (status === CLOSING_TAG_MATCHED) {
      guard.run += 1;
      if (guard.run >= guard.threshold) {
        guard.tripped = true;
        guard.run = 0;
        return true;
      }
      i = next;
      continue;
    }
    if (status === CLOSING_TAG_NEED_MORE) {
      guard.pending = buf.slice(i);
      return false;
    }
    if (guard.run > 0 && isRepeatLoopSeparator(buf[i])) {
      i += 1;
      continue;
    }
    guard.run = 0;
    i = next > i ? next : i + 1;
  }
  return false;
}

module.exports = {
  REPEAT_LOOP_THRESHOLD,
  createRepeatLoopGuard,
  feedRepeatLoopGuard,
};
