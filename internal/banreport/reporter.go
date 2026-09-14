// Package banreport 把账号被上游临时禁言/永久封号的事件上报到外部地址。
//
// 上报在独立 goroutine 中异步执行，不阻塞请求链路；任何失败只记录日志，
// 不影响调用方的业务处理。是否上报与目标地址来自运行时设置
// runtime.mute_report。
package banreport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ds2api/internal/config"
)

// 事件类型（上报报文 type 字段的取值）。
const (
	// KindMuted 表示账号被上游临时禁言，到期后自动恢复。
	KindMuted = "muted"
	// KindBanned 表示账号被上游永久封号（USER_IS_BANNED）。
	KindBanned = "banned"
)

// 检测点标识（上报报文 source 字段的取值）。
const (
	// SourceLogin 表示在登录/刷新 Token 时发现。
	SourceLogin = "login"
	// SourceCompletion 表示在对话补全响应中发现。
	SourceCompletion = "completion"
	// SourceStopStream 表示在提前停止流式补全时发现。
	SourceStopStream = "stop_stream"
	// SourceVercelStream 表示在 Vercel 直通流式补全的租约回调中发现。
	SourceVercelStream = "vercel_stream"
)

const (
	// RequestTimeout 是单次上报的最长等待时间。
	RequestTimeout = 5 * time.Second
	// maxDrainBytes 限制读取响应体的字节数，避免异常对端返回超大内容。
	maxDrainBytes = 4 << 10
	// userAgent 是上报请求使用的 User-Agent。
	userAgent = "ds2api-mute-report/1"
)

// defaultClient 是上报专用的 HTTP 客户端，超时与 RequestTimeout 一致。
var defaultClient = &http.Client{Timeout: RequestTimeout}

// Event 描述一次禁言/封号检测事件。
// 账号类型、剩余账号数等派生字段由 BuildPayload 依据配置快照补齐。
type Event struct {
	// Type 取值 KindMuted 或 KindBanned。
	Type string
	// Account 是账号标识（邮箱或手机号）。
	Account string
	// MuteUntil 是禁言到期时间（Unix 秒，可含小数）；永久封号时为 0。
	MuteUntil float64
	// Source 标记检测点，便于接收方区分登录刷新与请求链路。
	Source string
	// OccurredAt 是事件发生时间；零值表示取当前时间。
	OccurredAt time.Time
}

// Payload 是上报的 JSON 报文。
// Type/PoolType 为稳定的机器可读取值，TypeLabel/PoolTypeLabel 为对应的中文说明。
type Payload struct {
	Type          string `json:"type"`
	TypeLabel     string `json:"type_label"`
	Account       string `json:"account"`
	PoolType      string `json:"pool_type"`
	PoolTypeLabel string `json:"pool_type_label"`
	MuteSeconds   int64  `json:"mute_seconds"`
	MuteUntil     int64  `json:"mute_until"`
	EnabledCount  int    `json:"enabled_count"`
	TotalCount    int    `json:"total_count"`
	Source        string `json:"source"`
	OccurredAt    int64  `json:"occurred_at"`
}

// Report 异步上报一次禁言/封号事件。
// 未开启上报或地址无效时直接返回；上报失败只记录日志，不影响调用方。
func Report(store *config.Store, ev Event) {
	payload, endpoint, ok := BuildPayload(store, ev)
	if !ok {
		return
	}
	go func() {
		if err := Post(context.Background(), defaultClient, endpoint, payload); err != nil {
			config.Logger.Warn("[mute_report] report failed",
				"endpoint", endpoint, "type", payload.Type, "account", payload.Account, "error", err)
			return
		}
		config.Logger.Info("[mute_report] reported account event",
			"endpoint", endpoint, "type", payload.Type, "account", payload.Account)
	}()
}

// BuildPayload 依据当前配置快照构造上报报文。
// 返回的第二个值是上报地址，第三个值表示是否应当上报：
// 未开启上报、store 为空或地址为空时为 false。
func BuildPayload(store *config.Store, ev Event) (Payload, string, bool) {
	if store == nil || !store.RuntimeMuteReportEnabled() {
		return Payload{}, "", false
	}
	endpoint := strings.TrimSpace(store.RuntimeMuteReportURL())
	if endpoint == "" {
		return Payload{}, "", false
	}

	accounts := store.Accounts()
	enabled := 0
	for i := range accounts {
		// enabled_count 统计“当前仍可被调度的账号”，因此同样排除
		// 处于禁言/封号/冷却状态的账号，而不只是 Disabled=true 的账号。
		if accounts[i].IsSchedulable() {
			enabled++
		}
	}

	poolType := config.PoolTypeDefault
	if acc, ok := store.FindAccount(strings.TrimSpace(ev.Account)); ok {
		poolType = config.NormalizePoolType(acc.PoolType)
	}

	occurredAt := ev.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now()
	}

	kind := KindMuted
	if ev.Type == KindBanned {
		kind = KindBanned
	}

	payload := Payload{
		Type:          kind,
		TypeLabel:     kindLabel(kind),
		Account:       strings.TrimSpace(ev.Account),
		PoolType:      poolType,
		PoolTypeLabel: poolTypeLabel(poolType),
		MuteSeconds:   muteSeconds(ev.MuteUntil, occurredAt),
		MuteUntil:     int64(ev.MuteUntil),
		EnabledCount:  enabled,
		TotalCount:    len(accounts),
		Source:        strings.TrimSpace(ev.Source),
		OccurredAt:    occurredAt.Unix(),
	}
	return payload, endpoint, true
}

// Post 同步发送一次上报报文，供 Report 与测试复用。
// client 为 nil 时使用包内默认客户端。
func Post(ctx context.Context, client *http.Client, endpoint string, payload Payload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	if client == nil {
		client = defaultClient
	}
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			config.Logger.Warn("[mute_report] close response body failed", "error", closeErr)
		}
	}()
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes)); err != nil {
		config.Logger.Warn("[mute_report] drain response body failed", "error", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	return nil
}

// muteSeconds 计算禁言剩余秒数；时间戳缺失或已过期时返回 0。
func muteSeconds(muteUntil float64, now time.Time) int64 {
	if muteUntil <= 0 {
		return 0
	}
	remaining := int64(muteUntil) - now.Unix()
	if remaining < 0 {
		return 0
	}
	return remaining
}

func kindLabel(kind string) string {
	if kind == KindBanned {
		return "永久封号"
	}
	return "临时禁言"
}

func poolTypeLabel(poolType string) string {
	switch poolType {
	case config.PoolTypeNoTools:
		return "无工具"
	case config.PoolTypeToolsOnly:
		return "仅工具"
	default:
		return "默认"
	}
}
