package transport

import (
	"fmt"
	"net/http"
	"time"
)

type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client dials upstream with a browser-profiled (Chrome) TLS ClientHello and
// HTTP/2 fingerprint via httpcloak.
type Client struct {
	cloak *httpCloakDoer
}

func New(timeout time.Duration) *Client {
	return newHTTPCloakTransport(timeout, "", timeout == 0, false)
}

// NewWithProxy creates a browser-profiled client using httpcloak's TCP proxy
// support. The proxy URL is kept at the transport boundary so account-level
// proxy pools do not need to know about httpcloak types.
func NewWithProxy(timeout time.Duration, proxyURL string) *Client {
	return newHTTPCloakTransport(timeout, proxyURL, timeout == 0, false)
}

// NewFallback returns the fallback transport. It shares the primary's
// chrome-150-windows TLS/H2 fingerprint and header order, but negotiates the
// protocol in auto mode (H2 preferred, HTTP/1.1 when H2 fails), so an H2-level
// fault does not take out both paths. Replacing the former std-Go fallback,
// whose crypto/tls handshake and H1-only framing were an immediate bot tell
// the moment the primary transport hiccuped.
func NewFallback(timeout time.Duration) *Client {
	return newHTTPCloakTransport(timeout, "", timeout == 0, true)
}

func NewFallbackWithProxy(timeout time.Duration, proxyURL string) *Client {
	return newHTTPCloakTransport(timeout, proxyURL, timeout == 0, true)
}

func newHTTPCloakTransport(timeout time.Duration, proxyURL string, streaming, fallback bool) *Client {
	var cloak *httpCloakDoer
	if fallback {
		cloak = newHTTPCloakFallbackDoer(timeout, proxyURL, streaming)
	} else {
		cloak = newHTTPCloakDoer(timeout, proxyURL, streaming)
	}
	return &Client{cloak: cloak}
}

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if c == nil || c.cloak == nil {
		return nil, fmt.Errorf("transport client is not initialised")
	}
	return c.cloak.Do(req)
}

// CloseIdleConnections 释放 httpcloak 底层连接池的空闲连接，
// 供代理配置变更后清理缓存客户端使用。
func (c *Client) CloseIdleConnections() {
	if c == nil || c.cloak == nil || c.cloak.client == nil {
		return
	}
	c.cloak.client.Close()
}
