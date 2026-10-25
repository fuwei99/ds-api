package toolcall

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// realWorldSwallowedStructureBadCode is the actual malformed tool-call output
// that made a live repair pass fail. The second `edit` invoke's `old_string`
// parameter is missing its `]]>`, so the greedy CDATA scan runs on to the `]]>`
// of the FOLLOWING parameter and swallows
// `</|EPSE|parameter><|EPSE|parameter name="new_string"><![CDATA[` into a single
// slot — deleting a whole parameter node from the skeleton handed to the model.
// The fourth invoke additionally has two parameters with no `]]>` at all, which
// the greedy scan skips entirely, leaking raw content into the prompt.
const realWorldSwallowedStructureBadCode = `<|EPSE|tool_calls>
  <|EPSE|invoke name="edit">
    <|EPSE|parameter name="file_path"><![CDATA[D:\ds2api\internal\httpapi\openai\shared\deps.go]]></|EPSE|parameter>
    <|EPSE|parameter name="old_string"><![CDATA[	ExpertTextFileInlineEnabled() bool
	ExpertTextFileInlineAllowedExtensions() map[string]struct{}
	AutoRouteVisionEnabled() bool]]></|EPSE|parameter>
    <|EPSE|parameter name="new_string"><![CDATA[	ExpertTextFileInlineEnabled() bool
	AutoRouteVisionEnabled() bool]]></|EPSE|parameter>
  </|EPSE|invoke>
  <|EPSE|invoke name="edit">
    <|EPSE|parameter name="file_path"><![CDATA[D:\ds2api\internal\httpapi\openai\files\text_format.go]]></|EPSE|parameter>
    <|EPSE|parameter name="old_string"><![CDATA[// DefaultTextFileExtensions is the built-in allow-list for text-file inlining.
// Users can override it via config, but this covers the common formats that are
// safe to read as UTF-8 and insert directly into a prompt.
var DefaultTextFileExtensions = []string{</|EPSE|parameter>
    <|EPSE|parameter name="new_string"><![CDATA[// DefaultTextFileExtensions is the built-in allow-list for text-file inlining.
// It covers the common formats that are safe to read as UTF-8 and insert
// directly into a prompt.
var DefaultTextFileExtensions = []string{]]></|EPSE|parameter>
  </|EPSE|invoke>
  <|EPSE|invoke name="edit">
    <|EPSE|parameter name="file_path"><![CDATA[D:\ds2api\internal\httpapi\openai\files\text_format.go]]></|EPSE|parameter>
    <|EPSE|parameter name="old_string"><![CDATA[
// ExtensionSet converts a string slice into a lookup set. If the slice is empty
// it returns nil so that IsTextFile falls back to the built-in defaults.
func ExtensionSet(exts []string) map[string]struct{} {
	if len(exts) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(exts))
	for _, e := range exts {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		out[e] = struct{}{}
	}
	return out
}</|EPSE|parameter>
    <|EPSE|parameter name="new_string"><![CDATA[]></|EPSE|parameter>
  </|EPSE|invoke>
</|EPSE|tool_calls>`

// TestGreedySlottingSwallowsParameterShell pins the root cause: the greedy scan
// really does eat a following parameter shell on this input. It is the "before"
// half of the fix and guards against silently reverting to greedy-only slotting.
func TestGreedySlottingSwallowsParameterShell(t *testing.T) {
	greedy := slotCDATAContent(realWorldSwallowedStructureBadCode)
	if !greedy.slotted {
		t.Fatal("expected the greedy scan to slot something")
	}
	found := false
	for _, content := range greedy.slots {
		if slotContentSwallowedStructure(content) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected at least one greedy slot to have swallowed a parameter shell")
	}
	if strings.Count(greedy.skeleton, `name="new_string"`) != 2 {
		t.Fatalf("precondition changed: greedy skeleton has %d new_string params, want 2 (one swallowed)",
			strings.Count(greedy.skeleton, `name="new_string"`))
	}
	// Greedy also skips the two unclosed openers entirely, leaking raw content.
	if !strings.Contains(greedy.skeleton, "func ExtensionSet(exts []string)") {
		t.Fatal("precondition changed: greedy should leak the unclosed parameter body")
	}
}

// TestBoundedSlottingPreservesAllParameterNodes is the core assertion of the
// fix: on the real-world bad code every invoke and every parameter node survives
// into the skeleton, no slot swallowed structure, and no raw parameter body
// leaked into the prompt.
func TestBoundedSlottingPreservesAllParameterNodes(t *testing.T) {
	res := slotCDATAContentBounded(realWorldSwallowedStructureBadCode)
	if !res.slotted {
		t.Fatal("expected bounded slotting to slot content")
	}

	if got := strings.Count(res.skeleton, `<|EPSE|invoke name="edit">`); got != 3 {
		t.Fatalf("invoke count = %d, want 3", got)
	}
	for _, name := range []string{"file_path", "old_string", "new_string"} {
		if got := strings.Count(res.skeleton, `name="`+name+`"`); got != 3 {
			t.Fatalf("parameter %q count = %d, want 3", name, got)
		}
	}
	if got := len(res.slots); got != 9 {
		t.Fatalf("slot count = %d, want 9 (3 invokes x 3 params)", got)
	}
	for i, content := range res.slots {
		if slotContentSwallowedStructure(content) {
			t.Fatalf("bounded slot %d still swallowed structure: %q", i, content)
		}
	}
	// The long ExtensionSet body must be lifted out of the prompt skeleton.
	if strings.Contains(res.skeleton, "func ExtensionSet(exts []string)") {
		t.Fatalf("raw parameter body leaked into skeleton:\n%s", res.skeleton)
	}
	// Round trip must be byte-exact.
	restored, err := restoreCDATASlotsFor(res.skeleton, res)
	if err != nil {
		t.Fatalf("restore error: %v", err)
	}
	if restored != realWorldSwallowedStructureBadCode {
		t.Fatal("bounded slot round trip is not byte-exact")
	}
}

// TestBuildToolCallRepairPlanPromotesBounded verifies the repair plan picks the
// bounded interpretation for this input, keeps greedy as the alternate, and
// records a degrade reason.
func TestBuildToolCallRepairPlanPromotesBounded(t *testing.T) {
	plan := buildToolCallRepairPlan(realWorldSwallowedStructureBadCode, "")
	if plan.interpretation != "bounded" {
		t.Fatalf("interpretation = %q, want bounded", plan.interpretation)
	}
	if plan.degradeReason != "swallowed_structure" {
		t.Fatalf("degradeReason = %q, want swallowed_structure", plan.degradeReason)
	}
	if !plan.alternate.slotted {
		t.Fatal("expected greedy to be retained as the alternate interpretation")
	}
	if !strings.Contains(plan.prompt, "DS_SLOT_8") {
		t.Fatal("prompt should carry all nine placeholders")
	}
	if !strings.Contains(plan.prompt, "个参数占位符") {
		t.Fatal("prompt should carry the placeholder checklist")
	}
}

// TestRepairToolCallsWithLLMRecoversSwallowedParameter is the end-to-end fix: a
// model that repairs the bounded skeleton (adding the two missing `]]>` and
// fixing nothing else) now yields three complete edit calls. Before the fix the
// skeleton was missing a parameter node, so this was unreachable.
func TestRepairToolCallsWithLLMRecoversSwallowedParameter(t *testing.T) {
	var seen string
	invoke := func(_ context.Context, prompt string) (string, error) {
		seen = prompt
		// Emulate a well-behaved model: copy the skeleton verbatim and supply
		// the missing `]]>` before each unclosed parameter close tag, changing
		// nothing else. LastIndex, not Index: the format spec that prefixes the
		// prompt contains `<|EPSE|tool_calls>` samples of its own.
		skeleton := prompt[strings.LastIndex(prompt, "<|EPSE|tool_calls>"):]
		for i := 0; i < 9; i++ {
			unclosed := "DS_SLOT_" + strconv.Itoa(i) + "</|EPSE|parameter>"
			closed := "DS_SLOT_" + strconv.Itoa(i) + "]]></|EPSE|parameter>"
			skeleton = strings.ReplaceAll(skeleton, unclosed, closed)
		}
		return skeleton, nil
	}
	calls, ok := RepairToolCallsWithLLM(context.Background(), realWorldSwallowedStructureBadCode, invoke)
	if !ok {
		t.Fatal("expected repair to succeed")
	}
	if len(calls) != 3 {
		t.Fatalf("repaired call count = %d, want 3", len(calls))
	}
	for i, c := range calls {
		if c.Name != "edit" {
			t.Fatalf("call %d name = %q, want edit", i, c.Name)
		}
		for _, key := range []string{"file_path", "old_string", "new_string"} {
			if _, ok := c.Input[key]; !ok {
				t.Fatalf("call %d missing parameter %q (input=%#v)", i, key, c.Input)
			}
		}
	}
	// The second invoke's new_string must be the original bytes, restored.
	second, _ := calls[1].Input["new_string"].(string)
	if !strings.Contains(second, "// It covers the common formats") {
		t.Fatalf("second call new_string not restored verbatim: %q", second)
	}
	if strings.Contains(seen, "func ExtensionSet(exts []string)") {
		t.Fatal("prompt must not carry raw parameter bodies")
	}
}

// TestBoundedScanKeepsGreedyForLegitimateMarkupInParameter is the anti-regression
// anchor for the disambiguation rule. A parameter value may legitimately contain
// `</|EPSE|parameter>` text as long as its own `]]>` follows. The swallow
// signature deliberately also requires a further CDATA opener inside the body, so
// this input keeps the greedy interpretation untouched.
func TestBoundedScanKeepsGreedyForLegitimateMarkupInParameter(t *testing.T) {
	text := `<|EPSE|tool_calls><|EPSE|invoke name="write">` +
		`<|EPSE|parameter name="content"><![CDATA[docs say: a value ends with </|EPSE|parameter> and that is the shape]]></|EPSE|parameter>` +
		`</|EPSE|invoke></|EPSE|tool_calls>`

	greedy := slotCDATAContent(text)
	if len(greedy.slots) != 1 {
		t.Fatalf("greedy slot count = %d, want 1", len(greedy.slots))
	}
	degraded, reason := greedySlottingDegraded(greedy, slotCDATAContentBounded(text))
	if degraded {
		t.Fatalf("legitimate markup-in-parameter must not degrade to bounded (reason=%s)", reason)
	}
	plan := buildToolCallRepairPlan(text, "")
	if plan.interpretation != "greedy" {
		t.Fatalf("interpretation = %q, want greedy", plan.interpretation)
	}
	// The whole documented sample must live in one slot, not be truncated.
	if !strings.HasSuffix(greedy.slots[0], "is the shape") {
		t.Fatalf("greedy slot was truncated: %q", greedy.slots[0])
	}
}

// TestNestedCDATASampleResolvesToGreedyViaPickBetter covers the genuinely
// ambiguous case the bounded scan cannot decide on its own: a parameter whose
// value embeds a full tool-call sample, nested `<![CDATA[` included. That input
// does match the swallow signature, so bounded becomes primary — but the greedy
// alternate produces the richer parse and wins.
//
// The asserted invariant is parity with the deterministic parser: repair must
// yield exactly what parsing the equivalent well-formed input yields. (That
// baseline itself truncates at the inner `]]>`, which is pre-existing
// findToolCDATAEnd behaviour and out of scope here; the point is that repair does
// not do worse.)
func TestNestedCDATASampleResolvesToGreedyViaPickBetter(t *testing.T) {
	sample := `docs say: <|EPSE|parameter name="x"><![CDATA[v]]></|EPSE|parameter> is the shape`
	bad := `<|EPSE|call name="write">` +
		`<|EPSE|parameter name="content"><![CDATA[` + sample + `]]></|EPSE|parameter>` +
		`</|EPSE|call>`
	wellFormed := `<|EPSE|tool_calls><|EPSE|invoke name="write">` +
		`<|EPSE|parameter name="content"><![CDATA[` + sample + `]]></|EPSE|parameter>` +
		`</|EPSE|invoke></|EPSE|tool_calls>`

	plan := buildToolCallRepairPlan(bad, "")
	if plan.interpretation != "bounded" || plan.degradeReason != "swallowed_structure" {
		t.Fatalf("precondition: interpretation=%q reason=%q, want bounded/swallowed_structure",
			plan.interpretation, plan.degradeReason)
	}
	// A model that fixes only the invoke local name, reproducing the greedy
	// placeholder verbatim.
	invoke := func(_ context.Context, _ string) (string, error) {
		return `<|EPSE|tool_calls><|EPSE|invoke name="write">` +
			`<|EPSE|parameter name="content"><![CDATA[` + plan.alternate.token.format(0) + `]]></|EPSE|parameter>` +
			`</|EPSE|invoke></|EPSE|tool_calls>`, nil
	}
	calls, ok := RepairToolCallsWithLLM(context.Background(), bad, invoke)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected the greedy alternate to win, got ok=%v calls=%#v", ok, calls)
	}

	baseline := parseToolCallsDetailedXMLOnly(wellFormed).Calls
	if len(baseline) != 1 {
		t.Fatalf("baseline parse should yield one call, got %d", len(baseline))
	}
	if !reflect.DeepEqual(calls[0].Input, baseline[0].Input) {
		t.Fatalf("repair diverged from the deterministic parser:\n got: %#v\nwant: %#v",
			calls[0].Input, baseline[0].Input)
	}
}

// realWorldTruncatedCloseBadCode is a second real failure: every `]]>` lost its
// `>`, leaving `]]` before each parameter close tag. The greedy scan finds no
// `]]>` anywhere, slots nothing, and the model receives the raw text with no
// placeholder guidance.
const realWorldTruncatedCloseBadCode = `<|EPSE|tool_calls>
  <|EPSE|invoke name="process">
    <|EPSE|parameter name="action"><![CDATA[poll]]</|EPSE|parameter>
    <|EPSE|parameter name="session_id"><![CDATA[proc_958ca2e26a44]]</|EPSE|parameter>
  </|EPSE|invoke>
</|EPSE|tool_calls>`

// TestTruncatedCDATACloseKeepsDefectVisible covers the `]]` (missing `>`) shape.
// The truncated close marker must stay in the skeleton rather than being absorbed
// into the slot body: the model has to see the defect to fix it, and a `]]`
// hidden inside a slot would come back as a stray suffix on the parameter value.
func TestTruncatedCDATACloseKeepsDefectVisible(t *testing.T) {
	greedy := slotCDATAContent(realWorldTruncatedCloseBadCode)
	if greedy.slotted {
		t.Fatal("precondition: greedy finds no ]]> and must slot nothing")
	}

	res := slotCDATAContentBounded(realWorldTruncatedCloseBadCode)
	if len(res.slots) != 2 {
		t.Fatalf("bounded slot count = %d, want 2", len(res.slots))
	}
	// Slot bodies must be the clean values, with no `]]` tail.
	if res.slots[0] != "poll" || res.slots[1] != "proc_958ca2e26a44" {
		t.Fatalf("slot bodies absorbed the truncated close marker: %#v", res.slots)
	}
	// The defect (`]]` with no `>`) must remain visible in the skeleton.
	if got := strings.Count(res.skeleton, "]]</|EPSE|parameter>"); got != 2 {
		t.Fatalf("truncated close markers in skeleton = %d, want 2:\n%s", got, res.skeleton)
	}
	restored, err := restoreCDATASlotsFor(res.skeleton, res)
	if err != nil {
		t.Fatalf("restore error: %v", err)
	}
	if restored != realWorldTruncatedCloseBadCode {
		t.Fatalf("round trip mismatch:\n got: %q\nwant: %q", restored, realWorldTruncatedCloseBadCode)
	}
}

// TestRepairToolCallsWithLLMRecoversTruncatedClose is the end-to-end result for
// the `]]` shape: a model that supplies the missing `>` yields one complete call
// with both parameters carrying their exact original values.
func TestRepairToolCallsWithLLMRecoversTruncatedClose(t *testing.T) {
	invoke := func(_ context.Context, prompt string) (string, error) {
		skeleton := prompt[strings.LastIndex(prompt, "<|EPSE|tool_calls>"):]
		return strings.ReplaceAll(skeleton, "]]</|EPSE|parameter>", "]]></|EPSE|parameter>"), nil
	}
	calls, ok := RepairToolCallsWithLLM(context.Background(), realWorldTruncatedCloseBadCode, invoke)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected one repaired call, got ok=%v calls=%#v", ok, calls)
	}
	if calls[0].Name != "process" {
		t.Fatalf("call name = %q, want process", calls[0].Name)
	}
	want := map[string]string{"action": "poll", "session_id": "proc_958ca2e26a44"}
	for key, expected := range want {
		got, _ := calls[0].Input[key].(string)
		if got != expected {
			t.Fatalf("parameter %q = %q, want %q", key, got, expected)
		}
	}
}

// TestRepairToolCallsWithLLMTruncatedCloseNotFixedDegrades pins the safety side:
// a model that copies the `]]` skeleton verbatim without supplying the `>` must
// degrade cleanly rather than leak a DS_SLOT_ literal as a parameter value.
func TestRepairToolCallsWithLLMTruncatedCloseNotFixedDegrades(t *testing.T) {
	invoke := func(_ context.Context, prompt string) (string, error) {
		return prompt[strings.LastIndex(prompt, "<|EPSE|tool_calls>"):], nil
	}
	calls, ok := RepairToolCallsWithLLM(context.Background(), realWorldTruncatedCloseBadCode, invoke)
	if ok || len(calls) != 0 {
		t.Fatalf("expected clean degradation, got ok=%v calls=%#v", ok, calls)
	}
}

// TestRestoreAndParseRejectsLeakedPlaceholderValue covers the leak guard
// directly: when an interpretation's slot table does not line up with the model
// output, restoration can leave a placeholder literal sitting inside a CDATA
// body, and that must be rejected instead of surfacing as a parameter value.
func TestRestoreAndParseRejectsLeakedPlaceholderValue(t *testing.T) {
	res := slotCDATAContentBounded(realWorldTruncatedCloseBadCode)
	// An output that carries no placeholder at all for slot 1: restoration under
	// this table would fail on the missing placeholder. Use an output where both
	// placeholders survive but one is nested so it reaches a parsed value.
	out := `<|EPSE|tool_calls><|EPSE|invoke name="process">` +
		`<|EPSE|parameter name="action"><![CDATA[` + res.token.format(0) + `]]></|EPSE|parameter>` +
		`<|EPSE|parameter name="session_id"><![CDATA[` + res.token.format(1) + `]]></|EPSE|parameter>` +
		`</|EPSE|invoke></|EPSE|tool_calls>`
	// Restoring with an EMPTY slot table leaves both literals in place; the guard
	// must reject the resulting parse.
	calls, err := restoreAndParseRepairOutput(out, cdataSlotResult{skeleton: out, slotted: true}, res.token, true, "")
	if err == nil {
		t.Fatalf("expected the leak guard to reject placeholder literals, got calls=%#v", calls)
	}
	if !strings.Contains(err.Error(), "placeholder literal survived") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestBoundedScanSlotsTrailingUnclosedCDATA covers the second degradation
// trigger: a trailing unclosed CDATA that the greedy scan skips entirely (leaking
// raw content into the prompt) is slotted by the bounded scan with no close
// marker, and round-trips byte-exactly.
func TestBoundedScanSlotsTrailingUnclosedCDATA(t *testing.T) {
	text := `<|EPSE|invoke name="Bash"><|EPSE|parameter name="command"><![CDATA[` +
		strings.Repeat("x", 5000) + `</|EPSE|parameter></|EPSE|invoke>`

	greedy := slotCDATAContent(text)
	if greedy.slotted {
		t.Fatal("precondition: greedy must skip the unclosed opener entirely")
	}
	bounded := slotCDATAContentBounded(text)
	if len(bounded.slots) != 1 {
		t.Fatalf("bounded slot count = %d, want 1", len(bounded.slots))
	}
	if !strings.Contains(bounded.skeleton, "<![CDATA[DS_SLOT_0</|EPSE|parameter>") {
		t.Fatalf("expected the missing ]]> to stay visible in the skeleton: %q", bounded.skeleton)
	}
	if strings.Contains(bounded.skeleton, strings.Repeat("x", 100)) {
		t.Fatal("raw body leaked into the bounded skeleton")
	}
	degraded, reason := greedySlottingDegraded(greedy, bounded)
	if !degraded || reason != "unslotted_unclosed_cdata" {
		t.Fatalf("degraded=%v reason=%q, want true/unslotted_unclosed_cdata", degraded, reason)
	}
	restored, err := restoreCDATASlotsFor(bounded.skeleton, bounded)
	if err != nil {
		t.Fatalf("restore error: %v", err)
	}
	if restored != text {
		t.Fatalf("round trip mismatch:\n got: %q\nwant: %q", restored, text)
	}
}

// TestRepairToolCallsWithLLMUnclosedPlaceholderNotLeaked covers the safety
// requirement for the bounded path: a model that copies the skeleton without
// supplying the missing `]]>` must degrade cleanly, never surfacing a DS_SLOT_
// literal or a half-repaired call downstream.
func TestRepairToolCallsWithLLMUnclosedPlaceholderNotLeaked(t *testing.T) {
	bad := `<|EPSE|invoke name="Bash"><|EPSE|parameter name="command"><![CDATA[secret payload` +
		`</|EPSE|parameter></|EPSE|invoke>`
	invoke := func(_ context.Context, prompt string) (string, error) {
		return prompt[strings.LastIndex(prompt, "<|EPSE|invoke"):], nil
	}
	calls, ok := RepairToolCallsWithLLM(context.Background(), bad, invoke)
	if ok || len(calls) != 0 {
		t.Fatalf("expected clean degradation, got ok=%v calls=%#v", ok, calls)
	}
}

// TestPickBetterRepairResultPrefersRicherParse covers the tie-breaking rules of
// the two-interpretation choice: more calls wins, then more populated
// parameters, and an exact tie keeps the primary authoritative.
func TestPickBetterRepairResultPrefersRicherParse(t *testing.T) {
	one := []ParsedToolCall{{Name: "a", Input: map[string]any{"x": "1"}}}
	two := []ParsedToolCall{
		{Name: "a", Input: map[string]any{"x": "1"}},
		{Name: "b", Input: map[string]any{"y": "2"}},
	}
	richer := []ParsedToolCall{{Name: "a", Input: map[string]any{"x": "1", "y": "2"}}}

	if _, src := pickBetterRepairResult(one, "greedy", two, "bounded"); src != "bounded" {
		t.Fatalf("more calls should win, got %q", src)
	}
	if _, src := pickBetterRepairResult(one, "greedy", richer, "bounded"); src != "bounded" {
		t.Fatalf("more populated parameters should win, got %q", src)
	}
	if _, src := pickBetterRepairResult(one, "greedy", one, "bounded"); src != "greedy" {
		t.Fatalf("an exact tie must keep the primary, got %q", src)
	}
	if _, src := pickBetterRepairResult(nil, "bounded", one, "greedy"); src != "greedy" {
		t.Fatalf("an empty primary must yield to the alternate, got %q", src)
	}
	if _, src := pickBetterRepairResult(one, "greedy", nil, "bounded"); src != "greedy" {
		t.Fatalf("an empty alternate must not win, got %q", src)
	}
}

// TestRepairToolCallsWithLLMAlternateInterpretationWins exercises the择优 path
// end to end: the plan promotes bounded, but the model's output only restores
// under the greedy slot table, and the greedy parse still yields a call.
func TestRepairToolCallsWithLLMAlternateInterpretationWins(t *testing.T) {
	// Bounded degradation trigger (unclosed CDATA that greedy skips) combined
	// with a well-formed invoke greedy can slot.
	bad := `<|EPSE|call name="Bash"><|EPSE|parameter name="command"><![CDATA[pwd]]></|EPSE|parameter>` +
		`<|EPSE|parameter name="stray"><![CDATA[tail` +
		`</|EPSE|parameter></|EPSE|call>`
	plan := buildToolCallRepairPlan(bad, "")
	if plan.interpretation != "bounded" {
		t.Fatalf("precondition: interpretation = %q, want bounded", plan.interpretation)
	}
	// The model answers with a shape that only the greedy table can restore:
	// one placeholder, correctly closed.
	invoke := func(_ context.Context, _ string) (string, error) {
		return `<|EPSE|tool_calls><|EPSE|invoke name="Bash">` +
			`<|EPSE|parameter name="command"><![CDATA[` + plan.alternate.token.format(0) + `]]></|EPSE|parameter>` +
			`</|EPSE|invoke></|EPSE|tool_calls>`, nil
	}
	calls, ok := RepairToolCallsWithLLM(context.Background(), bad, invoke)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected the greedy alternate to win, got ok=%v calls=%#v", ok, calls)
	}
	if got, _ := calls[0].Input["command"].(string); got != "pwd" {
		t.Fatalf("command = %q, want pwd", got)
	}
}
