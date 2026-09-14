package client

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ds2api/internal/auth"
	"ds2api/internal/config"
	trans "ds2api/internal/deepseek/transport"
	"ds2api/internal/devcapture"
	"ds2api/internal/util"
)

// intFrom is a package-internal alias for the shared util version.
var intFrom = util.IntFrom

type Client struct {
	Store      *config.Store
	Auth       *auth.Resolver
	capture    *devcapture.Store
	regular    trans.Doer
	stream     trans.Doer
	fallback   trans.Doer
	fallbackS  trans.Doer
	maxRetries int

	powCache *powChallengeCache
	cookies  *cookieJar

	proxyClientsMu sync.RWMutex
	proxyClients   map[string]cachedProxyClients

	// pendingCloses 保存已从缓存移除、但仍可能被在途请求持有的代理 bundle。
	// 它们不再立即关闭（否则会掐断正在进行的长请求，尤其是专家模式分段发送），
	// 而是延迟一个宽限期后再释放底层连接池，宽限期结束或进程关闭时统一回收。
	pendingCloseMu sync.Mutex
	pendingCloses  map[*time.Timer]requestClients

	// nodeReporter 在每次经代理的上游请求完成后回调“代理 ID + 成败”，
	// 供 mihomo 代理桥把真实流量结果闭环反馈到节点健康。
	nodeReporterMu sync.RWMutex
	nodeReporter   func(proxyID string, success bool)

	// accountPoolChanged 在账号启用/禁用状态变化（含弹性号池补位）后触发，
	// 供 mihomo 代理桥立即按已有测速结果为新启用账号分配节点。
	poolChangedMu sync.RWMutex
	poolChanged   func()

	// deviceGenLocks 按账号标识串行化 device_id（重）生成，防止并发登录
	// 时同一账号重复走数美 fp SDK。
	deviceGenMu    sync.Mutex
	deviceGenLocks map[string]*sync.Mutex

	// realFailUntil 记录 real 生成最近一次失败后的冷却截止时间（UnixNano）。
	realFailUntil atomic.Int64
}

func NewClient(store *config.Store, resolver *auth.Resolver) *Client {
	client := &Client{
		Store:        store,
		Auth:         resolver,
		capture:      devcapture.Global(),
		regular:      trans.New(60 * time.Second),
		stream:       trans.New(0),
		fallback:     trans.NewFallback(60 * time.Second),
		fallbackS:    trans.NewFallback(0),
		maxRetries:   3,
		proxyClients: map[string]cachedProxyClients{},
		powCache:     newPowChallengeCache(),
		cookies:      newCookieJar(),
	}
	if resolver != nil {
		resolver.PostLogin = func(ctx context.Context, a *auth.RequestAuth) {
			client.reportClientSettingsAfterLogin(ctx, a, "")
		}
	}
	return client
}

// PreloadPow 保留兼容接口，纯 Go 实现无需预加载。
func (c *Client) PreloadPow(_ context.Context) error {
	return nil
}

// SetAccountPoolChanged 挂接账号池启用/禁用变化回调（mihomo 桥侧实现，
// 用于新启用账号立即获得节点绑定）。
func (c *Client) SetAccountPoolChanged(fn func()) {
	if c == nil {
		return
	}
	c.poolChangedMu.Lock()
	c.poolChanged = fn
	c.poolChangedMu.Unlock()
}

func (c *Client) notifyAccountPoolChanged() {
	if c == nil {
		return
	}
	c.poolChangedMu.RLock()
	fn := c.poolChanged
	c.poolChangedMu.RUnlock()
	if fn != nil {
		fn()
	}
}

// Close releases pooled upstream connections owned by this client.
func (c *Client) Close() {
	if c == nil {
		return
	}
	closeRequestClients(requestClients{
		regular:   c.regular,
		stream:    c.stream,
		fallback:  c.fallback,
		fallbackS: c.fallbackS,
	})
	c.proxyClientsMu.Lock()
	cached := make([]requestClients, 0, len(c.proxyClients))
	for key, entry := range c.proxyClients {
		cached = append(cached, entry.clients)
		delete(c.proxyClients, key)
	}
	c.proxyClientsMu.Unlock()
	for _, bundle := range cached {
		closeRequestClients(bundle)
	}
	c.flushPendingCloses()
}

// scheduleDeferredClose 把一个已从缓存移除的代理 bundle 交给宽限期计时器，
// 到期后再关闭其底层连接池。这样正在进行的在途请求（可能持有该 bundle 的引用，
// 例如专家模式多段发送会持续数十秒）仍能跑完，不会被中途掐断。
func (c *Client) scheduleDeferredClose(bundle requestClients) {
	grace := proxyClientCloseGrace()
	if grace <= 0 {
		closeRequestClients(bundle)
		return
	}
	c.pendingCloseMu.Lock()
	if c.pendingCloses == nil {
		c.pendingCloses = make(map[*time.Timer]requestClients)
	}
	var timer *time.Timer
	timer = time.AfterFunc(grace, func() {
		c.pendingCloseMu.Lock()
		delete(c.pendingCloses, timer)
		c.pendingCloseMu.Unlock()
		closeRequestClients(bundle)
	})
	c.pendingCloses[timer] = bundle
	c.pendingCloseMu.Unlock()
}

// flushPendingCloses 立即停掉所有宽限期计时器并关闭其持有的 bundle，
// 供进程关闭时兜底回收，避免延迟关闭的连接池泄漏。
func (c *Client) flushPendingCloses() {
	c.pendingCloseMu.Lock()
	pending := c.pendingCloses
	c.pendingCloses = nil
	c.pendingCloseMu.Unlock()
	for timer, bundle := range pending {
		timer.Stop()
		closeRequestClients(bundle)
	}
}

// proxyClientCloseGrace 返回代理 bundle 从缓存移除后延迟关闭的宽限期。
// 需覆盖最长的在途请求（专家模式分段发送可持续 1-2 分钟），默认 3 分钟。
// 可用 DS2API_PROXY_CLIENT_CLOSE_GRACE_SECONDS 覆盖；<=0 表示立即关闭。
func proxyClientCloseGrace() time.Duration {
	raw := strings.TrimSpace(os.Getenv("DS2API_PROXY_CLIENT_CLOSE_GRACE_SECONDS"))
	if raw == "" {
		return 3 * time.Minute
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return 3 * time.Minute
	}
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
