# API -> 网页对话纯文本兼容主链路说明

文档导航：[总览](../README.MD) / [架构说明](./ARCHITECTURE.md) / [接口文档](../API.md) / [测试指南](./TESTING.md)

> 本文档是 DS2API“把 OpenAI / Claude / Gemini 风格 API 请求兼容成 DeepSeek 网页对话纯文本上下文”的专项说明。
> 这是项目最重要的兼容产物之一。凡是修改消息标准化、tool prompt 注入、tool history 保留、文件引用、current input file、下游 completion payload 组装等行为，都必须同步更新本文档。

## 1. 核心结论

DS2API 当前的核心思路，不是把客户端传来的 `messages`、`tools`、`attachments` 原样转发给下游。

而是把这些高层 API 语义，统一压缩成 DeepSeek 网页对话更容易理解的三类输入：

1. `prompt`
   一个单字符串，里面带有角色标记、system 指令、历史消息、assistant reasoning 标签、历史 tool call XML 等。
2. `ref_file_ids`
   一个文件引用数组，承载附件、inline 上传文件，以及必要时被拆出去的历史文件。
3. 控制位
   例如 `thinking_enabled`、`search_enabled`、部分 passthrough 参数。

也就是说，项目最重要的兼容动作，是把“结构化 API 会话”翻译成“网页对话纯文本上下文 + 文件引用”。

## 2. 为什么这是核心产物

因为对下游来说，真正稳定的输入面不是 OpenAI/Claude/Gemini 的原生 schema，而是：

- 一段连续的对话 prompt
- 一组可引用文件
- 少量开关位

这也是为什么很多表面上看像“协议兼容”的代码，最终都会收敛到同一类逻辑：

- 先把不同协议的消息统一成内部消息序列
- 再把工具声明改写成 system prompt 文本
- 再把历史 tool call / tool result 改写成 prompt 可见内容
- 最后输出成 DeepSeek completion payload

## 3. 统一心智模型

当前主链路可以这样理解：

```text
客户端请求
  -> HTTP API surface（OpenAI / Claude / Gemini）
  -> promptcompat 统一消息标准化
  -> tool prompt 注入
  -> DeepSeek 风格 prompt 拼装
  -> 文件收集 / inline 上传（OpenAI 文件链路）
  -> current input file（completion runtime 全局入口）
  -> prompt segment（超长提示词分段；2.4.0 模型合并后适用于所有模型）
  -> completion payload
  -> 下游网页对话接口
  -> assistantturn 输出语义归一（Go 非流式 + 流式收尾）
  -> 各协议 renderer（OpenAI / Responses / Claude / Gemini）
```

对应的关键代码入口：

- OpenAI Chat / Responses：
  [internal/promptcompat/request_normalize.go](../internal/promptcompat/request_normalize.go)
- OpenAI prompt 组装：
  [internal/promptcompat/prompt_build.go](../internal/promptcompat/prompt_build.go)
- OpenAI 消息标准化：
  [internal/promptcompat/message_normalize.go](../internal/promptcompat/message_normalize.go)
- Claude 标准化：
  [internal/httpapi/claude/standard_request.go](../internal/httpapi/claude/standard_request.go)
- Claude 消息与 tool_use/tool_result 归一：
  [internal/httpapi/claude/handler_utils.go](../internal/httpapi/claude/handler_utils.go)
- Gemini 复用 OpenAI prompt builder：
  [internal/httpapi/gemini/convert_request.go](../internal/httpapi/gemini/convert_request.go)
- DeepSeek prompt 角色标记拼装：
  [internal/prompt/messages.go](../internal/prompt/messages.go)
- prompt 可见 tool history XML：
  [internal/prompt/tool_calls.go](../internal/prompt/tool_calls.go)
- 最新 user 思考格式注入：
  [internal/promptcompat/thinking_injection.go](../internal/promptcompat/thinking_injection.go)
- completion payload：
  [internal/promptcompat/standard_request.go](../internal/promptcompat/standard_request.go)
- Go 输出侧 assistant turn：
  [internal/assistantturn/turn.go](../internal/assistantturn/turn.go)
- Go completion runtime：
  [internal/completionruntime/nonstream.go](../internal/completionruntime/nonstream.go)

## 4. 下游真正收到的东西

在“完成标准化后”，下游 completion payload 的核心形态是：

```json
{
  "chat_session_id": "session-id",
  "model_type": "default",
  "parent_message_id": null,
  "prompt": "[System]:...",
  "ref_file_ids": [
    "file-history",
    "file-systemprompt",
    "file-other-attachment"
  ],
  "thinking_enabled": true,
  "search_enabled": false,
  "action": null,
  "preempt": false
}
```

重点是：

- `prompt` 才是对话上下文主载体。
- `ref_file_ids` 只承载文件引用，不承载普通文本消息。
- `tools` 不会作为“原生工具 schema”直接下发给下游，而是被改写进 `prompt`。
- 对外返回给客户端的 `prompt_tokens` / `input_tokens` / `promptTokenCount` 不再按“最后一条消息”或字符粗估近似返回，而是基于**完整上下文 prompt**做 tokenizer 计数；为了避免上下文实际超限但客户端误以为还能塞下，请求侧上下文 token 会额外保守上浮一点，宁可略大也不低估。
- 当前 `/v1/chat/completions` 业务路径仍是“每次请求新建一个远端 `chat_session_id`，并默认发送 `parent_message_id: null`”；因此 DS2API 对外默认表现为“新会话 + prompt 拼历史”，而不是复用 DeepSeek 原生会话树。
- 但 DeepSeek 远端本身支持同一 `chat_session_id` 的跨轮次持续对话。2026-04-27 已用项目内现有 DeepSeek client 做过一次不改业务代码的双轮实测：同一 `chat_session_id` 下，第 1 轮返回 `request_message_id=1` / `response_message_id=2` / 文本 `SESSION_TEST_ONE`；第 2 轮重新获取一次 PoW，并发送 `parent_message_id=2` 后，成功返回 `request_message_id=3` / `response_message_id=4` / 文本 `SESSION_TEST_TWO`。这说明“同远端会话持续聊天”能力存在，且每轮需要携带正确的 parent/message 链接信息，同时重新获取对应轮次可用的 PoW。
- OpenAI Chat / Responses 原生走统一 OpenAI 标准化与 DeepSeek payload 组装；Claude / Gemini 会尽量复用 OpenAI prompt/tool 语义，其中 Gemini 直接复用 `promptcompat.BuildOpenAIPromptForAdapter`。Go 主服务新增 `completionruntime` 启动层，统一执行 DeepSeek session/PoW/call；输出侧新增 `assistantturn` 语义层：非流式 OpenAI Chat / Responses / Claude / Gemini 会把 DeepSeek SSE 收集结果先归一成同一份 assistant turn，再分别渲染成各协议原生外形；流式 OpenAI Chat / Responses / Claude / Gemini 继续保持各协议实时 SSE framing，但最终收尾的 tool fallback、schema 归一、usage、empty-output / content-filter 错误语义同样由 `assistantturn` 判定。Claude / Gemini 的常规 Go 主路径不再依赖内部 `httptest` 转发到 OpenAI handler；`translatorcliproxy` 仅保留用于 Vercel bridge、后端缺失 fallback 和回归测试，不作为主业务协议转换中心。
- Vercel Node 流式路径本轮不迁移，仍使用现有 Node bridge / stream-tool-sieve 实现；后续若变更 Node 流式语义，需要按 `assistantturn` 的 Go canonical 输出语义同步对齐。
- 客户端传入的 thinking / reasoning 开关会被归一到下游 `thinking_enabled`。Gemini `generationConfig.thinkingConfig.thinkingBudget` 会翻译成同一套 thinking 开关；关闭时即使上游返回 `response/thinking_content`，兼容层也不会把它当作可见正文输出。若最终解析出的模型名带 `-nothinking` 后缀，则会无条件强制关闭 thinking，优先级高于请求体中的 `thinking` / `reasoning` / `reasoning_effort`。未显式关闭时，各 surface 会按解析后的 DeepSeek 模型默认能力开启 thinking，并用各自协议的原生形态暴露：OpenAI Chat 为 `reasoning_content`，OpenAI Responses 为 `response.reasoning_text.delta` / `reasoning` item，Claude 为 `thinking` block / `thinking_delta`，Gemini 为 `thought: true` part。
- 对 OpenAI Chat / Responses 的非流式收尾，如果最终可见正文为空，兼容层会优先尝试把思维链中的独立 EPSE / XML 工具块当作真实工具调用解析出来。流式链路也会在收尾阶段做同样的 fallback 检测，但不会因为思维链内容去中途拦截或改写流式输出；真正的工具识别始终基于原始上游文本，而不是基于“已经做过可见输出清洗”的版本。最终可见层会剥离已经成功解析成工具调用的完整 leaked EPSE / XML `tool_calls` wrapper；如果遇到完整 wrapper 但内部形态不符合可执行工具调用语义（例如 `<param>` 这类 malformed XML 工具壳），流式 sieve 会把该块作为普通文本释放，而不是吞掉或伪造成工具调用。补发结果会作为本轮 assistant 的结构化 `tool_calls` / `function_call` 输出返回，而不是塞进 `content` 文本；如果客户端没有开启 thinking / reasoning，思维链只用于检测，不会作为 `reasoning_content` 或可见正文暴露。只有正文为空且思维链里也没有可执行工具调用时，才继续按空回复错误处理。
- 工具调用兜底第三部分（LLM 修复）：当第一部分意图识别判定“模型想调工具但格式坏了”（`SawToolCallIntent=true`）、且确定性规则修复（含第二部分 CDATA slot）后仍存在无法解析的坏残片时，finalize 路径会追加一次 LLM 修复：把系统提示中那段「工具调用格式规范」（`toolcall.ToolCallFormatSpec`，与注入模型的规则逐字一致）+ 固定提示词 + 坏工具调用代码拼成 prompt，交由 LLM 修复。触发范围包括**非流式 finalize** 与**所有流式 finalize 定稿**（chat / responses / claude / gemini，在发送最终帧之前触发；若结构化 tool_calls 已在增量阶段流式发出则不再触发，因为已发结构无法撤回）。触发条件不再要求“零成功 call”：只要 `SawToolCallIntent=true` 且残余 `ResidualIntentText` 非空即修——**部分成功场景**（已解析出 ≥1 个成功 call 但仍有坏残片）也会对残片修复，并把修复出的 call 合并进已有成功 calls（已有在前、按 name+input 去重，`mergeRepairedToolCalls`），成功 calls 不受影响。修复调用遵循硬约束：同一账号、default 模式（`deepseek-v4.1-flash`→`model_type=default`；网页客户端 2.4.0 合并 fast/expert/vision 后已无独立 expert/vision 模式）、关闭思考（`thinking_enabled=false`）、新建 session、10 秒超时；修复完成后（无论成功 / 失败 / 超时）该 repair session 一律删除，删除跑在与请求/修复超时分离的 context 上，不泄漏、不拖长主请求。每次 LLM 修复子请求都会在响应记录（`internal/chathistory`）里新建一条独立记录，`surface` 为 `toolcall.repair`、`model` 为 `deepseek-v4.1-flash`、`final_prompt` 为送给 LLM 的修复 prompt、`content` 为 LLM 原始输出；上游报错 / 非 200 / 超时时记录为 `error` 状态并带上错误信息。记录是尽力而为：响应记录关闭或写入失败都不影响修复本身。非流式重试路径的中间态收集不携带 repair invoker，仅终态 build 触发一次修复，保证一次上游输出只发起一次 LLM 修复。交给 LLM 前坏码里的 CDATA 内容会先被 slot 成 `DS_SLOT_{idx}` 占位符，LLM 只处理骨架，输出后再按 slot 时所用的同一套扫描逐字节还原（`restoreCDATASlotsFor`）。slot 有两套扫描：**贪婪**（默认，与确定性解析器判定一致，只 slot 闭合完好的区间）与**边界受限**（仅 repair 路径，每个 CDATA opener 只在其所属元素边界之前找 `]]>`）。边界受限扫描修掉了贪婪扫描在坏格式输入上的两处失效：参数缺 `]]>` 时贪婪会一路吃到下一个参数的 `]]>`，把 `</|EPSE|parameter><|EPSE|parameter name="…">` 吞进同一个 slot，使交给模型的骨架整体丢失一个参数节点（模型看不见就修不出，且骨架修对后还原仍会逐字节复现坏格式）；元素内确实无 `]]>` 的区间贪婪完全跳过，超长原文直接进 prompt。边界受限扫描对后者改为 slot 且不输出闭合符（骨架呈 `<![CDATA[DS_SLOT_4</|EPSE|parameter>`），长内容被抽走、缺 `]]>` 的病灶留在模型视野内。歧义处理：参数正文可能合法含 `</|EPSE|parameter>` 字样甚至嵌套 CDATA 示例，吞噬签名要求「slot 内容含结构标签且其后还有 CDATA open 标记」，仅含前半条的合法长参数保持贪婪解释；命中签名时边界受限升为主解释、贪婪留作备选，**仍只发一次 LLM 请求**，用同一份输出按两份 slot 表分别还原解析，取 call 更多、参数更齐者（打平时主解释优先）。固定提示词的占位符措辞与实际输入保持一致（「可能已被替换」而非「已用变量代替」），并列明硬性要求：占位符逐字符原样输出、未替换内容逐字节保留、只允许改标签结构、每个 `parameter` 节点及其 `name` 必须保留、缺 `]]>` 要补齐；prompt 末尾追加本次占位符清单作为模型自检锚点。降级日志带 `interpretation` / `slot_count` / `skeleton_len` / `slot_degrade_reason`。按 LLM 输出走三路分支：①能解析为正确 `<|EPSE|tool_calls>…` 工具调用→作为结构化 tool_calls 输出（流式场景以修复后的 tool_calls 为准交付，坏文本不作为可见正文）；②字面量 `NO TOOL`→判定非工具调用，坏码原样保留为可见正文（注意：固定提示词里「如果你认为接下来的内容只是在正常聊天，直接返回 NO TOOL」这句已暂时移除——实测它容易误导模型把坏格式工具调用判成普通聊天而放弃修复；分支 ② 的判定代码保留，模型自发返回 `NO TOOL` 时仍按此处理）；③无法解析 / 超时 / 报错 / 占位符被改写导致还原失败→坏码原样保留为可见正文。任何失败都不会让半修复内容或 `DS_SLOT_` 占位符字面量泄漏给下游。逐 chunk 增量阶段按 phase1 §5.3 只做同步/确定性修复，不接入该异步 LLM 修复。
  - **意图识别不被围栏 strip 误吞（根因修复）**：残余意图探测（`DetectToolCallIntent` / `ResidualIntentText`）与 wrapper 扣减都基于“仅剥离已成功闭合围栏”后的原始文本坐标系。工具参数天然带 Markdown 围栏（```），当参数体里出现奇数个（未闭合）围栏时，`stripFencedCodeBlocks` 不再把从该围栏到末尾的内容整段丢弃：若被丢弃的尾部仍带工具结构闭标签（`</|EPSE|...>` / `</tool_calls>` / `</invoke>` / `</parameter>` 等），则判定该围栏为误判并保留原始尾部，使真实工具调用的闭标签不被截断、`SawToolCallIntent` 不被误置为 false。
  - **CDATA 结束边界以 `]]>` 字面量为硬边界**：`findToolCDATAEnd` 判定 CDATA 结束时，`]]>` 是 CDATA 语义里的硬边界，body 内的 Markdown 围栏只是普通字符、不参与配对。当 CDATA 体含奇数个围栏时，仍会回退到第一个后接结构性闭标签（`</...`）的 `]]>` 作为结束，避免“未闭合围栏”把唯一真实结束边界吞掉导致整个工具调用丢失。
  - **确定性解析器也按元素边界收敢越界的 CDATA（越界吞参数修复）**：上面提到的“参数缺 `]]>` 时贪婪会一路吃到下一个参数的 `]]>`”原先只在 repair 路径的 slot 扫描里被处理，确定性解析器仍会静默错解——把 `</|EPSE|parameter><|EPSE|parameter name="…"><![CDATA[…` 连同后一个参数的正文吞进前一个参数值，后一个参数从 `input` 里消失，而且因为它“解析成功了 1 个 call”，wrapper 被整段扣减、`SawToolCallIntent=false`，兜底链路根本不触发（比解析失败更糟：没有任何降级信号）。现在最终 parse 阶段会先识别这种越界形态，按元素边界补 `]]>` 后重解一次，仅在重解结果的 call 数 / 参数数**严格更优**时采用（择优口径与 `pickBetterRepairResult` 一致），因此贪婪解释对它能正确处理的输入保持权威。越界判定要求参数体内同时出现「自己的 `</parameter>` 闭合标签」与「其后一个未配对的 `<![CDATA[`」，所以参数正文里合法引用的完整工具调用示例（本仓库自身的 prompt / spec 文档就是这种）以及只在正文里提到裸 `<![CDATA[` 的情形都不会命中，值保持逐字节原样；相邻多个参数都漏 `]]>` 时迭代到不动点。修复只在 CDATA 内插入 `]]>`，不增删也不重排 wrapper，因此残余意图扣减依赖的 wrapper 序号对应关系不变；无法确定性修好的坏码仍保持零 call + `SawToolCallIntent=true`，继续走 LLM 兜底。Go 侧 `internal/toolcall/toolcalls_cdata_boundary_repair.go`，Node / Vercel 侧 `internal/js/helpers/stream-tool-sieve/parse_payload.js` + `parse.js`。
  - **兜底守门的防御触发路径（Direction 4）**：即使意图识别漏判（`SawToolCallIntent=false`），只要确定性解析器仍看到了工具调用语法（`SawToolCallSyntax=true`）且解析出零个 call，finalize 兜底仍会用原始未 strip 文本作为坏码触发一次 LLM 修复，避免任何意图检测漏判导致坏工具调用代码作为可见正文静默泄漏；对普通正文（无工具语法）不触发。
- OpenAI Chat / Responses、Claude Messages、Gemini generateContent 的空回复错误处理之前会默认做一次内部补偿重试：第一次上游完整结束后，如果最终可见正文为空、没有解析到工具调用、也没有已经向客户端流式发出工具调用，并且终止原因不是 `content_filter`，兼容层会复用同一个 `chat_session_id`、账号、token 与工具策略，把原始 completion `prompt` 追加固定后缀 `Previous reply had no visible output. Please regenerate the visible final answer or tool call now.` 后重新提交一次。Go 主路径的非流式重试由 `completionruntime.ExecuteNonStreamWithRetry` 统一处理；流式重试由 `completionruntime.ExecuteStreamWithRetry` 统一处理，各协议 runtime 只负责消费/渲染本协议 SSE framing。重试遵循 DeepSeek 多轮对话协议：从第一次上游 SSE 流中提取 `response_message_id`，并在重试 payload 中设置 `parent_message_id` 为该值，使重试成为同一会话的后续轮次而非断裂的根消息；同时重新获取一次 PoW（若 PoW 获取失败则回退到原始 PoW）。该同账号重试不会重新标准化消息、不会新建 session，也不会向流式客户端插入重试标记；第二次 thinking / reasoning 会按正常增量直接接到第一次之后，并继续使用 overlap trim 去重。若同账号补偿重试后即将返回 429 `upstream_empty_output`，并且当前是托管账号模式，runtime 会在返回 429 前切换到下一个可用账号，新建 `chat_session_id`，使用原始 completion payload 再做一次 fresh retry；该切号重试不携带空回复 prompt 后缀，也不设置上一账号的 `parent_message_id`。如果 current input file 已触发，切号前会在新账号上重新上传同一份 `HISTORY.txt`（以及需要时的 `TOOLS.txt`），并用新账号可见的 file_id 替换自动生成的旧 file_id；客户端原本传入的其他文件引用保持不变。如果没有可切换账号，或切号后的 fresh retry 仍没有可见正文或工具调用，则继续按原错误返回：无任何输出为 503 `upstream_unavailable`，有 reasoning 但没有可见正文或工具调用为 429 `upstream_empty_output`。若任一尝试触发空 `content_filter`，不做补偿重试并保持 `content_filter` 错误。当同账号补偿重试后即将返回 429 `upstream_empty_output` 时，若推理链（thinking）中包含上游审核拒绝文案「你好，这个问题我暂时无法回答，让我们换个话题再聊聊吧」，则改写为 400 `input_content_blocked`（消息「输入内容被审核拦截」），且不再切换账号；该判定在 Go 主路径（`assistantturn.UpstreamEmptyOutputDetail` / `shared.UpstreamEmptyOutputDetail`）与 Vercel Node 流式路径（`upstreamEmptyOutputDetail`）保持一致。Vercel Node 流式路径通过 Go 内部 prepare / pow / switch 端点获取初始 payload、重试 PoW 和切号 fresh retry payload，因此同样会重新上传 current-input 自动文件并替换为新账号 file_id。
 - 流式解析使用**粘性段游标（sticky cursor）**：`THINK` / `THINKING` fragment 出现即把当前段切到 `thinking`；`RESPONSE` fragment 或 `response/content` 路径是唯一切回 `text` 的途径。正文总是以 `RESPONSE` 片段开始。**一刀切规则（正文开始后不再输出任何思考）**：一旦本次请求已开始输出正文（游标已切到 `text`），之后流里再出现的任何 `THINK` / `THINKING` 增量、`response/thinking_content` 增量或 reasoning 类型片段一律直接丢弃——不再输出 `thinking_content` / `reasoning_content` 增量，也不把它们当作正文拼进 content；游标进入 `text` 后不再回退到 `thinking`，避免正文中途异常重放的 THINK 把后续正文误判成思考而吞掉。续写轮重放历史 `THINK` 快照时，若消息仍处于思考段（游标 `thinking`）则照常归 `thinking`；若消息已进入正文段（游标 `text`）则重放内容整体丢弃，不再当作思考输出。无前缀相对路径 `fragments/-1/content`（response BATCH 内嵌）与 `response/fragments/-N/content` 都遵循粘性游标，不硬编码为正文；嵌套 fragments BATCH 内无 `content` 字段、只有 `v` 载荷的增量（如尾部 `pass\n``` `）同样按游标保留。该语义在 Go 主路径（`internal/sse/parser.go`）与 Vercel Node 流式路径（`internal/js/chat-stream/sse_parse_impl.js`）保持一致。
  - **工具片段（`TOOL_` 前缀 fragment）归 thinking**：网页客户端 2.4.0 起，搜索/工具类 fragment（`TOOL_SEARCH` / `TOOL_OPEN` 等）的 content 是进度旁白（如「正在搜索图片」「已搜索到相关图片」），不是回答正文；兼容层把这类内容统一归入 thinking / reasoning 输出，不再落入可见正文（`isToolFragmentType`）。
  - **片段级 FINISHED 不终止整条流**：`status: FINISHED` patch 若嵌在单个片段作用域路径下（例如 `{"p":"response/fragments/-1","v":[{"p":"status","v":"FINISHED"}]}`），只表示该片段完成，不作为整条响应的终态（`isFragmentScopedPath`；集合追加路径 `response/fragments` 与更深路径 `fragments/-1/content` 不算片段作用域）。否则搜索类流会在「正在搜索图片」片段完成后被误判提前截断。片段作用域路径下裸的 `content` / `response/content` patch（如搜索片段自己的内容更新）也按旁白归入 thinking。

- 非流式 OpenAI Chat / Responses、Claude Messages、Gemini generateContent 在最终可见正文渲染阶段，会把 DeepSeek 搜索返回中的 `[citation:N]` / `[reference:N]` 标记替换成对应 Markdown 链接。`citation` 标记按一基序号解析；`reference` 标记只有在同一段正文中出现 `[reference:0]`（允许冒号后有空格）时才按零基序号映射，并且不会影响同段正文里的 `citation` 标记。
- 流式输出仍默认隐藏 `[citation:N]` / `[reference:N]` 这类上游内部标记，避免分片输出中泄漏尚未完成映射的引用占位符。
- **上游复读死循环打断（closing-tag repetition guard）**：DeepSeek 偶发会在生成末尾陷入复读，连续不断地吐闭合外壳且永远不下发终止状态，整条流只能等 idle 看门狗（`StreamIdleTimeout`）超时才结束。复读的标签不固定：已观测到纯 `</|EPSE>` 连刷、`</|EPSE|invoke>` / `</|EPSE|tool_calls>` 两个标签轮流刷，以及规范化后的裸壳 `</invoke>` / `</parameter>` 连刷。因此判据是**「任意非 HTML 白名单的闭合标签 `</…>`」**而不是某个字面量或某种前缀：兼容层在原始正文通道上做流式检测，**同一轮里连续出现 10 个及以上闭合标签（中间只允许空白/换行）即判定为复读死循环，立刻停止读取上游**，把复读之前已生成的内容原样交给正常收尾链路——工具筛分 flush、工具调用兜底修复、usage 统计、finish 帧与 `[DONE]` 全部照常执行，HTTP 状态码仍是 200，不会被当成 `upstream_interrupted` 报错，也不会触发 auto-continue 续答（续答只会立刻重新进入同一个死循环）。
  - **什么算「闭合标签」**：`</` + 至少一个非空白正文字节 + `>`。正文既可以是普通 XML 元素名（`</invoke>`、`</parameter>`、`</tool_calls>`），也可以是分隔符漂移的 EPSE 外壳（`</|EPSE>`、`</|EPSE|invoke>`、`</|EPSE|tool_calls>`、`</、EPSE、tool_calls>` 等）。**普通 HTML/SVG 闭合标签按名字排除**：`div`、`span`、`p`、`li`、`ul`、`table`、`tr`、`td`、`svg`、`g`、`path` 等常见元素（见 `htmlClosingTagNames` / `HTML_CLOSING_TAG_NAMES`）不计入连续数，避免深层嵌套 HTML 以长串 `</div>` 收尾被误判；而真实上游复读吐的规范化裸壳 `</invoke>` / `</parameter>` 不在白名单内，照常计数，不会漏检。终止符只认半角 `>`；漂移终止符（`＞`、`〉`）会让计数归零，即漏检而不误伤。
  - **阈值为什么是 10**：合法工具调用最多连着闭合三层（最后一个 parameter → invoke → tool_calls），再深的结构必然被开标签打断。实测各类合法输出的最长连续闭合数：标准/嵌套/多 invoke 工具调用都是 3；模型多闭一层（重复 `</parameter>` 后再闭 invoke 与 tool_calls）是 5；模型用自然语言讲解格式、逐行列出各种闭合外壳变体是 6——最后这种形态没有结构上限（模型想列多少行就多少行），所以阈值取 10 是在实测最大值之上留一档余量，而不是贴着实测值。计数要求「连续」，中间出现正文、开标签或参数体都会立即归零。
  - 检测在分片上做滚动匹配（保留跨 SSE 分片的尾巴），标签被拆到多个 `v` 片段里同样能计数；未闭合的 `</` 尾巴有长度上限，不会无限缓冲。若打断后可见正文为空，则继续走既有空回复兜底（补偿重试 / 切号）。
  - 实现：Go 侧 `internal/sse/repeat_guard.go`（阈值 `DefaultRepeatLoopThreshold`），流式经 `internal/httpapi/openai/shared/stream_accumulator.go` 汇入 OpenAI Chat / Responses / Gemini，Claude 在 `internal/httpapi/claude/stream_runtime_core.go` 自行接入，统一以 `stream.StopReasonRepeatLoop` 作为干净停止；非流式经 `internal/sse/consumer.go` 的 `CollectStream`（结果带 `RepeatLoop` 标记）；Vercel Node 流式路径为 `internal/js/chat-stream/repeat_guard.js`，阈值与 Go 侧保持一致。


## 5. prompt 是怎么拼出来的

OpenAI Chat / Responses 在标准化后、current input file 之前，会默认执行 `thinking_injection` 增强。它参考 DeepSeek V4 "把控制指令放在 user 消息末尾更稳定"的用法，在最新 user message 后追加思考增强提示词。当前内置默认提示词为：

```text
text: You are a helpful software engineer assistant.
description: Bootstrap with shell/read, then expose the full Standard tool catalog after the first durable tool call.
**when you thought, start with "we need..."**
```

该开关默认关闭，可通过 `thinking_injection.enabled=true` 开启；也可以通过 `thinking_injection.prompt` 自定义提示词，留空时使用内置默认提示词。

这段增强属于 prompt 可见上下文：

- 普通请求会直接出现在最终 `prompt` 的最新 user block 末尾。
- 如果触发 current input file，它会进入完整上下文文件中。

### 5.1 角色标记

最终 prompt 使用纯文本角色标记，以 `<>:` 包裹：

- `[System]:`
- `[User]:`
- `[Assistant]:`
- `[Tool]:`

每个角色块以对应标记开头，紧跟内容文本，不再有 begin/end 分隔符。

实现位置：
[internal/prompt/messages.go](../internal/prompt/messages.go)

### 5.2 相邻同角色消息会合并

在最终 `MessagesPrepareWithThinking` 中，相邻同 role 的消息会被合并成一个块，中间插入空行。

这意味着：

- prompt 中看到的是“合并后的 role block”
- 不是客户端传来的逐条 message 原样排列

## 6. tools 为什么是“文本注入”，不是原生下发

当前项目把工具能力视为“prompt 约束的一部分”。

具体做法：

1. 把每个 tool 的名称、描述、参数 schema 序列化成文本。
2. 拼成 `You have access to these tools:` 大段说明。
3. 再附上统一的 EPSE tool call 外壳格式约束。
4. 普通直传请求会把“工具描述 + 格式约束”一起并入 system prompt；如果 `input_file` 触发，则工具描述/schema 会单独上传成 `TOOLS.txt`，live prompt 和 system tool 格式提示都会明确要求模型把 `TOOLS.txt` 当作可调用工具和参数 schema 的权威来源。

### 6.1 system 文本声明工具（无 body `tools` 数组）

部分客户端（例如 RikkaHub workspace app）不发送顶层 `tools` 数组，而是把工具目录直接写进 `role=system` 消息文本（如 `Available tools: workspace_read_file ...`）。这类请求由 `RequestBodyHasTools`（`internal/promptcompat/tools_presence.go`）的第二层判定命中「system 文本含 `tools` / `tool` / `functions` 关键词（大小写不敏感）」，因此会被路由到工具号池。

对这类请求，注入决策也要覆盖：由于 body 没有 `tools` 数组，无从提取工具名与参数 schema，因此**不注入工具描述（descriptions），只注入与工具名无关的 EPSE 工具调用格式规范**（`toolcall.ToolCallFormatSpec()`，即「工具调用格式规范」规则块 + 调用决策 + 参数格式，但不含依赖具体工具名的正例）。工具目录本身已经在客户端提供的 system 文本里，兼容层只补上「用 `<|EPSE|tool_calls>` 格式输出工具调用」这一格式约束，使模型的工具调用能被 sieve / parser 正常识别。

- 判定入口：`promptcompat.MessagesDeclareToolsInSystemText(messages)`，与 `RequestBodyHasTools` 的 system 扫描共用同一份关键词与文本展平逻辑。
- 注入实现：`buildOpenAIPrompt`（`internal/promptcompat/prompt_build.go`）在 body 无 `tools` 数组但 system 命中关键词时，调用 `injectToolCallFormatSpecOnly` 追加格式规范；因无法可靠提取工具名，返回空 `toolNames`（流式 sieve / 工具识别不依赖工具名过滤，空列表照常工作）。
- `input_file` 触发时：工具目录随 `HISTORY.txt` 上传为上下文，live 续写 prompt 通过 `BuildOpenAIPromptWithForcedFormatSpec` 仍追加同一份格式规范，保证「继续会话」轮次里模型继续按 EPSE 格式输出工具调用。
- 行为不变：body 携带 `tools` 数组的请求照旧注入「描述 + 指令」（`injectToolPrompt`）。


工具调用正例现在优先示范半角管道符 EPSE 风格：`<|EPSE|tool_calls>` → `<|EPSE|invoke name="...">` → `<|EPSE|parameter name="...">`。
统一工具调用格式说明（`internal/toolcall/tool_prompt.go` 的 `BuildToolCallInstructions`）的规则文案、正反例标题已改为中文，但 EPSE 标签语法本身保持 ASCII（`<|EPSE|...>` / `invoke` / `parameter` / `CDATA` / 标点字符集 `< > / = " |` 等），中文文案仅用于规则解释与示例标题，不影响标签解析、容错归一与 schema 校验逻辑。
兼容层仍接受旧式纯 `<tool_calls>` wrapper，并会容错若干 EPSE 标签变体，包括短横线形式 `<epse-tool-calls>` / `<epse-invoke>` / `<epse-parameter>`、下划线形式 `<epse_tool_calls>` / `<epse_invoke>` / `<epse_parameter>`，以及其他前缀分隔形态如 `<vendor|tool_calls>` / `<vendor_tool_calls>` / `<vendor - tool_calls>`；标签壳扫描还会把全角 ASCII 漂移归一化，例如 `<ｅｐＳＥ|tool_calls>` 与全角 `＞` 结束符，也会容错 CJK 尖括号、全角感叹号或顿号分隔符、弯引号属性值、PascalCase 本地名和属性尾部分隔符漂移，例如 `<EPS|parameter name="command"|>...〈/EPS|parameter〉`、`<！EPSE！invoke name=“Bash”>`、`<、EPSE、tool_calls>`、`<DSmartToolCalls>`、`<EPSEtool_calls※>`。作为兜底，解析器还接受 EPSE 参数标签的“简写”形态：模型把 `<|EPSE|parameter ...>` 漏写成 `<|EPSE ...>`（缺少 `|parameter` 段），或把结束标签漏写成 `</|EPSE>`；带 `name=` 属性的打开简写（如 `<|EPSE name="workdir">`）会被当作 `<|EPSE|parameter name="workdir">`，`</|EPSE>` 会被当作 `</|EPSE|parameter>`，而不带 `name=` 的裸简写（如 `<|EPSE foo="bar">`）仍按普通文本处理。更一般地，Go / Node tag 扫描以固定本地标签名 `tool_calls` / `invoke` / `parameter` 为准，标签名前或标签名后的非结构性协议分隔符都会在解析入口剥离，例如 `<EPSE␂tool_calls>`、`<proto💥tool_calls>` 这类控制符或非 ASCII 分隔符漂移也会归一化回现有 XML 标签后继续走同一套 parser；结构性字符如 `<` / `>` / `/` / `=` / 引号、空白和 ASCII 字母数字不会被当作这类分隔符。进入现有 EPSE rewrite / XML parse 之前，Go / Node 还会先对“已经识别成工具标签壳的 candidate span”做一次窄 canonicalization：只折叠 wrapper / `invoke` / `parameter` / `name` / `CDATA` / `EPSE` 及其壳层分隔符里的 confusable 字符，清理零宽 / BOM / 控制类干扰，并把引号、空白、dash / underscore 变体等统一回可解析的工具语法。这个阶段不会广义改写普通正文、参数内容、Markdown 行内 code span、CDATA 里的示例文本或其他非工具 XML。CDATA 开头也使用同一类扫描式容错，`<![CDATA[` / `<！[CDATA[` / `<、[CDATA[` 都会作为参数原文容器处理。但提示词与示例只示范官方 EPSE 标签，不会把旧式无前缀标签写入提示词，并强调不能只输出 closing wrapper 而漏掉 opening tag。需要注意：这是“兼容 EPSE 外壳，内部仍以 XML 解析语义为准”，不是原生 EPSE 全链路实现。解析器会先截获非 Markdown 代码上下文中的疑似工具 wrapper，完整解析失败或工具语义无效时再按普通文本放行。
数组参数使用 `<item>...</item>` 子节点表示；当某个参数体只包含 item 子节点时，Go / Node 解析器会把它还原成数组，避免 `questions` / `options` 这类 schema 中要求 array 的参数被误解析成 `{ "item": ... }` 对象。除此之外，解析器还会回收一些更松散的列表写法，例如 JSON array 字面量或逗号分隔的 JSON 项序列，只要它们足够明确；但 `<item>` 仍然是首选形态。若模型把完整结构化 XML fragment 误包进 CDATA，兼容层会在保护 `content` / `command` 等原文字段的前提下，尝试把非原文字段中的 CDATA XML fragment 还原成 object / array。不过，如果 CDATA 只是单个平面的 XML/HTML 标签，例如 `<b>urgent</b>` 这种行内标记，兼容层会保留原始字符串，不会强行升成 object / array；只有明显表示结构的 CDATA 片段，例如多兄弟节点、嵌套子节点或 `item` 列表，才会触发结构化恢复。对 `command` / `content` 等长文本参数，CDATA 内部的 Markdown fenced EPSE / XML 示例会作为原文保护；示例里的 `]]></parameter>` 或 `</tool_calls>` 不会截断外层工具调用，解析器会继续等待围栏外真正的参数 / wrapper 结束标签。
Go 侧读取 DeepSeek SSE 时不再依赖 `bufio.Scanner` 的固定 2MiB 单行上限；当写文件类工具把很长的 `content` 放在单个 `data:` 行里返回时，非流式收集、流式解析和 auto-continue 透传都会保留完整行，再进入同一套工具解析与序列化流程。
在 assistant 最终回包阶段，如果某个 tool 参数在声明 schema 中明确是 `string`，兼容层会在把解析后的 `tool_calls` / `function_call` 重新序列化成 OpenAI / Responses / Claude 可见参数前，递归把该路径上的 number / bool / object / array 统一转成字符串；其中 object / array 会压成紧凑 JSON 字符串。这个保护只对 schema 明确声明为 string 的路径生效，不会改写本来就是 `number` / `boolean` / `object` / `array` 的参数。这样可以兼容 DeepSeek 输出了结构化片段、但上游客户端工具 schema 又严格要求字符串参数的场景（例如 `content`、`prompt`、`path`、`taskId` 等）。
工具 schema 的权威来源始终是**当前请求实际携带的 schema**，而不是同名工具在其他 runtime（Claude Code / OpenCode / Codex 等）里的默认印象。兼容层现在会同时兼容 OpenAI 风格 `function.parameters`、直接工具对象上的 `parameters` / `input_schema`、以及 camelCase 的 `inputSchema` / `schema`，并在最终输出阶段按这份请求内 schema 决定是保留 array/object，还是仅对明确声明为 `string` 的路径做字符串化。该规则同样适用于 Claude 的流式收尾和 Vercel Node 流式 tool-call formatter，避免不同 runtime 因 schema shape 差异而出现同名工具参数类型漂移。
正例中的工具名只会来自当前请求实际声明的工具；如果当前请求没有足够的已知工具形态，就省略对应的单工具、多工具或嵌套示例，避免把不可用工具名写进 prompt。
对执行类工具，脚本内容必须进入执行参数本身：`Bash` / `execute_command` 使用 `command`，`exec_command` 使用 `cmd`；不要把脚本示范成 `path` / `content` 文件写入参数。
工具提示词也会明确要求模型按本次调用实际需要填写参数，禁止输出 placeholder、空字符串或纯空白参数；如果必填参数未知，应先追问用户或正常文字回复，而不是输出空工具壳。对 `Bash` / `execute_command` 这类 shell 工具，命令或脚本必须写入 `command` 参数。解析层仍会把空字符串参数结构化返回；是否拒绝空 `command` 由后续工具执行侧 / 客户端 schema 校验决定。
如果当前请求声明了 `Read` / `read_file` 这类读取工具，兼容层会按条件额外注入一条 read-tool cache guard（仅当最近一次 read 类工具结果为无正文/异常时注入）：
- **判定依据**：反向查找历史中最后一条 `role=tool` / `role=function` 消息，若其对应的工具名（优先通过 `name` 解析，缺失则通过 `tool_call_id` / `call_id` 映射回填）经去除非字母数字字符并小写化后匹配 `read` 或 `readfile`，且其内容满足“无正文/异常”条件，则触发注入。无正文/异常包括：内容为空、纯空白、`null`、`{}` 空对象，或包含缓存类标记（大小写不敏感：`unchanged`、`already available`、`previous context`、`no file body`、`no content`、`not modified`、`cached`、`no changes`）及错误类标记（`error`、`failed`、`not found`、`does not exist`、`no such file`、`permission denied`、`denied`）。若最近一次工具结果为正常正文，或最近一次工具调用非 read 类工具，则不注入该 guard。
- **守卫内容**：当读取结果只表示“文件未变更 / 已在历史中 / 请引用先前上下文 / 没有正文内容”时，模型必须把它视为内容不可用，不能反复调用同一个无正文读取；应改为请求完整正文读取能力，或向用户说明需要重新提供文件内容。这个约束只缓解客户端缓存返回空内容导致的死循环，DS2API 不会也无法凭空恢复客户端本地文件正文。
- **适用协议范围**：OpenAI Chat、OpenAI Responses、Gemini（复用 `BuildOpenAIPromptForAdapter`），以及 `input_file` 续写重建路径（续写时使用原始 messages 计算守卫条件）。Vercel 流式路径使用 prepare 返回的 `final_prompt` 自动继承。Claude 走独立 `buildClaudeToolPrompt`，当前仍未接入该 guard（如需一并补齐另开任务）。

OpenAI Chat / Responses 流式链路中的 toolSieve 拦截恒为开启（`bufferToolContent` 不再依赖客户端是否携带 `tools`；Vercel Node 流式路径的 `toolSieveEnabled` 与之一致）：模型被要求在回复末尾以 `<|EPSE|tool_calls>...` 格式输出工具调用，当客户端发起不带 `tools` 的「继续会话」请求时，若拦截关闭会导致 EPSE 原文作为正文透传给客户端形成乱码；toolSieve 的解析不依赖工具名过滤，空工具列表同样能正常拦截。已发出 `tool_calls` 之后，工具调用块之后追加的尾巴正文不再透传给客户端（OpenAI 规范中 tool_calls 回合的 content 应为空/缺省），Go 与 Vercel Node 两条流式路径行为一致；该丢弃只作用于流式输出层，完整模型原文仍保留在内部历史归档与 usage 统计中，供后续「继续会话」上下文使用。此外，流式筛分还额外把 DSML 外壳（如 `<DSML calls>`，本地名任意）作为拦截入口：DSML 不会解析成工具调用，捕获块完整后从可见正文中隐藏，原始文本仍进入 finalize 兜底修复链路，避免异形外壳逐片泄漏到正文；流中途被主动打断时，未闭合的 DSML 块也一并隐藏（Go / Vercel Node 一致）。finalize 修复成功后的可见正文清理采用**标记区间扣减**而不是整段 residual 删除：`toolcall.BadToolMarkupSpans` 只圈出 DSML 包裹（未闭合时只吃它自己的标签串）与解析不出调用的 EPSE / canonical 块，`RemoveTextSpans` 只删这些区间，因此与坏调用同行的正常正文会保留；residual 因「成功 wrapper 被从中间挖掉」而不连续时，区间扣减仍能命中（旧的整体字符串定位在这种部分成功场景下会静默不扣减）。只有当找不到任何坏标记区间（例如本地名不在标记名表里的 EPSE 壳）时，才退回按 residual 原文整段删除。围栏代码块里的 DSML 示例不参与扣减。

统一工具调用格式说明（`internal/toolcall/tool_prompt.go` 的 `BuildToolCallInstructions`）还包含一段「调用决策」指引：历史对话中的 `<|EPSE|tool_calls>` 块是已执行完毕的工具调用记录而非待办事项；是否需要继续调用取决于当前任务还需什么，同一工具允许用不同参数多次调用、失败或结果不完整时也可重试，但不要因历史中出现过某工具而回避再次调用，也不要重复执行已完成且结果正确的调用。该段与原有格式规则并存，未改变标签语法、参数格式或示例强度。

### 6.1.1 末尾工具调用格式复述（trailing reminder）

只要本轮注入了工具提示词（工具描述 + 指令、仅指令、或 system 文本声明工具时的纯格式规范），prompt 末尾都会**额外**追加一个 system 块，内容为 `toolcall.ToolCallFormatReminder()`：

```
The only correct format for using tools is <|EPSE|tool_calls>
  <|EPSE|invoke name="TOOL_NAME_HERE">
    <|EPSE|parameter name="PARAMETER_NAME"><![CDATA[PARAMETER_VALUE]]></|EPSE|parameter>
  </|EPSE|invoke>
</|EPSE|tool_calls> .Please do not simplify or modify this framework in any way.The only valid label is EPSE; the use of any label prefixed with DSML is **strictly prohibited**.
```

- 位置：**最后一个 system 块**，紧贴结尾的 `[Assistant]:` 标记之前；若消息序列以 assistant 结尾（continuation 形态），则插到该 assistant 轮之前，保持续写外形不变。
- 目的：完整格式规范位于 prompt 前部，中间可能夹着很长的 transcript / 历史工具记录，导致骨架离生成点太远而被稀释；末尾复述把唯一合法骨架放在离生成最近的位置。
- 开关（行为设置「底部格式提示注入」，配置键 `bottom_format_injection`）：**默认开启**。关闭后整块不再注入（工具格式规范本身不受影响，仍照常注入）；`bottom_format_injection.prompt` 非空时用它替换上面的内置文本，留空则回退内置默认提示词。自定义文本同样按 EPSE 规范形态注入，出站时由 `ApplyToolMarker` 统一替换成调用者标识。管理端通过 `GET/PUT /admin/settings` 读写，读取侧额外返回 `default_prompt` 供 WebUI 预填输入框。
- 与格式规范的关系：`ToolCallFormatSpec()` 与 `BuildToolCallInstructions()` **不含**该复述块（否则会在描述段内重复），复述块由 prompt 组装层单独追加，且每次组装只追加一份。
- 生效范围：OpenAI Chat / Responses（`injectToolPrompt` / `injectToolPromptInstructionsOnly` / `injectToolCallFormatSpecOnly`，即 `appendToolCallFormatReminder`）、Gemini（复用 `BuildOpenAIPrompt`）、Claude（`injectClaudeToolPrompt` → `appendClaudeToolCallFormatReminder`，含 top-level `system` 与 system 消息两种形态）、`input_file` 续写路径（`BuildOpenAIPromptWithToolInstructionsOnly` / `BuildOpenAIPromptWithForcedFormatSpec`）。Vercel 流式路径不自行拼 prompt，使用 prepare 返回的 `final_prompt`，因此自动继承该复述。
- 重建路径沿用开关：解析结果随请求落到 `StandardRequest.ToolReminderDisabled` / `StandardRequest.ToolReminderPrompt`（两者零值即历史默认行为），`thinking_injection`（`ApplyThinkingInjection`）与 `input_file` 续写（`ApplyCurrentInputFile` 的两个 rebuild 分支）都从 `stdReq` 取该设置，避免重建把「已关闭」悄悄恢复成开启。新增任何「拿 `stdReq.Messages` 重新拼 prompt」的路径都必须同样沿用。
- 不生效场景：本轮未注入工具提示词（无 `tools` 数组且 system 文本未声明工具）或 `tool_choice=none` 时不追加；「底部格式提示注入」关闭时也不追加。

### 6.1.2 工具调用标识按 API Key 随机化（tool marker）

所有部署实例过去都在提示词里使用同一个 `<|EPSE|tool_calls>` 关键字，上游可以据此做跨调用者/跨实例的指纹关联。现在改为**按 API Key 稳定派生一个随机标识 M**，上游只会看到 M：

- **派生**：`M = HMAC-SHA256(tool_marker_secret, callerID)` 取前 6 个字符，字母表为去掉易混字符（`0/O`、`1/I`）的 `[A-Z2-9]`，并保证结果不含 `EPSE` 子串。`callerID` 是 API Key 的 `sha256`（`auth.RequestAuth.CallerID`），因此同一个 Key 永远得到同一个 M（多轮 prompt、历史重渲染保持一致），不同 Key 得到不同 M。
- **服务端密钥**：顶层配置 `tool_marker_secret`，首次启动在 `server.NewApp` 自动生成并持久化；环境变量 `DS2API_TOOL_MARKER_SECRET` 优先级更高。Vercel / 只读部署（配置无法落盘）必须设置该环境变量，否则冷启动会重新生成密钥、所有 Key 的 M 随之改变。
- **该密钥不对外暴露**：不进入 `/admin/settings` 与 WebUI；`GET /admin/config/export` 导出时会剔除；`POST /admin/config/import?mode=replace` 会保留运行中的值（导入载荷本来也带不了它）。管理员改 admin 密码或重新导入配置都不会影响 M。
- **出口（EPSE → M）**：不逐层线程化，而是在**最终 prompt 字符串**上做一次全局替换（`toolcall.ApplyToolMarker`）。因此格式规范、正反例、末尾复述、以及 prompt 可见的历史工具块统一变成 M。应用点：OpenAI Chat / Responses 在 `NormalizeOpenAIChatRequest` / `NormalizeOpenAIResponsesRequest` 内，Claude / Gemini 在 `Determine` 之后。**凡是在已有 prompt 之上重建 `FinalPrompt` 的路径都必须重新应用一次**，目前有两处：`thinking_injection`（`ApplyThinkingInjection`）与 `input_file` 续写（`ApplyCurrentInputFile` 的两个 rebuild 分支）——重建会按 EPSE 重新渲染规范，不重新应用就会在同一次请求里退回共享关键字。
- **上传文件与本地归档（`HISTORY.txt` / `TOOLS.txt`）同样用 M**：`input_file` 拆分把历史（含 assistant 工具块）上传为 `HISTORY.txt`，这段文本会被上游读取，因此上传载荷按 M 渲染；同时 `StandardRequest.HistoryText`（本地归档与 WebUI 响应记录中展示的来源）同步使用 M 渲染，使 WebUI 查阅/下载与实际上传载荷保持完全一致；切号重传时亦按 M 渲染。
- **入口（M → EPSE）**：模型输出在进入任何内部逻辑之前归一化回 EPSE。内部（parser / intent / repair / toolstream / assistantturn）**只认 EPSE**。流式归一化是**有状态**的：SSE 分片可能把标识切开（`<|Q7Z` + `K3M|tool_calls>`），因此遇到「尾部是 M 的真前缀」会先 hold，下一片到达再替换，避免半截标识泄漏成可见正文；收尾时 flush 释放。落点：`toolstream.State.Marker`（`ProcessChunk` / `Flush` 内部自动处理，对三个运行时零侵入）、`assistantturn.BuildOptions.ToolMarker`（`BuildTurnFromCollected` / `BuildTurnFromStreamSnapshot` 入口，幂等）、以及各协议的流式 finalize。
- **归档保持 EPSE**：`assistantturn` 把 raw / thinking 归一化成 EPSE 后归档，下一轮 prompt 出口再统一替换成 M，因此归档一致性与本地历史可读性都不受影响。
- **工具调用兜底修复（LLM repair）**：修复 prompt（格式规范 + 固定提示词）按 M 渲染，模型回答同样用 M，解析修复输出前先归一化回 EPSE。修复链路的残余坏码来自已归一化的内部文本，因此 slot / 结构判定不受影响。
- **Vercel / Node 流式镜像**：Node 侧不自行拼 prompt，使用 prepare 返回的 `final_prompt`，因此天然继承出口替换；同时 prepare / switch 响应新增 `tool_marker` 字段，Node 用同一套有状态归一化语义（`internal/js/chat-stream/marker.js`）在文本进入复读守卫、累计正文与 sieve 之前把 M 折回 EPSE。
- **thinking / reasoning 流式片段不归一化**：Go 与 Node 都只在收口（`assistantturn`）归一化 thinking，实时 `reasoning_content` / `thinking` 增量原样下发，两边保持一致；M 只依赖调用者自己的 Key，不构成跨租户信息泄漏。
- **空标识即完全无操作**：marker 为空或等于 `EPSE` 时，出口替换、流式归一化、修复渲染全部退化为旧行为，未传 marker 的调用与既有测试不受影响。
- **已知取舍**：出口替换是全局 `EPSE` → M 字符串替换，若用户正文里恰好出现字面量 `EPSE` 会被一并改写（概率低，且更严格的「只替换定界形」会让末尾复述里那句「The only valid label is EPSE」与 M 标签自相矛盾）；入口归一化是裸关键字替换，正文里恰好出现同 6 位随机串会被误改写（概率约 32⁻⁶ 量级）。两者都接受。
- **未覆盖范围**：Claude / Gemini 的**请求历史**走各自的消息渲染（`normalizeClaudeMessages` / Gemini 转换层），不经 `NormalizeOpenAIMessagesForPrompt`，因此历史里若以 M 形态出现 assistant 工具块，只有 OpenAI Chat / Responses 会识别并重渲染成规范外壳（`NormalizeOpenAIMessagesForPrompt` 会先归一化再 parse）；Claude / Gemini 不会重渲染，但出口替换仍会把其中的 EPSE 形态统一成 M。

### 6.2 按请求 body 是否含工具控制工具注入（含 API Key「强制禁用工具调用」）

是否注入工具提示词默认**完全由本次请求 body 是否携带工具定义（`tools` 字段非空）决定**。

- 请求 body 含 `tools`（非空）→ 注入工具提示词，并走「默认 / 仅工具」账号池
- 请求 body 不含 `tools`（空/缺省）→ 不注入工具提示词，走「默认 / 无工具」账号池

当请求不含 `tools` 时：

- 工具描述（`You have access to these tools:` 大段文本）不会被注入 system prompt
- EPSE 工具调用格式说明、正面/反面示例不会注入（包括「调用决策」指引段落）
- `TOOLS.txt` 工具描述文件不会上传

说明：OpenAI Chat / Responses 流式链路的 toolSieve 拦截本身恒为开启（安全兜底，与请求是否含工具无关），但请求不含 `tools` 时 EPSE 工具指令与 `TOOLS.txt` 均不注入，模型不会被引导输出可拦截的工具块，因此实际不会产生工具调用。

#### 6.2.1 API Key「强制禁用工具调用」开关

`api_keys[].force_disable_tools`（默认 `false`）是唯一可以覆盖上述「按请求 body 决定」的开关，粒度是**单个 API Key**。开启后，使用该 Key 的请求走强制无工具路径：

1. **强制清除请求中的工具定义**：各协议入口在标准化前调用 `promptcompat.DisableToolsForRequest(req)`，删除 body 的 `tools` / `tool_choice` / `functions` / `function_call` 字段，因此后续没有任何工具 schema 可被注入、上传为 `TOOLS.txt` 或透传。
2. **覆盖 system 关键词判定**：该函数同时在请求 map 上打内部标记 `_tools_force_disabled`（不进入 completion payload）。`RequestBodyHasTools` 对带标记的请求恒返回 `false`，因此 `role=system` 文本里的 `tools` / `tool` / `functions` 关键词（§6.1 的第二层判定）不再把请求判为含工具请求，号池路由也随之走「默认 / 无工具」池。`input_file` 的 `systemDeclaredTools` 判定同样以 `StandardRequest.ToolsDisabled` 短路，不再为这类请求补 EPSE 格式规范。
3. **不注入工具提示词**：标准化时把工具策略钉为 `ToolChoiceNone`（`StandardRequest.ToolChoice`），所有注入点都以 `policy.IsNone()` 短路——工具描述 + 指令、纯指令、纯 EPSE 格式规范（含 §6.1.1 末尾复述）全部不注入；`StandardRequest.ToolsRaw` 为 `nil`、`ToolNames` 为空。

判定入口是 `auth.Resolver.ForceDisableToolsForRequest(req)`（`internal/auth/request.go`）：按 caller token 查 store 的 `APIKeyForceDisableTools`。**直连 token（非托管 Key）与未知 Key 恒为关闭**，所以该开关只能由显式配置在 store 里的托管 Key 打开。五个协议入口（OpenAI Chat、OpenAI Responses、Vercel 流式 prepare、Claude Messages、Gemini generateContent）都在写 `auth.WithToolsPresent` 之前应用该开关，Vercel 流式镜像与主链路行为一致。

各协议适配器在标准化前判定“本次请求 body 是否携带工具”（`promptcompat.RequestBodyHasTools`），并通过 `auth.WithToolsPresent` 写入请求上下文，用于**号池路由**。注意：除上述「强制禁用工具调用」开关外，兼容层**不会**对请求 body 做删除 `tools` 的改写——是否注入完全取决于请求本身携带的内容。

`RequestBodyHasTools` 命中以下任一条件即判定为含工具（带 `_tools_force_disabled` 标记的请求恒为 `false`，不参与以下判定）：

- body 顶层 `tools` 字段是非空 `[]any`（结构化工具定义，规范形状）；
- body 的 `messages` 数组中存在 `role=system` 消息，其文本内容包含关键词 `tools`、`tool`、`functions`（大小写不敏感）。部分客户端（如 RikkaHub workspace 应用）不发送结构化 `tools` 数组，而是把工具目录直接写进 system 文本（例如 `Available tools: workspace_read_file ...`），该扫描保证这类请求也被路由到工具号池。

工具提示词**注入**分两种：

- 结构化 `tools` 数组非空 → 注入「工具描述 + EPSE 工具调用格式说明」（`injectToolPrompt`）。
- 无结构化 `tools`、但 system 文本命中关键词 → 注入与工具名无关的 EPSE 格式规范（`injectToolCallFormatSpecOnly`），因为此时无可用于生成 schema 的结构化定义，工具目录已由客户端自行写入 system，服务端只补「用 `<|EPSE|tool_calls>` 格式输出」这一格式约束，避免与客户端自身的工具协议冲突。详见 §6.1。

实现：
- [internal/promptcompat/tools_presence.go](../internal/promptcompat/tools_presence.go) `RequestBodyHasTools`
- [internal/promptcompat/tools_force_disable.go](../internal/promptcompat/tools_force_disable.go) `DisableToolsForRequest` / `ToolsForceDisabledInRequest`
- [internal/auth/request.go](../internal/auth/request.go) `WithToolsPresent`、`ForceDisableToolsForRequest`
- [internal/config/credentials.go](../internal/config/credentials.go) `APIKey.ForceDisableTools` 归一化与 store 索引

OpenAI 路径实现：
[internal/promptcompat/tool_prompt.go](../internal/promptcompat/tool_prompt.go)

Claude 路径实现：
[internal/httpapi/claude/handler_utils.go](../internal/httpapi/claude/handler_utils.go)

统一工具调用格式模板：
[internal/toolcall/tool_prompt.go](../internal/toolcall/tool_prompt.go)

这也是项目“网页对话纯文本兼容”的关键设计：

- tools 对下游来说，本质上是 prompt 内规则
- 不是 native tool schema transport

### 6.2 按账号号池类型过滤调度

每个 DeepSeek 账号可以独立配置号池类型（`pool_type`），控制该账号可被哪类请求调用。号池选择**直接根据本次请求 body 是否携带工具定义**判定（`promptcompat.RequestBodyHasTools`：顶层 `tools` 字段非空，或 `role=system` 消息文本含 `tools`/`tool`/`functions` 关键词）：

| `pool_type` | 含义 | 含工具请求（`RequestBodyHasTools` 为 true） | 无工具请求（`RequestBodyHasTools` 为 false） |
|---|---|---|---|
| `default` | 默认号池，允许无工具和含工具调用 | 可调用 | 可调用 |
| `no_tools` | 仅允许无工具调用 | 不可调用 | 可调用 |
| `tools_only` | 仅允许含工具调用 | 可调用 | 不可调用 |

未设置 `pool_type` 的旧账号视为 `default`，行为不变。

调度逻辑：

- 各协议适配器在标准化前判定“本次请求 body 是否携带工具”（`promptcompat.RequestBodyHasTools`：顶层 `tools` 非空，或 `role=system` 消息文本包含 `tools`/`tool`/`functions` 关键词），并通过 `auth.WithToolsPresent` 写入请求上下文；`Determine` 读取该标志构造 filter，只调度到 `MatchesPoolType` 返回 `true` 的账号。
- 若 caller 使用的 API Key 开启了「强制禁用工具调用」（`api_keys[].force_disable_tools`），入口会先调用 `promptcompat.DisableToolsForRequest` 清掉工具定义并打上内部标记，`RequestBodyHasTools` 随即恒为 `false`，本次请求按无工具请求路由（详见 §6.2.1）。
- 未携带 tools-present 上下文的路由（如 embeddings、文件上传等不解析 body `tools` 的端点）默认按无工具请求路由。
- 轮询模式下跳过不匹配的账号；若所有可用账号都不匹配，直接返回 `no accounts` 错误，不会无限排队。
- 排队等待仅在存在匹配候选账号时才允许；无匹配候选时不排队。
- `X-Ds2-Target-Account` 指定账号时同样应用 filter：若指定账号的号池类型与请求是否含工具不匹配，返回错误。
- 账号切换重试（`SwitchAccount`）也会携带原始请求的“是否含工具”标志（`RequestAuth.ToolsPresent`），保证切换后仍遵守号池约束。
- 直传 token 模式不经过账号池，号池设置对其无影响。

Vercel Node 流式镜像通过 `__stream_prepare` 把账号获取委托给 Go 侧 `handleVercelStreamPrepare`，该处理器已按同一逻辑（判定 body 是否含工具并写入上下文）选择号池，因此 Vercel 路径与主链路行为一致。

封号 / 禁言的弹性补位与自动换号：

- 选取顺序：每个账号可配置整数优先级（`accounts[].priority`，允许负数，默认 `0`）。`ReconcileElasticPool` 先在每个号池类型内按优先级从高到低排序，再取前 N 个启用；优先级相同的账号保持 `config.Accounts` 的原始顺序（靠前者优先）。负优先级表示低于默认账号（`0`），只有在更高优先级账号名额不足时才会被启用。该排序仅决定"哪些账号被启用"，不改变号池内部的轮询次序。
- 补位：当账号被上游禁言（`biz_code=5` / `biz_msg` 含 `muted`）或停用（`USER_IS_BANNED`）时，客户端在持久化 `muted_until` / `banned` 状态的同一事务内执行 `ReconcileElasticPool`：被封账号让出名额，并按上述优先级顺序启用一个休眠账号补上，全程无需人工干预。
- 账号变动即时判定：只要账号集合发生变化（新增 / 删除账号、导入账号、批量写入配置、修改 `pool_type`、修改 `priority`、单个或批量启用/禁用），就在同一 `Store.Update` 事务内执行一次 `ReconcileElasticPool`，立即把超出每批启用数的账号（含刚新增的账号）置为禁用，无需等待下一次请求或人工触发。启用集合发生变化时还会同步重置号池队列并触发代理桥补位。
- 开跑阶段换号重试（Go 主链路）：`CallCompletion` 在开跑阶段命中禁言时返回 `account_muted`，`StartCompletion` 不再把该错误直接透传，而是调用 `SwitchAccount` 切到下一个可调度账号，并用 `startStandardCompletionOnAlternateAccount` 在新 session 上重放本次请求（含 `input_file` 重新上传、分段提示词重新发送）；只要还有可调度账号就继续切换，全部耗尽才向客户端返回 `Account is muted by upstream.`。直传 token 模式（`UseConfigToken=false`）不参与切换。
- 流式中途换号重试（Go 主链路）：`ExecuteStreamWithRetry` / `ExecuteNonStreamStartedWithRetry` 在空输出重试耗尽后命中 `account_muted` / `upstream_unavailable` / 429 时同样走 `SwitchAccount` 重放。
- Vercel Node 流式镜像：检测到 muted / banned JSON 后通过 `__stream_switch` 循环切换账号，每轮都让 Go 侧持久化状态并触发弹性补位，直到拿到健康的 SSE 流或没有更多可调度账号为止。

临时上游故障的自动重试（Go 主链路 + Vercel 镜像）：

- 开跑阶段：`StartCompletion` 会把上游返回的临时 5xx（500 / 502 / 503 / 504）响应升级为可重试的 `upstream_unavailable` 错误，并把 `CallCompletion` 的传输层错误（网络中断等，原 `Failed to get completion.`）同样归类为 `upstream_unavailable`，随后按 `SwitchAccount` 切到下一个可调度账号重放；直传 token 模式（`UseConfigToken=false`）不参与切换，调用方取消（`ctx.Err() != nil`）保持原样透传，不触发切号。
- 非流式重试循环：synthetic retry 请求本身命中的临时 5xx 或传输错误也会先尝试切号，而不是直接返回 `Failed to get completion.`。
- 流式消费阶段：SSE 内嵌的上游错误信息（如 `{"type":"error","content":"服务器暂时不可用"}`）若命中临时关键词（暂时 / 稍后 / 请重试 / 繁忙 / 超时 / temporarily / try again / unavailable / timeout / overloaded / busy），会先走同账号 synthetic retry，再按 `upstream_unavailable` 走切号；输入超长（`内容超长，请删减后再试`）等永久错误仍直接透传，不重试。
- Vercel Node 流式镜像：初始 completion 命中临时 5xx 时通过 `__stream_switch` 循环切号；流中解析到临时上游错误时同样交回重试 / 切号循环；重试请求命中临时 5xx 时继续切号。

实现：
- [internal/promptcompat/tools_presence.go](../internal/promptcompat/tools_presence.go) `RequestBodyHasTools`
- [internal/config/account.go](../internal/config/account.go) `Account.MatchesPoolType` / `NormalizePoolType`
- [internal/account/pool_acquire.go](../internal/account/pool_acquire.go) `AccountFilter` / `Acquire` / `AcquireWait`
- [internal/auth/request.go](../internal/auth/request.go) `WithToolsPresent` / `Determine` / `acquireManagedRequestAuth` / `SwitchAccount`

## 7. assistant 的 tool_calls / reasoning 如何保留

### 7.1 reasoning 保留方式

assistant 的 reasoning 会变成一个显式标签块：

```text
[reasoning_content]
...
[/reasoning_content]
```

然后再接可见回答正文。

对最终返回给客户端的 assistant 轮次，reasoning 不会因为本轮输出了工具调用而被丢弃。OpenAI Chat 会在同一个 assistant message 上同时返回 `reasoning_content` 和 `tool_calls`；OpenAI Responses 会先返回一个包含 `reasoning` content 的 assistant message item，再返回后续 `function_call` item；Claude / Gemini 也会在各自原生 thinking / thought 结构后继续返回 tool_use / functionCall。

对进入后续 prompt / `HISTORY.txt` 的历史轮次，兼容层也会把同一轮工具调用前的 reasoning 绑定到 assistant tool call 历史上。OpenAI Chat 原生 `reasoning_content + tool_calls` 会直接保留；OpenAI Responses 若以 `reasoning` message item 后接 `function_call` item 的形式回放历史，会在归一化时合并为同一个 assistant 历史块；Claude 的 `thinking` block 会绑定到后续 `tool_use`；Gemini 的 `thought: true` part 会绑定到后续 `functionCall`。最终 prompt 中的顺序固定为 `[reasoning_content]...[/reasoning_content]`，再接 EPSE tool call 外壳。

### 7.2 历史 tool_calls 保留方式

assistant 历史 `tool_calls` 不会保留成 OpenAI 原生 JSON，而会转成 prompt 可见的 EPSE 外壳：

```xml
<|EPSE|tool_calls>
  <|EPSE|invoke name="read_file">
    <|EPSE|parameter name="path"><![CDATA[src/main.go]]></|EPSE|parameter>
  </|EPSE|invoke>
</|EPSE|tool_calls>
```

如果客户端历史里没有结构化 `tool_calls` 字段、却把一个可独立解析的 assistant 工具块放进了普通 `content`，兼容层会在写入后续 prompt 前先按工具调用解析它，再重渲染为规范 EPSE 历史外壳。这样可以避免一次 malformed 工具块未被结构化保存后，作为普通 assistant 文本回灌，继续污染后续模型的 few-shot 工具格式。

解析层对旧式纯 XML 形态（`<tool_calls>` / `<invoke>` / `<parameter>`）保留向后兼容解析，都会先归一到现有 XML 解析语义；但提示词与历史示例只重渲染 EPSE 外壳。其他旧格式都会作为普通文本保留，不会作为可执行调用语法。
例外是 parser 会对一个非常窄的模型失误做修复：如果 assistant 输出了 `<invoke ...>` ... `</tool_calls>`（或 EPSE 对应标签），但漏掉最前面的 opening wrapper，解析阶段会在 wrapper-confidence 足够高时补回 wrapper 后再尝试识别。这里的 wrapper-confidence 指 scanner 已经识别出白名单工具壳结构，剩余失败只像壳层结构漂移，而不是语义上接近但不在白名单内的 near-miss 标签名。修复成功时，wrapper 后面的 suffix prose 会继续保留在可见文本里；修复失败时，该块仍按普通文本处理。

这件事很重要，因为它决定了：

- 历史工具调用在 prompt 中是“可见文本历史”
- 不是“隐藏结构化元数据”

实现位置：
[internal/prompt/tool_calls.go](../internal/prompt/tool_calls.go)

### 7.3 tool result 保留方式

tool / function role 的结果会作为 `[Tool]:...` 进入 prompt。

如果 tool content 为空，当前会补成字符串 `"null"`，避免整个 tool turn 丢失。

## 8. files、附件、systemprompt 文件的实际语义

这里要明确区分两类东西：

1. 文本型 system prompt
   例如 OpenAI `developer` / `system` / Responses `instructions` / Claude top-level `system`
   这类会进入 `prompt`。
2. 文件型 systemprompt
   例如通过附件、`input_file`、base64、data URL 上传的文件
   这类不会直接内联进 `prompt`，而是进入 `ref_file_ids`。

OpenAI 文件相关实现：

- inline/base64/data URL 上传：
  [internal/httpapi/openai/files/file_inline_upload.go](../internal/httpapi/openai/files/file_inline_upload.go)
- 文件 ID 收集：
  [internal/promptcompat/file_refs.go](../internal/promptcompat/file_refs.go)
- 图片内容检测与剔除（原 `auto_route_vision`）：
  [internal/promptcompat/image_route.go](../internal/promptcompat/image_route.go)

OpenAI 的文件上传会向上传接口透传 `x-model-type`。网页客户端 2.4.0 把 fast / expert / vision 三种上游 `model_type` 合并成了单一的 `default`（视觉能力也由 default 承担，图片在上游服务端被归类为 model_kind VISION），因此上传阶段现在**固定携带 `x-model-type: default`**，即使请求解析出的模型带 `-nothinking` 后缀或使用搜索模型也一样。请求侧旧的 `default` / `expert` / `vision` 上传类型取值仍被接受（`expert` / `vision` 会被规范化成 `default`）。这个模型类型会同时用于：

- `/v1/files` 这类独立文件上传入口
- Chat / Responses 的 inline 图片、附件上传
- current input file 触发时生成的 `HISTORY.txt` 上下文文件

也就是说，文件上传和完成请求的 `model_type` 现在是一致的：完成 payload 里仍然是 `model_type`（统一为 `default`），上传文件则携带同样的模型类型信息。

另外，2.4.0 网页流程的文件解析是异步的：`upload_file` 返回时文件处于 `PENDING` / `PARSING`，通常在 completion 流执行期间才到达 `SUCCESS`。兼容层对「上传后等待解析完成」做了**尽力而为**处理——等待失败只记 warn 日志并继续发送 completion（file_id 已随 payload 下发，上游会在服务端继续解析），不会因解析慢而使上传失败。

结论：

- “systemprompt 文字”在 prompt 里
- “systemprompt 文件”通常只在 `ref_file_ids` 里

除非调用方自己把文件内容展开后再塞进 system/developer 文本，否则文件内容不会自动出现在 prompt 正文。

### 8.1 图片请求的自动处理（原 `auto_route_vision`）

网页客户端 2.4.0 把 fast / expert / vision 合并成单一 `default` 模型类型后，**不再存在独立的 vision 模型**，所有解析成功的模型（含搜索与 `-nothinking` 变体）都原生支持视觉。因此原来「把非 vision 模型临时改写成 `deepseek-v4-vision`」的模型名改写逻辑已移除，`VisionModelEquivalent` 保留但恒返回空串。

当前行为：

1. 在 OpenAI Chat / Responses handler（含 Vercel stream prepare 路径）解码请求后，调用 `promptcompat.MaybeAutoRouteVision` 判断**当前用户轮次**是否携带图片内容；模型名**不会被改写**，返回的 `originalModel` 用于保持客户端可见的响应模型。
2. 随后走 `PreprocessInlineFileInputs` 上传 inline 图片，上传统一携带 `model_type=default`。
3. 若检测到图片内容（`rerouted=true`），调用 `promptcompat.StripImageBlocksFromRequest` 把消息中的图片块剔除，但 `ref_file_ids` 中仍保留已上传的图片文件 ID——合并后的模型通过 `ref_file_ids` 接收图片。
4. 标准化得到 `StandardRequest` 后，`RequestedModel` / `ResponseModel` 恢复成客户端最初请求的模型名，因此客户端看到的响应模型不变。
5. 历史消息中的图片不会触发剔除；该处理仅作用于当前用户轮次。

配置层面的 `auto_route_vision.enabled` 开关仍保留在 admin settings 读写接口中（向后兼容旧配置），但**不再影响任何路由行为**；WebUI 设置页中的对应开关已移除，且 `config.Store.AutoRouteVisionEnabled()` 现在固定返回 `false`，旧配置里残留的 `enabled: true` 也不会再被读出。

相关实现：

- 路由与图片检测/剔除：
  [internal/promptcompat/image_route.go](../internal/promptcompat/image_route.go)
- OpenAI Chat 接入点：
  [internal/httpapi/openai/chat/handler_chat.go](../internal/httpapi/openai/chat/handler_chat.go)
- OpenAI Responses 接入点：
  [internal/httpapi/openai/responses/responses_handler.go](../internal/httpapi/openai/responses/responses_handler.go)

## 9. 多轮历史为什么不会一直完整内联在 prompt

兼容层现在只保留 `input_file` 这一种拆分方式；旧的 `history_split` 配置字段已移除，读取旧配置时会忽略它且不会再写回。`input_file` 由 `current_input_file` 更名而来：旧键在读取时会被直接忽略且不做自动迁移，需要手动改名。

- `input_file` 默认关闭；它在统一 completion runtime 入口全局生效，用于把“历史上下文”合并进 `HISTORY.txt` 上下文文件。阈值判定与分段输入一致，统计的是**整个 `FinalPrompt`**（含所有 role 标记和工具提示词）的 rune 字符数：当 `len([]rune(stdReq.FinalPrompt)) >= input_file.min_chars`（默认 `0`）时，runtime 会上传一个文件名为 `HISTORY.txt` 的上下文文件。之所以不再只看最新 user turn，是因为超长请求的主体通常在历史轮次和工具提示词里，只看单条 user 输入会让长多轮请求永远不触发拆分。文件内容会先经过各协议入口的标准化，再序列化成按轮次编号的 `HISTORY.txt` 风格 transcript，带有 `# HISTORY.txt` 标题和 `=== N. ROLE ===` 分段；在 `Prior conversation history and tool progress.` 描述行之后紧挨着插入一段 continuation 说明（“从 HISTORY.txt 的最新状态继续推进”），该说明不再注入 live prompt。如果当前请求声明了可用工具，还会把工具名称、描述和参数 schema 单独上传成 `TOOLS.txt`，带有 `# TOOLS.txt` 标题。live prompt 中则只保留一个极短的 `继续会话` **system** 消息（渲染为 `[System]:继续会话`），并在有工具文件时明确可用工具 schema 位于 `TOOLS.txt`；system prompt 也会在统一 EPSE 工具格式约束前说明 `TOOLS.txt` 是可调用工具和 schema 的权威来源，同时保留本轮工具选择策略，避免把任务拉回起点。
- **末尾 user turn 保留在 live prompt**：如果 transcript 里最后一条可见轮次是 user，它不会写进 `HISTORY.txt`，而是作为真实 user 消息跟在占位句之后，最终 prompt 形如 `[System]:继续会话[User]:用户输入内容[Assistant]:`；`HISTORY.txt` 只承载它之前的轮次。这样模型不必从附件里翻找“本轮到底要我做什么”。如果最后一条不是 user（例如 assistant 或 tool 结尾的续写请求），行为与以前一致：整段 transcript 上传，live prompt 只有 `[System]:继续会话`。
- 单轮请求例外：如果把末尾 user turn 移出后 `HISTORY.txt` 就没有任何轮次可上传（只有一条 user，或前面的轮次序列化后为空），拆分会拒绝移出、仍然整条上传。否则长的首轮输入就不再被拆出 live prompt，等于让 `input_file` 对这类请求失效。判定实现是 `promptcompat.SplitTrailingUserTurn`。
- 如果 `input_file.enabled=false`，请求会直接透传，不上传任何拆分上下文文件。
- 历史 expert（pro）模型不支持文件上传的跳过逻辑仍保留在代码中（`input_file` 跳过、`ref_file_ids` 清空、inline 文件预处理跳过），但 2.4.0 模型合并后所有模型解析出的 `model_type` 都是 `default`，这些分支不再触发；current input file 对所有模型生效。
- 即使触发 `input_file` 后 live prompt 被缩短，对客户端回包里的上下文 token 统计，仍会沿用**拆分前的完整 prompt 语义**做计数，而不是按缩短后的占位 prompt 计算；否则会把真实上下文显著算小。

相关实现：

- 配置访问器：
  [internal/config/store_accessors.go](../internal/config/store_accessors.go)
- 当前输入转文件：
  [internal/httpapi/openai/history/current_input_file.go](../internal/httpapi/openai/history/current_input_file.go)
- 全局 completion runtime 应用点：
  [internal/completionruntime/nonstream.go](../internal/completionruntime/nonstream.go)

当前输入转文件启用并触发时，上传的历史文件真实文件名是 `HISTORY.txt`，文件内容是**末尾 user turn 之前**的 `messages` 上下文（末尾不是 user、或移出后无历史可留时为完整上下文）；它会使用 OpenAI-compatible 的消息/transcript 序列化规则和 DeepSeek 角色标记，再按轮次编号成 `HISTORY.txt` 风格的 transcript（不再注入文件边界标签）：

```text
[uploaded filename]: HISTORY.txt
# HISTORY.txt
Prior conversation history and tool progress.
Continue from the latest state in the attached HISTORY.txt context. Treat it as the current working state and answer the latest user request directly.Do not mention HISTORY.txt in the main text.

=== 1. SYSTEM ===
...

=== 2. USER ===
...

=== 3. ASSISTANT ===
...

=== 4. TOOL ===
...
```

如果当前请求带有工具，runtime 同时上传 `TOOLS.txt`：

```text
[uploaded filename]: TOOLS.txt
# TOOLS.txt
Available tool descriptions and parameter schemas for this request.

You have access to these tools:

Tool: ...
Description: ...
Parameters: ...
```

开启后，请求的 live prompt 不再直接内联完整上下文，也不再内联大段工具 schema；它只保留一个极短的 `继续会话` system 消息（渲染为 `[System]:继续会话`，而不是 `[User]:继续会话`），并在有工具时引用 `TOOLS.txt`；如果末尾轮次是 user，它会作为真实 user 消息紧跟在占位句之后（`[System]:继续会话[User]:...[Assistant]:`）。引导模型从 `HISTORY.txt` 最新状态继续推进的 continuation 说明紧跟在上传的 `HISTORY.txt` 文件顶部描述行之后。上传后的 `HISTORY.txt` file_id 会排在 `ref_file_ids` 最前；如果存在 `TOOLS.txt`，它的 file_id 紧随其后；客户端已有的其他 file_id 保持在后面。上下文 token 统计会包含上传的历史文件、工具文件和 live prompt。对于托管账号模式切号 fresh retry，runtime 会重新上传这些自动文件，而不是把上一账号的 file_id 交给新账号。历史上 expert（pro）模型会清空 `ref_file_ids` 且不上传 current-input 文件；2.4.0 合并后该分支不再触发。

占位句用 `system` 而不是 `user`：它是兼容层合成的句子，放在 `user` 角色会把它伪装成用户输入；`system` 角色让它与后续工具格式约束同处 system 段，真正的用户发言（若存在）则以 `[User]:` 出现在其后。历史归档侧，`extractSingleUserInput` / `ExtractSingleUserInput` 优先取真实 user turn（即被保留在 live prompt 里的那条），仅在完全没有 user turn 时回退到最后一条非空消息（即占位句本身），避免条目标题空白。WebUI 的 `buildListModeMessages` 会在合并 `HISTORY.txt` 时丢弃占位句（同时接受 `system` 和旧的 `user` 角色、以及带工具后缀的变体），并把保留在 live prompt 里的末尾 user turn排在合并后的历史之后。

设计经验：凡是注入到上游会话里的固定提示词，都要尽量克制、自然。官方一旦识别出它是“为反代服务准备的专属指令”，就可能触发封号。所以能不加就不加；确实需要注入时，也要优先选普通对话里常见的说法（例如 `继续会话`，而不是结构化的英文长指令），措辞必须贴近用户真实会说的话，不能显得像系统指令。同时措辞也不宜过于简短——之前只写 `继续` 两个字时，模型偶尔会短暂困惑、接不上任务；换成 `继续会话` 这种仍然大众、但又把语义点满的短语后行为才稳定。简单说：保持自然、大众化、语义足够，且不要让官方看出这句话是在为代理服务兜底。

### 9.1 超长提示词分段（expert_prompt_segment，已停用）

该机制最初为 expert（pro）模型设计（因为旧版 expert 不支持文件上传，`input_file` 拆分方式无法为它缩短 live prompt）。网页客户端 2.4.0 合并 fast/expert/vision 后已无独立 expert 模型，分段配置保留原名，但**对所有解析成功的模型生效**：当 `FinalPrompt` 超过字符数阈值时，兼容层会按 rune 字数把提示词切分为多段，不再按 `[User]` / `[Assistant]` 等 role 标记边界切分；这些标记在 DeepSeek Web Chat 中只是普通文本。前 N-1 段使用 `FireCompletionAndStop`（发送后捕获 `response_message_id` 再调用 `stop_stream` 终止生成），最后一段正常返回完整响应。该机制复用 `StartCompletionWithSegments` 编排，对等 `StartCompletion` 可无缝接入现有流式与非流式 `Execute*` 流程。

- **该功能已停用**：`config.Store.ExpertPromptSegmentEnabled()` 现在固定返回 `false`，无论配置文件或旧配置中残留的 `enabled` 取值如何，都不会再触发分段。代码、切分算法与 `max_chars` 字段保留，便于将来按需恢复。
- `expert_prompt_segment.max_chars`（默认 `160000`）是分段阈值，以 rune 字符数统计 `FinalPrompt`（含所有 role 标记和工具提示词）。
- `FireCompletionAndStop` 捕获 `response_message_id` 后会继续消费 SSE，等待首批实际内容（`response/content`、`response/thinking_content` 或 `response/fragments`）到达后立即调用 `stop_stream`；若内容等待超时（30 秒）则直接报错终止后续段发送，不再兜底停止。`stop_stream` 后等待上游流真正关闭（drain 读到 EOF，上限 15 秒，对齐 deepseek2api）：流未在时限内关闭说明上一条消息可能尚未落库，本段直接报错终止整条分段链，而不是强关连接继续发送——否则下一段会撞上 `message still wip`。stop 后的固定 1 秒 settle 已暂时停用（`segmentSettleDelayEnabled` 开关，代码保留，需要时改回 true）。
- 分段链中每段取 PoW challenge 走按账号缓存：challenge 取出即消费（get 删除条目），使用后在后台预取下一个回填缓存（`DS2API_POW_PREFETCH_ENABLED`，默认开启；设为 `false`/`0`/`off`/`no` 关闭），使每段取 PoW 通常零 `create_pow_challenge` 往返。该缓存对所有 completion 请求生效，不只分段链。
- 首段扫描（`scanSegmentHead`）会用与 drain 阶段完全一致的规则（`segmentLineSignal`）判定每一行是内容、上游错误还是内容过滤。上游以普通 `data:` 行下发的 hint 错误（例如超长输入的 `finish_reason=input_exceeds_limit`，文案「内容超长，请删减后再试」）和 `code=content_filter` 都会立刻终止本段并带回真实原因，不再被当成「无内容的正常流」而静默丢弃。thinking 增量同样计入「本段有产出」。
- **无内容即结束的段一律判为失败**：若某段在产出任何内容前流就结束（EOF），该消息在上游会话中并未有效落库，它返回的 `response_message_id` 不可信。继续拿它作为下一段的 `parent_message_id` 会让上游回 `biz_code=26 / invalid message id`，报错文案与真实原因完全脱节（真实原因可能是该段本身被上游拒绝）。因此这种段直接报错终止整条分段链，错误定位在真正出问题的那一段；若 drain 阶段捕获到上游错误文案，会一并带出。
- 切分算法只按 rune 字数硬切，保证每段 rune 数不超过 `max_chars`，不会因为短 role 标记文本单独形成一段。
- 账号切换重试时也会在新 session 上重新走分段发送，保证切换后仍完整提交所有段。
- 分段发送返回的 `StartResult` 与 `StartCompletion` 完全一致，下游 `ExecuteNonStreamStartedWithRetry` / `ExecuteStreamWithRetry` 无缝接入。
- Vercel stream 链路（`__stream_prepare` / `__stream_switch`）原本同样接入分段；功能停用后 prepare/switch 都直接返回完整 prompt payload，不再调用 `FireCompletionAndStop`。以下为保留实现说明：prepare 在 Go 侧先对前 N-1 段执行 `FireCompletionAndStop`，只把最后一段的 payload 交给 Node 层直连 DeepSeek；账号切换时会在新 session 上重新分段。
- 代理连接池生命周期：分段发送是存活时间最长的请求（前 N-1 段串行 `FireCompletionAndStop`，可持续数十秒到 1-2 分钟）。当 mihomo 桥在请求进行中触发 `ResetProxyClients`（绑定变更、故障转移、补位）时，被移除的代理客户端 bundle **不会立即关闭**，而是交给宽限期延迟释放（`DS2API_PROXY_CLIENT_CLOSE_GRACE_SECONDS`，默认 180 秒；`<=0` 表示立即关闭）。这样在途请求仍持有的旧 httpcloak 连接池不会被中途 `Close()` 掐断（否则底层 `pool.Manager` 会被永久关闭，后续取连接返回 `ErrPoolClosed`、且 completion 流式路径无 fallback，导致请求硬失败）。进程关闭时 `Client.Close()` 会 `flushPendingCloses` 兜底回收所有仍在宽限期内的 bundle，不泄漏连接池。

相关实现：

- 分段判断：
  [internal/completionruntime/prompt_segment.go](../internal/completionruntime/prompt_segment.go)
- 多段续发编排：
  [internal/completionruntime/segments.go](../internal/completionruntime/segments.go)
- 内容检测与停止：
  [internal/deepseek/client/client_stop_stream.go](../internal/deepseek/client/client_stop_stream.go)
- 首段 SSE 扫描与错误 / 内容信号归一化：
  [internal/deepseek/client/client_stop_stream_scan.go](../internal/deepseek/client/client_stop_stream_scan.go)
- 字数切分算法：
  [internal/prompt/segment.go](../internal/prompt/segment.go)
- 配置访问器：
  [internal/config/store_accessors.go](../internal/config/store_accessors.go)

### 9.2 文本文件内联（expert_text_file_inline，2.4.0 后休眠）

该功能为旧版 expert（pro）模型设计：expert 不会收到任何 `ref_file_ids` 或 inline 文件引用，兼容层会在 expert 请求进入 prompt 构建前，把常见文本格式（`.txt`、`.md`、`.csv`、代码文件等）的内容从内存缓存中读出，替换为同位置的 `{"type":"text","text":"..."}` 块，使文件内容直接成为 `FinalPrompt` 的一部分。

2.4.0 模型合并后，所有模型解析出的 `model_type` 都是 `default`，`model_type == "expert"` 的触发分支不再命中，该功能**处于休眠状态**：代码与配置字段（`expert_text_file_inline.enabled` / `max_file_bytes`）保留以兼容旧配置，但不再改变任何请求；文本类附件现在对所有模型统一走 `ref_file_ids` 上传引用。若上游未来恢复独立的 expert 模式，该机制可立即重新生效。保留的规则（供恢复时参考）：

- 非 expert 模型保持原行为：文件走 `ref_file_ids` 上传引用。
- 内联前会校验文件扩展名或 MIME 类型；非文本文件（图片、PDF 等）会被保留但 expert 模型最终仍不会收到它们。
- 单个文件超过 `expert_text_file_inline.max_file_bytes`（默认 `3145728`，即 3 MiB）会直接拒绝请求（HTTP 413）。
- 扩展名白名单固定为内置默认列表（见 `text_format.go` 的 `DefaultTextFileExtensions`），不再支持在配置中自定义；MIME 回退（`DefaultTextFileMimeTypes`）同样固定。
- 文件内容统一按 UTF-8 读取，非法字节替换为 `\ufffd`。
- 内联后文本会计入 `FinalPrompt` 长度；分段功能已停用（见 [9.1](#91-超长提示词分段expert_prompt_segment已停用)），超长文本不再触发分段发送。
- 除 `messages`/`input`/`attachments` 中的 `input_file` 引用外，请求顶层 `file_ids` / `ref_file_ids` 中的文本文件同样会被内联到最后一个 user 消息（非文本引用仍会被 expert payload 丢弃）。
- 该处理在 OpenAI Chat、Responses 和 Vercel stream prepare 路径中统一执行，Vercel 续发/切换时复用已 prepared 的请求，无需再次处理。
- 注意：`MemoryContentStore` 是进程内内存缓存（默认 30 分钟 TTL）。在 Vercel 上文件上传（`POST /v1/files`）与后续 chat prepare 可能落到不同的 Go 实例，此时 prepare 会因缓存未命中返回 400 `text file content unavailable`。该限制在单实例自部署下不存在；如需多实例共享，可基于 `files.ContentStore` 接口自行实现共享存储（如 Redis）。

相关实现：

- 文本格式判断：
  [internal/httpapi/openai/files/text_format.go](../internal/httpapi/openai/files/text_format.go)
- 专家模型内联处理：
  [internal/httpapi/openai/files/expert_text_file_inline.go](../internal/httpapi/openai/files/expert_text_file_inline.go)
- 内存文件缓存：
  [internal/httpapi/openai/files/file_content_store.go](../internal/httpapi/openai/files/file_content_store.go)
- 上传时缓存文件内容：
  [internal/httpapi/openai/files/handler_files.go](../internal/httpapi/openai/files/handler_files.go)

## 10. 各协议入口的差异

### 10.1 OpenAI Chat / Responses

特点：

- `developer` 会映射到 `system`
- Responses `instructions` 会 prepend 为 system message
- 普通直传时 `tools` 会注入 system prompt；`input_file` 触发时工具描述/schema 会拆成 `TOOLS.txt`，system prompt 保留格式/策略规则并明确要求模型从 `TOOLS.txt` 获取可调用工具和 schema
- `attachments` / `input_file` / inline 文件会进入 `ref_file_ids`；2.4.0 模型合并后所有模型统一走文件引用上传，[9.2](#92-文本文件内联expert_text_file_inline240-后休眠) 的文本内联处于休眠状态
- current input file 在统一 completion runtime 入口全局生效

### 10.2 Claude Messages

特点：

- top-level `system` 优先作为系统提示
- `tool_use` / `tool_result` 会被转换成统一的 assistant/tool 历史语义
- 普通直传时 `tools` 同样会被并进 system prompt；`input_file` 触发时会沿用统一的 `TOOLS.txt` 拆分上传路径
- 常规执行通过 `internal/httpapi/claude/handler_messages.go` 转到 OpenAI chat 路径，模型 alias 会先解析成 DeepSeek 原生模型
- 当前代码里没有像 OpenAI 那样完整的 `ref_file_ids` 附件链路

### 10.3 Gemini

特点：

- `systemInstruction`、`contents.parts`、`functionCall`、`functionResponse` 会先归一
- tools 会转成 OpenAI 风格 function schema
- prompt 构建复用 OpenAI 的 `promptcompat.BuildOpenAIPromptForAdapter`，`input_file` 触发时也会使用统一的 `TOOLS.txt` 拆分上传路径
- 未识别的非文本 part 会被安全序列化进 prompt，并对二进制/疑似 base64 内容做省略或截断处理

也就是说，Gemini 在“最终 prompt 语义”上，尽量和 OpenAI 保持一致。

## 11. 一份贴近真实的最终上下文示意

假设用户发来一个多轮请求：

- 有 system/developer 文本
- 有 tools
- 有一个文件型 systemprompt 附件
- 有历史 assistant tool call / tool result
- 最后一条是 user turn
- current input file 已触发

那么最终上下文更接近：

```json
{
  "prompt": "[System]:继续会话 使用工具时请参照说明与格式要求，仅使用所列出的工具\n\n工具调用格式规范 — 请严格遵照执行： ...[User]:本轮用户输入内容[System]:The only correct format for using tools is <|EPSE|tool_calls> ... </|EPSE|tool_calls> .Please do not simplify or modify this framework in any way.The only valid label is EPSE; the use of any label prefixed with DSML is **strictly prohibited**.[Assistant]:",
  "ref_file_ids": [
    "file-ds2api-history",
    "file-ds2api-tools",
    "file-systemprompt",
    "file-other-attachment"
  ],
  "thinking_enabled": true,
  "search_enabled": false
}
```

这正是“API 转网页对话纯文本”的核心成果：

- 大部分结构化语义被压进 `prompt`
- 文件保持文件
- 需要时把末尾 user turn 之前的历史拆进 `HISTORY.txt` 上下文文件，并按轮次编号成 transcript；本轮 user 输入仍留在 `prompt` 里

## 12. 修改时必须同步本文档的场景

只要触碰以下任一类行为，就必须在同一提交或同一 PR 中更新本文档：

- 角色映射变更
- system / developer / instructions 合并规则变更
- assistant reasoning 保留格式变更
- assistant 历史 `tool_calls` 的 XML 呈现方式变更
- tool result 注入方式变更
- tool prompt 模板或 tool_choice 约束变更
- 末尾工具调用格式复述的开关（`bottom_format_injection`）、自定义文本、注入位置，或重建路径沿用规则变更
- 工具调用标识（tool marker）的派生、出口替换、重建路径重新应用、上传文件渲染、流式归一化或修复 prompt 渲染变更
- inline 文件上传 / 文件引用收集规则变更
- `auto_route_vision` 开关、图片剔除范围变更（2.4.0 合并后不再有路由目标模型改写）
- current input file 触发条件、上传格式、`HISTORY.txt` transcript 结构、末尾 user turn 是否保留在 live prompt 的判定变更
- 超长提示词分段（`expert_prompt_segment`）触发条件、切分算法或续发逻辑变更
- 上游 SSE fragment 类型归类（如 `TOOL_` 片段归 thinking）或片段级终态判定变更
- 旧 `history_split` 字段忽略/清理行为变更
- completion payload 字段语义变更
- 上游复读死循环打断（closing-tag repetition guard）的判据、阈值或收尾行为变更
- Claude / Gemini 对这套统一语义的复用关系变更

优先检查这些文件：

- `internal/promptcompat/request_normalize.go`
- `internal/promptcompat/prompt_build.go`
- `internal/promptcompat/message_normalize.go`
- `internal/promptcompat/tool_prompt.go`
- `internal/httpapi/openai/files/file_inline_upload.go`
- `internal/promptcompat/file_refs.go`
- `internal/promptcompat/image_route.go`
- `internal/httpapi/openai/history/current_input_file.go`
- `internal/completionruntime/nonstream.go`
- `internal/promptcompat/responses_input_normalize.go`
- `internal/httpapi/claude/standard_request.go`
- `internal/httpapi/claude/handler_utils.go`
- `internal/httpapi/gemini/convert_request.go`
- `internal/httpapi/gemini/convert_messages.go`
- `internal/httpapi/gemini/convert_tools.go`
- `internal/prompt/messages.go`
- `internal/prompt/tool_calls.go`
- `internal/prompt/segment.go`
- `internal/promptcompat/standard_request.go`
- `internal/toolcall/marker.go`
- `internal/toolcall/toolcalls_llm_repair.go`
- `internal/config/tool_marker.go`
- `internal/js/chat-stream/marker.js`
- `internal/completionruntime/prompt_segment.go`
- `internal/completionruntime/segments.go`
- `internal/sse/repeat_guard.go`
- `internal/js/chat-stream/repeat_guard.js`

## 13. 建议的最小验证

改动这条链路后，至少补齐或检查这些测试：

- `go test ./internal/prompt/...`
- `go test ./internal/httpapi/openai/...`
- `go test ./internal/httpapi/claude/...`
- `go test ./internal/httpapi/gemini/...`
- `go test ./internal/util/...`

如果改的是 tool call 相关兼容语义，还应同时检查：

- `go test ./internal/toolcall/...`
- `go test ./internal/toolstream/...`
- `./tests/scripts/run-unit-node.sh`

## 14. 文档同步约定

本文档是这条兼容链路的专项说明。

如果外部接口行为也变了，还应同步检查：

- [API.md](../API.md)
- [API.en.md](../API.en.md)
- [docs/toolcall-semantics.md](./toolcall-semantics.md)

原则是：

- 内部主链路变化，至少更新本文档
- 外部可见契约变化，再同步更新 API 文档
