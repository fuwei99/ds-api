package toolcall

import (
	"context"
	"fmt"
	"strings"

	"ds2api/internal/config"
)

// ToolCallRepairFixedPrompt is the verbatim instruction appended after the tool
// call format spec when asking an LLM to repair malformed tool-call code. It is
// reproduced from plan/tool-call-fallback-design-phase3.md §1, minus the
// "NO TOOL" opt-out sentence, plus the placeholder / structure-preservation hard
// constraints.
//
// NOTE: 原文末尾还有一句「如果你认为接下来的内容只是在正常聊天，直接返回 NO TOOL」。
// 实测这句话容易误导模型：模型会把本来就是坏格式工具调用的片段判成普通聊天并直接返回
// NO TOOL，导致该修的调用不修。因此暂时先移除这句提示词。分支 ② 的相关代码
// （toolCallRepairNoToolSentinel 以及 RepairToolCallsWithLLM 里的判定）保持不变：
// 模型若仍自发返回 NO TOOL，依旧按「非工具调用、坏码原样输出」处理。
//
// NOTE: 占位符相关的措辞必须与实际输入一致。早期版本声称「实际参数已用变量代替」，
// 但未闭合的 CDATA 不参与 slot，模型实际会同时看到占位符和原始长内容，指令与输入
// 自相矛盾，反而诱导模型「顺手整理」未被替换的部分。现在改为「可能已被替换」，并显式
// 要求逐字保留占位符、逐字节保留未替换内容、只允许改结构。
const ToolCallRepairFixedPrompt = `请根据上述说明的工具调用说明，修复以下错误的调用代码。直接输出修复后的格式，无需解释哪里错了，也无需说任何话。

硬性要求：
1) 参数值可能已被替换为形如 DS_SLOT_0、DS_SLOT_1 的占位符。所有占位符必须逐字符原样输出，禁止改名、改序号、翻译、增补或删除。
2) 未被替换为占位符的原始内容同样必须逐字节原样保留，不要重新缩进、转义、截断或"顺手整理"。
3) 你只允许修改标签结构本身：标签名、<|EPSE| 前缀、闭合标签、CDATA 的 ]]> 结束符、以及标签的嵌套层级。
4) 输入中出现的每一个 <|EPSE|parameter> 节点及其 name 属性都必须在输出中保留，禁止合并或丢弃任何参数节点。
5) 若某个 <![CDATA[ 缺少配对的 ]]>，请补上 ]]> 再闭合其所属的 parameter 标签。`

// ToolCallRepairFixedPromptForMarker is the marker-aware variant of
// ToolCallRepairFixedPrompt: the canonical EPSE keyword is rendered as the
// caller-specific marker so the repair request never shows the shared,
// fingerprintable keyword to the upstream model. An empty marker yields the
// verbatim canonical text.
func ToolCallRepairFixedPromptForMarker(marker string) string {
	marker = normalizeToolMarker(marker)
	if marker == EPSEKeyword {
		return ToolCallRepairFixedPrompt
	}
	return strings.ReplaceAll(ToolCallRepairFixedPrompt, EPSEKeyword, marker)
}

// toolCallRepairNoToolSentinel is the literal the repair model must return when
// it judges the fragment is not a tool call (branch ②). The instruction asking
// for it was removed from ToolCallRepairFixedPrompt (see the note above), but the
// sentinel handling is kept intentionally so a self-reported NO TOOL is still
// honored.
const toolCallRepairNoToolSentinel = "NO TOOL"

// ToolCallRepairInvoker sends the fully assembled repair prompt to an LLM and
// returns its raw text output. Implementations must honor the phase3 §3 hard
// constraints (same account, expert mode, thinking disabled, brand-new session,
// 10s timeout). A non-nil error (including timeout) routes the caller to the
// fallback path (branch ③).
type ToolCallRepairInvoker func(ctx context.Context, prompt string) (string, error)

// toolCallRepairPlan is the assembled repair request: one prompt plus the slot
// tables needed to restore the model's output.
//
// Malformed input can be slotted two ways (see scanBoundedCDATARanges), so the
// plan may carry an alternate interpretation. Only ONE LLM call is ever made;
// the alternate is merely a second slot table applied to that same output, and
// the interpretation whose restore+parse yields the better tool calls wins.
type toolCallRepairPlan struct {
	prompt string
	// primary is the interpretation the prompt skeleton was rendered from.
	primary cdataSlotResult
	// alternate is the other interpretation, or a zero value when the greedy
	// and bounded scans agree and there is nothing to disambiguate.
	alternate cdataSlotResult
	// interpretation names the primary scan ("greedy" or "bounded") for logs.
	interpretation string
	// degradeReason records why the bounded scan was preferred, for logs.
	degradeReason string
}

// buildToolCallRepairPlan slots badCode and assembles the repair prompt.
//
// The greedy scan (slotCDATAContent) is the default and matches the
// deterministic parser's CDATA interpretation exactly. When it mishandles the
// input — a slot body that swallowed the following parameter shell, or an
// unclosed CDATA left entirely unslotted — the bounded scan is promoted to
// primary and greedy is kept as the alternate, so a genuine long parameter that
// merely mentions tool markup in its text can still win on re-parse.
func buildToolCallRepairPlan(badCode string, marker string) toolCallRepairPlan {
	greedy := slotCDATAContent(badCode)
	bounded := slotCDATAContentBounded(badCode)

	plan := toolCallRepairPlan{primary: greedy, interpretation: "greedy"}
	if degraded, reason := greedySlottingDegraded(greedy, bounded); degraded {
		plan = toolCallRepairPlan{
			primary:        bounded,
			alternate:      greedy,
			interpretation: "bounded",
			degradeReason:  reason,
		}
	}
	plan.prompt = renderToolCallRepairPrompt(plan.primary, marker)
	return plan
}

// renderToolCallRepairPrompt assembles the prompt from four parts, in order
// (plan §1 plus the placeholder checklist):
//  1. the tool call format spec (verbatim, ToolCallFormatSpec),
//  2. the fixed instruction (ToolCallRepairFixedPrompt),
//  3. the placeholder checklist (omitted when nothing was slotted),
//  4. the slotted bad tool-call code.
//
// marker is the caller-specific tool-call marker; both the format spec and the
// fixed instruction are rendered with it so the whole repair request stays
// consistent with what the generating model saw. The model answers with the
// same marker, and the output is normalized back to EPSE before parsing.
func renderToolCallRepairPrompt(res cdataSlotResult, marker string) string {
	var b strings.Builder
	b.Grow(len(res.skeleton) + 4096)
	b.WriteString(ToolCallFormatSpecForMarker(marker))
	b.WriteString("\n\n")
	b.WriteString(ToolCallRepairFixedPromptForMarker(marker))
	if checklist := placeholderChecklist(res); checklist != "" {
		b.WriteString("\n\n")
		b.WriteString(checklist)
	}
	b.WriteString("\n\n")
	b.WriteString(res.skeleton)
	return b.String()
}

// BuildToolCallRepairPrompt assembles the repair prompt for badCode and returns
// it together with the slot table / token needed to restore the placeholders in
// the model's output. When the bad code has no slottable CDATA the returned
// slots are empty and the skeleton equals badCode.
//
// The optional toolMarker renders the prompt with the caller-specific tool-call
// marker; when omitted the canonical EPSE keyword is used (legacy behaviour).
func BuildToolCallRepairPrompt(badCode string, toolMarker ...string) (prompt string, slots []string, token cdataSlotToken) {
	plan := buildToolCallRepairPlan(badCode, firstRepairToolMarker(toolMarker))
	return plan.prompt, plan.primary.slots, plan.primary.token
}

// firstRepairToolMarker returns the optional caller-specific marker, keeping the
// pre-marker call sites and tests source-compatible.
func firstRepairToolMarker(markers []string) string {
	if len(markers) == 0 {
		return ""
	}
	return strings.TrimSpace(markers[0])
}

// RepairToolCallsWithLLM attempts to repair badCode (the residual intent text
// identified by phase 1) into valid tool calls using the provided LLM invoker.
// It implements the three-way branch semantics from plan §4:
//
//	① valid format  → parse into tool calls, return (calls, true)
//	② "NO TOOL"     → return (nil, false); caller emits the fallback verbatim
//	③ unparseable / timeout / error / slot mismatch → (nil, false); fallback
//
// Any failure (invoker error, missing slot, byte mismatch, unparseable output)
// degrades to (nil, false): the caller must never surface half-repaired content
// or leaked DS_SLOT placeholders downstream (hard constraint 3).
//
// The optional toolMarker renders the repair prompt with the caller-specific
// tool-call marker and normalizes the model's answer back to EPSE before
// parsing; when omitted the canonical EPSE keyword is used (legacy behaviour).
func RepairToolCallsWithLLM(ctx context.Context, badCode string, invoke ToolCallRepairInvoker, toolMarker ...string) ([]ParsedToolCall, bool) {
	if invoke == nil || strings.TrimSpace(badCode) == "" {
		return nil, false
	}
	marker := firstRepairToolMarker(toolMarker)
	plan := buildToolCallRepairPlan(badCode, marker)
	logAttrs := []any{
		"interpretation", plan.interpretation,
		"slot_count", len(plan.primary.slots),
		"skeleton_len", len(plan.primary.skeleton),
	}
	if plan.degradeReason != "" {
		logAttrs = append(logAttrs, "slot_degrade_reason", plan.degradeReason)
	}

	out, err := invoke(ctx, plan.prompt)
	if err != nil {
		// Branch ③: upstream/network error or timeout.
		config.Logger.Warn("[toolcall_repair] degraded", append([]any{"reason", "invoke_error", "error", err}, logAttrs...)...)
		return nil, false
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		config.Logger.Warn("[toolcall_repair] degraded", append([]any{"reason", "empty_output"}, logAttrs...)...)
		return nil, false
	}
	// Branch ②: the model declared the fragment is not a tool call.
	if strings.EqualFold(trimmed, toolCallRepairNoToolSentinel) {
		config.Logger.Warn("[toolcall_repair] degraded", append([]any{"reason", "no_tool"}, logAttrs...)...)
		return nil, false
	}

	// Restore the DS_SLOT placeholders back to the original CDATA bytes and
	// parse, for each candidate interpretation of the input. Both attempts read
	// the SAME model output; no extra LLM call is made.
	promptToken, guardLeaks := plan.primary.token, len(plan.primary.slots) > 0
	primaryCalls, primaryErr := restoreAndParseRepairOutput(out, plan.primary, promptToken, guardLeaks, marker)
	altCalls, altErr := restoreAndParseRepairOutput(out, plan.alternate, promptToken, guardLeaks, marker)

	best, source := pickBetterRepairResult(primaryCalls, plan.interpretation, altCalls, alternateInterpretationName(plan))
	if len(best) == 0 {
		reason := "unparseable_output"
		errAttr := primaryErr
		if primaryErr != nil {
			reason = "slot_restore_failed"
		} else if altErr != nil && plan.alternate.slotted {
			errAttr = altErr
		}
		attrs := append([]any{"reason", reason}, logAttrs...)
		if errAttr != nil {
			attrs = append(attrs, "error", errAttr)
		}
		config.Logger.Warn("[toolcall_repair] degraded", attrs...)
		return nil, false
	}
	if source != plan.interpretation {
		config.Logger.Info("[toolcall_repair] alternate interpretation won", append([]any{"winner", source}, logAttrs...)...)
	}
	// Branch ①: valid format parsed into one or more tool calls.
	return best, true
}

// restoreAndParseRepairOutput restores the slot placeholders in the model output
// using one interpretation's slot table, then parses the result. A zero-value
// (unset) interpretation yields no calls and no error.
//
// promptToken is the token the prompt skeleton actually used; guardLeaks says
// whether the prompt carried placeholders at all. Together they reject a parse
// whose values still contain a placeholder literal: when an interpretation's slot
// table does not line up with the model's output, restoration can silently leave
// `DS_SLOT_0` sitting inside a CDATA body (matchCDATASlotContent only errors on
// its own token), and that must never reach downstream as a parameter value.
//
// marker is the caller-specific tool-call marker the repair prompt was rendered
// with: the model answers with that marker, so the output is normalized back to
// EPSE before the slot restore / parse. The normalization only rewrites the tag
// keyword, never the slotted CDATA bodies.
func restoreAndParseRepairOutput(out string, res cdataSlotResult, promptToken cdataSlotToken, guardLeaks bool, marker string) ([]ParsedToolCall, error) {
	if !res.slotted && len(res.slots) == 0 && res.skeleton == "" {
		return nil, nil
	}
	restored := NormalizeMarkerText(out, marker)
	if len(res.slots) > 0 {
		var err error
		restored, err = restoreCDATASlotsFor(restored, res)
		if err != nil {
			return nil, err
		}
	}
	calls := parseToolCallsDetailedXMLOnly(restored).Calls
	if guardLeaks && toolCallsContainPlaceholderLiteral(calls, promptToken) {
		return nil, fmt.Errorf("cdata slot: placeholder literal survived into a parsed parameter")
	}
	return calls, nil
}

// toolCallsContainPlaceholderLiteral reports whether any parsed value still
// carries the placeholder prefix.
func toolCallsContainPlaceholderLiteral(calls []ParsedToolCall, token cdataSlotToken) bool {
	prefix := token.literalPrefix()
	for _, c := range calls {
		for _, v := range c.Input {
			if valueContainsSubstring(v, prefix) {
				return true
			}
		}
	}
	return false
}

func valueContainsSubstring(value any, needle string) bool {
	switch v := value.(type) {
	case string:
		return strings.Contains(v, needle)
	case []any:
		for _, item := range v {
			if valueContainsSubstring(item, needle) {
				return true
			}
		}
	case map[string]any:
		for _, item := range v {
			if valueContainsSubstring(item, needle) {
				return true
			}
		}
	}
	return false
}

// pickBetterRepairResult chooses between the two interpretations' parse results:
// more tool calls wins, then more total populated parameters. Ties go to the
// primary so the default (greedy) interpretation stays authoritative.
func pickBetterRepairResult(primary []ParsedToolCall, primaryName string, alternate []ParsedToolCall, alternateName string) ([]ParsedToolCall, string) {
	if len(alternate) == 0 {
		return primary, primaryName
	}
	if len(primary) == 0 {
		return alternate, alternateName
	}
	if len(alternate) > len(primary) {
		return alternate, alternateName
	}
	if len(alternate) == len(primary) && countToolCallInputs(alternate) > countToolCallInputs(primary) {
		return alternate, alternateName
	}
	return primary, primaryName
}

func countToolCallInputs(calls []ParsedToolCall) int {
	total := 0
	for _, c := range calls {
		total += len(c.Input)
	}
	return total
}

func alternateInterpretationName(plan toolCallRepairPlan) string {
	if plan.interpretation == "bounded" {
		return "greedy"
	}
	return "bounded"
}
