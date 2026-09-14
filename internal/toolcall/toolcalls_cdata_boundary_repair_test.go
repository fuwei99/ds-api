package toolcall

import (
	"strings"
	"testing"
)

// swallowedNextParameterBadCode is the shape reported from a live session: the
// `command` parameter's CDATA has no `]]>` of its own, so the unbounded
// findToolCDATAEnd search runs past the element and terminates on the `]]>` of
// the FOLLOWING `description` parameter. Before the element-boundary repair the
// parser returned a single-parameter call whose `command` value carried
// `</|EPSE|parameter><|EPSE|parameter name="description"><![CDATA[...` — the
// tool-call framing leaked into a parameter value and `description` vanished.
const swallowedNextParameterBadCode = `<|EPSE|tool_calls>
  <|EPSE|invoke name="pwsh">
    <|EPSE|parameter name="command"><![CDATA[$files = Get-ChildItem -Recurse -Filter *.go -File
$rx = [regex]'(?:errors\.New|fmt\.Errorf)\s*\(\s*"([^"]*)"'
Write-Host "TOTAL: $($all.Count)"</|EPSE|parameter>
    <|EPSE|parameter name="description"><![CDATA[Extract and count duplicate error message strings in Go files]]></|EPSE|parameter>
  </|EPSE|invoke>
</|EPSE|tool_calls>`

const swallowedNextParameterWantCommand = `$files = Get-ChildItem -Recurse -Filter *.go -File
$rx = [regex]'(?:errors\.New|fmt\.Errorf)\s*\(\s*"([^"]*)"'
Write-Host "TOTAL: $($all.Count)"`

// TestUnclosedCDATADoesNotSwallowNextParameter is the core assertion: both
// parameters survive and neither value carries tool-call framing.
func TestUnclosedCDATADoesNotSwallowNextParameter(t *testing.T) {
	calls := ParseToolCalls(swallowedNextParameterBadCode, nil)
	if len(calls) != 1 {
		t.Fatalf("call count = %d, want 1 (calls=%#v)", len(calls), calls)
	}
	if calls[0].Name != "pwsh" {
		t.Fatalf("call name = %q, want pwsh", calls[0].Name)
	}
	if len(calls[0].Input) != 2 {
		t.Fatalf("parameter count = %d, want 2 (input=%#v)", len(calls[0].Input), calls[0].Input)
	}
	command, _ := calls[0].Input["command"].(string)
	if command != swallowedNextParameterWantCommand {
		t.Fatalf("command value not bounded to its own element:\n got: %q\nwant: %q",
			command, swallowedNextParameterWantCommand)
	}
	if want := "Extract and count duplicate error message strings in Go files"; calls[0].Input["description"] != want {
		t.Fatalf("description = %#v, want %q", calls[0].Input["description"], want)
	}
	// The leak this regression is about: no tool-call framing inside a value.
	for name, value := range calls[0].Input {
		text, _ := value.(string)
		for _, marker := range []string{"EPSE", "<![CDATA[", "</parameter", "<parameter"} {
			if strings.Contains(text, marker) {
				t.Fatalf("parameter %q leaked tool-call framing %q: %q", name, marker, text)
			}
		}
	}
}

// TestConsecutiveUnclosedCDATAParametersAllSurvive covers the chained shape: two
// adjacent parameters both missing `]]>` present as one greedy range, so the
// repair has to run to a fixpoint rather than a single pass.
func TestConsecutiveUnclosedCDATAParametersAllSurvive(t *testing.T) {
	text := `<|EPSE|tool_calls><|EPSE|invoke name="Bash">` +
		`<|EPSE|parameter name="command"><![CDATA[echo one</|EPSE|parameter>` +
		`<|EPSE|parameter name="description"><![CDATA[two</|EPSE|parameter>` +
		`<|EPSE|parameter name="workdir"><![CDATA[/tmp]]></|EPSE|parameter>` +
		`</|EPSE|invoke></|EPSE|tool_calls>`

	calls := ParseToolCalls(text, nil)
	if len(calls) != 1 {
		t.Fatalf("call count = %d, want 1", len(calls))
	}
	want := map[string]string{"command": "echo one", "description": "two", "workdir": "/tmp"}
	if len(calls[0].Input) != len(want) {
		t.Fatalf("parameter count = %d, want %d (input=%#v)", len(calls[0].Input), len(want), calls[0].Input)
	}
	for key, expected := range want {
		if got, _ := calls[0].Input[key].(string); got != expected {
			t.Fatalf("parameter %q = %q, want %q", key, got, expected)
		}
	}
}

// TestQuotedToolCallExampleKeepsGreedyCDATAInterpretation is the negative that
// bounds the repair. A Write payload whose value quotes a complete tool-call
// example also contains `<|EPSE|parameter ...><![CDATA[` inside its CDATA body,
// but it never escaped its own element, so the value must come back byte-exact.
func TestQuotedToolCallExampleKeepsGreedyCDATAInterpretation(t *testing.T) {
	content := strings.Join([]string{
		"# How to call a tool",
		"",
		"```xml",
		`<|EPSE|tool_calls>`,
		`  <|EPSE|invoke name="Bash">`,
		`    <|EPSE|parameter name="command"><![CDATA[echo hi]]></|EPSE|parameter>`,
		`  </|EPSE|invoke>`,
		`</|EPSE|tool_calls>`,
		"```",
	}, "\n")
	text := `<|EPSE|tool_calls><|EPSE|invoke name="Write">` +
		`<|EPSE|parameter name="content"><![CDATA[` + content + `]]></|EPSE|parameter>` +
		`<|EPSE|parameter name="file_path"><![CDATA[docs/tools.md]]></|EPSE|parameter>` +
		`</|EPSE|invoke></|EPSE|tool_calls>`

	calls := ParseToolCalls(text, nil)
	if len(calls) != 1 || len(calls[0].Input) != 2 {
		t.Fatalf("expected one call with two parameters, got %#v", calls)
	}
	if got, _ := calls[0].Input["content"].(string); got != content {
		t.Fatalf("quoted tool-call example was mutated:\n got: %q\nwant: %q", got, content)
	}
	if got, _ := calls[0].Input["file_path"].(string); got != "docs/tools.md" {
		t.Fatalf("file_path = %q, want docs/tools.md", got)
	}
}

// TestProseCDATAMentionKeepsGreedyCDATAInterpretation is the second negative: a
// value that mentions a bare unclosed `<![CDATA[` in prose has an unbalanced
// opener but no preceding close tag, so the repair must not fire.
func TestProseCDATAMentionKeepsGreedyCDATAInterpretation(t *testing.T) {
	content := "the <![CDATA[ opener is what protects a long value"
	text := `<|EPSE|tool_calls><|EPSE|invoke name="Write">` +
		`<|EPSE|parameter name="content"><![CDATA[` + content + `]]></|EPSE|parameter>` +
		`<|EPSE|parameter name="file_path"><![CDATA[notes.md]]></|EPSE|parameter>` +
		`</|EPSE|invoke></|EPSE|tool_calls>`

	calls := ParseToolCalls(text, nil)
	if len(calls) != 1 || len(calls[0].Input) != 2 {
		t.Fatalf("expected one call with two parameters, got %#v", calls)
	}
	if got, _ := calls[0].Input["content"].(string); got != content {
		t.Fatalf("prose CDATA mention was mutated:\n got: %q\nwant: %q", got, content)
	}
}

// TestSwallowedCDATARepairLeavesWellFormedMarkupUntouched pins the no-op path:
// the repair must be byte-identical on input it has no business changing.
func TestSwallowedCDATARepairLeavesWellFormedMarkupUntouched(t *testing.T) {
	text := `<|EPSE|tool_calls><|EPSE|invoke name="Bash">` +
		`<|EPSE|parameter name="command"><![CDATA[echo hi]]></|EPSE|parameter>` +
		`<|EPSE|parameter name="description"><![CDATA[greet]]></|EPSE|parameter>` +
		`</|EPSE|invoke></|EPSE|tool_calls>`
	if got := repairSwallowedCDATARanges(text); got != text {
		t.Fatalf("well-formed markup was rewritten:\n got: %q\nwant: %q", got, text)
	}
}

// TestSwallowedCDATARepairKeepsIntentFallbackForUnrepairableCode pins that the
// repair does not shadow the LLM fallback: input it cannot improve still reports
// zero calls plus a tool-call intent, which is what routes to repair.
func TestSwallowedCDATARepairKeepsIntentFallbackForUnrepairableCode(t *testing.T) {
	parsed := parseToolCallsDetailedXMLOnly(realWorldSwallowedStructureBadCode)
	if len(parsed.Calls) != 0 {
		t.Fatalf("call count = %d, want 0 (input is not deterministically repairable)", len(parsed.Calls))
	}
	if !parsed.SawToolCallIntent {
		t.Fatal("SawToolCallIntent = false, want true so the LLM repair path still runs")
	}
}
