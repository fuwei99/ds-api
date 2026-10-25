package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"time"

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

// powHeaderJSON 的字段顺序即线上 JSON 的 key 顺序，必须与真实网页客户端
// JSON.stringify 的插入序一致：algorithm,challenge,salt,answer,signature,target_path。
// 不能改回 map[string]any——Go 对 map 按字母序序列化，answer 会排到 challenge
// 前面，解码后即可被上游定点识别。passthrough 字段用 any 以保留空值的 null 语义。
type powHeaderJSON struct {
	Algorithm  any   `json:"algorithm"`
	Challenge  any   `json:"challenge"`
	Salt       any   `json:"salt"`
	Answer     int64 `json:"answer"`
	Signature  any   `json:"signature"`
	TargetPath any   `json:"target_path"`
}

// BuildPowHeader 序列化 {algorithm,challenge,salt,answer,signature,target_path} 为 base64(JSON)。
func BuildPowHeader(challenge map[string]any, answer int64) (string, error) {
	payload := powHeaderJSON{
		Algorithm:  challenge["algorithm"],
		Challenge:  challenge["challenge"],
		Salt:       challenge["salt"],
		Answer:     answer,
		Signature:  challenge["signature"],
		TargetPath: challenge["target_path"],
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
	// prefetching 记录正在后台预取的 key，避免同一账号同一路径重复预取。
	prefetching map[string]struct{}
}

func newPowChallengeCache() *powChallengeCache {
	return &powChallengeCache{
		entries:     map[string]powChallengeEntry{},
		prefetching: map[string]struct{}{},
	}
}

func (c *powChallengeCache) key(accountID, targetPath string) string {
	return accountID + ":" + targetPath
}

func (c *powChallengeCache) get(accountID, targetPath string) (map[string]any, bool) {
	if c == nil {
		return nil, false
	}
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
	if c == nil || challenge == nil || !isFreshChallenge(challenge) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[c.key(accountID, targetPath)] = powChallengeEntry{challenge: challenge}
}

// hasFresh 报告该 key 是否已缓存未消费的新鲜 challenge（不消费条目）。
func (c *powChallengeCache) hasFresh(accountID, targetPath string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[c.key(accountID, targetPath)]
	return ok && isFreshChallenge(entry.challenge)
}

// markPrefetch 原子登记一次后台预取：该 key 已有预取在途、或缓存中已有
// 新鲜条目（无需再取）时返回 false；否则登记在途标记并返回 true。
func (c *powChallengeCache) markPrefetch(accountID, targetPath string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := c.key(accountID, targetPath)
	if _, inflight := c.prefetching[key]; inflight {
		return false
	}
	if entry, ok := c.entries[key]; ok && isFreshChallenge(entry.challenge) {
		return false
	}
	c.prefetching[key] = struct{}{}
	return true
}

// clearPrefetch 清除预取在途标记，由预取协程结束时调用。
func (c *powChallengeCache) clearPrefetch(accountID, targetPath string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.prefetching, c.key(accountID, targetPath))
}

func isFreshChallenge(challenge map[string]any) bool {
	if challenge == nil {
		return false
	}
	expireAt := toInt64(challenge["expire_at"], 0)
	if expireAt == 0 {
		return false
	}
	// DeepSeek 2.4.0 returns expire_at in milliseconds; older payloads used
	// seconds. Normalize to seconds before comparing against time.Now().Unix(),
	// otherwise a millisecond value is treated as fresh forever and an expired
	// cached challenge (e.g. for file upload) gets reused and rejected.
	if expireAt > 1_000_000_000_000 {
		expireAt /= 1000
	}
	return expireAt > time.Now().Unix()+powPrefetchFreshnessMargin
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
