package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"ds2api/internal/auth"
	dsprotocol "ds2api/internal/deepseek/protocol"
	"ds2api/pow"
)

// ComputePow 使用纯 Go 实现求解 PoW challenge (DeepSeekHashV1)。
func ComputePow(ctx context.Context, challenge map[string]any) (int64, error) {
	algo, _ := challenge["algorithm"].(string)
	if algo != "DeepSeekHashV1" {
		return 0, errors.New("unsupported algorithm")
	}
	challengeStr, _ := challenge["challenge"].(string)
	salt, _ := challenge["salt"].(string)
	expireAt := toInt64(challenge["expire_at"], 1680000000)
	difficulty := toInt64FromFloat(challenge["difficulty"], 144000)

	return pow.SolvePow(ctx, challengeStr, salt, expireAt, difficulty)
}

// BuildPowHeader 序列化 {algorithm,challenge,salt,answer,signature,target_path} 为 base64(JSON)。
func BuildPowHeader(challenge map[string]any, answer int64) (string, error) {
	payload := map[string]any{
		"algorithm":   challenge["algorithm"],
		"challenge":   challenge["challenge"],
		"salt":        challenge["salt"],
		"answer":      answer,
		"signature":   challenge["signature"],
		"target_path": challenge["target_path"],
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// powPrefetchFreshnessMargin is how many seconds before expire_at a cached
// challenge is considered stale. Matches deepseek2api's 30s margin.
const powPrefetchFreshnessMargin = 30

type powChallengeEntry struct {
	challenge map[string]any
}

type powChallengeCache struct {
	mu      sync.Mutex
	entries map[string]powChallengeEntry
}

func newPowChallengeCache() *powChallengeCache {
	return &powChallengeCache{entries: map[string]powChallengeEntry{}}
}

func (c *powChallengeCache) key(accountID, targetPath string) string {
	return accountID + ":" + targetPath
}

func (c *powChallengeCache) get(accountID, targetPath string) (map[string]any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[c.key(accountID, targetPath)]
	if !ok {
		return nil, false
	}
	if !isFreshChallenge(entry.challenge) {
		delete(c.entries, c.key(accountID, targetPath))
		return nil, false
	}
	delete(c.entries, c.key(accountID, targetPath))
	return entry.challenge, true
}

func (c *powChallengeCache) set(accountID, targetPath string, challenge map[string]any) {
	if challenge == nil || !isFreshChallenge(challenge) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[c.key(accountID, targetPath)] = powChallengeEntry{challenge: challenge}
}

func isFreshChallenge(challenge map[string]any) bool {
	if challenge == nil {
		return false
	}
	expireAt := toInt64(challenge["expire_at"], 0)
	if expireAt == 0 {
		return false
	}
	return expireAt > time.Now().Unix()+powPrefetchFreshnessMargin
}

// fetchChallengeForPrefetch fetches a raw challenge from DeepSeek without solving.
// It is used by the prefetch path. Errors are logged and swallowed.
func (c *Client) fetchChallengeForPrefetch(ctx context.Context, a *auth.RequestAuth, targetPath string) (map[string]any, bool) {
	clients := c.requestClientsForAuth(ctx, a)
	headers := c.authHeaders(a.DeepSeekToken)
	resp, status, err := c.postJSONWithStatus(ctx, clients.regular, clients.fallback, dsprotocol.DeepSeekCreatePowURL, headers, map[string]any{"target_path": targetPath})
	if err != nil || status != 200 {
		return nil, false
	}
	code, bizCode, _, _ := extractResponseStatus(resp)
	if code != 0 || bizCode != 0 {
		return nil, false
	}
	data, _ := resp["data"].(map[string]any)
	bizData, _ := data["biz_data"].(map[string]any)
	challenge, _ := bizData["challenge"].(map[string]any)
	return challenge, challenge != nil
}

// prefetchPowChallenge asynchronously fetches the next PoW challenge for the
// given account and target path, caching it for subsequent requests.
func (c *Client) prefetchPowChallenge(a *auth.RequestAuth, targetPath string) {
	if c == nil || a == nil || c.powCache == nil {
		return
	}
	if cached, ok := c.powCache.get(a.AccountID, targetPath); ok && isFreshChallenge(cached) {
		c.powCache.set(a.AccountID, targetPath, cached)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		challenge, ok := c.fetchChallengeForPrefetch(ctx, a, targetPath)
		if !ok {
			return
		}
		c.powCache.set(a.AccountID, targetPath, challenge)
	}()
}

func toFloat64(v any, d float64) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return d
	}
}

func toInt64(v any, d int64) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	default:
		return d
	}
}

// toInt64FromFloat 与 toInt64 等价，仅名称区分用途。
func toInt64FromFloat(v any, d int64) int64 {
	return toInt64(v, d)
}
