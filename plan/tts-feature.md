# DS2API TTS（朗读）功能方案

状态：可行性已验证（含真实账号端到端 POC），待实现。

目标：给 DS2API 增加一个「模拟 TTS」能力——把任意文本交给 DeepSeek 复述成一条助手消息，再调用官网朗读接口把该消息读出来，返回音频。对外可暴露为一个新资源端点。

---

## 1. 协议（来自 tts.har 抓包）

朗读不是独立 HTTP 下载，而是「一次 HTTP 换 ticket + 一条 WebSocket 流式拉 Opus 音频」。

```
① POST https://chat.deepseek.com/api/v0/auth/ticket   body: {"scope":"tts"}
   → data.biz_data.{ticket, expires_in_secs=600}
② WS  GET wss://chat.deepseek.com/api/v0/chat/tts/
       ?chat_session_id=<会话id>&message_id=<助手消息id>
       &ticket=<ticket>&mode=manual&format=opus
③ 服务端：{"event":"ready",audio_id,format:"opus",voice_id:"mira",trace_id}
          → N 个二进制帧
          → {"event":"finish","code":0,"msg":"success"}
   客户端：收到服务端 finish 后再回 {"event":"finish"}
```

要点：

- `message_id` 是会话内**已存在的助手消息** id，来自 completion SSE 的
  `event: ready` → `response_message_id`（现有 `continueState.observe` 已在解析）。
- WS 鉴权完全靠 query 里的 `ticket`；`auth/ticket` 用 `Authorization: Bearer <token>`，不需要 PoW。
- 二进制帧格式：`[4 字节大端序号][裸 Opus 包]`，序号 0,1,2…；Opus TOC 恒为 `0x6b`
  （config 13 = Hybrid SWB 20ms，单声道）。**不是 Ogg 容器**，前端用 WASM 解码器播放。
- `{"event":"finish"}` 必须等服务端 finish 之后再发；提前发会截断音频（POC 实测）。

TTS 只能朗读**已存在的助手消息**，不能直接对任意文本发声。所以要朗读任意文本，
必须先发一次 completion 让模型复述，这带来额外的 token 成本与延迟，且复述内容可能被模型改写
（需用强约束 prompt，例如「直接原样复述以下内容，无需多余文字」）。

### 1.1 音色切换（来自 音色.har 抓包）

官网设置里有 4 种音色，通过一个独立 HTTP 接口切换：

```
POST https://chat.deepseek.com/api/v0/chat/tts/voice
Authorization: Bearer <token>
Content-Type: application/json
body: {"voice_id": "<id>"}
→ {"code":0,"data":{"biz_code":0,"biz_msg":"","biz_data":{}}}   # 成功无额外数据
```

音色 id（共 4 种）：

| voice_id | 说明 |
|---|---|
| `mira` | 默认音色（tts.har 的 ready 事件返回的就是它） |
| `echo` | |
| `stella` | |
| `tide` | |

补充事实：

- `GET /api/v0/users/settings` 只返回 `training_allowed`，**不包含音色**；音色选择由服务端按账号
  持久化，读取方式是朗读时 WS 的 `{"event":"ready",...,"voice_id":"mira"}` 回显。
- WS 连接 query **没有** voice 参数；要换音色必须先用 `POST /api/v0/chat/tts/voice` 设置。
- 该接口同样用 Bearer token 鉴权，不需要 PoW。

实现影响：

- 需要在客户端层加一个 `SetVoice(ctx, a, voiceID)`（照 `CreateSession` 写，POST 小 JSON，读 `biz_data`）。
- `SynthesizeSpeech` 前若指定了非默认音色，先调 `SetVoice` 再开 WS；
  也可直接从 ready 事件读回 `voice_id` 用于校验/回显。
- 对外 API 建议暴露 `voice` 参数，映射到上述 4 个 id；不指定则沿用账号当前设置（默认 mira）。

---

## 2. 关键难点验证（第 3 条：WS 如何走 httpcloak 指纹）

结论：**可行，且已用真实账号端到端跑通 101 + 22 个 Opus 帧。**

### 2.1 被否定的两条路

| 路径 | 结果 |
|---|---|
| `httpcloak.LocalProxy` + CONNECT | `local_proxy.go:486-536` 只做裸 TCP 隧道，注释自述 CONNECT 不做指纹；客户端自己 TLS = Go crypto/tls，等于裸奔 |
| `httpcloak.LocalProxy` HTTP 转发 | `isHopByHopHeader` 把 `Upgrade` 当 hop-by-hop 丢弃（`local_proxy.go:959`），`Session.DoStream` 无法 hijack，承载不了 101 |

### 2.2 可行路径：preset.ClientHelloID + sardanioss/utls 自拨号

httpcloak 不暴露指纹裸连接，但其 `fingerprint` 包暴露了 preset 的 `ClientHelloID`，
配合底层 `github.com/sardanioss/utls` 可以拿到带 Chrome TLS 指纹的 `net.Conn`，
再在其上跑 HTTP/1.1 WebSocket Upgrade：

```go
preset := fingerprint.GetStrict(transport.ChromePresetName) // chrome-150-windows
cfg := &utls.Config{ServerName: host, NextProtos: []string{"http/1.1"}, MinVersion: utls.VersionTLS12}
uconn := utls.UClient(rawConn, cfg, preset.ClientHelloID)

// 关键：ClientHelloID 会自己生成 ALPN=[h2,http/1.1]，cfg.NextProtos 被忽略。
// 必须先实体化扩展，再把 ALPN 改成 http/1.1，否则协商成 h2，H1 升级字节写进 H2 连接直接 EOF。
if err := uconn.BuildHandshakeState(); err != nil { ... }
for _, ext := range uconn.Extensions {
    if alpn, ok := ext.(*utls.ALPNExtension); ok {
        alpn.AlpnProtocols = []string{"http/1.1"}
        break
    }
}
fingerprint.ApplySignatureAlgorithms(uconn.Extensions, preset.SignatureAlgorithms)
if err := uconn.HandshakeContext(ctx); err != nil { ... }
```

这与 httpcloak 自身 H1 transport 的做法完全一致（`transport/http1_transport.go` 内注释：
「ClientHelloID includes ALPN with [h2, http/1.1], so we must modify it」）。

### 2.3 POC 实测结果

```
login → create session → completion(response_message_id=2) → auth/ticket(scope=tts)
→ uTLS(Chrome 指纹, ALPN=http/1.1) → WS upgrade
HTTP/1.1 101 Switching Protocols
{"event":"ready","voice_id":"mira","format":"opus",...}
BINARY frame: 222 bytes (00 00 00 00 6b 85 29 27)   ← 序号0 + 裸 Opus
... 22 个二进制帧 ...
{"event":"finish","code":0,"msg":"success"} → 客户端回 {"event":"finish"}
共 frames=23, audioBytes=5944
```

帧头与 HAR 完全吻合。指纹传输这一原「最大不确定性」已解除。

### 2.4 由此确定的技术选择

- **不需要新增 WebSocket 依赖**：可手写极简 RFC6455 帧读写（POC 已实现文本/二进制/close）。
  若倾向用库，`coder/websocket`/`gorilla/websocket` 支持自定义 `net.Conn` 拨号，也可。
- **绕开 `*httpcloak.Client`**，直接用 `fingerprint` preset + `sardanioss/utls` 拨号。
  - H1 的 TLS 指纹与主传输同源；
  - H2 Akamai 指纹不适用（WS 本就是 H1，无影响）。
- **代理绑定需自己接**：TCP 拨号前套 SOCKS5 / HTTP CONNECT。
  可复用 `github.com/sardanioss/httpcloak/proxy` 的 `NewSOCKS5Dialer`。
  账号的 `ProxyID` → `config.Proxy` 的解析已有 `resolveProxyForAccount`（`client/proxy.go:98`）。

---

## 3. 整体实现方案

### 3.1 分层（遵守 AGENTS.md 的协议边界原则）

朗读业务逻辑（ticket、WS 拉流、Opus 组装）只写一份，放在 DeepSeek 客户端层；
协议适配器（OpenAI / Claude / Gemini / 未来的 TTS 资源端点）只负责请求归一化与响应渲染。

### 3.2 改动清单

1. `internal/deepseek/protocol/constants.go`
   - 新增 `DeepSeekAuthTicketURL = "https://chat.deepseek.com/api/v0/auth/ticket"`
   - 新增 TTS WS 路径常量 `/api/v0/chat/tts/`

2. `internal/deepseek/client/client_tts.go`（新增）
   - `GetTicket(ctx, a, scope) (string, error)`：照 `CreateSession` 写，POST `{"scope":scope}`，
     读 `data.biz_data.ticket`。可放 `client_auth.go`。
   - `SynthesizeSpeech(ctx, a, sessionID, messageID, opts) ([]byte, error)`：
     取 ticket → 用 uTLS 拨号（走账号代理）→ WS 升级 → 收 ready/二进制帧/finish →
     回 finish → 返回音频。
   - 音频组装：默认封装为 **Ogg Opus**（`OpusHead` + `OpusTags` + 每包 granule position），
     或先返回「裸帧数组 + 采样参数」由上层决定。需引入极简 Ogg muxer（约 100 行，含 CRC）。

3. `internal/deepseek/transport/`（可能新增）
   - `DialTLSFingerprint(ctx, host, proxy) (net.Conn, error)`：封装 2.2 的 uTLS 拨号，
     复用到 H1 指纹常量（`ChromePresetName`），不复制常量。

4. `internal/httpapi/`（新增 TTS 资源路由）
   - 新端点（建议 `/v1/audio/speech`，OpenAI 兼容形态），请求体含 `input`/`voice`/`response_format`。
   - 内部归一化为标准请求 → 复用共享 TTS 逻辑 → 渲染为 `audio/ogg` 或 base64 JSON。
   - 在 `internal/server/router.go:108-124` 注册（`/v1/...` 与根别名两套都要）。
   - 按 AGENTS.md，同步验证 Vercel 路径。

5. WebUI（可选，后续）
   - 对话气泡上加「朗读」按钮，或独立 TTS 测试面板。

### 3.3 调用链（模拟 TTS）

```
用户文本
  → CreateSession
  → completion(prompt="直接原样复述：<文本>")，解析 response_message_id
  → GetTicket(scope=tts)
  → SynthesizeSpeech(sessionID, messageID)
  → Ogg Opus 返回
```

可选的优化：对同一账号复用会话 / 缓存 ticket 到过期前，减少往返。

---

## 4. 风险与未决问题

| 项 | 说明 | 处理 |
|---|---|---|
| 复述成本与语义不可控 | 每次朗读都要一次 completion，且模型可能改写 | 强约束 prompt；或提供「已有消息 id」直读模式 |
| 代理绑定 | POC 走直连；生产需把账号代理接到 uTLS 拨号 | 复用 `resolveProxyForAccount` + SOCKS5/HTTP CONNECT dialer |
| Vercel | WS + 长连接在 serverless 上大概率不可用 | 需明确降级为非流式或排除；AGENTS.md 要求同步验证 |
| Opus 封装 | 裸 Opus 需封 Ogg 才能被多数播放器识别 | 自写极简 Ogg muxer，或无第三方依赖 |
| 账号风控 | 朗读属正常功能，但复述式调用频率高 | 复用现有账号池限流；POC 用直连未见拦截 |
| 音频格式/音色 | `format=opus`，voice 由服务端 ready 事件给出（默认 mira） | 先固定 opus；其他 format 待抓包 |

---

## 5. 验证方式

- 单元：帧头解析（序号、Opus TOC）、Ogg 封装 CRC、ticket 响应解析。
- 集成：`GetTicket` + WS 拉流（可在有账号时跑真实用例，参考已删的 POC 做法：
  清空 `acc.ProxyID` 走直连）。
- PR gate：`./scripts/lint.sh`、`check-refactor-line-gate.sh`、`run-unit-all.sh`、
  `npm run build --prefix webui`（见 AGENTS.md）。
- 文档：若影响 prompt 兼容链路或用户可见行为，同步更新 `docs/prompt-compatibility.md`。

---

## 6. 难度评估

原评估「中等偏高」，POC 后下调为**中等**：

- 协议已完全摸清；
- 最危险的指纹传输问题已用 22 帧真实 Opus 音频证实可行；
- 剩余为常规工程：ticket 接口、uTLS 拨号封装、Opus→Ogg、新路由、Vercel 兼容。
