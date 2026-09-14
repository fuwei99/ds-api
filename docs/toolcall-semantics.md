# Tool call parsing semantics（Go/Node 统一语义）

本文档描述当前代码中的**实际行为**，以 `internal/toolcall`、`internal/toolstream` 与 `internal/js/helpers/stream-tool-sieve` 为准。

文档导航：[总览](../README.MD) / [架构说明](./ARCHITECTURE.md) / [测试指南](./TESTING.md)

## 1) 当前可执行格式

当前版本推荐模型输出半角管道符 EPSE 外壳：

```xml
<|EPSE|tool_calls>
  <|EPSE|invoke name="read_file">
    <|EPSE|parameter name="path"><![CDATA[README.MD]]></|EPSE|parameter>
  </|EPSE|invoke>
</|EPSE|tool_calls>
```

这不是原生 EPSE 全链路实现。EPSE 主要用于让模型有意识地输出协议标识，隔离普通 XML 语义；进入 parser 前会按固定本地标签名归一化成 `<tool_calls>` / `<invoke>` / `<parameter>`，内部仍以现有 XML 解析语义为准。提示词与全部示例只示范 EPSE 外壳，模型应始终输出带 `<|EPSE|` 前缀的标签；解析层对 legacy canonical XML（`<tool_calls>` / `<invoke>` / `<parameter>`）仅保留向后兼容解析，不作为推荐输出格式。

### 1.1) 按 API Key 随机化的工具调用标识（tool marker）

提示词出口会把规范里的 `EPSE` 渲染成**调用者专属标识 M**（6 位 `[A-Z2-9]`，由 `HMAC-SHA256(tool_marker_secret, callerID)` 派生，`callerID` 为 API Key 的 `sha256`），因此上游实际看到的是 `<|Q7ZK3M|tool_calls>` 这类标签，而不是共享的 `<|EPSE|tool_calls>`。设计动机、密钥管理与各协议接线见 `docs/prompt-compatibility.md` §6.1.2。

对本解析语义的影响：

- M **不属于**上面列出的可容忍噪声前缀：解析层不会把任意 6 位前缀当作协议壳噪声，因此未归一化的 M 形态标签会被当作普通文本整段透传（`internal/toolstream/marker_stream_test.go` 的 `TestProcessToolSieveNeedsMarkerNormalization` 固化了这一点）。
- 所以模型输出在进入 sieve / parser / 意图识别 / 兜底修复之前，必须由**有状态**归一化器把 M 折回 `EPSE`。Go：`internal/toolcall/marker.go` 的 `MarkerNormalizer` + `toolstream.State.Marker`（`ProcessChunk` / `Flush` 内部自动执行）+ `assistantturn.BuildOptions.ToolMarker`（收口幂等兜底）；Node / Vercel：`internal/js/chat-stream/marker.js`。归一化跨分片安全：尾部若为 M 的真前缀会先 hold，下一片到达再替换，收尾 flush 释放，因此半截标识不会泄漏成可见正文。
- 本文档其余所有语义（EPSE 外壳、噪声前缀容错、简写形态、CDATA 边界修复、`sawToolCallIntent` 的 `<|EPSE` 成对判定、slot 修复）都以**归一化之后**的 EPSE 形态文本为前提。
- 兜底修复（LLM repair）的 prompt（格式规范 + 固定提示词）同样按 M 渲染，模型回答在解析前归一化回 EPSE；残余坏码本身来自已归一化的内部文本，slot / 结构判定不受影响。
- M 为空或等于 `EPSE` 时全部退化为旧行为（无操作）。

约束：

- 必须有 `<|EPSE|tool_calls>...</|EPSE|tool_calls>` wrapper
- 每个调用必须在 `<|EPSE|invoke name="...">...</|EPSE|invoke>` 内
- 工具名必须放在 `invoke` 的 `name` 属性
- 参数必须使用 `<|EPSE|parameter name="...">...</|EPSE|parameter>`
- 一个工具块内的标签必须统一使用带 EPSE 前缀的格式，不要省略前缀或与无前缀标签混用

兼容修复：

- 如果模型漏掉 opening wrapper，但后面仍输出了一个或多个 invoke 并以 closing wrapper 收尾，Go 解析链路会在解析前补回缺失的 opening wrapper。
- 在进入现有 EPSE rewrite / XML parse 之前，Go / Node 都会先做一次非常窄的 candidate-span canonicalization：只处理已经被 scanner 识别为工具标签壳的 wrapper / `invoke` / `parameter` / `name` / `CDATA` / `EPSE` 及其结构分隔符；这里会移除零宽 / BOM / 控制类干扰字符，并把 `<`、`>`、`/`、`|`、`=`、引号、Unicode 空白、常见 dash / underscore 变体这类工具语法外壳符号折回 ASCII 语义。
- Go / Node 解析层不再枚举每一种 EPSE typo。它以固定本地标签名 `tool_calls` / `invoke` / `parameter` 为准，把标签名前的任意协议前缀壳视为可容忍噪声，并继续兼容半角管道符、全角感叹号 `！`、顿号 `、`、空白、重复 leading `<`、可视控制符 `␂`、原始 STX `\x02`、非 ASCII 分隔符、CJK 尖括号 `〈` / `〉`、弯引号属性值、PascalCase 本地名等漂移。例如 `<EPSE|tool_calls>`、`<<|EPSE|tool_calls>`、`<|EPSE tool_calls>`、`<EPSEtool_calls>`、`<DSmartToolCalls>`、`<<EPSE|EPSE|tool_calls>`、`<EPSE␂tool_calls>`、`<proto💥tool_calls>`、`<EPS|tool_calls>...〈/EPS|tool_calls〉`、`<！EPSE！tool_calls>...<！/EPSE！tool_calls>`、`<、EPSE、tool_calls>...<、/EPSE、tool_calls>` 都会归一化；相似但非固定标签名（如 `tool_calls_extra` / `ToolCallsExtra`）仍按普通文本处理。
- 作为兜底，解析器还接受 EPSE 参数标签的“简写”形态：模型把 `<|EPSE|parameter ...>` 漏写成 `<|EPSE ...>`（缺少 `|parameter` 段），或把结束标签漏写成 `</|EPSE>`。打开简写只有在携带 `name=` 属性时才生效（`<|EPSE name="workdir">` → `<|EPSE|parameter name="workdir">`）；`</|EPSE>` 会被当作 `</|EPSE|parameter>`，而不带 `name=` 的裸简写（如 `<|EPSE foo="bar">`）以及空的 `<|EPSE>` 仍按普通文本处理。
- 这个 candidate-span canonicalization 不会对普通 prose、参数正文、CDATA 内容或嵌套的非工具 XML 做广义 Unicode 归一化。也就是说，参数里的示例 `<invοke>`、普通聊天文本里的 confusable 单词、或其他非工具壳 XML 片段都保持原样；只有真正落在工具标签壳上的 whitelist 关键字和结构符号会被折叠。
- 如果模型在固定工具标签名后多输出一个非结构性分隔符，例如 `<|EPSE|tool_calls|` / `<|EPSE|invoke|` / `<|EPSE|parameter|` / `<EPSEtool_calls※>`，或在带属性标签的结束符前多输出一个尾部分隔符（如 `<EPS|parameter name="command"|>`），兼容层会把这个尾部分隔符当作异常标签终止符并补齐或归一化；如果后面已经有 `>` / `〉`，也会消费这个多余分隔符后再归一化。结构性字符如 `<` / `>` / `/` / `=` / 引号、空白和 ASCII 字母数字不会被当作这类分隔符。
- “缺失 opening wrapper”的修复只会在 wrapper-confidence 足够高时触发：scanner 必须已经识别出白名单工具壳结构（wrapper / invoke / parameter / `name=` 等），且剩余失败看起来只是壳层结构问题。相似但不在白名单内的 near-miss 标签名，或缺少足够 wrapper 证据的 malformed 片段，仍会按普通文本透传。
- 这是一个针对常见模型失误的窄修复，不改变推荐输出格式；prompt 仍要求模型直接输出完整 EPSE 外壳。
- 裸 `<invoke ...>` / `<parameter ...>` 不会被当成“已支持的工具语法”；只有 `tool_calls` wrapper 或可修复的缺失 opening wrapper 才会进入工具调用路径。

## 2) 非兼容内容

任何不满足上述 EPSE / legacy canonical XML 形态的内容，都会保留为普通文本，不会执行。一个例外是上一节提到的“缺失 opening wrapper、但 closing wrapper 仍存在”的窄修复场景。

当前 parser 不把 allow-list 当作硬安全边界：即使传入了已声明工具名列表，XML 里出现未声明工具名时也会尽量解析并交给上层协议输出；真正的执行侧仍必须自行校验工具名和参数。

## 3) 流式与防泄漏行为

在流式链路中（Go / Node 一致）：

- EPSE `<|EPSE|tool_calls>` wrapper、短横线形式（如 `<epse-tool-calls>` / `<epse-invoke>` / `<epse-parameter>`）、基于固定本地标签名的 EPSE 噪声容错形态、尾部非结构性分隔符形态（如 `<|EPSE|tool_calls|` / `<EPSEtool_calls※>`）和 canonical `<tool_calls>` wrapper 都会进入结构化捕获
- 调用者专属标识（tool marker）形态在进入捕获前先被归一化回 EPSE（见 §1.1）。归一化器按分片滚动，遇到「尾部是 M 的真前缀」会先 hold；这条 hold 与 sieve 自身的工具标签 hold 相互独立，两者叠加保证既不会漏掉被切开的标识，也不会把半截标识透传成正文。Go 与 Node 行为一致。
- DSML 外壳（如 `<DSML calls>` / `<|DSML|calls>` / `<DSML invoke name="...">`，本地名任意）也会作为额外的流式拦截入口进入捕获：DSML 不会解析成工具调用，捕获块完整后从可见正文中丢弃（隐藏），原始文本仍保留在 `accumulator.RawText` 中交给 finalize 兜底修复（`DetectToolCallIntent` → 修复链路），因此不会逐片泄漏到正文；流中途被主动打断时，未闭合的 DSML 块也一并隐藏。DSML 标签在 CDATA / 注释 / 围栏 / 行内 code span 内不触发，跨 chunk 的半截 DSML 标签也会被 hold。Go 侧实现 `internal/toolcall/toolcalls_intent.go` 的 `FindDSMLTagOutsideIgnored` / `FindMatchingDSMLClose` + `internal/toolcall/toolcalls_scan.go` 的 `isPartialDSMLTagPrefix`，Node / Vercel 侧对齐在 `internal/js/helpers/stream-tool-sieve/parse_payload.js` 与 `sieve.js`。修复成功后的可见正文清理按**标记区间**进行（Go 侧 `internal/toolcall/toolcalls_deduct.go` 的 `BadToolMarkupSpans` / `RemoveTextSpans`）：只删除 DSML 包裹与解析失败的 EPSE / canonical 块，prose 保留；residual 因部分成功而不连续时仍能扣减，找不到坏标记区间时才退回整段 residual 删除。
- 如果流里直接从 invoke 开始，但后面补上了 closing wrapper，Go 流式筛分也会按缺失 opening wrapper 的修复路径尝试恢复
- 已识别成功的工具调用不会再次回流到普通文本
- 不符合新格式的块不会执行，并继续按原样文本透传
- 如果一个 confusable / 漂移过的工具壳在 candidate-span canonicalization + repair 后仍能形成有效工具调用，wrapper 后面的 suffix prose 会继续按普通文本输出；如果 canonicalization 后仍不满足 wrapper-confidence 或 XML 语义，整块就作为普通文本释放，不会半吞半漏。
- fenced code block（反引号 `` ``` `` 和波浪线 `~~~`）以及 Markdown inline code span（例如 `` `<tool_calls>...</tool_calls>` ``）中的 XML 示例始终按普通文本处理
- 支持嵌套围栏（如 4 反引号嵌套 3 反引号）和 CDATA 内围栏保护
- 对 `command` / `content` 等长文本参数，CDATA 内部如果包含 Markdown fenced EPSE / XML 示例，即使示例里出现 `]]></parameter>` / `</tool_calls>` 这类看起来像外层结束标签的片段，也会继续按参数原文保留，直到真正位于围栏外的外层结束标签
- CDATA 开头也按扫描式识别，除了标准 `<![CDATA[`，还会接受 `<！[CDATA[`、`<、[CDATA[` 这类分隔符漂移，并统一还原为原文字段内容。
- 如果模型把 `<![CDATA[` 打开后却没有闭合，流式扫描阶段仍会保守地继续缓冲，不会误把 CDATA 里的示例 XML 当成真实工具调用；在最终 parse / flush 恢复阶段，会对这类 loose CDATA 做窄修复，尽量保住外层已完整包裹的真实工具调用
- 某个 `parameter` 的 `<![CDATA[` 漏了自己的 `]]>` 时，CDATA 结束符查找本身没有元素边界，会一路命中**后一个** `parameter` 的 `]]>`，把 `</|EPSE|parameter><|EPSE|parameter name="…">` 外壳和后一个参数的正文一起吞进前一个参数值，同时让后一个参数从解析结果里消失。最终 parse 阶段会识别这种“越界”形态，按元素边界补上 `]]>` 后重解一次，并只在重解结果的 call 数 / 参数数**严格更优**时采用（择优口径与兜底修复的 `pickBetterRepairResult` 一致），因此贪婪解释对它能正确处理的输入保持权威。判定要求参数体内同时出现「自己的 `</parameter>` 闭合标签」和「其后一个未配对的 `<![CDATA[`」两个特征，所以参数正文里合法引用的完整工具调用示例、以及只在正文里提到裸 `<![CDATA[` 的情形都不会触发该修复，值保持逐字节原样。相邻两个参数都漏 `]]>` 时会迭代到不动点。Go 侧实现 `internal/toolcall/toolcalls_cdata_boundary_repair.go`，Node / Vercel 侧对齐实现在 `internal/js/helpers/stream-tool-sieve/parse_payload.js` 与 `parse.js`。
- 当文本中 mention 了某种标签名（如 `<epse|tool_calls>` 或 Markdown inline code 里的 `<|EPSE|tool_calls>`）而后面紧跟真正工具调用时，sieve 会跳过不可解析的 mention 候选并继续匹配后续真实工具块；行内 code span 中即使出现完整 `<tool_calls>...</tool_calls>` 示例也不会执行，不会因 mention 导致工具调用丢失，也不会截断 mention 后的正文
- Go 侧 SSE 读取不再使用 `bufio.Scanner` 的固定 token 上限；单个 `data:` 行中包含很长的写文件参数时，非流式收集、流式解析与 auto-continue 透传都应保留完整行，再交给 tool parser 处理

另外，`<parameter>` 的值如果本身是合法 JSON 字面量，也会按结构化值解析，而不是一律保留为字符串。例如 `123`、`true`、`null`、`[1,2]`、`{"a":1}` 都会还原成对应的 number / boolean / null / array / object。
结构化 XML 参数也会还原为 JSON 结构：如果参数体只包含一个或多个 `<item>...</item>` 子节点，会输出数组；嵌套对象里的 item-only 字段也同样按数组处理。例如 `<parameter name="questions"><item><question>...</question></item></parameter>` 会输出 `{"questions":[{"question":"..."}]}`，而不是 `{"questions":{"item":...}}`。
如果模型误把完整结构化 XML fragment 放进 CDATA，Go / Node 会先保护明显的原文字段（如 `content` / `command` / `prompt` / `old_string` / `new_string`），其余参数会尝试把 CDATA 内的完整 XML fragment 还原成 object / array；常见的 `<br>` 分隔符会按换行归一化后再解析。但如果 CDATA 只是单个平面的 XML/HTML 标签，例如 `<b>urgent</b>` 这种行内标记，兼容层会把它保留为原始字符串，而不会强行升成 object / array；只有明显表示结构的 CDATA 片段，例如多兄弟节点、嵌套子节点或 `item` 列表，才会触发结构化恢复。

## 4) 输出结构

`ParseToolCallsDetailed` / `parseToolCallsDetailed` 返回：

- `calls`：解析出的工具调用列表（`name` + `input`）
- `sawToolCallSyntax`：检测到 EPSE / canonical wrapper，或命中“缺失 opening wrapper 但可修复”的形态时会为 `true`；裸 `invoke` 不计入该标记
- `sawToolCallIntent`：残余意图信号。在**归一化之前的原始文本**上（剥离 markdown 围栏后、`normalizeEPSEToolCallMarkup` 之前），扣除已成功解析的 wrapper 源区间后，残余文本里仍存在**成对的** `<|EPSE` 开标签 + `</|EPSE` 闭合标签时为 `true` —— 代表“模型想调工具但格式坏了、还有无法解析的意图”，是兜底修复的触发条件之一（详见第 7 节）
- `rejectedByPolicy`：当前固定为 `false`
- `rejectedToolNames`：当前固定为空数组

解析层不会因为参数值为空而丢弃工具调用。若模型输出了显式空字符串或纯空白参数，它们会按空字符串进入结构化 `tool_calls`；是否拒绝缺参或空命令应由后续工具执行侧 / 客户端 schema 校验决定。Prompt 层仍会要求模型不要主动输出空参数。

完整的 EPSE / XML wrapper 只有在成功解析出有效 `invoke name`，并且参数节点（如存在）符合 `parameter` 语义后，才会变成结构化工具调用；真正的零参数工具调用仍然有效。如果 wrapper 完整但内部不是可执行工具调用形态（例如使用 `<param>`、缺少有效 `invoke name`、或其他 malformed XML 工具壳），流式 sieve 会把原始 wrapper 作为普通文本释放，不会吞掉内容，也不会生成空的工具调用。

## 5) 落地建议

1. Prompt 里只示范 EPSE 外壳语法。
2. 上游客户端应直接输出完整 EPSE 外壳（提示词只示范 EPSE，不写入无前缀标签）；DS2API 解析层对旧式 canonical XML 仅保留向后兼容解析，并只对“closing tag 在、opening tag 漏掉”的常见失误做窄修复，不会泛化接受其他旧格式。
3. 模型只有在知道本次调用所需参数值时才应输出工具调用；不要输出 placeholder、空字符串或纯空白参数。对 `Bash` / `execute_command`，实际命令必须在 `command` 参数里。
4. 不要依赖 parser 做安全控制；执行器侧仍应做工具名和参数校验。

## 6) 回归验证

可直接运行：

```bash
go test -v -run 'TestParseToolCalls|TestProcessToolSieve' ./internal/toolcall ./internal/toolstream ./internal/httpapi/openai/...
./tests/scripts/run-unit-node.sh
```

重点覆盖：

- EPSE `<|EPSE|tool_calls>` wrapper 正常解析
- legacy canonical `<tool_calls>` wrapper 正常解析
- 固定本地标签名的 EPSE 噪声容错形态（如 `<EPSE|tool_calls>`、`<<|EPSE|tool_calls>`、`<|EPSE tool_calls>`、`<EPSEtool_calls>`、`<DSmartToolCalls>`、`<<EPSE|EPSE|tool_calls>`、`<EPS|tool_calls>...〈/EPS|tool_calls〉`、`<！EPSE！tool_calls>...<！/EPSE！tool_calls>`）正常解析
- EPSE 参数标签的“简写”形态（`<|EPSE ...>` / `</|EPSE>`，漏掉 `|parameter` 段）正常解析，且不带 `name=` 的裸简写仍按普通文本处理
- 混搭标签（EPSE wrapper + canonical inner）归一化后正常解析
- 波浪线围栏 `~~~` 内的示例不执行
- 嵌套围栏（4 反引号嵌套 3 反引号）内的示例不执行
- Markdown 行内 code span 内的完整工具调用示例不执行
- 文本 mention 标签名后紧跟真正工具调用的场景（含同一 wrapper 变体）
- 空参数结构化保留，malformed executable-looking XML wrapper 作为文本释放
- 参数 CDATA 漏 `]]>` 时不吞掉后一个参数（含相邻两个参数都漏的情形），且参数正文里引用的完整工具调用示例保持逐字节原样
- 非兼容内容按普通文本透传
- 代码块示例不执行

## 7) 工具调用失败兜底修复

当模型输出工具调用但格式损坏（本地名写错、闭合标签错位、wrapper 混乱等），解析器解不出任何有效 call 时，DS2API 不再静默降级（把 EPSE 原文当正文透传，或在 finalize 阶段丢弃），而是走「工具调用兜底修复」链路。分三层：

### 7.1 意图识别（`SawToolCallIntent`）

- 判断标准：在**归一化之前的原始文本**上（剥离 markdown 围栏后、`normalizeEPSEToolCallMarkup` 之前），扣除已成功解析的 wrapper 源区间后，残余文本里是否存在**成对的** `<|EPSE` 开标签 + `</|EPSE` 闭合标签（开在前、闭在后，且都在 CDATA / 注释 / 围栏之外）。
- 只有开标签、其后无任何 EPSE 闭合标签（流中途截断）不算意图；CDATA 包裹的 `<|EPSE`（参数内容的字面示例）一律不算；无 EPSE 前缀的 canonical `<tool_calls>` 不算意图信号。
- 对应实现：`internal/toolcall/toolcalls_intent.go` 的 `DetectToolCallIntent`，结果写入 `ToolCallParseResult.SawToolCallIntent`。

### 7.2 CDATA 占位符（slot）

- 超长 CDATA 参数（几十 KB ~ MB 级）在进入修复层前，内容被替换为短占位符 `DS_SLOT_{idx}`（保留 `<![CDATA[...]]>` 外壳），修复层只面对"骨架 + 短占位符"，避免 token 爆炸与内容被误改；修复完成后、解析前再逐字节还原。
- **贪婪扫描（默认）**：与确定性解析器的 CDATA 判定完全一致（`findToolCDATAEnd` 向后找到全文下一个 `]]>`），只 slot 闭合完好的区间，未闭合 opener 跳过继续扫。
- **边界受限扫描（仅 repair 路径）**：每个 opener 先定位其所属元素的边界（下一个工具结构标签），只在边界之前找 `]]>`。它解决贪婪扫描在坏格式输入上的两种失效：
  - 参数缺 `]]>` 时，贪婪扫描会一路吃到**下一个**参数的 `]]>`，把 `</|EPSE|parameter><|EPSE|parameter name="...">` 整段吞进一个 slot，导致交给修复层的骨架**整体丢失一个参数节点** —— 模型看不见就不可能修对，且即使骨架修得完美，还原后坏格式仍逐字节复现。
  - 元素内确实没有 `]]>` 的区间，贪婪扫描完全跳过，超长原文直接进 prompt，slot 机制在最需要它的场景失效。边界受限扫描改为 slot 该区间且不输出闭合符，骨架渲染成 `<![CDATA[DS_SLOT_4</|EPSE|parameter>`，长内容被抽走、同时把「缺 `]]>`」这个病灶留在模型视野里。
- **歧义与择优**：参数正文本身可能合法地包含 `</|EPSE|parameter>` 字样甚至嵌套 `<![CDATA[` 示例。吞噬签名要求「slot 内容含结构标签 **且** 该标签之后还有 CDATA open 标记」，仅含前半条的合法长参数保持贪婪解释不变。命中签名时边界受限骨架升为主解释、贪婪保留为备选；**只发一次 LLM 请求**，用同一份输出分别按两份 slot 表还原并解析，取 call 更多、参数更齐者（完全打平时主解释优先）。
- 还原必须用与 slot 时相同的扫描重扫：边界受限骨架可能带元素内未闭合 CDATA，用贪婪重扫会越过这些 opener 并吞掉后面的占位符。
- 对应实现：`internal/toolcall/toolcalls_cdata_slots.go`（贪婪 + 替换）、`toolcalls_cdata_slots_bounded.go`（边界受限扫描、吞噬判定、占位符清单）、`toolcalls_cdata_slots_restore.go`（还原）。

### 7.3 LLM 修复（finalize 兜底）

- 当意图为真（`SawToolCallIntent=true`）且确定性规则修复（含 CDATA slot）后仍存在无法解析的坏残片时，finalize 路径追加一次 LLM 修复：把系统提示中的「工具调用格式规范」（`toolcall.ToolCallFormatSpec`，与注入模型的规则逐字一致）+ 固定提示词 + 占位符清单 + 坏工具调用代码拼成 prompt 交给 LLM。
- 触发范围：非流式 finalize + 所有流式 finalize 定稿（chat / responses / claude / gemini，发送最终帧前触发；若结构化 tool_calls 已在增量阶段流式发出则不再触发）。
- 部分成功也修复：`SawToolCallIntent=true` 且残余 `ResidualIntentText` 非空时，即使已解析出 ≥1 个成功 call，也会对坏残片修复并把修复出的 call 合并进已有 calls（已有在前，按 name+input 去重）。
- LLM 输出三路分支：① 正确工具调用格式 → 解析为 tool_calls 替换到输出；② 字面量 `NO TOOL` → 该片段并非工具调用，原样输出；③ 无法解析 / 超时 → 原样输出兜底片段。
- 修复调用使用 default 模式（`deepseek-v4.1-flash` → `model_type=default`；网页客户端 2.4.0 合并 fast/expert/vision 后已无独立 vision 模式），同账号、关闭思考、新建 session、10 秒超时。
- 固定提示词里原本要求「如果你认为接下来的内容只是在正常聊天，直接返回 NO TOOL」的那句已暂时移除：实测该提示容易误导模型把坏格式工具调用判成普通聊天，导致该修的调用不修。分支 ② 的判定代码保持不变，模型自发返回 `NO TOOL` 时仍按非工具调用处理。
- 固定提示词的占位符措辞必须与模型实际所见一致。早期版本声称「实际参数已用变量代替」，但未闭合 CDATA 不参与 slot，模型会同时看到占位符和原始长内容，指令与输入自相矛盾，反而诱导模型改写未被替换的部分。现在改为「可能已被替换」，并列出硬性要求：占位符逐字符原样输出、未替换内容逐字节保留、只允许改标签结构、每个 `parameter` 节点及其 `name` 必须保留、缺 `]]>` 要补齐。prompt 末尾还会追加本次占位符清单（`DS_SLOT_0 … DS_SLOT_{N-1}`）作为模型自检锚点。
- 降级日志（`[toolcall_repair] degraded`）带 `interpretation` / `slot_count` / `skeleton_len` / `slot_degrade_reason`，可直接定位失败发生在哪一环。
- 对应实现：`internal/toolcall/toolcalls_llm_repair.go`、`internal/completionruntime/toolcall_repair.go`（经 `NewToolCallRepairInvoker` 注入 chat/claude/gemini/responses 各 surface）。

### 7.4 防御触发路径

- 即使意图识别漏判（`SawToolCallIntent=false`），只要确定性解析器仍看到工具调用语法（`SawToolCallSyntax=true`）且解析出零个 call，finalize 兜底仍会用原始未 strip 文本作为坏码触发一次 LLM 修复，避免任何意图检测漏判导致坏工具调用代码作为可见正文静默泄漏；对普通正文（无工具语法）不触发。
