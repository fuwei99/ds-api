package toolstream

import (
	"ds2api/internal/toolcall"
	"strings"
)

type State struct {
	pending                strings.Builder
	capture                strings.Builder
	capturing              bool
	codeFenceStack         []int
	codeFencePendingTicks  int
	codeFencePendingTildes int
	codeFenceNotLineStart  bool // inverted: zero-value false means "at line start"
	markdownCodeSpanTicks  int
	pendingToolRaw         string
	pendingToolCalls       []toolcall.ParsedToolCall
	disableDeltas          bool
	toolNameSent           bool
	toolName               string
	toolArgsStart          int
	toolArgsSent           int
	toolArgsString         bool
	toolArgsDone           bool

	// Marker 是本次调用者专属的工具调用标识。非空且不等于 EPSE 时，
	// ProcessChunk 会先把流入文本里的标识归一化回 EPSE，再交给 sieve 解析。
	Marker     string
	normalizer *toolcall.MarkerNormalizer
}

func (s *State) ensureNormalizer() *toolcall.MarkerNormalizer {
	if s.Marker == "" {
		return nil
	}
	if s.normalizer == nil {
		s.normalizer = toolcall.NewMarkerNormalizer(s.Marker)
	}
	return s.normalizer
}

// normalizeIncoming 把新流入的文本从调用者标识翻译回 EPSE，并在必要时保留
// 可能跨越分片的标识前缀。
func (s *State) normalizeIncoming(chunk string) string {
	n := s.ensureNormalizer()
	if n == nil {
		return chunk
	}
	return n.Write(chunk)
}

// flushNormalizer 在流结束时释放被保留的标识残片。
func (s *State) flushNormalizer() string {
	if s.normalizer == nil {
		return ""
	}
	return s.normalizer.Flush()
}

type Event struct {
	Content        string
	ToolCalls      []toolcall.ParsedToolCall
	ToolCallDeltas []ToolCallDelta
}

type ToolCallDelta struct {
	Index     int
	Name      string
	Arguments string
}

func (s *State) resetIncrementalToolState() {
	s.disableDeltas = false
	s.toolNameSent = false
	s.toolName = ""
	s.toolArgsStart = -1
	s.toolArgsSent = -1
	s.toolArgsString = false
	s.toolArgsDone = false
}

func (s *State) noteText(content string) {
	if !hasMeaningfulText(content) {
		return
	}
	updateMarkdownCodeSpanState(s, content)
	updateCodeFenceState(s, content)
}

func hasMeaningfulText(text string) bool {
	return strings.TrimSpace(text) != ""
}

func insideCodeFenceWithState(state *State, text string) bool {
	if state == nil {
		return insideCodeFence(text)
	}
	simulated := simulateCodeFenceState(
		state.codeFenceStack,
		state.codeFencePendingTicks,
		state.codeFencePendingTildes,
		!state.codeFenceNotLineStart,
		text,
	)
	return len(simulated.stack) > 0
}

func insideCodeFence(text string) bool {
	if text == "" {
		return false
	}
	return len(simulateCodeFenceState(nil, 0, 0, true, text).stack) > 0
}

func updateMarkdownCodeSpanState(state *State, text string) {
	if state == nil || !hasMeaningfulText(text) {
		return
	}
	state.markdownCodeSpanTicks = simulateMarkdownCodeSpanTicks(state, state.markdownCodeSpanTicks, text)
}

func simulateMarkdownCodeSpanTicks(state *State, initialTicks int, text string) int {
	ticks := initialTicks
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
		} else if run == ticks {
			ticks = 0
		}
		i += run
	}
	return ticks
}

func countBacktickRun(text string, start int) int {
	count := 0
	for start+count < len(text) && text[start+count] == '`' {
		count++
	}
	return count
}

func atMarkdownFenceLineStart(text string, idx int) bool {
	for i := idx - 1; i >= 0; i-- {
		switch text[i] {
		case ' ', '\t':
			continue
		case '\n', '\r':
			return true
		default:
			return false
		}
	}
	return true
}

func updateCodeFenceState(state *State, text string) {
	if state == nil || !hasMeaningfulText(text) {
		return
	}
	next := simulateCodeFenceState(
		state.codeFenceStack,
		state.codeFencePendingTicks,
		state.codeFencePendingTildes,
		!state.codeFenceNotLineStart,
		text,
	)
	state.codeFenceStack = next.stack
	state.codeFencePendingTicks = next.pendingTicks
	state.codeFencePendingTildes = next.pendingTildes
	state.codeFenceNotLineStart = !next.lineStart
}

type codeFenceSimulation struct {
	stack         []int
	pendingTicks  int
	pendingTildes int
	lineStart     bool
}

func simulateCodeFenceState(stack []int, pendingTicks, pendingTildes int, lineStart bool, text string) codeFenceSimulation {
	chunk := text
	nextStack := append([]int(nil), stack...)
	ticks := pendingTicks
	tildes := pendingTildes
	atLineStart := lineStart

	flushPending := func() {
		if ticks > 0 {
			if atLineStart && ticks >= 3 {
				applyFenceMarker(&nextStack, ticks) // positive = backtick
			}
			atLineStart = false
			ticks = 0
		}
		if tildes > 0 {
			if atLineStart && tildes >= 3 {
				applyFenceMarker(&nextStack, -tildes) // negative = tilde
			}
			atLineStart = false
			tildes = 0
		}
	}

	for i := 0; i < len(chunk); i++ {
		ch := chunk[i]
		if ch == '`' {
			if tildes > 0 {
				// Mixed chars — flush tildes first.
				flushPending()
			}
			ticks++
			continue
		}
		if ch == '~' {
			if ticks > 0 {
				flushPending()
			}
			tildes++
			continue
		}
		flushPending()
		switch ch {
		case '\n', '\r':
			atLineStart = true
		case ' ', '\t':
			if atLineStart {
				continue
			}
			atLineStart = false
		default:
			atLineStart = false
		}
	}

	return codeFenceSimulation{
		stack:         nextStack,
		pendingTicks:  ticks,
		pendingTildes: tildes,
		lineStart:     atLineStart,
	}
}

// applyFenceMarker pushes or pops a fence marker on the stack.
// Positive values represent backtick fences, negative represent tilde fences.
// A closing marker must match the sign (type) of the opening marker.
func applyFenceMarker(stack *[]int, marker int) {
	if stack == nil || marker == 0 {
		return
	}
	if len(*stack) == 0 {
		*stack = append(*stack, marker)
		return
	}
	top := (*stack)[len(*stack)-1]
	// Signs must match: backtick closes backtick, tilde closes tilde.
	sameType := (top > 0 && marker > 0) || (top < 0 && marker < 0)
	if !sameType {
		// Different fence type — treat as nested.
		*stack = append(*stack, marker)
		return
	}
	absMarker := marker
	absTop := top
	if absMarker < 0 {
		absMarker = -absMarker
	}
	if absTop < 0 {
		absTop = -absTop
	}
	if absMarker >= absTop {
		*stack = (*stack)[:len(*stack)-1]
		return
	}
	*stack = append(*stack, marker)
}
