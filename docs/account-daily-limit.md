# 24h 速率限制（单账号每日请求上限）

给每个托管账号设一个每天（GMT+8 自然日）的请求数上限：达到上限的账号当天不再被调度。

- 位置：**管理面板 → 设置中心 → 运行时设置 → 24h 速率限制**
- 默认：关闭，且不填数值（不填数值时不生效）
- 计数文件：`data/account_daily_limit.json`

## 1. 开启方式

管理面板里勾选「24h 速率限制」，在「单账号每天最大请求数」填入数字，保存后立即生效（热更新，无需重启）。

配置文件：

```json
{
  "runtime": {
    "account_daily_limit": {
      "enabled": true,
      "max_per_day": 200
    }
  }
}
```

`max_per_day` 取值 1～1000000；不填或写 `0` 表示不生效（`0` 也用于在接口上清除该数值）。

## 2. 行为

- 第 N 次请求照常执行，第 N+1 次起该账号当天不可用；`GET /admin/queue/status` 的 `daily_limited_accounts` 列出它们。
- 所有候选账号都到上限时请求立即失败（`429`，`detail` 为
  `no idle account: every candidate account reached the 24h request limit`），不进入等待队列。
- 跨天自动重置；把数值改大，已达上限的账号立即恢复。
- 计数持久化到上面的文件：启动时文件日期不是今天就清零，是今天就沿用当天已用次数（重启不会白送一天额度）。
- 新增/禁用账号导致号池重建不清空当天计数；关掉开关会清空计数。

## 3. 运维提示

- 计数文件路径可用 `DS2API_ACCOUNT_DAILY_LIMIT_PATH` 指定；Vercel 上 `/var/task` 只读，回退
  `/tmp/account_daily_limit.json`（各实例各自计数）。
- 计数最多每秒写盘一次；某账号用满额度与跨天翻页时立即写盘。
- 想立刻重置额度：关掉开关保存一次（清空计数）再打开。
