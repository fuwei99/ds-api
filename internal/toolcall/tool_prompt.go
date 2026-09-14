package toolcall

import "strings"

// BuildToolCallInstructions generates the unified tool-calling instruction block
// used by all adapters (OpenAI, Claude, Gemini). It uses attention-optimized
// structure: rules → negative examples → positive examples → anchor.
//
// The toolNames slice should contain the actual tool names available in the
// current request; the function picks real names for examples.
func BuildToolCallInstructions(toolNames []string) string {
	return BuildToolCallInstructionsForMarker(toolNames, EPSEKeyword)
}

// BuildToolCallInstructionsForMarker is the marker-aware variant of
// BuildToolCallInstructions: every canonical EPSE keyword in the format spec and
// examples is rendered as the caller-specific marker. Parsing normalizes the
// marker back to EPSE before the sieve/parser run, so the marker never leaks
// into the internal tool-call model.
func BuildToolCallInstructionsForMarker(toolNames []string, marker string) string {
	marker = normalizeToolMarker(marker)
	return ToolCallFormatSpecForMarker(marker) + buildCorrectToolExamples(toolNames, marker)
}

// normalizeToolMarker maps an empty/whitespace marker to the canonical keyword.
func normalizeToolMarker(marker string) string {
	marker = strings.TrimSpace(marker)
	if marker == "" {
		return EPSEKeyword
	}
	return marker
}

// ToolCallFormatSpec returns the tool-call format specification verbatim: the
// "工具调用格式规范 — 请严格遵照执行：" block including rules 1–15 and the closing
// "请记住：使用工具的唯一正确方式…" anchor. It is the tool-name-independent portion
// of BuildToolCallInstructions and is reused verbatim by the LLM tool-call
// repair prompt (see plan/tool-call-fallback-design-phase3.md §1). It must stay
// byte-identical to what BuildToolCallInstructions injects into the system
// prompt so the repair model sees exactly the same rules the generating model
// saw.
func ToolCallFormatSpec() string {
	return ToolCallFormatSpecForMarker(EPSEKeyword)
}

// ToolCallFormatSpecForMarker is the marker-aware variant of ToolCallFormatSpec.
// The spec is written with the canonical EPSE keyword and the keyword is
// substituted with marker at the end, so the rules, examples and anchor stay
// structurally identical (only the tag keyword changes).
func ToolCallFormatSpecForMarker(marker string) string {
	marker = normalizeToolMarker(marker)
	spec := `工具调用格式规范 — 请严格遵照执行：

<|EPSE|tool_calls>
  <|EPSE|invoke name="TOOL_NAME_HERE">
    <|EPSE|parameter name="PARAMETER_NAME"><![CDATA[PARAMETER_VALUE]]></|EPSE|parameter>
  </|EPSE|invoke>
</|EPSE|tool_calls>

规则说明：
1) 必须使用 <|EPSE|tool_calls> 标签作为整体的封装容器。
2) 允许在一个 <|EPSE|tool_calls> 根节点内部放置一个或多个 <|EPSE|invoke> 调用项。
3) 需在 invoke 元素的 name 属性中明确指定具体工具名称：<|EPSE|invoke name="TOOL_NAME">。
3a) 标签语法中所允许使用的标点符号字符集仅限 ASCII 的 < > / = " 以及半角竖线 |。
4) 所有字符串类型的参数值都必须通过 <![CDATA[...]]> 进行包裹，即使内容极其简短也不例外；这涵盖了代码段、脚本、文件内容、提示词、路径、名称和查询语句等。
5) 每一个顶层参数都必须体现为 <|EPSE|parameter name="ARG_NAME">...</|EPSE|parameter> 节点形式。
6) 对象在参数主体内使用嵌套的 XML 元素。数组可以使用重复的 <item> 子元素。
7) 数字、布尔值和 null 保持为纯文本。
8) 仅使用工具架构(schema)中定义的参数名称。请勿自行创建字段。
9) 使用本次调用所需的实际值填充参数。请勿输出占位符、空参数或仅包含空白字符的参数。
10) 如果所需的参数值未知，请询问用户或正常回答，而不是输出空的工具调用。
11) 对于 Bash / execute_command 等 Shell 工具，命令或脚本必须包含在 command 参数中。切勿在命令为空的情况下调用它们。
12) 请勿使用 Markdown 代码块标记包裹 XML。请勿输出解释、角色标识或内心独白。
13) 如果调用工具，该工具代码块的第一个非空白字符必须严格为 <|EPSE|tool_calls>。
14) 切勿省略起始标签 <|EPSE|tool_calls>，即使你打算随后使用 </|EPSE|tool_calls> 闭合标签。
15) 工具标签只有上述带 EPSE 前缀的一种格式：<|EPSE|tool_calls> / <|EPSE|invoke> / <|EPSE|parameter>。请始终完整输出 <|EPSE| 前缀，不要省略前缀或使用无前缀的标签写法。

调用决策：
- 历史对话中出现的 <|EPSE|tool_calls> 块是已经执行完毕的工具调用记录，其结果已包含在后续的 tool 消息中，属于背景信息而非待办事项。
- 是否需要调用工具，取决于当前任务还需要什么：若还需要新的信息或操作，请继续调用——同一工具允许用不同参数多次调用（如读取不同文件、执行不同命令），上次调用失败或结果不完整时也可重试；若信息已足够，则直接回答。
- 不要因历史中曾调用过某工具而回避再次调用，也不要重复执行已经完成且结果正确的调用。

参数格式：
- string => <|EPSE|parameter name="x"><![CDATA[value]]></|EPSE|parameter>
- object => <|EPSE|parameter name="x"><field>...</field></|EPSE|parameter>
- array => <|EPSE|parameter name="x"><item>...</item><item>...</item></|EPSE|parameter>
- number/bool/null => <|EPSE|parameter name="x">plain_text</|EPSE|parameter>

【Wrong — 请勿这样操作】:

Wrong 1 — XML 之后包含其他文本:
  <|EPSE|tool_calls>...</|EPSE|tool_calls> I hope this helps.
Wrong 2 — 使用 Markdown 代码块标记:
  ` + "```xml" + `
  <|EPSE|tool_calls>...</|EPSE|tool_calls>
  ` + "```" + `
Wrong 3 — 缺少起始包裹标签:
  <|EPSE|invoke name="TOOL_NAME">...</|EPSE|invoke>
  </|EPSE|tool_calls>
Wrong 4 — 参数为空:
  <|EPSE|tool_calls>
    <|EPSE|invoke name="Bash">
      <|EPSE|parameter name="command"></|EPSE|parameter>
    </|EPSE|invoke>
  </|EPSE|tool_calls>

请记住：使用工具的唯一正确方式是在回复末尾使用 <|EPSE|tool_calls>...</|EPSE|tool_calls> 代码块。
`
	return strings.ReplaceAll(spec, EPSEKeyword, marker)
}

// ToolCallFormatReminder returns the short final reminder block that restates
// the one and only valid tool-call skeleton. It is injected as the last system
// turn of the prompt (immediately before the trailing `[Assistant]:` marker)
// whenever tool-call instructions or the format spec are present, so the
// skeleton is the closest thing to the generation point and cannot be diluted
// by a long transcript sitting between the spec and the completion marker.
func ToolCallFormatReminder() string {
	return ToolCallFormatReminderForMarker(EPSEKeyword)
}

// ToolCallFormatReminderForMarker is the marker-aware variant of
// ToolCallFormatReminder.
func ToolCallFormatReminderForMarker(marker string) string {
	marker = normalizeToolMarker(marker)
	reminder := `The only correct format for using tools is <|EPSE|tool_calls>
  <|EPSE|invoke name="TOOL_NAME_HERE">
    <|EPSE|parameter name="PARAMETER_NAME"><![CDATA[PARAMETER_VALUE]]></|EPSE|parameter>
  </|EPSE|invoke>
</|EPSE|tool_calls> .Please do not simplify or modify this framework in any way.The only valid label is EPSE; the use of any label prefixed with DSML is **strictly prohibited**.`
	return strings.ReplaceAll(reminder, EPSEKeyword, marker)
}

type promptToolExample struct {
	name   string
	params string
}

func buildCorrectToolExamples(toolNames []string, marker string) string {
	names := uniqueToolNames(toolNames)
	examples := make([]string, 0, 4)

	if single, ok := firstBasicExample(names, marker); ok {
		examples = append(examples, "示例 A — 单个工具：\n"+renderToolExampleBlock([]promptToolExample{single}, marker))
	}

	if parallel := firstNBasicExamples(names, 2, marker); len(parallel) >= 2 {
		examples = append(examples, "示例 B — 两个工具并行：\n"+renderToolExampleBlock(parallel, marker))
	}

	if nested, ok := firstNestedExample(names, marker); ok {
		examples = append(examples, "示例 C — 带嵌套 XML 参数的工具：\n"+renderToolExampleBlock([]promptToolExample{nested}, marker))
	}

	if script, ok := firstScriptExample(names, marker); ok {
		examples = append(examples, "示例 D — 使用 CDATA 的长脚本工具（对代码/脚本可靠）：\n"+renderToolExampleBlock([]promptToolExample{script}, marker))
	}

	if len(examples) == 0 {
		return ""
	}
	return "【正确示例】：\n\n" + strings.Join(examples, "\n\n") + "\n\n"
}

func uniqueToolNames(toolNames []string) []string {
	names := make([]string, 0, len(toolNames))
	seen := map[string]bool{}
	for _, name := range toolNames {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

func firstBasicExample(names []string, marker string) (promptToolExample, bool) {
	for _, name := range names {
		if params, ok := exampleBasicParams(name, marker); ok {
			return promptToolExample{name: name, params: params}, true
		}
	}
	return promptToolExample{}, false
}

func firstNBasicExamples(names []string, count int, marker string) []promptToolExample {
	out := make([]promptToolExample, 0, count)
	for _, name := range names {
		if params, ok := exampleBasicParams(name, marker); ok {
			out = append(out, promptToolExample{name: name, params: params})
			if len(out) == count {
				return out
			}
		}
	}
	return out
}

func firstNestedExample(names []string, marker string) (promptToolExample, bool) {
	for _, name := range names {
		if params, ok := exampleNestedParams(name, marker); ok {
			return promptToolExample{name: name, params: params}, true
		}
	}
	return promptToolExample{}, false
}

func firstScriptExample(names []string, marker string) (promptToolExample, bool) {
	for _, name := range names {
		if params, ok := exampleScriptParams(name, marker); ok {
			return promptToolExample{name: name, params: params}, true
		}
	}
	return promptToolExample{}, false
}

func renderToolExampleBlock(calls []promptToolExample, marker string) string {
	var b strings.Builder
	b.WriteString("<|" + marker + "|tool_calls>\n")
	for _, call := range calls {
		b.WriteString(`  <|` + marker + `|invoke name="`)
		b.WriteString(call.name)
		b.WriteString(`">` + "\n")
		b.WriteString(indentPromptParameters(call.params, "    ", marker))
		b.WriteString("\n  </|" + marker + "|invoke>\n")
	}
	b.WriteString("</|" + marker + "|tool_calls>")
	return b.String()
}

func indentPromptParameters(body, indent, marker string) string {
	if strings.TrimSpace(body) == "" {
		return indent + `<|` + marker + `|parameter name="content"></|` + marker + `|parameter>`
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[i] = line
			continue
		}
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}

func wrapParameter(name, inner, marker string) string {
	return `<|` + marker + `|parameter name="` + name + `">` + inner + `</|` + marker + `|parameter>`
}

func exampleBasicParams(name, marker string) (string, bool) {
	switch strings.TrimSpace(name) {
	case "Read":
		return wrapParameter("file_path", promptCDATA("README.md"), marker), true
	case "Glob":
		return wrapParameter("pattern", promptCDATA("**/*.go"), marker) + "\n" + wrapParameter("path", promptCDATA("."), marker), true
	case "read_file":
		return wrapParameter("path", promptCDATA("src/main.go"), marker), true
	case "list_files":
		return wrapParameter("path", promptCDATA("."), marker), true
	case "search_files":
		return wrapParameter("query", promptCDATA("tool call parser"), marker), true
	case "Bash", "execute_command":
		return wrapParameter("command", promptCDATA("pwd"), marker), true
	case "exec_command":
		return wrapParameter("cmd", promptCDATA("pwd"), marker), true
	case "Write":
		return wrapParameter("file_path", promptCDATA("notes.txt"), marker) + "\n" + wrapParameter("content", promptCDATA("Hello world"), marker), true
	case "write_to_file":
		return wrapParameter("path", promptCDATA("notes.txt"), marker) + "\n" + wrapParameter("content", promptCDATA("Hello world"), marker), true
	case "Edit":
		return wrapParameter("file_path", promptCDATA("README.md"), marker) + "\n" + wrapParameter("old_string", promptCDATA("foo"), marker) + "\n" + wrapParameter("new_string", promptCDATA("bar"), marker), true
	case "MultiEdit":
		return wrapParameter("file_path", promptCDATA("README.md"), marker) + "\n" + `<|` + marker + `|parameter name="edits"><item><old_string>` + promptCDATA("foo") + `</old_string><new_string>` + promptCDATA("bar") + `</new_string></item></|` + marker + `|parameter>`, true
	}
	return "", false
}

func exampleNestedParams(name, marker string) (string, bool) {
	switch strings.TrimSpace(name) {
	case "MultiEdit":
		return wrapParameter("file_path", promptCDATA("README.md"), marker) + "\n" + `<|` + marker + `|parameter name="edits"><item><old_string>` + promptCDATA("foo") + `</old_string><new_string>` + promptCDATA("bar") + `</new_string></item></|` + marker + `|parameter>`, true
	case "Task":
		return wrapParameter("description", promptCDATA("Investigate flaky tests"), marker) + "\n" + wrapParameter("prompt", promptCDATA("Run targeted tests and summarize failures"), marker), true
	case "ask_followup_question":
		return wrapParameter("question", promptCDATA("Which approach do you prefer?"), marker) + "\n" + `<|` + marker + `|parameter name="follow_up"><item><text>` + promptCDATA("Option A") + `</text></item><item><text>` + promptCDATA("Option B") + `</text></item></|` + marker + `|parameter>`, true
	}
	return "", false
}

func exampleScriptParams(name, marker string) (string, bool) {
	scriptCommand := `cat > /tmp/test_escape.sh <<'EOF'
#!/bin/bash
echo 'single "double"'
echo "literal dollar: \$HOME"
EOF
bash /tmp/test_escape.sh`
	scriptContent := `#!/bin/bash
echo 'single "double"'
echo "literal dollar: $HOME"`

	switch strings.TrimSpace(name) {
	case "Bash":
		return wrapParameter("command", promptCDATA(scriptCommand), marker) + "\n" + wrapParameter("description", promptCDATA("Test shell escaping"), marker), true
	case "execute_command":
		return wrapParameter("command", promptCDATA(scriptCommand), marker), true
	case "exec_command":
		return wrapParameter("cmd", promptCDATA(scriptCommand), marker), true
	case "Write":
		return wrapParameter("file_path", promptCDATA("test_escape.sh"), marker) + "\n" + wrapParameter("content", promptCDATA(scriptContent), marker), true
	case "write_to_file":
		return wrapParameter("path", promptCDATA("test_escape.sh"), marker) + "\n" + wrapParameter("content", promptCDATA(scriptContent), marker), true
	}
	return "", false
}

func promptCDATA(text string) string {
	if text == "" {
		return ""
	}
	if strings.Contains(text, "]]>") {
		return "<![CDATA[" + strings.ReplaceAll(text, "]]>", "]]]]><![CDATA[>") + "]]>"
	}
	return "<![CDATA[" + text + "]]>"
}
