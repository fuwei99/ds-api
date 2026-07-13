package main

import (
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
	"time"
	"unicode/utf8"

	dsprotocol "ds2api/internal/deepseek/protocol"
	"ds2api/pow"
	"github.com/andybalholm/brotli"
)

type accountCreds struct {
	Label    string
	Email    string
	Mobile   string
	Password string
}

type stepResult struct {
	Name       string
	StatusCode int
	RespBody   string
	Err        string
	Success    bool
	Skipped    bool
	Duration   time.Duration
}

type roundResult struct {
	Label         string
	PromptLen     int
	PromptRunes   int
	PromptPreview string
	CreateSession stepResult
	GetPow        stepResult
	Completion    stepResult
}

var (
	jsonClient   = &http.Client{Timeout: 60 * time.Second}
	streamClient = &http.Client{Timeout: 0}
)

const (
	maxBodyDisplay    = 8000
	longTextCharCount = 200000
	longTextSuffix    = "\n\n超长文本发送测试。如果你能看到全部，直接回复收到即可"
)

func main() {
	var email, mobile, password string
	flag.StringVar(&email, "email", "", "账号邮箱")
	flag.StringVar(&mobile, "mobile", "", "账号手机号")
	flag.StringVar(&password, "password", "", "账号密码")
	flag.Parse()

	if password == "" || (email == "" && mobile == "") {
		fmt.Fprintln(os.Stderr, "错误：请提供账号凭据（邮箱或手机号 + 密码）")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "用法:")
		fmt.Fprintln(os.Stderr, "  go run scripts/long-input-test/main.go \\")
		fmt.Fprintln(os.Stderr, "    -email xxx@example.com -password xxx")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  或用手机号:")
		fmt.Fprintln(os.Stderr, "  go run scripts/long-input-test/main.go \\")
		fmt.Fprintln(os.Stderr, "    -mobile 12345678901 -password xxx")
		os.Exit(1)
	}

	creds := accountCreds{Label: "测试账号", Email: email, Mobile: mobile, Password: password}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	identifier := creds.Email
	if identifier == "" {
		identifier = creds.Mobile
	}
	fmt.Printf("========== 登录 %s (%s) ==========\n", creds.Label, identifier)

	deviceID := createDeviceID()
	token, r0 := doLogin(ctx, creds, deviceID)
	printStep(r0)
	if !r0.Success {
		fmt.Fprintln(os.Stderr, "登录失败，退出")
		os.Exit(1)
	}

	normalPrompt := "<User>: 你好"
	fmt.Printf("\n========== 第一轮：正常文本 ==========\n")
	printPromptInfo(normalPrompt)
	normal := runRound(ctx, token, "正常文本", normalPrompt)

	fmt.Printf("正在生成 %d 随机字符...\n", longTextCharCount)
	longText := generateLongText(longTextCharCount)
	longPrompt := "<User>: " + longText + longTextSuffix
	fmt.Printf("\n========== 第二轮：超长文本 ==========\n")
	printPromptInfo(longPrompt)
	long := runRound(ctx, token, "超长文本", longPrompt)

	printSummary(normal, long)
}

func printPromptInfo(prompt string) {
	fmt.Printf("  Prompt 长度: %d 字节 / %d 字符\n", len(prompt), utf8.RuneCountInString(prompt))
	fmt.Printf("  Prompt 预览: %s\n", truncate(prompt, 200))
}

func runRound(ctx context.Context, token, label, prompt string) roundResult {
	res := roundResult{
		Label:         label,
		PromptLen:     len(prompt),
		PromptRunes:   utf8.RuneCountInString(prompt),
		PromptPreview: truncate(prompt, 200),
	}

	sessionID, r1 := doCreateSession(ctx, token)
	res.CreateSession = r1
	printStep(r1)
	if !r1.Success {
		res.GetPow = stepResult{Name: "获取 PoW", Skipped: true}
		res.Completion = stepResult{Name: "Completion", Skipped: true}
		fmt.Printf("  -> 创建会话失败，跳过后续步骤\n\n")
		return res
	}

	powHeader, r2 := doGetPow(ctx, token)
	res.GetPow = r2
	printStep(r2)
	if !r2.Success {
		res.Completion = stepResult{Name: "Completion", Skipped: true}
		fmt.Printf("  -> 获取 PoW 失败，跳过后续步骤\n\n")
		return res
	}

	r3 := doCompletion(ctx, token, sessionID, powHeader, prompt)
	res.Completion = r3
	printStep(r3)
	fmt.Println()
	return res
}

func doLogin(ctx context.Context, creds accountCreds, deviceID string) (string, stepResult) {
	r := stepResult{Name: "登录"}
	payload := map[string]any{
		"email":     "",
		"mobile":    "",
		"password":  creds.Password,
		"area_code": "",
		"device_id": deviceID,
		"os":        "web",
	}
	if creds.Email != "" {
		payload["email"] = creds.Email
	} else if creds.Mobile != "" {
		mobile, areaCode := normalizeMobile(creds.Mobile)
		payload["mobile"] = mobile
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

func doCompletion(ctx context.Context, token, sessionID, powHeader, prompt string) stepResult {
	start := time.Now()
	r := stepResult{Name: "Completion"}
	payload := map[string]any{
		"chat_session_id":   sessionID,
		"parent_message_id": nil,
		"model_type":        "expert",
		"prompt":            prompt,
		"ref_file_ids":      []any{},
		"thinking_enabled":  true,
		"search_enabled":    false,
		"action":            nil,
		"preempt":           false,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dsprotocol.DeepSeekCompletionURL, bytes.NewReader(body))
	if err != nil {
		r.Err = err.Error()
		r.Duration = time.Since(start)
		return r
	}
	setHeaders(req, map[string]string{
		"authorization":     "Bearer " + token,
		"x-ds-pow-response": powHeader,
	})

	resp, err := streamClient.Do(req)
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

func generateLongText(n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 \n.,!?;:'-()"
	cl := len(charset)
	randBytes := make([]byte, n)
	if _, err := rand.Read(randBytes); err != nil {
		panic("failed to generate random text: " + err.Error())
	}
	out := make([]byte, n)
	for i, b := range randBytes {
		out[i] = charset[int(b)%cl]
	}
	return string(out)
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
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n... (截断，共 %d 字节)", len(s))
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

func printSummary(normal, long roundResult) {
	fmt.Println("========== 对比摘要 ==========")
	fmt.Printf("%-20s %-30s %-30s\n", "项目", normal.Label, long.Label)
	fmt.Println(strings.Repeat("-", 80))
	fmt.Printf("%-20s %-30s %-30s\n", "Prompt 字节数", fmt.Sprintf("%d", normal.PromptLen), fmt.Sprintf("%d", long.PromptLen))
	fmt.Printf("%-20s %-30s %-30s\n", "Prompt 字符数", fmt.Sprintf("%d", normal.PromptRunes), fmt.Sprintf("%d", long.PromptRunes))
	fmt.Printf("%-20s %-30s %-30s\n", "创建会话", stepSummary(normal.CreateSession), stepSummary(long.CreateSession))
	fmt.Printf("%-20s %-30s %-30s\n", "获取 PoW", stepSummary(normal.GetPow), stepSummary(long.GetPow))
	fmt.Printf("%-20s %-30s %-30s\n", "Completion", stepSummary(normal.Completion), stepSummary(long.Completion))
	fmt.Printf("%-20s %-30s %-30s\n", "Completion 耗时", durationSummary(normal.Completion.Duration), durationSummary(long.Completion.Duration))
	fmt.Printf("%-20s %-30s %-30s\n", "响应体大小", fmt.Sprintf("%d 字节", len(normal.Completion.RespBody)), fmt.Sprintf("%d 字节", len(long.Completion.RespBody)))

	fmt.Println()
	fmt.Println("========== Completion SSE 原始内容对比 ==========")

	fmt.Printf("\n--- %s ---\n", normal.Label)
	printSSEContent(normal.Completion)

	fmt.Printf("\n--- %s ---\n", long.Label)
	printSSEContent(long.Completion)

	fmt.Println()
	fmt.Println("========== 请将以上输出完整提供给助手分析 ==========")
}

func stepSummary(r stepResult) string {
	if r.Skipped {
		return "跳过"
	}
	if r.Success {
		return fmt.Sprintf("成功 (HTTP %d)", r.StatusCode)
	}
	return fmt.Sprintf("失败 (HTTP %d)", r.StatusCode)
}

func durationSummary(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return d.Round(time.Millisecond).String()
}

func printSSEContent(r stepResult) {
	if r.Skipped {
		fmt.Println("（跳过 - 前置步骤失败）")
		return
	}
	if r.RespBody == "" {
		fmt.Println("（空响应体 - SSE 流完全为空）")
		return
	}
	fmt.Println(truncate(r.RespBody, maxBodyDisplay))
}
