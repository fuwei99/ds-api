# 兜底修复的 CDATA 占位符（slot）设计文档

> 状态：已实现（2026-08-29 补充边界受限扫描）—— 初稿 2026-08-21
> 范围：工具调用兜底的**第二部分（兜底恢复）**的支撑机制，与第一部分（意图识别，`tool-call-fallback-design-phase1.md`）正交。本机制解决「超长 CDATA 参数在兜底修复时的干扰」问题。
> 前置：阅读前需理解 phase1 的坐标系约定（`original` / `normalized` / `parseSource` / `residual`）与 §4.3 的消费层接线边界。

---

## 0. 背景与目标

模型输出工具调用时，参数值包在 `<![CDATA[...]]>` 里。常见于创建代码、文学创作等场景，单条参数可达几十 KB 甚至 MB 级。

当格式错到解析失败、进入兜底修复（Part 2）时，直接拿这份原文去修会有三个干扰源：

1. **确定性修复的成本**：任何要重建/重序列化整个文本的修复手段，都会无谓地反复拷贝、扫描这些超大 CDATA。
2. **LLM/异步修复的 token 爆炸**（若 finalize 兜底允许异步 LLM）：超长代码吃满上下文，还稀释注意力、抬高成本。
3. **内容被误改风险**：修复过程若重写文本，可能"顺手"污染 CDATA 里的真实代码/正文。

本机制目标：**在把残余文本交给修复层之前，把 CDATA 内容全部替换为短占位符（slot），修复完成后、解析之前再逐字节还原**。修复层永远只面对"骨架 + 短占位符"，不再接触真实长内容。

核心结论一句话：

> 修复层处理的 CDATA 内容是**对结构不可见的 opaque 数据**；把 `long content` 换成 `D1` 不改变标签骨架，因此结构修复结果不变。前提：占位符**留在 CDATA 外壳内**，且修复层**逐字节透传 CDATA**。

---

## 1. 可行性结论

**可行，低风险。**

- 现有解析器已把 CDATA 当 opaque 跳过（`skipXMLIgnoredSection`），"修复层不关心 CDATA 内容"是既有事实，不是新假设。
- 占位符替换只改 CDATA **内部字节**，不改 `invoke`/`parameter` 的标签、属性、嵌套、配对，故结构骨架逐字节等价。
- 风险集中在替换/还原两端的**边界实现**，而非方案本身（详见 §4）。

前提（须作为 Part 2 修复实现的硬约束）：

1. 修复层对 CDATA 区域必须**逐字节透传**，不 unescape、不 re-escape、不 trim、不重排、不 drop 内容。
2. 还原只能发生在修复完成之后、最终解析之前。
3. **修复层不得用 `findToolCDATAEnd` / `skipXMLIgnoredSection` 重新判定 CDATA 边界**——CDATA 区间在 slot 时以绝对坐标登记，修复层只透传该区间，不得因 markdown 围栏等依赖内容本身的判定而改变对 CDATA 闭合/边界的判断。

---

## 2. 方案设计

### 2.1 占位符形态：留外壳、换内容

**错误做法**：把整个 `<![CDATA[...]]>` 换成裸 token（如 `D1`），会破坏 parameter 子节点结构。

**正确做法**：只换 CDATA 内部内容，外壳保留：

```
原文（本地名错，解析失败）：
<|EPSE|call name="Bash">
  <|EPSE|parameter name="command"><![CDATA[#!/bin/bash\n...几十KB...]]></|EPSE|parameter>
</|EPSE|call>

slot 后（交给修复层）：
<|EPSE|call name="Bash">
  <|EPSE|parameter name="command"><![CDATA[DS_SLOT_0]]></|EPSE|parameter>
</|EPSE|call>
```

骨架完全一致，`findToolCDATAEnd` / `skipXMLIgnoredSection` 对它的处理与原文无差别。

### 2.2 占位符 token 选择

**硬约束**：token 内不得出现 `]]>`、`]]＞`、`]]〉`、`<`、`>`、`&`、`|`（避免提前终结 CDATA 或干扰标签扫描）。

推荐形态：`DS_SLOT_{idx}`（或用不可打印控制符，如 `\x1e{idx}\x1e`，碰撞概率趋近零）。

**防碰撞**：替换前先扫一遍所有待 slot 的 CDATA 内容，若任一原文含所选 token 前缀字符串，则整体换用另一 delimiter（或控制符）。**换 delimiter 后必须重新扫描**（新 delimiter 的碰撞集与旧的不同），直到找到与所有待 slot 内容均无前缀冲突的形态为止。**不要用裸 `D1`/`D2`**——原文代码里出现 `D1` 字样的概率不可忽略。

### 2.3 时序：替换在 residual 之后、repair 之前

与 phase1 的衔接（**必须隔离**，替换后的文本是全新字符串，与 `original`/`parseSource` 的字节坐标无映射关系）：

```
original（归一化前原文，含坏 EPSE 外壳）
  → [phase1] 扣除成功 wrapper → residual（残余原文，仍含超长 CDATA）
  → [本机制] slotCDATAContent / slotCDATAContentBounded(residual)  // 双解释 slot
  → [Part2] repair(skeleton)                         // 修复层只见骨架+占位符
（slot 遇未闭合 CDATA：贪婪跳过继续、边界受限则按元素边界 slot 且不输出闭合符）
  → [本机制] restoreCDATASlotsFor(repaired, res)     // 用与 slot 相同的扫描重扫还原
  → 从头 parse 修复后的完整文本 → 与已成功 calls 合并
```

**实现偏离（2026-08-29）**：本节原设计要求在 slot 之前先跑 `SanitizeLooseCDATA(residual)` 剥掉未闭合 opener。实现**不这么做**，理由是：`SanitizeLooseCDATA` 剥掉 opener 等于抹掉「缺 `]]>`」这个病灶，而修复层必须看见病灶才能补上闭合符。边界受限扫描改为直接 slot 元素内未闭合区间（`closeLen=0`），既抽走长内容又保留病灶，是更贴合 §0 目标的做法。

### 2.4 数据结构与函数签名（示意）

新文件：`internal/toolcall/toolcalls_cdata_slots.go`（与 Part 1 同一包，复用扫描原语）。

```go
// slotCDATAContent 把 text 里所有闭合完好的 CDATA 内容替换为占位符，
// 返回 skeleton、按出现顺序排列的原内容表，以及各 CDATA 区间的绝对坐标
// （skeleton 坐标，供 restore 精确还原，避免修复层重扫）。
// 未闭合的 CDATA 不参与 slot：遍历遇到未闭合区间时**跳过该区间继续扫**，
// 不阻断后续完好 CDATA 的 slot（注意与 skipXMLIgnoredSection 的 blocked 行为区分）。
func slotCDATAContent(text string) (skeleton string, slots []string, cdataSpans [][2]int)

// restoreCDATASlots 用 slots 表把 skeleton 里的占位符还原为原内容。
// 按 token 精确匹配（非序号、非裸 ReplaceAll），缺失/多出的 token 视为
// 修复层异常，返回错误由调用方降级到"不 slot 直接兜底"路径。
func restoreCDATASlots(skeleton string, slots []string) (string, error)
```

### 2.5 复用原语（不新造）

- `indexToolCDATAOpen` / `toolCDATAOpenLenAt` / `findToolCDATAEnd` / `toolCDATACloseLenAt` —— 定位 CDATA 区间（`toolcalls_candidates.go`、`toolcalls_parse_markup.go`）。
- `SanitizeLooseCDATA` —— 先剥未闭合 opener（`toolcalls_markup.go:143`）。
- slot 的遍历骨架照搬 `skipXMLIgnoredSection` 的循环（同一套 CDATA/注释跳过），保证与解析器识别出的 CDATA 区间**同一口径**，不出现"解析器认为的 CDATA"与"slot 认为的 CDATA"不一致。但**差异点**：`skipXMLIgnoredSection` 遇未闭合 CDATA 返回 `blocked=true` 会中断整体扫描；`slotCDATAContent` 必须改为**跳过未闭合区间继续扫**，否则一处未闭合会吞掉后续所有完好的 CDATA。

---

## 3. 边界条件

- **未闭合 CDATA**：贪婪扫描不参与 slot；边界受限扫描（repair 路径）按元素边界 slot 该区间且不输出闭合符，把「缺 `]]>`」留给修复层补。
- **`]]>` 嵌在内容里**：复用 `findToolCDATAEnd` 的结构化判定（`cdataEndLooksStructural`），slot 与解析器对 CDATA 边界的认识保持一致。
- **`findToolCDATAEnd` 无边界前向搜索**（2026-08-29 补）：该函数会一路找到全文下一个 `]]>`，不受当前 parameter 元素约束。参数缺 `]]>` 时贪婪 slot 会把 `</|EPSE|parameter><|EPSE|parameter name="…">` 吞进同一个 slot，**骨架整体丢失一个参数节点**，这是线上一次修复失败的直接根因。边界受限扫描先用 `nextToolMarkupStructuralBoundary` 定位元素边界，再在 `text[:limit]` 上找 `]]>`，从而封顶搜索范围。
- **同一根因也会让确定性解析器静默错解**（2026-09-01 补）：本节的边界受限扫描当时只服务于 repair 路径的 slotting，确定性解析器仍按无边界搜索工作，于是 `<|EPSE|parameter name="command"><![CDATA[…（缺 `]]>`）</|EPSE|parameter><|EPSE|parameter name="description"><![CDATA[…]]>` 这类输入会“解析成功 1 个 call”，但 `command` 值里带上了 `</|EPSE|parameter><|EPSE|parameter name="description"><![CDATA[…` 的工具壳、`description` 直接消失；因为解析成功，wrapper 被整段扣减、`SawToolCallIntent=false`，本兜底链路完全不触发。修复见 `internal/toolcall/toolcalls_cdata_boundary_repair.go`：最终 parse 阶段识别越界形态（参数体内同时有自己的 `</parameter>` 闭合标签与其后一个未配对的 `<![CDATA[`），按元素边界补 `]]>` 后重解，并按与 `pickBetterRepairResult` 一致的口径择优；无法确定性修好的坏码仍回到零 call + 意图为真，继续走本文档的兜底链路。
- **大量 slot**：`slots` 表按出现顺序，索引开销 O(1) 查表，还原为 O(n) 单次扫描。
- **修复层重排/drop 了某个 invoke**：用唯一 token 匹配（非序号）还原，被 drop 的 invoke 其 slot 内容随之丢弃（该 invoke 本就应被丢弃），不产生错位污染。
- **参数内容恰好包含 slot token 前缀**：见 §2.2 防碰撞守卫，换 delimiter。
- **还原必须与 slot 用同一套扫描**（2026-08-29 补）：边界受限骨架可能带元素内未闭合 CDATA，若用贪婪重扫会越过这些 opener 并把后面的占位符当成「区间外泄漏」报错。`restoreCDATASlotsFor` 按 `cdataSlotResult.bounded` 选择扫描。
- **与 `preservesCDATAStringParameter` 无交互**：还原后 CDATA 字节与原文一致，后续 `parseInvokeParameterValue` 看到的输入不变，类型判定结果不变。

---

## 4. 风险与降级

1. **修复层未按约束透传 CDATA**（如重序列化时 html-unescape 了内容）→ 还原后内容与原文不等。缓解：还原后比对 slot 表字节，不等则**整条降级**为"不 slot 的原始兜底路径"，绝不出错渲染。
2. **占位符 token 泄漏进非 CDATA 区**（修复层不正确地移动了内容）→ `restoreCDATASlots` 仅在 CDATA 区间内匹配 token，区间外的 token 不还原，**且一旦发现区间外 token 即整条降级**到“不 slot 的原始兜底路径”——绝不能让占位符字面量（`DS_SLOT_0`）被当作普通内容传给下游（否则 `parseInvokeParameterValue` 会把它当垃圾值解析）。
3. **LLM 修复不按"原样输出占位符"指令执行** → 若 Part 2 引入 LLM 修复，必须在 prompt 里强制"输出与输入完全一致的 CDATA 占位符，禁止改写"，且用 §4.1 的字节比对做最终校验；校验失败降级。

---

## 5. 已定决策项 / 待拍板

已定案（本文档）：

1. 占位符**留 CDATA 外壳、只换内容**，不用裸 token。
2. 替换只在 residual 之后、repair 之前发生；还原只在 repair 之后、parse 之前发生，与 Part 1 坐标彻底隔离。
3. 还原用唯一 token 匹配 + 字节比对校验，失败降级到"不 slot 兜底"，不产出错误内容。

待拍板（需用户在实现 Part 2 时确认）：

- **阈值 or 全量**：默认对 residual 里**所有**闭合完好 CDATA 统一 slot（实现最简单、无边界分支）。可选优化：仅对 `len(content) > N`（如 1KB）的 CDATA 做 slot，短参数原样保留——代价是多一个分支和"长/短 CDATA 混合"的测试面。
- **token 形态**：默认 `DS_SLOT_{idx}`；若担心原文命中，换控制符 `\x1e{idx}\x1e`（碰撞趋零）。二者由 §2.2 防碰撞守卫自动切换。

已在实现中定案（2026-08-29）：

1. **全量 slot**，无长度阈值。
2. token 形态取 `DS_SLOT_{idx}`，碰撞时由守卫自动切到 `\x1e` 包裹变体。
3. 不在 slot 前跑 `SanitizeLooseCDATA`（见 §2.3 偏离说明）。
4. §4 风险 1 的「还原后比对 slot 表字节」退化为构造性保证：还原时写回的就是原始字节，不可能不等；违规只会以 §4 的报错条件浮现。
5. 双解释择优只用一次 LLM 调用的同一份输出，不重复请求上游。

---

## 6. 覆盖矩阵（slot 行为 vs 场景）

| 场景 | 行为 |
|---|---|
| 参数超长（代码/长文），CDATA 闭合完好 | slot，修复层见短占位符，还原后 parse |
| 参数短（`pwd`、`1+1`） | 默认也 slot（若选全量）；还原无感 |
| 未闭合 CDATA（流中断残留） | 贪婪跳过；边界受限按元素边界 slot（`closeLen=0`），缺 `]]>` 留在骨架给修复层补 |
| 参数缺 `]]>` 且后接另一个参数 | 贪婪跨节点吞噬（骨架丢参数节点）→ 触发 `swallowed_structure`，改用边界受限骨架 |
| 参数正文含 `</|EPSE|parameter>` 字样但自身 `]]>` 正常 | 吞噬签名不成立，保持贪婪解释，不误切 |
| 参数正文嵌套完整工具调用示例（含 `<![CDATA[`） | 签名成立→边界受限升主；但贪婪备选解析更齐，择优回到贪婪 |
| 多个参数、多条 invoke | 按出现顺序入 `slots` 表，唯一 token 还原 |
| 修复层 drop 了某条坏 invoke | 该 invoke 的 slot 随之下沉，不污染其余 |
| 原文含 `DS_SLOT_` 字样 | 防碰撞守卫换 delimiter，不误替换 |
| 修复层重排 invoke 顺序 | 唯一 token 匹配，顺序无关 |
| 修复层改写了 CDATA 内容（违规） | 还原报错 → 整条降级，不泄漏占位符 |
| 模型照抄骨架但没补 `]]>` | 未闭合 opener + 干净占位符可还原；最终 parse 若仍失败则安全降级 |
