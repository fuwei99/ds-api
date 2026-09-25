package client

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"ds2api/internal/auth"
	"ds2api/internal/config"
	dsprotocol "ds2api/internal/deepseek/protocol"
)

// powPrefetchTimeout 限定单次后台 PoW 预取请求的时长。
const powPrefetchTimeout = 15 * time.Second

// powPrefetchEnabled 报告后台 PoW challenge 预取是否开启，默认开启。
// 设 DS2API_POW_PREFETCH_ENABLED=false/0/off/no 关闭：缓存不再回填，
// 每次取 PoW 都退回同步 create_pow_challenge 往返。
func powPrefetchEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DS2API_POW_PREFETCH_ENABLED"))) {
	case "false", "0", "off", "no":
		return false
	default:
		return true
	}
}

// prefetchPowChallenge 为 (account, target_path) 在后台预取下一个 PoW
// challenge 并写入缓存（对齐 deepseek2api 的策略）：challenge 取出即消费
// （get 删除条目），使用后由这里回填，同一账号的下一个请求可省一次
// create_pow_challenge 往返。账号字段先做值快照再起协程，避免与账号
// 切换 / token 刷新对同一 *RequestAuth 的并发写产生数据竞态。
func (c *Client) prefetchPowChallenge(a *auth.RequestAuth, targetPath string) {
	if c == nil || a == nil || !powPrefetchEnabled() {
		return
	}
	accountID := strings.TrimSpace(a.AccountID)
	if accountID == "" {
		// BYO token 请求没有稳定账号标识，不做跨请求缓存。
		return
	}
	if !c.powCache.markPrefetch(accountID, targetPath) {
		return
	}
	acc := a.Account
	token := a.DeepSeekToken
	locale := a.Account.Locale
	go func() {
		defer c.powCache.clearPrefetch(accountID, targetPath)
		ctx, cancel := context.WithTimeout(context.Background(), powPrefetchTimeout)
		defer cancel()
		clients := c.requestClientsForAccount(acc)
		headers := c.authHeaders(token, locale)
		resp, status, err := c.postJSONWithStatus(ctx, clients.regular, clients.fallback, dsprotocol.DeepSeekCreatePowURL, headers, map[string]any{"target_path": targetPath})
		if err != nil {
			config.Logger.Warn("[pow_prefetch] request error", "account", accountID, "target_path", targetPath, "error", err)
			return
		}
		code, bizCode, msg, bizMsg := extractResponseStatus(resp)
		if status != http.StatusOK || code != 0 || bizCode != 0 {
			config.Logger.Warn("[pow_prefetch] non-success response", "account", accountID, "target_path", targetPath, "status", status, "code", code, "biz_code", bizCode, "msg", msg, "biz_msg", bizMsg)
			return
		}
		data, _ := resp["data"].(map[string]any)
		bizData, _ := data["biz_data"].(map[string]any)
		challenge, _ := bizData["challenge"].(map[string]any)
		c.powCache.set(accountID, targetPath, challenge)
	}()
}
