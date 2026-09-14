# Mihomo 代理桥（一号一 IP）

ds2api 内置的 Mihomo 代理桥可以把机场订阅节点转换为本地独立代理端口，并把
每个 DeepSeek 账号绑定到独占节点出口，实现"一个账号一个 IP"的防连坐效果。

- 子进程管理：ds2api 启动时自动拉起 `mihomo` / `mihomo.exe`，退出时自动回收。
- 订阅解析：支持 Clash/Mihomo YAML 订阅、Base64 整体编码订阅，以及按行分享链接
  （`vmess://` `ss://` `trojan://` `vless://` `hysteria2://` 等）。
- 账号 ↔ 节点绑定：为每个被绑定的节点分配独立本地 SOCKS5 端口
  （默认从 `127.0.0.1:10801` 起递增），账号的上游请求经对应端口发出。

## 工作原理

代理桥不新增请求路径，而是复用 ds2api 既有的"代理 + 账号 proxy_id"分流机制：

1. 添加订阅后，解析出的节点保存在 `mihomo_subscriptions.json`（与 `config.json` 同目录，
   由系统独立维护）；旧版 `config.json` 中残留的 `mihomo.subscriptions`/`mihomo.port_map`
   仍会兼容读取，下次增删订阅或保存配置时自动迁移到独立文件。
2. 绑定账号时，系统为该节点分配本地端口（持久化在 `mihomo_subscriptions.json` 的
   `port_map`），并自动创建一个指向 `127.0.0.1:<port>` 的**托管代理**
   （ID 形如 `mihomo-<hash>`，可在"代理 IP"页看到，请勿手改），
   然后把账号的 `proxy_id` 指向它。
3. 系统重新生成 `data/mihomo/runtime.yaml` 并重启 mihomo：每个活跃绑定对应一个
   `listeners` 入站（`type: socks` + `proxy: <节点名>`），该端口的全部流量直出
   所绑节点，跳过规则匹配。
4. ds2api 处理该账号的请求时，走既有的按账号 SOCKS5 拨号路径，经
   `127.0.0.1:<port>` 从绑定节点出口访问 DeepSeek——不同账号互不共享出口 IP。
5. 解绑、删订阅、节点从订阅中消失时，系统自动回收端口分配、删除无引用的
   托管代理、清理悬空绑定（GC）。

因为分流发生在统一的 DeepSeek client 层，OpenAI / Claude / Gemini / Ollama
等所有协议入口自动继承该能力，无需分别适配。

## 快速开始

1. 准备 mihomo 内核（Clash.Meta）：
   - WebUI「代理桥」页点击 **下载mihomo** 按钮自动完成下载、解压、改名并放到根目录
     （仅当探测不到二进制时可用；下载走加速镜像，失败自动兜底直连 GitHub）。
   - 或手动下载 <https://github.com/MetaCubeX/mihomo/releases>：
     - Windows：`mihomo-windows-amd64-*.zip` 解压后重命名为 `mihomo.exe`
     - Linux/macOS：重命名为 `mihomo` 并 `chmod +x`
   - 内核版本默认 `v1.19.29`，可用环境变量 `MIHOMO_VERSION` 覆盖。
2. 放置到以下任一位置（按此顺序探测）：
   - 配置项 `mihomo.binary_path` 或环境变量 `MIHOMO_PATH` 指定的路径
   - ds2api 可执行文件同目录 / 当前工作目录
   - 上述目录的 `bin/` 子目录
   - 系统 `PATH`
3. 启动 ds2api，登录管理后台，进入左侧 **代理桥** 页签：
   - 打开"启用代理桥"，保存设置（端口默认值通常无需修改）
   - 添加机场订阅链接，等待解析出节点
   - 在"节点与账号绑定"中为每个账号选择独占节点
   - 点击该栏右上角 **一键分配账号绑定**，可批量把全部账号绑定到节点：弹出确认
     窗口（推荐先测试延迟）；若已测过延迟，只绑定测试成功的节点（按延迟升序，
     超时/失败的节点不绑定账号），否则全部节点按当前顺序分配。分配时尽量保证
     每个节点只绑定一个账号，账号多于节点时从头循环。
   - 点击右侧 **测试延迟** 按钮，可批量测试全部节点延迟
     （经节点隧道请求 `https://www.google.com/generate_204` 计真实连接延迟，
     与 Clash 等代理软件一致）；测完后节点按延迟升序排列，
     失败/超时节点垫底，并在节点行显示延迟徽标。
4. 绑定成功后，账号请求即经由 `127.0.0.1:<分配端口>` 从对应节点出口发出。

## 配置参考

`config.json` 中的 `mihomo` 节（全部可选，推荐在 WebUI 中维护）：

| 字段 | 默认 | 说明 |
| --- | --- | --- |
| `enabled` | `false` | 是否启用代理桥（启动时拉起子进程） |
| `binary_path` | 空 | mihomo 二进制路径，留空自动探测 |
| `base_port` | `10801` | 节点本地端口分配起始值（已有分配时禁止修改） |
| `api_port` | `19090` | mihomo external-controller 监听端口（就绪探测用） |
| `auto_bind` | `false` | 自动调度：后台定时"测速 + 自动分配"（见下） |

订阅与端口映射**不再写入 config.json**，而是存放在与 config.json 同目录的
`mihomo_subscriptions.json`（结构：`{ "version": 1, "subscriptions": [...], "port_map": {...} }`，
节点最近的测速结果（延迟/健康状态/测试时间）直接挂在订阅节点上随文件持久化；
可用环境变量 `DS2API_MIHOMO_SUBSCRIPTIONS_PATH` 覆盖路径）。旧版写在
`config.mihomo.subscriptions` / `config.mihomo.port_map` 的配置仍可读取，
下次增删订阅或保存配置时自动迁移到独立文件。

环境变量：

- `MIHOMO_PATH`：等价于 `binary_path`，优先级低于配置项
- `MIHOMO_VERSION`：一键下载使用的内核版本（默认 `v1.19.29`）
- `DS2API_MIHOMO_DIR`：运行目录（默认 `./data/mihomo`），存放生成的
  `runtime.yaml`、`mihomo.log` 与 mihomo 缓存
- `DS2API_MIHOMO_SUBSCRIPTIONS_PATH`：订阅独立存储文件路径
  （默认与 config.json 同目录；Docker Compose 推荐 `/app/data/mihomo_subscriptions.json` 随挂载的 `./data` 卷持久化）
- `DS2API_MIHOMO_INTERVAL`：自动巡检定时器节拍（默认 `60` 秒，最小 5 秒）。
  服务启动 5 秒后立即触发第一轮"测速 + 自动分配"，之后按此节拍循环，
  始终保持已绑定/异常节点的延迟数据最新
- `DS2API_MIHOMO_FAILOVER_COOLDOWN_MINUTES`：坏节点安全转移后账号的换号冷却期
  （默认 `15` 分钟）。冷却期内自动调度不再切换该账号的节点，避免故障节点间反复横跳；
  手动绑定 / 一键分配会重置该冷却
- `DS2API_MIHOMO_HEALTH_TIMEOUT`：单节点测速超时（默认 `5000` 毫秒）
- `DS2API_MIHOMO_FAIL_THRESHOLD`：连续失败多少次判定为坏节点并触发故障转移
  （默认 `2`，同时适用于周期性测速与真实请求失败反馈）
- `DS2API_MIHOMO_TEST_URL`：测速目标 URL（默认
  `https://www.google.com/generate_204`，经节点隧道测真实连接延迟）

### 自动调度（auto_bind）

开启 `mihomo.auto_bind` 后，后台定时器循环执行"测速 + 自动分配"：

1. 每轮对"已绑定 + 已标记异常"的节点经节点隧道测延迟并落盘（前端定时轮询，
   面板上的延迟实时刷新）；另外每轮随机补测最多 10 个未绑定节点，
   保持候选池新鲜，避免新添加的节点长期停留在 unknown；
2. 发现某个账号绑定的节点连续失败达到阈值时，**立即**把账号安全转移到
   负载最低的健康节点，并让账号进入 15 分钟换号冷却期；
3. **真实请求失败闭环**：账号每次经节点的上游请求会把传输层成败回报给桥。
   节点上真实请求连续失败达到同一阈值时，不等下一拍巡检，立即把节点标为
   fail 并触发即时故障转移（同一节点 1 秒内的多条失败只计一次，成功请求
   清零计数）；测速成功的节点自动恢复并重新纳入可用池；
4. 弹性号池新启用（补位）且未配置代理的账号自动分配健康节点；
5. 被禁用/封号的账号自动解除绑定，端口归还可用池；
6. mihomo 子进程意外退出时自动尝试重启（节流 60 秒）。

**显式解绑的账号不再自动分配**：在"代理桥"页把某个账号解绑（或把账号的
代理切换为"无"）后，该账号被标记为 `no_proxy` 强制走直连——后台补位与
"一键分配"都会跳过它，不再被 auto_bind 自动绑回。只有显式重新绑定节点（或
为其选择代理）后才重新参与自动调度。节点失效/订阅变更等自动解除绑定不受此
标记影响，账号仍可被自动重新分配。

**启用账号立即分配**：当账号被启用时——包括管理台手动启用、弹性号池开启/补位
（含封号、禁言、验证码冷却后自动从休眠账号中启用补位）——会立即触发一次
轻量绑定调和，仅基于已有测速结果为新启用且未配置代理的账号分配一个健康节点，
不必等待下一轮巡检（默认 60 秒）。未开启 `auto_bind` 时该触发自动忽略。

**配置无变化不重启**：重新应用运行时配置时，若生成的 `runtime.yaml` 与
当前文件一致、二进制路径未变且进程仍在运行，则跳过 mihomo 重启，避免每次
绑定变更都断开所有账号的在途连接。


## 管理 API

均挂在管理员鉴权之下（前缀 `/admin`）：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/admin/mihomo/status` | 子进程状态、监听列表、最近错误 |
| PUT | `/admin/mihomo/settings` | 更新 enabled/binary_path/base_port/api_port 并应用 |
| POST | `/admin/mihomo/apply` | 重新生成配置并重启 mihomo |
| GET | `/admin/mihomo/binary` | 二进制探测结果与下载任务进度（found/state/progress） |
| POST | `/admin/mihomo/binary/download` | 后台下载 mihomo 内核到根目录（异步，轮询上述接口看进度） |
| GET/POST | `/admin/mihomo/subscriptions` | 订阅列表 / 添加订阅（抓取并解析） |
| POST | `/admin/mihomo/subscriptions/{id}/refresh` | 重新抓取订阅，同步节点与绑定 |
| DELETE | `/admin/mihomo/subscriptions/{id}` | 删除订阅并解除相关绑定 |
| GET | `/admin/mihomo/nodes` | 全部节点及其端口、已绑账号 |
| POST | `/admin/mihomo/delay-test` | 批量测试全部节点延迟（每批最多 60 并发，结果按延迟升序、失败项垫底） |
| POST | `/admin/mihomo/assign` | 一键为全部账号分配节点绑定，body `{"node_keys": ["<nodeKey>", ...]}`（单事务 + 单次 Apply） |
| PUT | `/admin/mihomo/bindings/{identifier}` | 绑定/解绑，body `{"node": "<nodeKey 或空串>"}` |

## 注意事项

- **Vercel 等 serverless 部署不支持**拉起子进程：状态接口返回
  `supported: false`，页面仅展示只读状态；配置仍可编辑，回到常驻进程
  部署后生效。延迟测试依赖运行中的 mihomo 子进程，未启用/未运行时
  接口返回错误，WebUI 按钮也会禁用并提示先启用代理桥。
- 订阅数据单独存放在 `mihomo_subscriptions.json`，备份/迁移时请连同该文件
  一起复制；直接用旧版 `config.json`（含订阅）覆盖也不会丢，重启后首次保存
  即自动迁移到独立文件。
- 托管代理由系统增删，请勿在"代理 IP"页手动修改或删除；解除绑定请到
  "代理桥"页操作。
- 同一节点可绑定多个账号（共享同一本地端口与出口 IP）；要严格"一号一 IP"，
  请为每个账号选择不同节点。
- mihomo 子进程日志写在 `data/mihomo/mihomo.log`；应用失败时 WebUI 状态卡片
  会展示最近错误（端口被占用、二进制缺失、配置非法等）。
- `external-controller` 仅绑定 `127.0.0.1`，且每次 mihomo 进程（重新）启动会
  生成随机 `secret` 写入 `runtime.yaml` 的 `secret` 字段，控制 API 访问需携带
  `Authorization: Bearer`；健康测速自动带上该鉴权头。请勿在非本机场景暴露
  `api_port`。
- 就绪/存活探测会带 `secret` 请求 `GET /version` 确认端口确实由 mihomo
  控制面服务，而不是只验证 TCP 可连——`api_port` 被其它进程占用时不会被
  误判为“桥在运行”。
- 订阅与端口映射随 `mihomo_subscriptions.json` 独立持久化；普通配置保存
  （账号/密钥等变更）不会重写该文件，仅测速结果等 mihomo 数据变化时才落盘，
  反之纯 mihomo 变更也不会连带重写 `config.json`。
- `api_port` 会自动从节点监听端口分配中排除，即使它落在 `base_port` 起始的
  端口区间内也不会被后续账号绑定占用。
