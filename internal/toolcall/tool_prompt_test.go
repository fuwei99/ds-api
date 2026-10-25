package toolcall

import (
	"strings"
	"testing"
)

func TestBuildToolCallInstructions_ExecCommandUsesCmdExample(t *testing.T) {
	out := BuildToolCallInstructions([]string{"exec_command"})
	if !strings.Contains(out, `<|EPSE|invoke name="exec_command">`) {
		t.Fatalf("expected exec_command in examples, got: %s", out)
	}
	if !strings.Contains(out, `<|EPSE|parameter name="cmd"><![CDATA[pwd]]></|EPSE|parameter>`) {
		t.Fatalf("expected cmd parameter example for exec_command, got: %s", out)
	}
}

func TestBuildToolCallInstructions_ExecuteCommandUsesCommandExample(t *testing.T) {
	out := BuildToolCallInstructions([]string{"execute_command"})
	if !strings.Contains(out, `<|EPSE|invoke name="execute_command">`) {
		t.Fatalf("expected execute_command in examples, got: %s", out)
	}
	if !strings.Contains(out, `<|EPSE|parameter name="command"><![CDATA[pwd]]></|EPSE|parameter>`) {
		t.Fatalf("expected command parameter example for execute_command, got: %s", out)
	}
}

func TestBuildToolCallInstructions_BashUsesCommandAndDescriptionExamples(t *testing.T) {
	out := BuildToolCallInstructions([]string{"Bash"})
	blocks := findInvokeBlocks(out, "Bash")
	if len(blocks) == 0 {
		t.Fatalf("expected Bash examples, got: %s", out)
	}

	sawDescription := false
	for _, block := range blocks {
		if !strings.Contains(block, `<|EPSE|parameter name="command">`) {
			t.Fatalf("expected every Bash example to use command parameter, got: %s", block)
		}
		if strings.Contains(block, `<|EPSE|parameter name="path">`) || strings.Contains(block, `<|EPSE|parameter name="content">`) {
			t.Fatalf("expected Bash examples not to use file write parameters, got: %s", block)
		}
		if strings.Contains(block, `<|EPSE|parameter name="description">`) {
			sawDescription = true
		}
	}
	if !sawDescription {
		t.Fatalf("expected Bash long-script example to include description, got: %s", out)
	}
	if strings.Contains(out, `<|EPSE|invoke name="Read">`) {
		t.Fatalf("expected examples to avoid unavailable hard-coded Read tool, got: %s", out)
	}
}

func TestBuildToolCallInstructions_ExecuteCommandLongScriptUsesCommand(t *testing.T) {
	out := BuildToolCallInstructions([]string{"execute_command"})
	blocks := findInvokeBlocks(out, "execute_command")
	if len(blocks) == 0 {
		t.Fatalf("expected execute_command examples, got: %s", out)
	}

	for _, block := range blocks {
		if !strings.Contains(block, `<|EPSE|parameter name="command">`) {
			t.Fatalf("expected execute_command examples to use command parameter, got: %s", block)
		}
		if strings.Contains(block, `<|EPSE|parameter name="path">`) || strings.Contains(block, `<|EPSE|parameter name="content">`) {
			t.Fatalf("expected execute_command examples not to use file write parameters, got: %s", block)
		}
	}
	if !strings.Contains(out, `test_escape.sh`) {
		t.Fatalf("expected execute_command long-script example, got: %s", out)
	}
}

func TestBuildToolCallInstructions_ExecCommandLongScriptUsesCmd(t *testing.T) {
	out := BuildToolCallInstructions([]string{"exec_command"})
	blocks := findInvokeBlocks(out, "exec_command")
	if len(blocks) == 0 {
		t.Fatalf("expected exec_command examples, got: %s", out)
	}

	for _, block := range blocks {
		if !strings.Contains(block, `<|EPSE|parameter name="cmd">`) {
			t.Fatalf("expected exec_command examples to use cmd parameter, got: %s", block)
		}
		if strings.Contains(block, `<|EPSE|parameter name="command">`) || strings.Contains(block, `<|EPSE|parameter name="path">`) || strings.Contains(block, `<|EPSE|parameter name="content">`) {
			t.Fatalf("expected exec_command examples not to use command or file write parameters, got: %s", block)
		}
	}
	if !strings.Contains(out, `test_escape.sh`) {
		t.Fatalf("expected exec_command long-script example, got: %s", out)
	}
}

func TestBuildToolCallInstructions_WriteUsesFilePathAndContent(t *testing.T) {
	out := BuildToolCallInstructions([]string{"Write"})
	blocks := findInvokeBlocks(out, "Write")
	if len(blocks) == 0 {
		t.Fatalf("expected Write examples, got: %s", out)
	}

	for _, block := range blocks {
		if !strings.Contains(block, `<|EPSE|parameter name="file_path">`) || !strings.Contains(block, `<|EPSE|parameter name="content">`) {
			t.Fatalf("expected Write examples to use file_path and content, got: %s", block)
		}
		if strings.Contains(block, `<|EPSE|parameter name="path">`) {
			t.Fatalf("expected Write examples not to use path, got: %s", block)
		}
	}
}

func TestBuildToolCallInstructions_AnchorsMissingOpeningWrapperFailureMode(t *testing.T) {
	out := BuildToolCallInstructions([]string{"read_file"})
	if !strings.Contains(out, "切勿省略起始标签 <|EPSE|tool_calls>") {
		t.Fatalf("expected explicit missing-opening-tag warning, got: %s", out)
	}
	if !strings.Contains(out, "Wrong 3 — 缺少起始包裹标签") {
		t.Fatalf("expected missing-opening-wrapper negative example, got: %s", out)
	}
}

func TestBuildToolCallInstructions_RejectsEmptyParametersInPrompt(t *testing.T) {
	out := BuildToolCallInstructions([]string{"Bash"})
	for _, want := range []string{
		"请勿输出占位符、空参数或仅包含空白字符的参数。",
		"如果所需的参数值未知，请询问用户或正常回答，而不是输出空的工具调用。",
		"切勿在命令为空的情况下调用它们。",
		"Wrong 4 — 参数为空",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected empty-parameter instruction %q, got: %s", want, out)
		}
	}
}

func TestBuildToolCallInstructions_UsesPositiveTagPunctuationAlphabet(t *testing.T) {
	out := BuildToolCallInstructions([]string{"Bash"})
	want := `标签语法中所允许使用的标点符号字符集仅限 ASCII 的 < > / = " 以及半角竖线 |。`
	if !strings.Contains(out, want) {
		t.Fatalf("expected positive tag punctuation alphabet %q, got: %s", want, out)
	}
	for _, bad := range []string{"lookalike", "substitute", "！", "〈", "〉", "“", "”"} {
		if strings.Contains(out, bad) {
			t.Fatalf("tool prompt should not include negative punctuation examples %q, got: %s", bad, out)
		}
	}
}

func TestToolCallFormatReminder_RestatesSkeletonVerbatim(t *testing.T) {
	out := ToolCallFormatReminder()
	want := `The only correct format for using tools is <|EPSE|tool_calls>
  <|EPSE|invoke name="TOOL_NAME_HERE">
    <|EPSE|parameter name="PARAMETER_NAME"><![CDATA[PARAMETER_VALUE]]></|EPSE|parameter>
  </|EPSE|invoke>
</|EPSE|tool_calls> .Please do not simplify or modify this framework in any way.The only valid label is EPSE; the use of any label prefixed with DSML is **strictly prohibited**.`
	if out != want {
		t.Fatalf("reminder must stay byte-identical, got: %q", out)
	}
}

func TestToolCallFormatReminderForMarker(t *testing.T) {
	const marker = "Q7ZK3M"
	out := ToolCallFormatReminderForMarker(marker)
	if strings.Contains(out, EPSEKeyword) {
		t.Fatalf("expected marker to replace EPSE, got %q", out)
	}
	if !strings.Contains(out, "<|"+marker+"|tool_calls>") {
		t.Fatalf("expected marker in skeleton, got %q", out)
	}
	if !strings.Contains(out, "The only valid label is "+marker) {
		t.Fatalf("expected label name to be replaced with marker, got %q", out)
	}
}

func TestToolCallFormatSpec_DoesNotEmbedReminder(t *testing.T) {
	// The reminder is injected separately as the last system turn; keeping it out
	// of the spec avoids duplicating it inside the descriptions block.
	if strings.Contains(ToolCallFormatSpec(), "The only correct format for using tools is") {
		t.Fatalf("format spec should not embed the trailing reminder")
	}
	if strings.Contains(BuildToolCallInstructions([]string{"Bash"}), "The only correct format for using tools is") {
		t.Fatalf("tool instructions should not embed the trailing reminder")
	}
}

func findInvokeBlocks(text, name string) []string {
	open := `<|EPSE|invoke name="` + name + `">`
	remaining := text
	blocks := []string{}
	for {
		start := strings.Index(remaining, open)
		if start < 0 {
			return blocks
		}
		remaining = remaining[start:]
		end := strings.Index(remaining, `</|EPSE|invoke>`)
		if end < 0 {
			return blocks
		}
		end += len(`</|EPSE|invoke>`)
		blocks = append(blocks, remaining[:end])
		remaining = remaining[end:]
	}
}
