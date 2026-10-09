package client

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"sync"

	"ds2api/internal/sse"
)

// segmentPreviewLimit 限制首段扫描保留的 SSE 原文长度，仅用于报错日志。
const segmentPreviewLimit = 2000

// segmentScanResult 记录对一个分段 SSE 流首轮扫描的结果。
type segmentScanResult struct {
	requestMessageID  int
	responseMessageID int
	hadContent        bool
	// streamEnded 表示首轮扫描是因为上游流结束（EOF）而退出，
	// 而不是因为已经拿到 response_message_id 或检测到错误。
	streamEnded bool
	// upstreamError 是上游在流内下发的错误文案（如 input_exceeds_limit
	// 对应的「内容超长，请删减后再试」）。非空即代表本段没有有效产出。
	upstreamError string
	contentFilter bool
	preview       string
}

// scanSegmentHead 扫描分段 completion 的 SSE 流头部，直到满足任一条件：
// 拿到 response_message_id、上游在流内报错 / 触发内容过滤、或流结束。
//
// 这里必须复用 sse.ParseDeepSeekContentLine（而不是只看 content 片段），
// 否则上游以普通 data: 行下发的 hint 错误（例如超长输入的
// finish_reason=input_exceeds_limit）会被静默丢弃，上层只能在下一段拿到
// 含义完全不同的 "invalid message id"，真实原因永久丢失。
func scanSegmentHead(reader *bufio.Reader) (segmentScanResult, error) {
	var out segmentScanResult
	var preview strings.Builder
	appendPreview := func(trimmed string) {
		if trimmed == "" || preview.Len() >= segmentPreviewLimit {
			return
		}
		if preview.Len() > 0 {
			preview.WriteByte('\n')
		}
		preview.WriteString(trimmed)
	}

	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := strings.TrimSpace(string(line))
			appendPreview(trimmed)
			if strings.HasPrefix(trimmed, "data:") {
				out.observeIDs(strings.TrimSpace(strings.TrimPrefix(trimmed, "data:")))
				out.observeSignal(line)
			}
			// 上游已明确报错 / 内容过滤：立即结束扫描，把真实原因带回上层。
			// 此时即便已收到 response_message_id，该消息也未有效落库。
			if out.upstreamError != "" || out.contentFilter {
				out.preview = preview.String()
				return out, nil
			}
			if out.responseMessageID > 0 {
				out.preview = preview.String()
				return out, nil
			}
		}
		if readErr != nil {
			out.preview = preview.String()
			if readErr == io.EOF {
				out.streamEnded = true
				return out, nil
			}
			return out, readErr
		}
	}
}

// observeIDs 从一条 data: 负载中提取 request/response message id。
func (r *segmentScanResult) observeIDs(data string) {
	if data == "" || data == "[DONE]" {
		return
	}
	var chunk map[string]any
	if json.Unmarshal([]byte(data), &chunk) != nil {
		return
	}
	if id := intFrom(chunk["request_message_id"]); id > 0 && r.requestMessageID == 0 {
		r.requestMessageID = id
	}
	extractResponseMessageID(chunk, &r.responseMessageID)
}

// observeSignal 按与 drain 阶段完全一致的规则判定一行是内容、错误还是过滤。
func (r *segmentScanResult) observeSignal(line []byte) {
	hasContent, errMsg, filtered := segmentLineSignal(line)
	if hasContent {
		r.hadContent = true
	}
	if filtered {
		r.contentFilter = true
	}
	if errMsg != "" && r.upstreamError == "" {
		r.upstreamError = errMsg
	}
}

// segmentLineSignal 归一化一行 DeepSeek SSE 的语义，供首段扫描与后台 drain
// 共用，保证两处对“什么算内容 / 什么算错误”的判定不会漂移。
func segmentLineSignal(line []byte) (hasContent bool, errMsg string, contentFilter bool) {
	result := sse.ParseDeepSeekContentLine(line, true, "text")
	if !result.Parsed {
		return false, "", false
	}
	if result.ContentFilter {
		return false, "", true
	}
	if msg := strings.TrimSpace(result.ErrorMessage); msg != "" {
		return false, msg, false
	}
	if result.Stop {
		// [DONE] / response/status=FINISHED 等正常终止信号，不算内容也不算错误。
		return false, "", false
	}
	return len(result.Parts) > 0 || len(result.ToolDetectionThinkingParts) > 0, "", false
}

// segmentDrainErrors 收集 drain 协程在后台读到的上游错误文案，
// 供主流程在“等内容却等到流结束”时给出真实原因而不是笼统报错。
type segmentDrainErrors struct {
	mu  sync.Mutex
	msg string
}

func (d *segmentDrainErrors) record(msg string) {
	if msg == "" {
		return
	}
	d.mu.Lock()
	if d.msg == "" {
		d.msg = msg
	}
	d.mu.Unlock()
}

func (d *segmentDrainErrors) get() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.msg
}
