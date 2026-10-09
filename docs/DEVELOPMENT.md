# DS2API 开发者速查

语言 / Language: 中文

本文面向维护者和贡献者，用于快速判断“从哪里看、改哪里、跑什么”。架构细节仍以 [ARCHITECTURE.md](./ARCHITECTURE.md) 为准，接口行为以 [API.md](../API.md) 为准。

## 1. 本地入口

常用启动与检查：

```bash
# 后端
go run ./cmd/ds2api

# WebUI 开发服务器
npm run dev --prefix webui

# WebUI 生产构建
npm run build --prefix webui
```

PR 前固定门禁：

```bash
./scripts/lint.sh
./tests/scripts/check-refactor-line-gate.sh
./tests/scripts/run-unit-all.sh
npm run build --prefix webui
```

修改 Go 文件后先运行：

```bash
gofmt -w <changed-go-files>
```

## 2. 代码定位

优先从这些入口顺着调用链看：

| 目标 | 入口 |
| --- | --- |
| 总路由、CORS、健康检查 | `internal/server/router.go` |
| OpenAI Chat / Responses | `internal/httpapi/openai/chat`、`internal/httpapi/openai/responses` |
| Claude / Gemini 兼容入口 | `internal/httpapi/claude`、`internal/httpapi/gemini` |
| API 请求归一到网页纯文本上下文 | `internal/promptcompat`、`docs/prompt-compatibility.md` |
| 工具调用解析与流式防泄漏 | `internal/toolcall`、`internal/toolstream`、`docs/toolcall-semantics.md` |
| DeepSeek 上游调用、登录、PoW、代理 | `internal/deepseek/client`、`internal/deepseek/transport` |
| 账号池、并发槽位、等待队列 | `internal/account` |
| Admin API | `internal/httpapi/admin` |
| WebUI 页面 | `webui/src/layout/DashboardShell.jsx`、`webui/src/features/*` |
| 服务器端对话记录 | `internal/chathistory`、`internal/httpapi/admin/history` |

## 3. 常见改动建议（先选扩展点，再动手）

仓库已为几类高频改动准备了单一扩展点。**先按类别找扩展点**——绕过它，一次小改动会从 1～4 个文件扩散到 20 多个：

三张注册表覆盖三类配置改动：**设置**（`internal/config/settings.go`，一个段里的若干标量字段）、**配置段**（`internal/config/sections.go`，嵌套对象 / 枚举 / 派生值 / 记录列表）、**按 Key 开关**（`internal/config/credential_flags.go`，一个 key 对象上的布尔字段）。每一类都遵守同一条规则：声明一次，其余全部派生；派生不出来的部分留在声明旁边，并在下面各节写明。

### 3.1 新增一条正文通道截断规则 → 改 4 个 Go 文件 + 2 个 Node 文件

正文通道的截断规则（上游复读死循环、编造 `[Tool]:`，以及后续同类规则）由 `internal/sse/content_policy.go` 的 `ContentPolicy` 声明一次。

1. 写规则本身（新文件，例如 `internal/sse/xxx_guard.go`）。
2. 在 `ContentPolicy.Feed` 里喂它，并给它一个 `CutReason`。
3. `internal/stream/cut.go` 的 `stopReasonForCut` 补一条映射，`internal/stream/engine.go` 补一个 `StopReason` 常量。
4. Node 侧镜像：`internal/js/chat-stream/` 下同名的 guard，并在 `content_policy.js` 注册。
5. 往 `tests/compat/fixtures/content_policy/` 加语料（含跨分片与优先级用例），在 `expected/` 写期望。

四个协议出口（OpenAI Chat / Responses / Gemini / Claude）、`StreamAccumulator`、`CollectStream`、Vercel Node 流链路**都不需要改**；Go↔Node 等价由共享语料锁定。

### 3.2 修改一个设置改 1 个文件，新增一个设置改 3～4 个文件

设置由 `internal/config/settings.go` 的注册表声明一次。注册表覆盖**一个 section 里的若干标量字段**（bool / int / string），这也是仓库里绝大多数设置的样子。

**改一个已有设置**（默认值、取值范围、标签、说明）→ 只改 `settings.go` 里那一条声明：

- `Default` 同时决定读取侧的回退值和设置接口报给前端的值；
- `Min` / `Max` 同时决定写入口的校验和前端数字输入框的上下限；
- `ForceValue` 是停用功能的杀开关：配置照常往返、接口照常显示，但读取侧恒为该值，历史配置里的 `enabled: true` 不会把功能重新打开。

**加一个新设置** → 注册表一条声明 + 语言包：

1. 在 `settings.go` 的 `init` 里加一条 `RegisterSetting(SettingSpec{Key, TitleKey, DescriptionKey, Fields})`，每个字段给出 `Name`、`Kind`、`Default`（int 字段还有 `Min`/`Max`），以及 `LabelKey`。
2. 在 `webui/src/locales/{zh,en}.json` 里补 `TitleKey` / `DescriptionKey` / 各字段的 `LabelKey`（可选的 `HelpKey` 同）。
3. 在读它的地方调用 `store.SettingBool / SettingInt / SettingString("<key>", "<field>")`。

配置文件编解码、`Clone`、校验、admin settings 的读与写、WebUI 的表单渲染与控件类型（复选框 / 数字框 / 单行输入 / 多行文本）**都不需要改**：设置接口把注册表作为 `setting_specs` 下发给前端，前端按字段类型泛化渲染。`Hidden: true` 的设置在配置里照常读写，但不出现在设置页。

这些派生出来的部分**不要手写**：多写一个 codec 分支、多写一处校验、多写一个 admin handler 分支，都是让这条设置偏离它自己的声明；改默认值或取值范围也只改声明，不在调用点改。声明里引用的语言键在 zh/en 两侧都必须存在，`internal/compat/setting_locale_keys_test.go` 会检查（漏键只会渲染成空白标签，不会报错）。

**设置页的一张卡片**由同一个文件里的 `RegisterSettingCard(SettingCardSpec{Key, TitleKey, Order, Settings})` 声明一次：卡片标题、它在页面上的位置（`Order`，与配置段卡片共用一条序列）、以及它收纳哪些设置。没被任何卡片收纳的设置在页面上自成一卡（标题就是设置自己的 `TitleKey`），所以「加一个设置」仍然只需要一条 `RegisterSetting` + 语言包。

- 把设置放进已有卡片 → 在那张卡片的 `Settings` 里加一个 key；新开一张卡片 → 加一条 `RegisterSettingCard` + 语言包；
- 一张卡片里每行控件用字段自己的 `LabelKey` / `HelpKey`，只有开关用设置自己的 `TitleKey` / `DescriptionKey`：卡片标题命名的是「主题」，开关要说明它打开的是其中哪一项。自成一卡时卡片标题已经说了这件事，开关回退到字段的 `LabelKey`（`settings.fieldEnabled`），不再重复一遍；
- 注册表在启动期拒绝空 key、重复 key、收纳不存在的设置、收纳 `Hidden` 的设置，以及一个设置被两张卡片认领（`internal/config/settings_test.go` 锁定这些拒绝项，接口形状由 `internal/httpapi/admin/handler_settings_registry_test.go` 锁定，前端的排布由 `webui/src/features/settings/settingCards.test.mjs` 锁定）。

边界：需要嵌套对象、枚举、派生值或记录列表的设置不是注册项，而是注册段（`runtime`、`elastic_pool`、`mihomo`、`vercel`、`api_keys` 等），走 3.3 的路径。

### 3.3 修改一个非标量配置段改 1 个文件，新增一个改 1～2 个文件

标量设置之外的顶层配置段（`runtime`、`admin`、`auto_delete`、`elastic_pool`、`device_id_pool`、`vercel`、`mihomo`、`accounts`、`api_keys` 等）由 `internal/config/sections.go` 的 `SectionSpec` 注册表声明一次。这一层覆盖的正是标量注册表有意不覆盖的形状：嵌套对象、枚举、派生值、记录列表。

**改一段已有配置**（默认值、取值范围、文案、新增一个字段）→ 只改这一段自己的注册文件（`section_runtime.go`、`section_admin.go`、`section_auto_delete.go`、`section_records.go` …）：

- 字段用 `SectionBool` / `SectionInt` / `SectionString` / `SectionEnum` / `SectionGroup` 声明，读（`Read`）与写（`Apply`）都在同一条声明里，字段路径（`"runtime.mute_report.enabled"`）同时是配置文件键名和报错文案；
- `SectionGroup` 的标签可以为空：一组只是把某个子功能的开关和它管辖的设置绑在一起时，写 `""` 表示这一组没有自己的名字，前端会把组内字段直接排进卡片，而不是打一个与开关同名的小标题（`runtime.mute_report`、`runtime.account_rate_limit` 与 `runtime.account_daily_limit` 就是这种）；需要小标题的组照常给 `LabelKey`；
- 枚举取值改名时，在这条声明上用 `withRenamedValues` 把旧值映射到新值：配置文件里的旧值由这一段的 `Normalize` 迁移，写入侧发来的旧值（老客户端、改名前备份的合并导入）由这层别名迁移为同一个选项，旧名既不出现在下拉里、也不再写回配置（`runtime.device_id_mode` 的 `real` → `auto` 就是这样做的一次改名）；
- `Decode` / `Encode` / `Clone` / `Normalize` / `Validate` 是这一段自己的钩子，写在同一处；
- 段里的枚举取值、上下限、默认回退都只在这里改：设置接口的读值与写值、前端数字框的上下限与下拉选项、配置文件校验，全都从这条声明派生。

**加一段新配置** → 新建一个 `section_xxx.go`（段的类型 + 一条 `RegisterSection`）+ 语言包：

1. `RegisterSection(SectionSpec{Key, Decode, Encode, Clone, Normalize, Validate})`：编解码、`Clone`、校验、归一化四件事从此由注册表分发，`codec.go`、`validation.go`、`config.go` 都不再改。
2. 需要出现在设置页就再声明 `Form`：`TitleKey` / `DescriptionKey`、字段列表（含分组与枚举）、以及 `Extra`（随段下发但不参与编辑的元数据，如 `admin.has_password_hash`）。
3. 在 `webui/src/locales/{zh,en}.json` 里补段与各字段、各枚举取值的 `LabelKey` / `HelpKey` / `DescriptionKey` / `WarningKey`。

设置接口把注册表作为 `section_specs` 下发给前端，前端按字段类型泛化渲染（复选框 / 数字框 / 单行输入 / 多行文本 / 下拉 / 分组卡片），卡片顺序由声明的 `Order` 决定（与 3.2 的设置卡片共用一条序列，见 `setting_cards`），所以**不需要**新组件、新 handler、新路由。只有确实需要专门布局的段才在自己组件里渲染，并在声明上标 `Bespoke: true`（如 `admin` 的口令修改卡片）——它的字段仍然走同一条读写路径。若这一段在写入后还要把新值推给某个运行中的组件（如 `runtime` 推给号池），在 `internal/httpapi/admin/settings` 的 `sectionPostWriteActions` 里加一行：注册表负责判断这次更新有没有碰到它，具体通知谁由那一个文件决定。

同样地，**不要手写**派生出来的部分：多一个 codec 分支、多一行校验、多一个 admin handler 分支，都是让这一段偏离它自己的声明；没有标 `Bespoke` 的段也不要让 Web UI 组件按名字读它的字段。`internal/compat/section_registry_test.go` 同时锁定这两条：非 bespoke 段在 `webui/src/features/settings/*.jsx` 里出现手写键、或已注册的段在 `codec.go` 里出现手写分支，都会失败。

容易踩的坑：一个新的顶层配置键若没有 `RegisterSection` 声明，会静默落进 `AdditionalFields`——配置里被原样携带、往返不丢，但没有任何代码会去读它。新增顶层键时先确认注册表里有它。

导入合并也走注册表：声明了 `Form` 的段按字段合并（和设置页保存的规则一致），合并规则特殊的段（`admin` 保留口令哈希、`vercel` 非空覆盖、`model_aliases` 逐条并入）在自己的声明里写 `Merge`；记录列表（`accounts` / `api_keys`）仍由各自的接口决定同名记录怎么合并。

边界：这里覆盖的是「一个顶层键」的接线，不覆盖它自己的业务接口。记录列表仍有自己的 CRUD 端点与界面（账号管理、Key 管理、device_id 弹窗），那部分不会因为注册表而消失。

### 3.4 修改一个「按 API Key 的开关」改 1 个文件，新增一个改 3～4 个文件

按 Key 的布尔开关（目前是 `force_disable_tools`）由 `internal/config/credential_flags.go` 的 `CredentialFlagSpec` 声明一次。注册表覆盖**一个 key 对象上的若干布尔字段**，这也是仓库里这类开关的样子。

**改一个已有开关**（默认值、标签、说明、徽标文案）→ 只改 `credential_flags.go` 里那一条声明：

- `Default` 同时决定读取侧的回退值、设置接口报给前端的值，以及「等于默认值就不落盘」的判定；
- `Restrictive` 决定配置导入合并时的策略：限制性开关两侧取或，导入不会静默丢掉用户已设置的限制；
- `LabelKey` / `HelpKey` / `BadgeKey` 是语言包键，分别对应弹窗里的控件标签、说明文字和列表里的徽标（`BadgeKey` 为空表示不显示徽标）。

**加一个新开关** → 注册表一条声明 + 语言包 + 动作点：

1. 在 `credential_flags.go` 的 `init` 里加一条 `RegisterCredentialFlag(CredentialFlagSpec{...})`，并加一个名字常量。
2. 在 `webui/src/locales/{zh,en}.json` 的 `accountManager` 段里补 `LabelKey` / `HelpKey` / `BadgeKey`。
3. 在要生效的地方读它。**请求路径**上的动作写在 `internal/promptcompat/caller_flags.go` 的 `ApplyCallerFlags` 里（一个分支），出口本身零改动：五个协议出口都只调用这一个函数，且都不认识任何具体开关。**非请求路径**走 `store.APIKeyFlag(key, config.CredentialFlagXxx)`。

`APIKey` 的 JSON 编解码、`Clone`、凭据归一化与去重、等值比较、store 的 O(1) 索引、admin 的 `/admin/keys` 增删改、`/admin/config` 的导入合并、五个协议出口，以及 WebUI 的开关控件与列表徽标（`webui/src/features/account/credentialFlags.js`）**都不需要改**：`/admin/config` 把注册表作为 `credential_flag_specs` 下发给前端，前端按声明泛化渲染；请求路径上的开关统一由 `ApplyCallerFlags` 在工具判定之前应用，顺序只写在这一处。等于默认值的开关不落盘，配置文件形态与手写字段时一致。

「出口不认识任何开关」这条性质由 `internal/compat/caller_flag_action_test.go` 锁定：出口里出现直接读开关（`.CredentialFlagForRequest(`）、直接清除工具定义（`promptcompat.DisableToolsForRequest(`）或自行判定工具存在性（`promptcompat.RequestBodyHasTools(`）都会失败；新增协议出口时它也会提醒把新出口接上 `ApplyCallerFlags`。

加开关也不需要改测试：`internal/promptcompat/caller_flags_test.go` 断言的是「共享步骤读到的开关名都注册过」，而不是钉死某一个名字。

边界：这里只覆盖布尔开关。按 Key 的非布尔配置（如 `name`、`remark` 这类自由文本，或按 Key 的列表）仍走 `internal/config/credentials.go` 的手写路径。

### 3.5 新增一个影响 prompt 组装的设置 → 改 promptcompat 一处

工具提示词、EPSE 格式规范、底部格式提示与最终 prompt 渲染只写在 `internal/promptcompat`：OpenAI Chat / Responses、Gemini、Claude 四个出口都经由 `promptcompat.BuildOpenAIPrompt` 产出 prompt，适配层只把各自协议的消息摊成标准消息（content / reasoning_content / tool_calls）。加一个影响 prompt 的设置因此是一处改动，四个出口同时生效：

- 改注入内容（工具描述块、格式规范、示例）→ `internal/promptcompat/tool_prompt.go`、`internal/toolcall/tool_prompt.go`；
- 改渲染形状（角色块、思考块、prompt 可见的工具历史）→ `internal/promptcompat/message_normalize.go`、`internal/prompt/messages.go`；
- 加一个「在标准请求上多走一步」的开关（如 `thinking_injection`）→ 在 `internal/promptcompat` 里写成一次 apply（`promptcompat.ApplyThinkingInjection`），各出口调用一行；四个出口都必须调用，`internal/compat/thinking_injection_surfaces_test.go` 会检查。

出口不得自建提示词文本，也不得自行渲染消息列表：`internal/compat/prompt_text_ownership_test.go` 会失败（工具提示词文本、`toolcall.BuildToolCallInstructions` / `ToolCallFormatSpec`、`prompt.MessagesPrepare` 都算越界）。

内置提示词（前端用来占位或预填的那段文本）也归这里：文本与「哪个设置字段用它」的关联都写在 `internal/promptcompat/setting_default_prompts.go`，设置接口逐字段来问（`promptcompat.SettingDefaultPrompt`），所以新增一个带内置提示词的设置不需要动 admin handler。

### 3.6 修改「账号级鉴权失败怎么恢复」→ 改 auth 一处

托管账号被上游以鉴权失败拒绝后怎么救（先刷新 Token、再换号、还是放弃）只写在 `internal/auth/auth_recovery.go` 的 `RecoverAuthFailure` 一处，返回 `AuthRecoveryRefreshed` / `AuthRecoverySwitched` / `AuthRecoveryNone` 三种动作。

调用点只提供「这次失败算不算鉴权失败」的判定与重试计数，不实现恢复顺序。三层调用点都经由它：

- `internal/deepseek/client` 的上游调用（`CreateSession` / `GetPow` / `GetSessionCount` / `DeleteSession` / `UploadFile`）共用 `recoverManagedAuth`（`client_auth_failure.go`）：`shouldAttemptRefresh` 判定这次失败算不算鉴权失败，算则交给共享步骤，不算（429 / 5xx / 非鉴权业务码）则换一次号——本账号这次不可用，但 Token 没有被拒绝，刷新没有意义；
- `internal/completionruntime` 的重试链经 `canRetryOnAlternateAccount`；
- Vercel 路径在 `internal/httpapi/openai/chat/vercel_stream.go`。

要加一种新的恢复手段（例如账号级冷却、新的重试上限），只改 `RecoverAuthFailure` 一处。这条性质由 `internal/compat/auth_recovery_sites_test.go` 锁定：三层之外出现手写的恢复序列（直接调用 `RefreshTokenErr` / `RefreshTokenOnce` / `AuthRefreshAlreadySucceeded` / `MarkAuthFailure`，或重新引入 `handleManagedAuthFailure` / `retryManagedAuthFailure` 这类分层副本）都会失败。该测试按模块根目录扫描，调用点无论是直接命名 `RecoverAuthFailure` 还是经 `recoverManagedAuth` 这类层内辅助函数进入恢复，都会被认出来并要求登记。

边界：这里收口的是「鉴权失败」这一类。因其他原因放弃当前账号（验证码 `coolDownAfterCaptcha`、禁言 `persistMutedUntil`、封禁 `SetAccountBanned`、429）仍是各调用点自己判定后调用 `SwitchAccount`，因为它们各自还要做额外的持久化动作。

### 3.7 目前仍需逐处接线的改动

下面这些还没有单一扩展点，改动会横向铺开，动手前先确认范围：

- **新增协议出口**：需要自己的 handler / runtime / deps 三件套。出口里与按 Key 开关相关的只有照抄一行 `r = r.WithContext(auth.WithToolsPresent(r.Context(), promptcompat.ApplyCallerFlags(h.Auth, r, req)))`——开关的读取与动作都已在共享路径里，新出口不需要认识任何开关（契约见 `internal/compat/caller_flag_action_test.go`）；prompt 侧同理，照抄 `promptcompat.ApplyThinkingInjection` 一行。出口清单不需要手工登记：`internal/compat/surface_packages_test.go` 按「记录工具存在性」从源码派生出口集合，思考注入与 prompt 归属两条检查都会自动把新出口算进去。**不要**再引入一份手工维护的出口清单——它只会与源码漂移，而检查不会因此失败。
- **非标量配置段的业务接口**：段的接线已经收口到 `SectionSpec` 注册表（见 3.3），加一段不再需要动 codec、校验、Clone、admin 读写和设置页。但一段自己的 admin 接口与界面仍要自己写：记录列表的增删改（账号管理、Key 管理、device_id 弹窗、Mihomo 订阅）是它们各自的业务，注册表只负责这个顶层键的编解码与生命周期。参照 `3d8f2a7`（device_id 人工录入）时，codec / 校验 / 归一化 / admin 设置读写那部分现在已经由注册表承担，剩下的才是新接口与界面本身。
- **Node 镜像**：`internal/js/chat-stream/` 下的流处理与截断规则是第二份实现，改 Go 侧行为时要在 Node 侧镜像。截断规则的镜像由语料回放锁定，且 `internal/compat/content_policy_coverage_test.go` 要求每条 `CutReason` 都有语料、在 Node 侧出现且有日志文案；其余流处理逻辑仍是手工对齐。
- **账号级异常的其他原因**（验证码 / 禁言 / 封禁 / 429）：见 3.6 的边界，各调用点自己判定后换号。

碰到上面这类改动时，**明确说出它没有扩展点**并让改动面保持可见；不要为了「收口」而另造一套机制与已有的那套并排存在——第二套机制会让下一个人不知道该跟随哪一个，也会让 3.1～3.6 的守卫测试失去意义。

其余通用约定：

- 改接口行为时，同时检查 `API.md` / `API.en.md`。
- 改 prompt 兼容链路时，必须同步 `docs/prompt-compatibility.md`。
- 改 tool call 语义时，同时检查 Go、Node sieve 和 `docs/toolcall-semantics.md`。
- 改 WebUI 配置项时，同时检查 `webui/src/features/settings`、语言包和 `config.example.json`。
- `config.example.json` 是设置注册表的手工镜像，`internal/compat/settings_example_test.go` 在它漂移时失败，所以改设置要在同一次改动里更新它；它的其它顶层键就是注册段，同一个测试从 `config.SectionKeys()` 派生这份清单。
- 拆分大文件时，保持对外函数签名稳定，并跑 `./tests/scripts/check-refactor-line-gate.sh`。

## 4. 故障定位

接口请求先看路由入口，再看协议适配层，最后看共享 runtime：

1. 路由是否命中：`internal/server/router.go` 和对应 `RegisterRoutes`。
2. 鉴权与账号选择：`internal/auth`、`internal/account`。
3. 请求归一化：`internal/promptcompat` 或协议转换包。
4. 上游请求：`internal/deepseek/client`。
5. 流式输出：`internal/stream`、`internal/sse`、`internal/toolstream`。
6. 响应格式：主路径看 `internal/assistantturn` 与 `internal/format/*`；`internal/translatorcliproxy` 只用于 Vercel/fallback/test 桥接。

对话记录页面问题优先检查：

- Admin API：`/admin/chat-history`、`/admin/chat-history/{id}`。
- 后端存储：`internal/chathistory/store.go`。
- 输出归档：`internal/responsehistory` 在协议回译/裁剪前记录 DeepSeek 上游 assistant text / thinking；即使工具调用已被对外响应转成结构化 `tool_calls` 并从可见正文剔除，后台历史仍应保留原始 EPSE / XML 片段，方便排查格式漂移。
- 前端轮询和 ETag：`webui/src/features/chatHistory/ChatHistoryContainer.jsx`。

Tool call 问题优先跑：

```bash
go test -v ./internal/toolcall ./internal/toolstream -count=1
./tests/scripts/run-unit-node.sh
```

## 5. 测试选择

小范围 Go 改动：

```bash
go test ./internal/<package> -count=1
```

前端改动：

```bash
npm run build --prefix webui
```

高风险协议或流式改动：

```bash
./tests/scripts/run-unit-all.sh
```

发布或真实账号链路验证：

```bash
./tests/scripts/run-live.sh
```

端到端测试产物默认写入 `artifacts/testsuite/`。分享日志前需要清理 token、密码、cookie 和原始请求响应内容。
