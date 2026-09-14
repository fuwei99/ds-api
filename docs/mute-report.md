# 禁言 / 封号上报（Mute Report）

当托管账号被上游临时禁言或永久封号时，DS2API 可以把这次事件推送到一个由你指定的 HTTP 地址，
便于接入告警、自动补号、数据看板等外部流程。

- 开关与地址位于：**管理面板 → 设置中心 → 运行时设置 → 禁言时上报**
- 默认状态：**关闭**
- 默认地址：`http://127.0.0.1:8100`

## 1. 开启方式

### 1.1 管理面板

在「运行时设置」中勾选「禁言时上报」，并填写上报地址，点击「保存设置」后立即生效（热更新，无需重启）。

### 1.2 配置文件

```json
{
  "runtime": {
    "mute_report": {
      "enabled": true,
      "url": "http://127.0.0.1:8100"
    }
  }
}
```

| 字段 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `runtime.mute_report.enabled` | bool | `false` | 是否开启上报。 |
| `runtime.mute_report.url` | string | `http://127.0.0.1:8100` | 接收上报的 HTTP 地址，必须是带 host 的 `http` / `https` URL；留空时回退到默认地址。 |

> 校验规则：`url` 非空时必须是合法的 `http`/`https` URL（含 host），否则保存会被拒绝并返回 `runtime.mute_report.url ...` 错误。

## 2. 触发时机

上报只在**真正检测到**账号被禁言/封号时触发，覆盖以下四条链路：

| `source` | 触发场景 |
| --- | --- |
| `login` | 登录或刷新托管账号 Token 时，上游返回 `USER_IS_BANNED`（`biz_code=10` 或 `biz_msg` 含 `user_is_banned`），或登录响应中 `chat.is_muted == 1`。 |
| `completion` | 对话补全请求的响应体不是 SSE，而是禁言 JSON 错误（`biz_code == 5`、`biz_msg` 含 `muted`，或 `biz_data.is_muted == 1`）。 |
| `stop_stream` | 提前停止流式补全（`fire_completion_and_stop`）时检测到禁言。 |
| `vercel_stream` | Vercel 直通流式补全的租约回调中上报了 `mute_until` / `banned`。 |

被禁言/封号的账号会被立即移出调度池（禁言到期自动恢复，封号需成功刷新 Token 后才解除），
因此同一账号的同一次事件通常只会上报一次。

## 3. 请求规范

上报是一个**异步、单向**的 HTTP 请求，不参与业务响应链路。

```
POST {配置的上报地址}
Content-Type: application/json
User-Agent: ds2api-mute-report/1
```

- 超时时间：**5 秒**（连接 + 读取）。
- 发送方式：独立 goroutine 异步发送，**不阻塞**正在处理的请求。
- 请求体：UTF-8 编码的 JSON 对象，见下表。

### 3.1 请求体字段

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `type` | string | 事件类型。`muted` = 临时禁言；`banned` = 永久封号。**机器判断请以该字段为准。** |
| `type_label` | string | `type` 的中文说明：`临时禁言` / `永久封号`。 |
| `account` | string | 被处理的账号标识（邮箱或手机号）。 |
| `pool_type` | string | 账号类型。`default` = 默认；`no_tools` = 无工具；`tools_only` = 仅工具。**机器判断请以该字段为准。** |
| `pool_type_label` | string | `pool_type` 的中文说明：`默认` / `无工具` / `仅工具`。 |
| `mute_seconds` | int | 禁言剩余时长（秒），由 `mute_until` 与事件发生时间计算；封号或时间戳缺失时为 `0`。 |
| `mute_until` | int | 禁言到期时间（Unix 秒）；封号时为 `0`。 |
| `enabled_count` | int | 上报时**当前剩余可调度账号数量**（启用且未禁言、未封号、未处于本地风控冷却）。已扣除本次被禁言/封号的账号。 |
| `total_count` | int | 上报时**当前账号池总数**（`config.accounts` 的条目数）。 |
| `source` | string | 检测点，取值见「2. 触发时机」。 |
| `occurred_at` | int | 事件发生时间（Unix 秒）。 |

### 3.2 请求体示例

临时禁言：

```json
{
  "type": "muted",
  "type_label": "临时禁言",
  "account": "user@example.com",
  "pool_type": "no_tools",
  "pool_type_label": "无工具",
  "mute_seconds": 3600,
  "mute_until": 1766000000,
  "enabled_count": 4,
  "total_count": 10,
  "source": "login",
  "occurred_at": 1765996400
}
```

永久封号：

```json
{
  "type": "banned",
  "type_label": "永久封号",
  "account": "user@example.com",
  "pool_type": "tools_only",
  "pool_type_label": "仅工具",
  "mute_seconds": 0,
  "mute_until": 0,
  "enabled_count": 3,
  "total_count": 10,
  "source": "login",
  "occurred_at": 1765996400
}
```

## 4. 响应要求

- 返回任意 **2xx** 状态码即视为上报成功。
- 响应体内容会被忽略（最多读取 4 KB 后丢弃），无需返回特定结构。
- 非 2xx 状态码会被记录为告警日志。

## 5. 失败处理

- **不重试**：单次上报失败不会重试，也不会影响正在处理的业务请求。
- **不阻塞**：上报在后台 goroutine 中完成，业务响应不等待上报结果。
- 失败与成功都会写入日志，便于排查：
  - 成功：`[mute_report] reported account event ...`
  - 失败：`[mute_report] report failed ...`

## 6. 对接示例

### 6.1 手动验证

```bash
curl -i -X POST http://127.0.0.1:8100 \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "muted",
    "type_label": "临时禁言",
    "account": "user@example.com",
    "pool_type": "default",
    "pool_type_label": "默认",
    "mute_seconds": 3600,
    "mute_until": 1766000000,
    "enabled_count": 4,
    "total_count": 10,
    "source": "login",
    "occurred_at": 1765996400
  }'
```

### 6.2 接收端示例（Python）

```python
from http.server import BaseHTTPRequestHandler, HTTPServer
import json

class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get('Content-Length', 0))
        event = json.loads(self.rfile.read(length) or b'{}')
        if event.get('type') == 'banned':
            print(f"封号: {event['account']} 剩余可用 {event['enabled_count']}/{event['total_count']}")
        else:
            print(f"禁言 {event['mute_seconds']}s: {event['account']} 剩余可用 {event['enabled_count']}/{event['total_count']}")
        self.send_response(204)
        self.end_headers()

HTTPServer(('127.0.0.1', 8100), Handler).serve_forever()
```

## 7. 注意事项
- 单次上报仅支持单个账号，而封禁通常呈多账号并发触发，建议接收端在捕获首条封禁报告后开启 10–30 秒缓冲等待窗口，聚合并批量处理后续事件。
- 地址指向本机（`127.0.0.1`）时，接收端必须与 DS2API 在同一主机 / 同一容器网络内；容器部署请改用可达的主机名或容器名。
- 开启开关但未启动接收端时，每次事件只会在日志里留下一条告警，不影响服务。
- `enabled_count` / `total_count` 反映的是**上报时刻**的快照；若同一时刻多个账号同时被封，多次上报的数量会依次递减。
- 该上报仅携带账号标识与统计数字，不包含 Token、密码等凭据。

## 8. 相关实现

| 关注点 | 代码位置 |
| --- | --- |
| 上报构造与发送 | `internal/banreport/reporter.go` |
| 开关与地址配置 | `internal/config/config.go`（`RuntimeConfig.MuteReport`）、`internal/config/store_accessors.go` |
| 地址校验 | `internal/config/validation.go`（`ValidateMuteReportConfig`） |
| 检测点（登录 / 补全 / 停止流式） | `internal/deepseek/client/client_auth.go`、`client_completion.go`、`client_stop_stream.go` |
| 检测点（Vercel 直通流式） | `internal/auth/request.go` |
| 面板读写接口 | `internal/httpapi/admin/settings/handler_settings_{read,parse,write,runtime}.go` |
| 面板 UI | `webui/src/features/settings/RuntimeSection.jsx` |
