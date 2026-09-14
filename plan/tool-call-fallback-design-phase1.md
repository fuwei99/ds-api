# 工具调用兜底设计文档

> 状态：草案（未实现代码）—— 2026-08 二轮审计修订：① 意图探测在**归一化之前的原文**坐标系上执行（见 §4.2）；② 成功区间扣除改为 **wrapper 粒度**（连 `<tool_calls>` 外壳一起挖，避免空外壳假阳性），扣除在 original 上二次扫描定位、不与 normalized/recovered 坐标纠缠（见 §4.2、§5.1）。
> 范围：本文档为**第一部分 —— 工具调用意图识别**。第二部分（兜底恢复策略、与上下游重试的衔接）后续补充。

---

## 0. 背景与目标

模型输出工具调用时，偶尔会写错格式（畸形闭合标签、漏 wrapper、本地名拼错等）。当格式错到解析器解不出任何 tool call 时，当前行为是**静默降级**：要么把 EPSE 原文当正文透传给客户端，要么在 finalize 阶段把整块丢掉，0 calls、工具不执行、请求正常结束。

我们要做「工具调用兜底」：**先识别出"模型想调工具"的意图，再对解析失败的内容走兜底恢复**。

本部分只解决第一个问题：**如何判断"工具调用意图"**。核心结论一句话：

> 以「**成对的** `<|EPSE...` 开标签 + `</|EPSE...` 闭合标签」作为意图判断的**唯一**条件——**只开不闭、有开无闭都不算**；探测用独立函数实现、不污染扫描器，但**在精确解析完成、扣除成功 call 源区间之后对残余文本执行**。**CDATA 包裹的 `<|EPSE` 一律不算意图。**

---

## 1. 当前工具识别框架（简略）

### 1.1 两条解析链

| 链 | 入口 | 时机 | 失败时行为 |
|---|---|---|---|
| 流式 sieve | `internal/toolstream` 的 `ProcessChunk` → `consumeToolCapture` → `consumeXMLToolCapture` | 逐 chunk 实时 | 块完整但解析失败 → **原文透传为正文**（rejected 分支）；块未闭合 → 一直 hold |
| finalize 重解析 | `internal/assistantturn` 的 `BuildTurnFromStreamSnapshot` → `shared.DetectAssistantToolCalls(RawText)` | 流结束 | 0 calls 时正文只取 `VisibleText`，RawText 里的坏 EPSE 块可能被丢 |

### 1.2 工具调用解析核心（`internal/toolcall/`）

流程分四步，每一层职责清晰：

1. **扫描** `toolcalls_scan.go::scanToolMarkupTagAt`
   - 识别 `<|EPSE|tool_calls>` / `<|EPSE|invoke>` / `<|EPSE|parameter>` 及无前缀的 canonical 旧格式。
   - 本地名白名单 `toolMarkupNames = {tool_calls, tool-calls, toolcalls, invoke, parameter}`。
   - 兼容各种前缀变体（大小写、全角 `＜`、`|` 与空格、任意协议前缀 `<proto💥tool_calls>` 等）。

2. **归一化** `toolcalls_dsml.go::normalizeEPSEToolCallMarkup`
   - 分两步：先 `canonicalizeToolCallCandidateSpans` 把候选标签 span 归一化成标准 `<|EPSE|invoke>`（这一步**保留** `|EPSE|` 前缀，见 `canonicalizeRecognizedToolMarkupTag`），再 `rewriteEPSEToolMarkupOutsideIgnored` 把**每个识别出合法本地名**的 EPSE 外壳重写为 canonical `<invoke>`（**去掉** `|EPSE|` 前缀），供 XML 解析。本地名不识别的标签（如 `<|EPSE|call>`）被 `scanToolMarkupTagAt` 判 !ok，两步都原样保留 `|EPSE|`。这条「归一化会抹掉合法本地名标签的 `|EPSE|`」是 §4.2 必须用原文坐标系探测的根本原因。

3. **解析** `toolcalls_parse_markup.go::parseXMLToolCalls` → `parseSingleXMLToolCall`
   - 用 `findMatchingXMLEndTagOutsideCDATA` 按深度匹配闭合标签，提取 `invoke` 的 `name` 与 `parameter` 的 `name`/值。

4. **装配** `toolcalls_parse.go::parseToolCallsDetailedXMLOnly`
   - Trim → `stripFencedCodeBlocks` → normalize → parse → CDATA 兜底 → 过滤空 name。

### 1.3 现状的"意图信号"

`ToolCallParseResult` 里已有 `SawToolCallSyntax bool`，由 `looksLikeToolCallSyntax(normalized)` 计算，后者调用 `ContainsToolCallWrapperSyntaxOutsideIgnored` —— **它只认 `tool_calls` 这个 wrapper 名字**（`toolcalls_scan.go` 里 `if tag.Name != "tool_calls" { continue }`）。

即：现在的意图信号 = 「扫到了 `tool_calls` 外壳」，**且不区分开闭、不要求成对**。

---

## 2. 现状问题：意图信号太窄

把意图等同于「wrapper 外壳被识别」，有两个真实空档：

1. **外壳在、本地名错**：模型写出 `<|EPSE|call name="bash">…</|EPSE|call>`（应为 `invoke`）。`scanToolMarkupTagAt` 匹配 `toolMarkupNames` 失败返回 `!ok`，整个标签被当普通文本，`SawToolCallSyntax=false`，意图彻底丢失。
2. **漏 wrapper 的裸标签**：模型直接写 `<|EPSE|invoke name="...">…</|EPSE|invoke>`（无 `tool_calls` 包裹）。扫描器能扫到 `invoke`，但意图判断的 `ContainsToolCallWrapperSyntaxOutsideIgnored` 因「没有 `tool_calls`」而不计。

结论：**需要把「意图」与「语法识别」解耦**，用一层独立探测覆盖外壳坏掉/缺失的情况——判断标准是「EPSE 开标签与闭合标签成对出现」。

---

## 3. 意图判断方案（详细）

### 3.1 唯一条件：成对的 `<|EPSE...` 开标签 + `</|EPSE...` 闭合标签

**判定规则**：仅当文本中**同时存在**——

- 一个 EPSE **开标签**：`<|EPSE` / `<|epse` / `<EPSE` 前缀（含全角 `<`、`ＥＰＳＥ` 等归一化等价形式），后跟任意本地名、甚至本地名为空；
- 一个 EPSE **闭合标签**：`</|EPSE` / `</|epse` / `</EPSE` 前缀（同样含大小写/全角变体），后跟任意本地名、甚至本地名为空；

两者**成对出现**——这里「成对」是**存在性判定**：至少存在一个 EPSE 开标签，且在其后存在至少一个 EPSE 闭合标签（开在前、闭在后，且都在 CDATA / 注释 / 围栏之外）。开、闭两个标签的本地名允许不同甚至为空，所以**不做按名深度配对**；判定只问「开之后还有没有闭」。**只有开标签、其后无任何 EPSE 闭合标签 → 不算意图。**

选定理由：

- `|EPSE` 是 ds2api 协议的**专有 token**，自然正文、代码、markdown 中几乎不可能出现，**假阳性趋近于零**——满足「不要太敏感」。
- 「成对闭合」是比「只看前缀」更保守的一层约束：把「流中途被截断、只吐了半截开标签」这类假信号直接挡在意图之外，进一步压低压触发概率。
- 模型写错时，坏掉的通常是「本地名 / 参数结构」，而**开/闭这对 EPSE 外壳往往仍在**——但这句只在**归一化之前的原文**上成立；归一化会把合法本地名的 EPSE 外壳改写成 canonical 并抹掉 `|EPSE|`（见 §1.2 第 2 步、§4.2）。所以探测必须在原文上做。只要原文里开闭成对，哪怕本地名写错（`<|EPSE|call>…</|EPSE|call>`）、闭合漏名（`<|EPSE|tool_calls>…</|EPSE>`），意图就能被识别。

这一级覆盖 §2 的空档：`<|EPSE|call>…</|EPSE|call>`、`<|EPSE|function>…</|EPSE|function>`、漏 wrapper 的裸 `<|EPSE|invoke>…</|EPSE|invoke>` 等——只要带**成对的** `<|EPSE` / `</|EPSE` 外壳就都覆盖。

### 3.2 排除条件（不要太敏感）

> **⚠️ 重要（最高优先级）：CDATA 包裹的 `<|EPSE` 必须排除。**
>
> 协议中所有 string 参数值都包在 `<![CDATA[...]]>` 里，CDATA 内是**字面文本**，不是标签——里面出现 `<|EPSE` 不代表工具调用意图，而是参数内容的示例 / 文档 / 代码。
>
> 反例警示：本设计文档自身就满篇是 `<|EPSE` 字面示例。一旦这类内容作为参数或正文经过意图探测，若不跳过 CDATA，就会触发多次**假兜底**。
>
> **规则：只有 CDATA 之外、且成对出现的 `<|EPSE` 才算意图，CDATA 内的一律不算。** 现有 `skipXMLIgnoredSection` 已实现 CDATA（及 `<!-- -->` 注释）跳过，意图探测必须复用，**严禁裸 grep**。

以下一律**不算意图**：

- **CDATA 已忽略区内的 EPSE / 工具标签**（见上方重要提示，独立成条、最优先）。
- markdown 代码围栏（```）内、行内 code span（`` ` ``）内、注释 `<!-- -->` 内的 EPSE/工具标签 —— **复用 `skipXMLIgnoredSection`（CDATA+注释）、`markdownCodeSpanEnd`（行内 code span），围栏由 `stripFencedCodeBlocks` 剥除——三者都在 `toolcall` 包内，不复用 `toolstream` 包的有状态函数（原因见 §4.1）**，不新造。
- **只有 EPSE 开标签、无对应闭合标签的未闭合块**（流中途截断；见边界 §5.2）—— 本身即不满足「成对」条件。
- 无 `<|EPSE` 前缀的 canonical 工具标签（`<tool_calls>` / `<invoke>` / `<parameter>` 旧格式）—— 解析层仍向后兼容解析，但**不作为意图信号**（避免正文讲解工具调用时误中）。
- 纯文字提到工具词（「调用工具」「invoke」「parameter」等）而无标签语法。
- 孤立的 `<` / `>` 未构成标签。
- 已成功解析出的 call 不再作为意图信号的一部分——兜底触发与否，由「扣除这些成功区间后残余文本是否仍有成对 EPSE」决定（见 §4.2、§5.1）。

---

## 4. 在哪里加

### 4.1 新增意图探测函数

**位置**：`internal/toolcall/toolcalls_intent.go`（新文件）

**签名**：`func DetectToolCallIntent(text string) bool` —— 返回「CDATA/注释/围栏之外的文本里，是否存在**成对**的 `<|EPSE` 开标签 + `</|EPSE` 闭合标签」。

**入参语义**：传入的 `text` 是**扣除成功 call 源区间之后**的残余文本，不是原始全文——「是否还有成对 EPSE 外壳」即「是否还有无法解析的工具调用意图」。扣除由调用方（`parseToolCallsDetailedXMLOnly`，§4.2）在精确解析完成后执行；`DetectToolCallIntent` 自身不做解析、不做扣除、不关心区间归属。

**实现要点**：

- 第一步 `stripFencedCodeBlocks`：剥掉 markdown 代码围栏，保证围栏内的 EPSE 字面示例不参与探测（函数内部完成，调用方无需预处理）。
- 循环骨架照搬 `FindToolMarkupTagOutsideIgnored`（`toolcalls_scan.go`，同一包内、无状态）：逐位置先调 `skipXMLIgnoredSection` 跳过 CDATA 与注释，再按 `markdownCodeSpanEnd` 跳过行内 code span。
  > ⚠️ 不要引用 `toolstream` 包的 `insideCodeFenceWithState` / `markdownCodeSpanStateAt`——它们是有状态函数且属于 `toolstream`，而本函数在 `toolcall` 包；反向 import 会形成 `toolstream → toolcall → toolstream` 循环依赖。围栏用 `stripFencedCodeBlocks` 解决，行内 span 用同包 `markdownCodeSpanEnd` 解决。
- 在每个未被忽略的位置调扫描原语：先命中一个 EPSE **开标签**（`consumeToolMarkupLessThan` 消费 `<` → 无 `/` → `consumeToolMarkupNamePrefix` 消费前缀），随后继续向后扫描；若后续又命中任意 EPSE **闭合标签**（`consumeToolMarkupLessThan` 消费 `<` → 命中 `consumeToolMarkupClosingSlash` 消费 `/` → `consumeToolMarkupNamePrefix` 消费前缀），两者都在 → 返回 `true`。闭合标签的 `/` 在 `<` 与 `|EPSE` 之间，判定必须走 closing-slash 分支，不能按开标签路径消费。
- 闭标签本地名为空（`</|EPSE>`）在扫描器里是 `isEPSEShorthandParameter` 特例（返回 `name="parameter"`，要求前缀后直接到 `>`）。意图探测应把「`</|EPSE` 前缀 + 空名」**当作合法的 EPSE 闭合信号**，不要求本地名——闭标签只要带 `</|EPSE`（或等价变体）前缀即算闭合，本地名可有可无。
  > ⚠️ `epseLike` 不能当作「命中 epse 前缀」的证据：`consumeToolMarkupNamePrefix` 对 `<|foo|…>` 这类不含 epse 字面量的管道式前缀也会返回 `epseLike==true`。判断「前缀确实是 epse」必须校验消费到的前缀含 `epse` 字面量（可用 `hasEPSENamePrefixOrPartial`，或比对消费段原文）。否则假阳性退化成「见到 `<|` 就触发」，推翻 §3.1 的「专有 token 假阳性趋近零」论据。
- **开、闭两个标签的本地名可以不同，都不校验白名单**；只要开闭成对即返回 `true`，**不解析参数、不提取 name**。
- **严禁裸 grep**（`strings.Contains(text, "<|EPSE")` 会命中 CDATA 内、围栏内的字面示例，也看不到闭合关系）。
- 与 `looksLikeToolCallSyntax` 平行存在，不修改其语义，不污染精确扫描器。

### 4.2 数据结构变更

`internal/toolcall/toolcalls_parse.go::ToolCallParseResult` 增加字段：

```go
type ToolCallParseResult struct {
    Calls             []ParsedToolCall
    SawToolCallSyntax bool      // 保留原语义不变（扫到 tool_calls wrapper）
    SawToolCallIntent bool      // 新增：残余意图信号（扣除成功 wrapper 区间后仍有成对 EPSE = 还有无法解析的意图）
    RejectedByPolicy  bool
    RejectedToolNames []string
}
```

在 `parseToolCallsDetailedXMLOnly` 中，**先精确解析、再扣除、后探测**，不再对全文探测。

> ⚠️ **坐标系（本修订核心）**：扣除与探测都在**归一化之前的原文**（strip 围栏后、`normalizeEPSEToolCallMarkup` 之前的那份文本，下称 `original`）上进行，**不能用 `normalized`**。`normalized` 只用于精确解析。理由：归一化会把合法本地名的 EPSE 外壳改写成 canonical（`rewriteEPSEToolMarkupOutsideIgnored` 只写 `tag.Name`、不写 `|EPSE|` 前缀），抹掉 `|EPSE|` 标记——在 `normalized` 上 `DetectToolCallIntent` 恒找不到 EPSE、恒返回 false（详见 §1.2 第 2 步、§3.1、§7 第 4 行）。

```go
// 时序（示意，非最终实现）：原文 → 精确解析（在 normalized 上）→ 成功 call 区间
// 重定位回原文 → 从原文扣除 → 对原文残余探测
original := stripFencedCodeBlocks(trimmed)              // 归一化之前的原文
normalized, ok := normalizeEPSEToolCallMarkup(original)  // 仅供精确解析
parsed := parseXMLToolCalls(normalized)                  // 成功 call 需携带源区间（见下方前提）
// spans = 把每个成功 call 在 normalized 上的 [start,end) 重定位回 original（位置映射或二次扫描定位）
// residual = 从 original 里挖掉每个「成功 wrapper」的整个源区间后剩下的文本
// （全部 invoke 解析成功的 wrapper 连 <tool_calls> 开闭标签一起挖；部分失败的保留外壳、只挖成功 invoke）
result.SawToolCallIntent = DetectToolCallIntent(residual) // 在原文残余上探测，而非 normalized
```

`SawToolCallIntent` 是**残余级**意图信号：只回答「扣除成功 wrapper 在原文中的源区间后，残余原文里还有没有成对的 EPSE 外壳」——也就是「还有没有无法解析的工具调用意图」。它**依赖解析结果、在扣除之后执行**。

> ⚠️ 探测必须放在解析之后，且**不能被 `parseToolCallsDetailedXMLOnly` 里任何一条 early-return 跳过**——「全部失败」（0 calls）恰恰是兜底最关键的场景，早退会漏掉 `SawToolCallIntent=true`。当前实现共有 **3 个**可达早退点：`trimmed==""`、strip 后 `trimmed==""`、`len(parsed)==0`，其中 `len(parsed)==0` 最关键（非空文本、解析全失败时走这里）。注：`normalizeEPSEToolCallMarkup` 之后的 `if !ok { return }` 是**死代码**——该函数三条 return 路径恒返回 `true`（`toolcalls_dsml.go`），`!ok` 永不成立，不构成吞掉意图的路径。实施时要把「重定位 + 扣除 + 探测」放在 `len(parsed)==0` 早退之前，或重构为无论哪条路径都会执行到探测；空文本的两个早退（`trimmed==""` / strip 后空）本无 EPSE、意图为 false 属正确默认值，无需特殊处理。

兜底触发条件（第二部分使用）：**`SawToolCallIntent` 为真即触发**（探测已经在残余文本上跑过，不再叠加第二次扣除判断）。

- 同时覆盖「全部失败」「全部成功」「部分失败」（§5.1 已定案）：
  - **全部失败**：`len(Calls)==0`，无可扣除区间，残余=原文，`SawToolCallIntent` 即对**原文**的探测；
  - **全部成功**：`len(Calls)>0` 且每个 wrapper 内所有 invoke 都解析成功，扣除后无残余，`SawToolCallIntent=false`。⚠️ 前提是扣除按 **wrapper 粒度**：连 `<tool_calls>` 开闭标签一起挖掉；若只挖 invoke 区间，会留下空外壳 `<|EPSE|tool_calls></|EPSE|tool_calls>`，其成对的 EPSE 会让 `DetectToolCallIntent` 误判 true（本轮审计修正的核心）。
  - **部分失败**：`len(Calls)>0` 但某 wrapper 内存在坏 invoke 残片，残余=在**原文**里扣除「成功 wrapper 整个区间 / 成功 invoke 区间（保留失败 wrapper 的外壳）」后的残片，`SawToolCallIntent` 即对残片的探测。
- 实现前提（二轮修订，wrapper 粒度 + original 二次扫描）：扣除以 **wrapper 为最小单位**，不用 invoke 级 span。`findToolCallElementBlocksOutsideIgnored` 已返回每个 `<tool_calls>` 块的**绝对** `Start/End`（相对 normalized 全文），`parseXMLToolCalls` 内部对每个 wrapper 按 `findXMLElementBlocks(wrapper.Body, "invoke")` 逐 invoke 解析——invoke 的 span 是**相对 `wrapper.Body` 子串**的，并非全文坐标（原「补 invoke span」方案被低估的坑）。wrapper 粒度天然回避这个子串偏移：全部成功就挖整个 wrapper 绝对区间；部分失败时失败残片限于该 wrapper 内部，在该 wrapper 内二次扫描定位即可，不跨块匹配。
- **坐标映射统一方案**：扣除所需区间**在 original 上二次扫描定位**，而不是把 normalized/recovered 坐标回映。`findToolCallElementBlocksOutsideIgnored` + `FindMatchingToolMarkupClose` 均无状态，可直接跑在 original 上（识别 EPSE 变体）。normalize / rewrite / `SanitizeLooseCDATA` 只改标签写法与 CDATA 内容，**不增删、不重排 wrapper 数量与顺序**，故解析结果里的「第 N 个 wrapper 是否全部成功」可按序号与 original 上第 N 个 wrapper 一一对应。这样一并规避了「normalized→original 位置映射」与「CDATA 恢复分支的 recovered 坐标系」两处坐标纠缠。

> 注意：`SawToolCallSyntax` 语义保持不变，避免破坏 sieve 的 rejected 分支与现有调试文档（`references/epse-parse-debugging.md`）对它的依赖。

### 4.3 消费层接线（第二部分实施时用）

意图识别结果需要在两个消费点落地，边界不同：

1. **流式 sieve**（`internal/toolstream/tool_sieve_xml.go::consumeXMLToolCapture`）
   - **兜底在「块闭合」之后、「透传坏文本」之前同步完成**（不发坏 delta，先修复）。
   - 时序：
     - 块未闭合 → `anyOpenFound` hold 分支不触发兜底（本就不满足「成对」、`Intent=false`），继续等流到齐；
     - 块闭合 + 解析出 call → 直接发 tool_calls（现状不变）；
     - 块闭合 + 残余仍有意图（`SawToolCallIntent` 为真：扣除成功 call 区间后剩坏残片，覆盖全部失败与部分失败）→ **先同步兜底**，修复成功发 tool_calls，失败才透传原文为正文。
   - 约束：兜底必须是**同步、确定性**修复（补闭合标签/修本地名/重解析等），不能依赖「看到更多未来内容」或异步 LLM 调用，否则卡住实时流——这约束第二部分的修复手段。

2. **finalize**（`internal/assistantturn/turn.go::BuildTurnFromStreamSnapshot` 及 `shared.DetectAssistantToolCalls`）
   - 作为**第二道防线**：流式兜底成功时坏文本不进 `visibleText`，finalize 通常无需再兜底；保留 finalize 兜底覆盖「流式未兜住」与「非流式路径直接 finalize」。
   - 必须**独立于** `DetectAssistantToolCalls` 内部的 `visibleText != ""` 早退分支（`internal/httpapi/openai/shared/assistant_toolcalls.go` 第 14 行已确认存在），对 `RawText`（或 strip 结果）单独跑一次意图探测与兜底。落地位置是 `BuildTurnFromStreamSnapshot`（`turn.go`）里 `shared.DetectAssistantToolCalls(...)` 调用之外/之前，**不能**只在 `DetectAssistantToolCalls` 内部加逻辑，否则早退仍会跳过。

---

## 5. 边界条件

### 5.1 单块内部分成功仍兜底（已定案）

模型一个块里写 2 个 invoke、1 个成功 1 个失败：当前 `parseXMLToolCalls` 静默丢弃失败的 invoke、只返回 1 call。**决策：成功的保留，失败的也尽力兜底修复，合并输出。**

做法（与 §4.2 触发条件一致）：先精确解析得到成功 wrapper（及其内部成功 invoke）与源文本区间 → 按 wrapper 粒度扣除：该 wrapper 内**全部 invoke 成功**则连外壳一起挖掉；**部分失败**则保留外壳、只挖成功的 invoke 区间 → 对剩余残片跑 `DetectToolCallIntent`，仍为真则对残片兜底修复 → 修复出的 call 与成功 call 合并。

> **实现状态（M3，已落地）**：`assistantturn.maybeRepairToolCalls` 的门槛已从「`len(Calls)>0` 就不修」改为「`SawToolCallIntent && ResidualIntentText != ""` 即修」，因此部分成功场景（有 ≥1 成功 call 但仍有坏残片）也会触发 LLM 残片修复。修复出的 call 由 `mergeRepairedToolCalls` 合并进已有成功 calls：**已有 calls 在前、修复残片在后**，按 `name`+`input`（`reflect.DeepEqual`）去重，避免重复调用。合并后统一 `NormalizeParsedToolCallsForSchemas`。非流式与所有流式 finalize 路径共用同一套合并逻辑。残片修复失败仍走降级（不泄漏占位符、不破坏已有成功 calls）。

Caveat：
- 成功 wrapper 的绝对区间由 `findToolCallElementBlocksOutsideIgnored` 的 `Start/End` 提供，扣除定位走 §4.2 的「original 二次扫描 + 序号对应」，不补 invoke 级 span；裸 invoke（经 `repairMissingXMLToolCallsOpeningWrapper` 合成 wrapper 的成功解析）原文无 wrapper 外壳，扣除对象退化为该 invoke 本身在原文中的区间。
- **边界错乱时兜底能力有限**：若坏 invoke 是「闭合标签漏名」导致深度匹配错位（与相邻 invoke 边界纠缠），精确解析连成功 invoke 的区间都可能判错，扣除与残片定位随之失真。以「能救则救」为准，不追求完备性。

### 5.2 流中途未闭合块不兜底

`anyOpenFound`（有开标签、无匹配闭合）表示流还没到齐，属正常分片，**必须等**，不触发兜底。同时，这种「只开不闭」本身就不满足 §3.1 的成对条件，`SawToolCallIntent=false`。半截工具调用是上游断连/继续写问题，归 auto_continue 管，不是格式错。

### 5.3 兜底先于透传，不做「事后剥离」（已定案）

不采用「先透传坏文本、兜底成功后再从 content 剥离已发 delta」的方案——已发 delta 无法可靠撤回，剥离会改动客户端已渲染内容。

改为**事前拦截**：流式阶段兜底在「块闭合、透传坏文本之前」同步完成（§4.3.1），兜底成功直接发 tool_calls、不产出坏文本 delta。「已泄露文本」从根上不产生。

注意：这要求兜底是同步/确定性修复（同 §4.3.1 约束）。若某类修复必须依赖更多上下文或异步手段（如 phase3 的 LLM 修复），则放 finalize 兜底处理，流式阶段宁可透传也不卡流——此时坏文本已进 `visibleText`，finalize 兜底成功时需在 content 层对「已识别 EPSE 区间」做一次剥除（仅影响最终 content，不涉及已发 delta）。这是退化路径，不是主路径。

> **实现状态（M4，已落地）**：LLM 修复（phase3）已接入所有流式 finalize 定稿路径（chat / responses / claude / gemini）。定稿时机在「最终帧发送之前」触发一次修复：一旦修复成功得到 `tool_calls`，最终交付以修复后的结构化 tool_calls 为准——有 tool_calls 即无 visible text，坏文本不作为可见正文暴露。若结构化 tool_calls 已在增量阶段发出（`AlreadyEmittedCalls`/`AlreadyEmittedToolRaw`），则不再触发 finalize 修复（已发结构无法撤回）。逐 chunk 增量阶段仍不引入异步 LLM。

---

## 6. 已定决策项

1. §5.1：单块 ≥1 call 成功时，失败的残片也兜底修复（先解析 → 扣除成功区间 → 对残余兜底 → 合并）。已定案。
2. §5.3：兜底先于 delta 透传，不做事后剥离。已定案。

遗留注意点（第二部分设计时的约束）：
- 流式兜底必须是同步/确定性修复，否则卡流；依赖上下文或异步的手段放 finalize。
- 扣除按 wrapper 粒度（全部成功连外壳一起挖、部分失败保留外壳只挖成功 invoke），span 用 wrapper 绝对区间 + original 二次扫描，不补 invoke 级 span。
- 边界错乱的坏 invoke（闭合漏名导致深度匹配错位）兜底能力有限，以「能救则救」为准。

---

## 7. 覆盖矩阵（意图信号 vs 典型格式错误）

> 注：本列「新意图信号」指**在归一化之前的原文上、扣除成功 call 源区间后**对残余原文的探测结果（§4.2）。解析成功的规范形态因「扣除后无残余」判 **false**，属期望行为（已成功解析、无需兜底），不是漏判。第 4 行「闭合漏名」为 true 的前提是探测跑在原文上——若误在 `normalized` 上探测，`<|EPSE|tool_calls>`/`</|EPSE>` 已被改写成 `<tool_calls>`/`</parameter>`，EPSE 归零、恒 false（这正是 §4.2 必须用原文坐标系的原因）。

| 模型输出形态 | 当前 `SawToolCallSyntax` | 新意图信号 |
|---|---|---|
| `<\|EPSE\|tool_calls>…</\|EPSE\|tool_calls>`（规范，成对） | true | false（解析成功，按 wrapper 粒度扣除后无残余，不触发兜底） |
| `<\|EPSE\|call name="bash">…</\|EPSE\|call>`（本地名错，成对） | **false（漏）** | **true** |
| `<\|EPSE\|invoke name="bash">…</\|EPSE\|invoke>`（漏 wrapper，成对） | false | **true** |
| `<\|EPSE\|tool_calls>…</\|EPSE>`（闭合漏名，仍 EPSE 闭合） | true | true（本就要靠解析失败走兜底） |
| `<\|EPSE\|tool_calls>…`（只有开标签、无闭合，流中断） | true | **false（不闭合不算意图）** |
| `<tool_calls>…</tool_calls>`（canonical 无前缀） | true | **false（无 EPSE 前缀，不算意图）** |
| markdown 围栏内的 `<\|EPSE\|tool_calls>` | false | false（排除） |
| `<![CDATA[<\|EPSE\|tool_calls>]]>`（CDATA 包裹的字面示例） | false | **false（重要：CDATA 内排除）** |
| 正文提到「tool_calls」字样 | false | false（排除） |
| `{"name":"bash","arguments":{}}`（JSON 形态） | false | false（无 EPSE 前缀，不算意图） |
