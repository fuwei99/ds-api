package toolstream

import "ds2api/internal/toolcall"

func ProcessChunk(state *State, chunk string, toolNames []string) []Event {
	if state == nil {
		return nil
	}
	if chunk != "" {
		state.pending.WriteString(state.normalizeIncoming(chunk))
	}
	events := make([]Event, 0, 2)
	if len(state.pendingToolCalls) > 0 {
		events = append(events, Event{ToolCalls: state.pendingToolCalls})
		state.pendingToolRaw = ""
		state.pendingToolCalls = nil
	}

	for {
		if state.capturing {
			if state.pending.Len() > 0 {
				state.capture.WriteString(state.pending.String())
				state.pending.Reset()
			}
			prefix, calls, suffix, ready := consumeToolCapture(state, toolNames, false)
			if !ready {
				break
			}
			state.capture.Reset()
			state.capturing = false
			state.resetIncrementalToolState()
			if len(calls) > 0 {
				if prefix != "" {
					state.noteText(prefix)
					events = append(events, Event{Content: prefix})
				}
				// Emit the completed tool call immediately so the runtime can mark
				// the turn as tool-call emitting before the capture suffix (tail
				// prose after the tool block) is released as content. Deferring
				// to the next ProcessChunk call would let same-chunk tail text
				// slip through as visible content in the same onParsed pass.
				events = append(events, Event{ToolCalls: calls})
				state.pendingToolRaw = ""
				if suffix != "" {
					state.pending.WriteString(suffix)
				}
				continue
			}
			if prefix != "" {
				state.noteText(prefix)
				events = append(events, Event{Content: prefix})
			}
			if suffix != "" {
				state.pending.WriteString(suffix)
			}
			continue
		}

		pending := state.pending.String()
		if pending == "" {
			break
		}
		start := findToolSegmentStart(state, pending)
		if start == holdToolSegmentStart {
			break
		}
		if start >= 0 {
			prefix := pending[:start]
			if prefix != "" {
				resetMarkdownSpan := shouldResetUnclosedMarkdownPrefix(state, prefix, pending[start:])
				state.noteText(prefix)
				if resetMarkdownSpan {
					state.markdownCodeSpanTicks = 0
				}
				events = append(events, Event{Content: prefix})
			}
			state.pending.Reset()
			state.capture.WriteString(pending[start:])
			state.capturing = true
			state.resetIncrementalToolState()
			continue
		}

		safe, hold := splitSafeContentForToolDetection(state, pending)
		if safe == "" {
			break
		}
		state.pending.Reset()
		state.pending.WriteString(hold)
		state.noteText(safe)
		events = append(events, Event{Content: safe})
	}

	return events
}

func Flush(state *State, toolNames []string) []Event {
	if state == nil {
		return nil
	}
	if tail := state.flushNormalizer(); tail != "" {
		state.pending.WriteString(tail)
	}
	events := ProcessChunk(state, "", toolNames)
	if state.pending.Len() > 0 && state.markdownCodeSpanTicks > 0 {
		// At end of stream, an unmatched backtick is literal Markdown text.
		// Re-scan pending content so a real tool call after that stray
		// backtick is not permanently hidden by inline-code state.
		state.markdownCodeSpanTicks = 0
		events = append(events, ProcessChunk(state, "", toolNames)...)
	}
	if len(state.pendingToolCalls) > 0 {
		events = append(events, Event{ToolCalls: state.pendingToolCalls})
		state.pendingToolRaw = ""
		state.pendingToolCalls = nil
	}
	if state.capturing {
		consumedPrefix, consumedCalls, consumedSuffix, ready := consumeToolCapture(state, toolNames, true)
		if ready {
			if consumedPrefix != "" {
				state.noteText(consumedPrefix)
				events = append(events, Event{Content: consumedPrefix})
			}
			if len(consumedCalls) > 0 {
				events = append(events, Event{ToolCalls: consumedCalls})
			}
			if consumedSuffix != "" {
				state.noteText(consumedSuffix)
				events = append(events, Event{Content: consumedSuffix})
			}
		} else {
			content := state.capture.String()
			if content != "" {
				recovered := toolcall.SanitizeLooseCDATA(content)
				if recovered != content {
					if prefix, calls, suffix, recoveredReady := consumeXMLToolCapture(recovered, toolNames); recoveredReady && len(calls) > 0 {
						if prefix != "" {
							state.noteText(prefix)
							events = append(events, Event{Content: prefix})
						}
						events = append(events, Event{ToolCalls: calls})
						if suffix != "" {
							state.noteText(suffix)
							events = append(events, Event{Content: suffix})
						}
					} else {
						// If capture never resolved into a real tool call, release
						// the buffered text instead of swallowing it.
						state.noteText(content)
						events = append(events, Event{Content: content})
					}
				} else {
					// If capture never resolved into a real tool call, release the
					// buffered text instead of swallowing it.
					state.noteText(content)
					events = append(events, Event{Content: content})
				}
			}
		}
		state.capture.Reset()
		state.capturing = false
		state.resetIncrementalToolState()
	}
	if state.pending.Len() > 0 {
		content := state.pending.String()
		// If pending never resolved into a real tool call, release it as text.
		state.noteText(content)
		events = append(events, Event{Content: content})
		state.pending.Reset()
	}
	return events
}

func splitSafeContentForToolDetection(state *State, s string) (safe, hold string) {
	if s == "" {
		return "", ""
	}
	if xmlIdx := findPartialXMLToolTagStart(s); xmlIdx >= 0 {
		if insideCodeFenceWithState(state, s[:xmlIdx]) {
			return s, ""
		}
		markdown := markdownCodeSpanStateAt(state, s[:xmlIdx])
		if markdown.ticks > 0 {
			if markdownCodeSpanCloses(s[xmlIdx:], markdown.ticks) {
				return s, ""
			}
			if markdown.fromPrior {
				return "", s
			}
		}
		if xmlIdx > 0 {
			return s[:xmlIdx], s[xmlIdx:]
		}
		return "", s
	}
	return s, ""
}

const holdToolSegmentStart = -2

func findToolSegmentStart(state *State, s string) int {
	if s == "" {
		return -1
	}
	offset := 0
	for {
		start, end, ok := findNextToolInterceptTag(s, offset)
		if !ok {
			return -1
		}
		start = includeDuplicateLeadingLessThan(s, start)
		if insideCodeFenceWithState(state, s[:start]) {
			offset = end + 1
			continue
		}
		markdown := markdownCodeSpanStateAt(state, s[:start])
		if markdown.ticks == 0 {
			return start
		}
		if markdownCodeSpanCloses(s[start:], markdown.ticks) {
			offset = end + 1
			continue
		}
		if markdown.fromPrior {
			return holdToolSegmentStart
		}
		return start
	}
}

// findNextToolInterceptTag returns the earliest streaming interception entry at
// or after offset: a recognized EPSE / canonical tool markup tag, or a
// DSML-prefixed tag. DSML output never parses into a tool call, but its wrapper
// still has to be captured so the raw alien markup is not leaked piecemeal.
func findNextToolInterceptTag(s string, offset int) (start, end int, ok bool) {
	tag, tagOK := toolcall.FindToolMarkupTagOutsideIgnored(s, offset)
	dsml, dsmlOK := toolcall.FindDSMLTagOutsideIgnored(s, offset)
	switch {
	case tagOK && dsmlOK:
		if tag.Start <= dsml.Start {
			return tag.Start, tag.End, true
		}
		return dsml.Start, dsml.End, true
	case tagOK:
		return tag.Start, tag.End, true
	case dsmlOK:
		return dsml.Start, dsml.End, true
	default:
		return 0, 0, false
	}
}

// consumeDSMLHiddenCapture hides a DSML block from visible output:
// prefix is the text before the first DSML tag; suffix is the text after the
// matched closing tag. handled=true means a DSML tag was found; hold=true means
// the outer wrapper has not closed yet and the caller should keep buffering.
// When final is true an unclosed tail is discarded as well.
func consumeDSMLHiddenCapture(captured string, final bool) (prefix, suffix string, handled, hold bool) {
	open, ok := toolcall.FindDSMLTagOutsideIgnored(captured, 0)
	if !ok {
		return "", "", false, false
	}
	prefix = captured[:open.Start]
	closeTag, ok := toolcall.FindMatchingDSMLClose(captured, open)
	if !ok {
		if final {
			return prefix, "", true, false
		}
		return "", "", true, true
	}
	return prefix, captured[closeTag.End+1:], true, false
}

type markdownCodeSpanScan struct {
	ticks     int
	fromPrior bool
}

func markdownCodeSpanStateAt(state *State, text string) markdownCodeSpanScan {
	ticks := 0
	fromPrior := false
	if state != nil && state.markdownCodeSpanTicks > 0 {
		ticks = state.markdownCodeSpanTicks
		fromPrior = true
	}
	for i := 0; i < len(text); {
		if text[i] != '`' {
			i++
			continue
		}
		run := countBacktickRun(text, i)
		if ticks == 0 {
			if run >= 3 && atMarkdownFenceLineStart(text, i) {
				i += run
				continue
			}
			if state != nil && insideCodeFenceWithState(state, text[:i]) {
				i += run
				continue
			}
			ticks = run
			fromPrior = false
		} else if run == ticks {
			ticks = 0
			fromPrior = false
		}
		i += run
	}
	return markdownCodeSpanScan{ticks: ticks, fromPrior: fromPrior}
}

func markdownCodeSpanCloses(text string, ticks int) bool {
	if ticks <= 0 {
		return false
	}
	for i := 0; i < len(text); {
		if text[i] != '`' {
			i++
			continue
		}
		run := countBacktickRun(text, i)
		if run == ticks {
			return true
		}
		i += run
	}
	return false
}

func shouldResetUnclosedMarkdownPrefix(state *State, prefix, suffix string) bool {
	markdown := markdownCodeSpanStateAt(state, prefix)
	return markdown.ticks > 0 && !markdown.fromPrior && !markdownCodeSpanCloses(suffix, markdown.ticks)
}

func includeDuplicateLeadingLessThan(s string, idx int) int {
	for idx > 0 && s[idx-1] == '<' {
		idx--
	}
	return idx
}

func consumeToolCapture(state *State, toolNames []string, final bool) (prefix string, calls []toolcall.ParsedToolCall, suffix string, ready bool) {
	captured := state.capture.String()
	if captured == "" {
		return "", nil, "", false
	}

	// XML tool call extraction only.
	if xmlPrefix, xmlCalls, xmlSuffix, xmlReady := consumeXMLToolCapture(captured, toolNames); xmlReady {
		return xmlPrefix, xmlCalls, xmlSuffix, true
	}
	// If XML tags are present but block is incomplete, keep buffering.
	if hasOpenXMLToolTag(captured) {
		return "", nil, "", false
	}
	// DSML never parses into a tool call: hide it from visible content while
	// keeping the raw text available for the finalize repair path.
	if dsmlPrefix, dsmlSuffix, handled, hold := consumeDSMLHiddenCapture(captured, final); handled {
		if hold {
			return "", nil, "", false
		}
		return dsmlPrefix, nil, dsmlSuffix, true
	}
	if shouldKeepBareInvokeCapture(captured) {
		return "", nil, "", false
	}
	return captured, nil, "", true
}
