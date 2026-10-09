package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	dsprotocol "ds2api/internal/deepseek/protocol"
	"ds2api/internal/sse"
	"ds2api/pow"
	"github.com/andybalholm/brotli"
)

// 默认测试 prompt，可按你的约定修改这三段常量。
// 验证目标：第三轮让模型复述看到的全部内容，期望模型只看到两段 user 消息，
// 看不到第一轮被中断时模型自己生成的任何内容。
const (
	defaultPromptPart1  = "【第一段】这是一段测试消息的前半部分。"
	defaultPromptPart2  = "【第二段】这是接续上一段的后半部分。两段合起来才是完整输入。此次直接回复“收到”即可。接下来会有验证"
	defaultPromptVerify = "请原样复述你目前能看到的本次对话的全部消息内容，包括我发的每一条消息和你发的每一条消息，按顺序列出。"
)

type stepResult struct {
	Name       string
	StatusCode int
	RespBody   string
	Err        string
	Success    bool
	Skipped    bool
	Duration   time.Duration
}

type streamState struct {
	mu                sync.Mutex
	responseMessageID int
	content           string
	thinking          string
	status            string
	hadDone           bool
	rawSSE            string
	finished          bool
	err               error
	currentType       string
	hadContent        bool
}

type streamOutcome struct {
	state *streamState
}

var (
	jsonClient   = &http.Client{Timeout: 60 * time.Second}
	streamClient = &http.Client{Timeout: 0}
)

const (
	maxBodyDisplay     = 8000
	streamEndTimeout   = 15 * time.Second
	idWaitTimeout      = 30 * time.Second
	contentWaitTimeout = 30 * time.Second
)

func main() {
	var email, mobile, password, modelType string
	flag.StringVar(&email, "email", "", "账号邮箱")
	flag.StringVar(&mobile, "mobile", "", "账号手机号")
	flag.StringVar(&password, "password", "", "账号密码")
	flag.StringVar(&modelType, "model-type", "expert", "模型类型 (default / expert / vision)")
	flag.Parse()

	if password == "" || (email == "" && mobile == "") {
		fmt.Fprintln(os.Stderr, "错误：请提供账号凭据（邮箱或手机号 + 密码）")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "用法:")
		fmt.Fprintln(os.Stderr, "  go run tests/stop-continue-test/main.go \\")
		fmt.Fprintln(os.Stderr, "    -email xxx@example.com -password xxx")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  可选参数:")
		fmt.Fprintln(os.Stderr, "    -model-type expert  default / expert / vision")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	identifier := email
	if identifier == "" {
		identifier = mobile
	}
	fmt.Printf("========== 登录 (%s) ==========\n", identifier)

	deviceID := createDeviceID()
	token, r0 := doLogin(ctx, email, mobile, password, deviceID)
	printStep(r0)
	if !r0.Success {
		fmt.Fprintln(os.Stderr, "登录失败，退出")
		os.Exit(1)
	}

	runStopContinueTest(ctx, token, modelType)
}

func runStopContinueTest(ctx context.Context, token, modelType string) {
	part1 := defaultPromptPart1
	part2 := defaultPromptPart2
	verify := defaultPromptVerify

	fmt.Printf("\n========== 测试流程：发第一段 -> 停止 -> 发第二段 -> 验证 ==========\n")
	fmt.Printf("  模型类型: %s\n", modelType)
	fmt.Printf("  停止策略: 收到首批流式内容(思考或回复文本)后立即停止\n")
	fmt.Printf("  第一段 prompt: %s\n", part1)
	fmt.Printf("  第二段 prompt: %s\n", part2)
	fmt.Printf("  验证 prompt: %s\n", verify)

	// Step 1: 创建会话（全程复用同一个 session）
	fmt.Printf("\n[Step 1] 创建会话\n")
	sessionID, r1 := doCreateSession(ctx, token)
	printStep(r1)
	if !r1.Success {
		fmt.Fprintln(os.Stderr, "创建会话失败，退出")
		os.Exit(1)
	}
	fmt.Printf("  -> session_id: %s\n", sessionID)

	// Step 2: 获取 PoW #1
	fmt.Printf("\n[Step 2] 获取 PoW #1\n")
	pow1, r2 := doGetPow(ctx, token)
	printStep(r2)
	if !r2.Success {
		os.Exit(1)
	}

	// Step 3: 发送第一段，流式读取并捕获 response_message_id，收到首批内容后停止
	fmt.Printf("\n[Step 3] 发送第一段 (parent_message_id=nil)\n")
	out1, r3 := doCompletionStreamWithStop(ctx, token, sessionID, pow1, part1, modelType, nil, true)
	printStep(r3)
	if !r3.Success {
		os.Exit(1)
	}
	fmt.Printf("  -> 第一轮 response_message_id: %d\n", out1.state.responseMessageID)
	fmt.Printf("  -> 第一轮模型状态: %s\n", out1.state.status)
	fmt.Printf("  -> 中断前模型生成的内容 (content): %s\n", truncate(out1.state.content, 2000))
	fmt.Printf("  -> 中断前模型生成的思考 (thinking): %s\n", truncate(out1.state.thinking, 2000))

	if out1.state.responseMessageID <= 0 {
		fmt.Fprintln(os.Stderr, "未捕获到 response_message_id，无法续发，退出")
		os.Exit(1)
	}
	respID1 := out1.state.responseMessageID

	// Step 4: 获取 PoW #2
	fmt.Printf("\n[Step 4] 获取 PoW #2\n")
	pow2, r4 := doGetPow(ctx, token)
	printStep(r4)
	if !r4.Success {
		os.Exit(1)
	}

	// Step 5: 发送第二段，复用 session，parent_message_id = respID1
	fmt.Printf("\n[Step 5] 发送第二段 (parent_message_id=%d)\n", respID1)
	out2, r5 := doCompletionStreamWithStop(ctx, token, sessionID, pow2, part2, modelType, &respID1, false)
	printStep(r5)
	if !r5.Success {
		os.Exit(1)
	}
	fmt.Printf("  -> 第二轮 response_message_id: %d\n", out2.state.responseMessageID)
	fmt.Printf("  -> 第二轮模型生成的内容: %s\n", truncate(out2.state.content, 2000))

	if out2.state.responseMessageID <= 0 {
		fmt.Fprintln(os.Stderr, "未捕获到第二轮 response_message_id，无法验证，退出")
		os.Exit(1)
	}
	respID2 := out2.state.responseMessageID

	// Step 6: 获取 PoW #3
	fmt.Printf("\n[Step 6] 获取 PoW #3\n")
	pow3, r6 := doGetPow(ctx, token)
	printStep(r6)
	if !r6.Success {
		os.Exit(1)
	}

	// Step 7: 发送验证消息，复用 session，parent_message_id = respID2
	fmt.Printf("\n[Step 7] 发送验证消息 (parent_message_id=%d)\n", respID2)
	out3, r7 := doCompletionStreamWithStop(ctx, token, sessionID, pow3, verify, modelType, &respID2, false)
	printStep(r7)
	if !r7.Success {
		os.Exit(1)
	}

	// 最终摘要
	fmt.Println("\n========== 验证摘要 ==========")
	fmt.Printf("session_id: %s\n", sessionID)
	fmt.Printf("第一轮 response_message_id: %d (被中断)\n", respID1)
	fmt.Printf("第二轮 response_message_id: %d\n", respID2)
	fmt.Println()
	fmt.Println("【第一轮被中断时模型生成的内容】(期望: 应被 DeepSeek 丢弃，模型后续看不见)")
	fmt.Println(truncate(out1.state.content, maxBodyDisplay))
	if out1.state.content == "" {
		fmt.Println("(空 - 模型还没来得及生成可见内容就被停止)")
	}
	fmt.Println()
	fmt.Println("【第二轮模型回复内容】")
	fmt.Println(truncate(out2.state.content, maxBodyDisplay))
	fmt.Println()
	fmt.Println("【第三轮验证 - 模型复述看到的全部对话】")
	fmt.Println(truncate(out3.state.content, maxBodyDisplay))
	fmt.Println()
	fmt.Println("========== 请检查上方验证输出 ==========")
	fmt.Println("期望: 模型应只看到两条 user 消息(第一段+第二段)和它自己的第二轮回复，")
	fmt.Println("      不应复述出第一轮被中断时它自己生成过的任何内容。")
}

// doCompletionStreamWithStop 发起 completion 流式请求，边读边捕获 response_message_id。
// 当 shouldStop=true 时，捕获到 response_message_id 后，等待首批流式内容(思考或回复文本)到达，
// 到达后立即调用 stop_stream；若 contentWaitTimeout 内仍未收到内容则兜底强制停止。
// 这样可避免固定等待带来的网络环境差异，并保证中断时有真实内容被丢弃。
// 当 parentMessageID != nil 时，设置 parent_message_id（用于复用 session 续发）。
func doCompletionStreamWithStop(ctx context.Context, token, sessionID, powHeader, prompt, modelType string, parentMessageID *int, shouldStop bool) (streamOutcome, stepResult) {
	start := time.Now()
	r := stepResult{Name: "Completion (stream)"}
	outcome := streamOutcome{}

	payload := map[string]any{
		"chat_session_id":   sessionID,
		"parent_message_id": parentMessageID,
		"model_type":        modelType,
		"prompt":            prompt,
		"ref_file_ids":      []any{},
		"thinking_enabled":  true,
		"search_enabled":    false,
		"action":            nil,
		"preempt":           false,
	}
	body, _ := json.Marshal(payload)

	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	req, err := http.NewRequestWithContext(streamCtx, http.MethodPost, dsprotocol.DeepSeekCompletionURL, bytes.NewReader(body))
	if err != nil {
		r.Err = err.Error()
		r.Duration = time.Since(start)
		return outcome, r
	}
	setHeaders(req, map[string]string{
		"authorization":     "Bearer " + token,
		"x-ds-pow-response": powHeader,
	})

	resp, err := streamClient.Do(req)
	if err != nil {
		r.Err = err.Error()
		r.Duration = time.Since(start)
		return outcome, r
	}

	state := streamState{currentType: "thinking"}
	idCh := make(chan int, 1)
	contentCh := make(chan struct{}, 1)
	doneCh := make(chan struct{})

	// 读流 goroutine
	go func() {
		defer close(doneCh)
		defer func() { _ = resp.Body.Close() }()
		reader := bufio.NewReaderSize(resp.Body, 64*1024)
		for {
			select {
			case <-streamCtx.Done():
				state.err = streamCtx.Err()
				return
			default:
			}
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				state.mu.Lock()
				state.rawSSE += line
				observeLine(line, &state, idCh, contentCh)
				state.mu.Unlock()
			}
			if err != nil {
				if err == io.EOF {
					state.finished = true
				} else {
					state.err = err
				}
				return
			}
		}
	}()

	// 等待捕获 response_message_id，再等待首批流式内容到达后停止
	if shouldStop {
		select {
		case <-idCh:
			// 拿到 ID
		case <-time.After(idWaitTimeout):
			r.Err = "等待 response_message_id 超时"
			r.StatusCode = resp.StatusCode
			r.Duration = time.Since(start)
			streamCancel()
			<-doneCh
			outcome.state = &state
			return outcome, r
		case <-doneCh:
			// 流提前结束
			r.Err = "流在捕获 response_message_id 前就结束了"
			r.StatusCode = resp.StatusCode
			r.Duration = time.Since(start)
			outcome.state = &state
			if state.err != nil {
				r.Err = state.err.Error()
			}
			return outcome, r
		}

		fmt.Printf("  -> 已捕获 response_message_id=%d，等待首批流式内容...\n", state.responseMessageID)
		select {
		case <-contentCh:
			fmt.Printf("  -> 已收到首批流式内容，立即停止...\n")
		case <-time.After(contentWaitTimeout):
			fmt.Fprintf(os.Stderr, "  -> 等待首批内容超时(%s)，强制停止\n", contentWaitTimeout)
		case <-doneCh:
			r.Err = "流在收到首批流式内容前就结束了"
			r.StatusCode = resp.StatusCode
			r.Duration = time.Since(start)
			outcome.state = &state
			if state.err != nil {
				r.Err = state.err.Error()
			}
			return outcome, r
		}

		// 调用 stop_stream
		fmt.Printf("  -> 调用 stop_stream...\n")
		stopR := doStopStream(ctx, token, sessionID, state.responseMessageID)
		printStep(stopR)
		if !stopR.Success {
			fmt.Fprintf(os.Stderr, "  -> stop_stream 失败: %s\n", stopR.Err)
		}
	}

	// 等待流结束
	select {
	case <-doneCh:
	case <-time.After(streamEndTimeout):
		fmt.Fprintf(os.Stderr, "  -> 停止后流未在 %s 内结束，强制关闭连接\n", streamEndTimeout)
		streamCancel()
		<-doneCh
	}

	r.StatusCode = resp.StatusCode
	r.Duration = time.Since(start)
	if state.err != nil && state.err != context.Canceled {
		r.Err = state.err.Error()
		outcome.state = &state
		return outcome, r
	}
	r.Success = true
	outcome.state = &state
	return outcome, r
}

// doStopStream 调用 DeepSeek 停止生成接口。
func doStopStream(ctx context.Context, token, sessionID string, messageID int) stepResult {
	start := time.Now()
	r := stepResult{Name: "stop_stream"}
	payload := map[string]any{
		"chat_session_id": sessionID,
		"message_id":      messageID,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://chat.deepseek.com/api/v0/chat/stop_stream", bytes.NewReader(body))
	if err != nil {
		r.Err = err.Error()
		r.Duration = time.Since(start)
		return r
	}
	setHeaders(req, map[string]string{"authorization": "Bearer " + token})

	resp, err := jsonClient.Do(req)
	if err != nil {
		r.Err = err.Error()
		r.Duration = time.Since(start)
		return r
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := readBody(resp)
	if err != nil {
		r.Err = "read body: " + err.Error()
		r.StatusCode = resp.StatusCode
		r.Duration = time.Since(start)
		return r
	}
	r.StatusCode = resp.StatusCode
	r.RespBody = respBody
	r.Duration = time.Since(start)
	if resp.StatusCode != 200 {
		r.Err = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return r
	}
	r.Success = true
	return r
}

// observeLine 解析一行 SSE，更新 state，并在首次拿到 response_message_id 时通知 idCh。
// 在首次拿到实际流式内容(思考或回复文本)时通知 contentCh（仅通知一次）。
func observeLine(line string, state *streamState, idCh chan<- int, contentCh chan<- struct{}) {
	// ParseDeepSeekContentLine 不暴露 status，这里单独提取一次。
	if chunk, _, parsed := sse.ParseDeepSeekSSELine([]byte(line)); parsed && chunk != nil {
		extractStatus(chunk, state)
	}
	result := sse.ParseDeepSeekContentLine([]byte(line), true, state.currentType)
	state.currentType = result.NextType
	if !result.Parsed {
		return
	}
	if result.ResponseMessageID > 0 && state.responseMessageID == 0 {
		state.responseMessageID = result.ResponseMessageID
		select {
		case idCh <- result.ResponseMessageID:
		default:
		}
	}
	if result.Stop {
		state.hadDone = true
		state.finished = true
		if result.ErrorMessage != "" {
			state.status = result.ErrorMessage
		} else if result.ContentFilter {
			state.status = "content_filter"
		}
	}
	for _, p := range result.Parts {
		if p.Type == "thinking" {
			state.thinking += p.Text
		} else {
			state.content += p.Text
		}
	}
	if !state.hadContent && (state.content != "" || state.thinking != "") {
		state.hadContent = true
		select {
		case contentCh <- struct{}{}:
		default:
		}
	}
}

// extractStatus 从 SSE chunk 中提取 response status（INCOMPLETE/FINISHED 等）。
func extractStatus(chunk map[string]any, state *streamState) {
	if p, _ := chunk["p"].(string); p == "response/status" || p == "status" || p == "response/quasi_status" || p == "quasi_status" {
		if s, _ := chunk["v"].(string); s != "" {
			state.status = s
		}
	}
	if v, ok := chunk["v"].(map[string]any); ok {
		if response, _ := v["response"].(map[string]any); response != nil {
			if s, _ := response["status"].(string); s != "" {
				state.status = s
			}
		}
	}
	if message, ok := chunk["message"].(map[string]any); ok {
		if response, _ := message["response"].(map[string]any); response != nil {
			if s, _ := response["status"].(string); s != "" {
				state.status = s
			}
		}
	}
}

func doLogin(ctx context.Context, email, mobile, password, deviceID string) (string, stepResult) {
	r := stepResult{Name: "登录"}
	payload := map[string]any{
		"email":     "",
		"mobile":    "",
		"password":  password,
		"area_code": "",
		"device_id": deviceID,
		"os":        "web",
	}
	if email != "" {
		payload["email"] = email
	} else if mobile != "" {
		m, areaCode := normalizeMobile(mobile)
		payload["mobile"] = m
		payload["area_code"] = areaCode
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dsprotocol.DeepSeekLoginURL, bytes.NewReader(body))
	if err != nil {
		r.Err = err.Error()
		return "", r
	}
	setHeaders(req, nil)

	resp, err := jsonClient.Do(req)
	if err != nil {
		r.Err = err.Error()
		return "", r
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := readBody(resp)
	if err != nil {
		r.Err = "read body: " + err.Error()
		r.StatusCode = resp.StatusCode
		return "", r
	}
	r.StatusCode = resp.StatusCode
	r.RespBody = respBody

	if resp.StatusCode != 200 {
		r.Err = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return "", r
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(respBody), &parsed); err != nil {
		r.Err = "JSON parse: " + err.Error()
		return "", r
	}
	if intFrom(parsed["code"]) != 0 {
		r.Err = fmt.Sprintf("login failed: %v", parsed["msg"])
		return "", r
	}
	data, _ := parsed["data"].(map[string]any)
	if intFrom(data["biz_code"]) != 0 {
		r.Err = fmt.Sprintf("login failed: %v", data["biz_msg"])
		return "", r
	}
	bizData, _ := data["biz_data"].(map[string]any)
	user, _ := bizData["user"].(map[string]any)
	token, _ := user["token"].(string)
	if strings.TrimSpace(token) == "" {
		r.Err = "missing token"
		return "", r
	}
	r.Success = true
	return token, r
}

func doCreateSession(ctx context.Context, token string) (string, stepResult) {
	r := stepResult{Name: "创建会话"}
	body, _ := json.Marshal(map[string]any{})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dsprotocol.DeepSeekCreateSessionURL, bytes.NewReader(body))
	if err != nil {
		r.Err = err.Error()
		return "", r
	}
	setHeaders(req, map[string]string{"authorization": "Bearer " + token})

	resp, err := jsonClient.Do(req)
	if err != nil {
		r.Err = err.Error()
		return "", r
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := readBody(resp)
	if err != nil {
		r.Err = "read body: " + err.Error()
		r.StatusCode = resp.StatusCode
		return "", r
	}
	r.StatusCode = resp.StatusCode
	r.RespBody = respBody

	if resp.StatusCode != 200 {
		r.Err = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return "", r
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(respBody), &parsed); err != nil {
		r.Err = "JSON parse: " + err.Error()
		return "", r
	}
	if intFrom(parsed["code"]) != 0 {
		r.Err = fmt.Sprintf("failed: %v", parsed["msg"])
		return "", r
	}
	data, _ := parsed["data"].(map[string]any)
	if intFrom(data["biz_code"]) != 0 {
		r.Err = fmt.Sprintf("failed: %v", data["biz_msg"])
		return "", r
	}
	bizData, _ := data["biz_data"].(map[string]any)
	sessionID, _ := bizData["id"].(string)
	if sessionID == "" {
		if chatSession, ok := bizData["chat_session"].(map[string]any); ok {
			sessionID, _ = chatSession["id"].(string)
		}
	}
	if strings.TrimSpace(sessionID) == "" {
		r.Err = "missing session id"
		return "", r
	}
	r.Success = true
	return sessionID, r
}

func doGetPow(ctx context.Context, token string) (string, stepResult) {
	r := stepResult{Name: "获取 PoW"}
	body, _ := json.Marshal(map[string]any{"target_path": dsprotocol.DeepSeekCompletionTargetPath})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dsprotocol.DeepSeekCreatePowURL, bytes.NewReader(body))
	if err != nil {
		r.Err = err.Error()
		return "", r
	}
	setHeaders(req, map[string]string{"authorization": "Bearer " + token})

	resp, err := jsonClient.Do(req)
	if err != nil {
		r.Err = err.Error()
		return "", r
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := readBody(resp)
	if err != nil {
		r.Err = "read body: " + err.Error()
		r.StatusCode = resp.StatusCode
		return "", r
	}
	r.StatusCode = resp.StatusCode
	r.RespBody = respBody

	if resp.StatusCode != 200 {
		r.Err = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return "", r
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(respBody), &parsed); err != nil {
		r.Err = "JSON parse: " + err.Error()
		return "", r
	}
	if intFrom(parsed["code"]) != 0 {
		r.Err = fmt.Sprintf("failed: %v", parsed["msg"])
		return "", r
	}
	data, _ := parsed["data"].(map[string]any)
	if intFrom(data["biz_code"]) != 0 {
		r.Err = fmt.Sprintf("failed: %v", data["biz_msg"])
		return "", r
	}
	bizData, _ := data["biz_data"].(map[string]any)
	challengeMap, _ := bizData["challenge"].(map[string]any)
	if challengeMap == nil {
		r.Err = "missing challenge"
		return "", r
	}

	challenge := pow.Challenge{
		Algorithm:  getString(challengeMap, "algorithm"),
		Challenge:  getString(challengeMap, "challenge"),
		Salt:       getString(challengeMap, "salt"),
		ExpireAt:   int64From(challengeMap, "expire_at"),
		Difficulty: int64From(challengeMap, "difficulty"),
		Signature:  getString(challengeMap, "signature"),
		TargetPath: getString(challengeMap, "target_path"),
	}

	fmt.Printf("  -> 正在计算 PoW (difficulty=%d)...\n", challenge.Difficulty)
	powHeader, err := pow.SolveAndBuildHeader(ctx, &challenge)
	if err != nil {
		r.Err = "PoW solve: " + err.Error()
		return "", r
	}
	r.Success = true
	return powHeader, r
}

func createDeviceID() string {
	buf := make([]byte, 64)
	if _, err := rand.Read(buf); err != nil {
		panic("failed to generate device id: " + err.Error())
	}
	return "B" + base64.StdEncoding.EncodeToString(buf)
}

func setHeaders(req *http.Request, extra map[string]string) {
	for k, v := range dsprotocol.BaseHeaders {
		req.Header.Set(k, v)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
}

func readBody(resp *http.Response) (string, error) {
	encoding := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	var reader io.Reader = resp.Body
	switch encoding {
	case "gzip":
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return "", err
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	case "br":
		reader = brotli.NewReader(resp.Body)
	}
	b, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func normalizeMobile(raw string) (string, string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ""
	}
	hasPlus := strings.HasPrefix(s, "+")
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if digits == "" {
		return "", ""
	}
	if (hasPlus || strings.HasPrefix(digits, "86")) && strings.HasPrefix(digits, "86") && len(digits) == 13 {
		return digits[2:], "+86"
	}
	return digits, "+86"
}

func intFrom(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}

func int64From(m map[string]any, key string) int64 {
	switch n := m[key].(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	default:
		return 0
	}
}

func getString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + fmt.Sprintf("\n... (截断，共 %d 字符)", utf8.RuneCountInString(s))
}

func printStep(r stepResult) {
	fmt.Printf("\n[%s]\n", r.Name)
	if r.Skipped {
		fmt.Printf("  状态: 跳过（前置步骤失败）\n")
		return
	}
	if r.Duration > 0 {
		fmt.Printf("  耗时: %s\n", r.Duration.Round(time.Millisecond))
	}
	fmt.Printf("  HTTP 状态码: %d\n", r.StatusCode)
	if r.Err != "" {
		fmt.Printf("  错误: %s\n", r.Err)
	}
	if r.RespBody != "" {
		fmt.Printf("  响应体:\n%s\n", truncate(r.RespBody, maxBodyDisplay))
	}
	if r.Success {
		fmt.Printf("  结果: 成功\n")
	} else {
		fmt.Printf("  结果: 失败\n")
	}
}
