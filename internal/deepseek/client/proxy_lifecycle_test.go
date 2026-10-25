package client

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

type lifecycleDoer struct {
	mu     sync.Mutex
	closed int
}

func (d *lifecycleDoer) Do(*http.Request) (*http.Response, error) { return nil, nil }
func (d *lifecycleDoer) CloseIdleConnections() {
	d.mu.Lock()
	d.closed++
	d.mu.Unlock()
}

func (d *lifecycleDoer) closedCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closed
}

func TestCloseRequestClientsClosesEveryBundleMember(t *testing.T) {
	items := []*lifecycleDoer{{}, {}, {}, {}}
	closeRequestClients(requestClients{
		regular:   items[0],
		stream:    items[1],
		fallback:  items[2],
		fallbackS: items[3],
	})
	for i, item := range items {
		if item.closedCount() != 1 {
			t.Fatalf("bundle member %d closed %d times", i, item.closedCount())
		}
	}
}

// TestScheduleDeferredCloseDelaysUntilGrace 验证被移除的代理 bundle 不会立即
// 关闭，而是等宽限期结束后才释放，避免掐断在途请求。
func TestScheduleDeferredCloseDelaysUntilGrace(t *testing.T) {
	t.Setenv("DS2API_PROXY_CLIENT_CLOSE_GRACE_SECONDS", "1")
	c := &Client{}
	item := &lifecycleDoer{}
	c.scheduleDeferredClose(requestClients{regular: item})

	if item.closedCount() != 0 {
		t.Fatalf("bundle closed before grace elapsed: %d", item.closedCount())
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if item.closedCount() == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("bundle not closed after grace elapsed: %d", item.closedCount())
}

// TestScheduleDeferredCloseImmediateWhenGraceZero 验证宽限期配置为 0 时退化为
// 立即关闭。
func TestScheduleDeferredCloseImmediateWhenGraceZero(t *testing.T) {
	t.Setenv("DS2API_PROXY_CLIENT_CLOSE_GRACE_SECONDS", "0")
	c := &Client{}
	item := &lifecycleDoer{}
	c.scheduleDeferredClose(requestClients{regular: item})
	if item.closedCount() != 1 {
		t.Fatalf("expected immediate close when grace=0, got %d", item.closedCount())
	}
}

// TestFlushPendingClosesClosesImmediately 验证进程关闭时会兜底关闭所有仍在
// 宽限期内的 bundle。
func TestFlushPendingClosesClosesImmediately(t *testing.T) {
	t.Setenv("DS2API_PROXY_CLIENT_CLOSE_GRACE_SECONDS", "600")
	c := &Client{}
	item := &lifecycleDoer{}
	c.scheduleDeferredClose(requestClients{regular: item})
	if item.closedCount() != 0 {
		t.Fatalf("bundle closed before flush: %d", item.closedCount())
	}
	c.flushPendingCloses()
	if item.closedCount() != 1 {
		t.Fatalf("expected flush to close bundle once, got %d", item.closedCount())
	}
}

// TestResetProxyClientsDefersClose 验证 ResetProxyClients 移除缓存后不会立即
// 关闭旧 bundle（在途请求仍可安全使用），而是走延迟释放。
func TestResetProxyClientsDefersClose(t *testing.T) {
	t.Setenv("DS2API_PROXY_CLIENT_CLOSE_GRACE_SECONDS", "600")
	item := &lifecycleDoer{}
	c := &Client{
		proxyClients: map[string]cachedProxyClients{
			"p1": {clients: requestClients{regular: item}},
		},
	}
	c.ResetProxyClients()
	if len(c.proxyClients) != 0 {
		t.Fatalf("expected proxy cache cleared, got %d entries", len(c.proxyClients))
	}
	if item.closedCount() != 0 {
		t.Fatalf("expected deferred close (not immediate) after reset, got %d", item.closedCount())
	}
	// 关服兜底应能立即回收。
	c.flushPendingCloses()
	if item.closedCount() != 1 {
		t.Fatalf("expected flush to close deferred bundle, got %d", item.closedCount())
	}
}
