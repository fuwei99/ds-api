// Command wire-capture inspects what this proxy actually sends upstream and
// compares it against a real Chrome request captured from chat.deepseek.com.
//
// Three modes, all optional and combinable:
//
//	-ours                 print the headers we send, in on-wire order (no network)
//	-curl <file>          diff a browser "Copy as cURL" export against ours
//	-live -email -password  probe the real API and report what it sends back
//
// Secrets (authorization, cookie, password-ish values) are redacted unless
// -show-secrets is passed. A cURL export copied from a logged-in browser
// contains a bearer token and session cookies that grant full account access,
// so the default output is safe to paste into an issue or chat; the raw file is
// NOT. Redact before sharing.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	dsprotocol "ds2api/internal/deepseek/protocol"
	trans "ds2api/internal/deepseek/transport"
)

var showSecrets bool

func main() {
	var (
		ours     bool
		curlFile string
		live     bool
		email    string
		mobile   string
		password string
		locale   string
	)
	flag.BoolVar(&ours, "ours", false, "打印本项目发送的请求头（按线上顺序），不联网")
	flag.StringVar(&curlFile, "curl", "", "浏览器 Copy as cURL 导出的文件路径，与本项目对比")
	flag.BoolVar(&live, "live", false, "用真实账号探测线上接口，报告服务端行为")
	flag.StringVar(&email, "email", "", "账号邮箱（-live）")
	flag.StringVar(&mobile, "mobile", "", "账号手机号（-live）")
	flag.StringVar(&password, "password", "", "账号密码（-live）")
	flag.StringVar(&locale, "locale", "zh_CN", "账号 locale")
	flag.BoolVar(&showSecrets, "show-secrets", false, "不脱敏输出（危险：会打印 token 和 cookie）")
	flag.Parse()

	if !ours && curlFile == "" && !live {
		ours = true // most useful zero-arg behaviour
	}

	if ours {
		printOurHeaders(locale)
	}
	if curlFile != "" {
		if err := diffAgainstCurl(curlFile, locale); err != nil {
			fmt.Fprintf(os.Stderr, "\n对比失败: %v\n", err)
			os.Exit(1)
		}
	}
	if live {
		if password == "" || (email == "" && mobile == "") {
			fmt.Fprintln(os.Stderr, "错误：-live 需要 -password 加上 -email 或 -mobile")
			os.Exit(1)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		probeLive(ctx, email, mobile, password, locale)
	}
}

// ---------------------------------------------------------------- our headers

// orderedHeaders returns our headers sorted the way they hit the wire:
// the pinned Chrome order first, then anything unlisted, alphabetically.
func orderedHeaders(headers map[string]string) [][2]string {
	rank := map[string]int{}
	for i, name := range trans.ChromeHeaderOrder() {
		rank[name] = i
	}
	type kv struct {
		name, value string
		rank        int
		listed      bool
	}
	items := make([]kv, 0, len(headers))
	for name, value := range headers {
		if strings.EqualFold(name, "Host") {
			continue // carried as :authority
		}
		lower := strings.ToLower(name)
		r, listed := rank[lower]
		items = append(items, kv{name: lower, value: value, rank: r, listed: listed})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].listed != items[j].listed {
			return items[i].listed
		}
		if items[i].listed {
			return items[i].rank < items[j].rank
		}
		return items[i].name < items[j].name
	})
	out := make([][2]string, 0, len(items))
	for _, it := range items {
		out = append(out, [2]string{it.name, it.value})
	}
	return out
}

func printOurHeaders(locale string) {
	fmt.Println("=========================================================")
	fmt.Printf(" 本项目发送的请求头 (locale=%s)\n", locale)
	fmt.Println("=========================================================")

	fmt.Println("\n[HTTP/2 伪头顺序]")
	fmt.Println("  " + strings.Join(trans.ChromePseudoHeaderOrder(), ", "))

	headers := dsprotocol.BaseHeadersFor(locale)
	// These two are added per-request; shown so the comparison is complete.
	headers["authorization"] = "Bearer <token>"
	headers["x-ds-pow-response"] = "<base64 pow>"

	fmt.Println("\n[普通头，按线上顺序]")
	for _, kv := range orderedHeaders(headers) {
		fmt.Printf("  %-26s %s\n", kv[0]+":", redact(kv[0], kv[1]))
	}

	fmt.Printf("\n[HTTP 层] 自称 Chrome %s\n", trans.ChromeMajorVersion)
	if tlsName, ok := trans.ChromeClientHelloName(); ok {
		fmt.Printf("[TLS 层] preset %s -> ClientHello %s", trans.ChromePresetName, tlsName)
		if major, ok := trans.ChromeClientHelloMajor(); ok && major != trans.ChromeMajorVersion {
			fmt.Printf("  <- TLS 指纹为 Chrome %s，落后于 UA（ClientHello 版本变化远慢于版本号）", major)
		}
		fmt.Println()
	} else {
		fmt.Printf("[TLS 层] 无法解析 preset %s 的 ClientHello\n", trans.ChromePresetName)
	}
	fmt.Println("[HTTP/2] SETTINGS 1:65536;2:0;4:6291456;6:262144 | WINDOW_UPDATE 15663105")
}

// ------------------------------------------------------------------ curl diff

var curlHeaderRe = regexp.MustCompile(`(?:-H|--header)\s+('([^']*)'|"([^"]*)")`)

// parseCurl extracts header name/value pairs in the order they appear, which
// for a DevTools "Copy as cURL" export is the browser's real send order.
func parseCurl(raw string) ([][2]string, error) {
	// Join shell line continuations so the regex sees one string.
	raw = strings.ReplaceAll(raw, "\\\n", " ")
	raw = strings.ReplaceAll(raw, "^\n", " ") // cmd.exe style
	raw = strings.ReplaceAll(raw, "`\n", " ") // powershell style

	matches := curlHeaderRe.FindAllStringSubmatch(raw, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("没有解析到任何 -H 请求头，确认导出的是 'Copy as cURL' 而不是别的格式")
	}
	out := make([][2]string, 0, len(matches))
	for _, m := range matches {
		header := m[2]
		if header == "" {
			header = m[3]
		}
		name, value, found := strings.Cut(header, ":")
		if !found {
			continue
		}
		out = append(out, [2]string{
			strings.ToLower(strings.TrimSpace(name)),
			strings.TrimSpace(value),
		})
	}
	return out, nil
}

func diffAgainstCurl(path, locale string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	theirs, err := parseCurl(string(raw))
	if err != nil {
		return err
	}

	ourHeaders := dsprotocol.BaseHeadersFor(locale)
	ourHeaders["authorization"] = "Bearer <token>"
	ourHeaders["x-ds-pow-response"] = "<base64 pow>"
	ours := orderedHeaders(ourHeaders)

	ourMap := map[string]string{}
	for _, kv := range ours {
		ourMap[kv[0]] = kv[1]
	}
	theirMap := map[string]string{}
	theirOrder := make([]string, 0, len(theirs))
	for _, kv := range theirs {
		if _, seen := theirMap[kv[0]]; !seen {
			theirOrder = append(theirOrder, kv[0])
		}
		theirMap[kv[0]] = kv[1]
	}

	fmt.Println("\n=========================================================")
	fmt.Println(" 与真实浏览器对比")
	fmt.Println("=========================================================")

	fmt.Println("\n[真实浏览器的头顺序]")
	fmt.Println("  " + strings.Join(theirOrder, "\n  "))

	fmt.Println("\n[逐项对比]")
	fmt.Printf("  %-28s %-10s %s\n", "HEADER", "状态", "说明")
	fmt.Println("  " + strings.Repeat("-", 78))

	seen := map[string]bool{}
	for _, name := range theirOrder {
		seen[name] = true
		theirValue := theirMap[name]
		ourValue, have := ourMap[name]
		switch {
		case !have:
			fmt.Printf("  %-28s %-10s 浏览器有我们没有 -> %s\n", name, "缺失", truncate(redact(name, theirValue), 40))
		case valuesEquivalent(name, ourValue, theirValue):
			fmt.Printf("  %-28s %-10s\n", name, "一致")
		default:
			fmt.Printf("  %-28s %-10s 我们=%s\n", name, "不同", truncate(redact(name, ourValue), 40))
			fmt.Printf("  %-28s %-10s 浏览=%s\n", "", "", truncate(redact(name, theirValue), 40))
		}
	}
	for _, kv := range ours {
		if seen[kv[0]] {
			continue
		}
		if kv[0] == "accept-encoding" {
			// DevTools 导出时会剥掉 accept-encoding（免得 curl 拿到没法显示的
			// 压缩数据）。真实 Chrome 一定会发，这不是真实差异。
			fmt.Printf("  %-28s %-10s DevTools 导出会剥掉此头，非真实差异，保留我们的值\n", kv[0], "工具假象")
			continue
		}
		fmt.Printf("  %-28s %-10s 我们有浏览器没有 -> %s\n", kv[0], "多余", truncate(redact(kv[0], kv[1]), 40))
	}

	fmt.Println("\n[顺序对比] 只看两边都有的头：")
	var ourSeq, theirSeq []string
	for _, kv := range ours {
		if seen[kv[0]] {
			ourSeq = append(ourSeq, kv[0])
		}
	}
	for _, name := range theirOrder {
		if _, have := ourMap[name]; have {
			theirSeq = append(theirSeq, name)
		}
	}
	switch {
	case isSorted(theirOrder):
		// DevTools 的 "Copy as cURL" 按字母序输出请求头，不保留 HTTP/2 帧里的
		// 真实顺序。真实 Chrome 的头顺序从来不是字母序，照着改等于把自己伪装成
		// 一个不存在的浏览器。
		fmt.Println("  !! 浏览器侧是完整字母序，说明这是 DevTools 的显示排序而非线上真实顺序。")
		fmt.Println("     本次顺序对比无效，不要据此修改 chromeHeaderOrder。")
		fmt.Println("     要拿顺序的 ground truth，需要用 mitmproxy / Wireshark 看原始 HEADERS 帧。")
	case strings.Join(ourSeq, ",") == strings.Join(theirSeq, ","):
		fmt.Println("  顺序完全一致")
	default:
		fmt.Println("  我们  : " + strings.Join(ourSeq, ", "))
		fmt.Println("  浏览器: " + strings.Join(theirSeq, ", "))
		fmt.Println("  -> 若确认来源保留了真实顺序，把 transport/chrome.go 的 chromeHeaderOrder 改成浏览器顺序")
	}
	return nil
}

// isSorted 判断请求头是否呈完整字母序——那是抓包工具排序过的标志。
func isSorted(names []string) bool {
	if len(names) < 4 {
		return false // 太短，可能是巧合
	}
	return sort.StringsAreSorted(names)
}

// valuesEquivalent ignores differences that are expected to differ per request.
func valuesEquivalent(name, ours, theirs string) bool {
	switch name {
	case "authorization", "x-ds-pow-response", "cookie", "content-length", "x-client-timezone-offset":
		return true // per-request or per-account, not a fingerprint constant
	case "referer":
		// Now derived from the chat session id, so it legitimately differs
		// from whatever conversation the capture came from.
		return true
	}
	return ours == theirs
}

// ----------------------------------------------------------------- live probe

func probeLive(ctx context.Context, email, mobile, password, locale string) {
	fmt.Println("\n=========================================================")
	fmt.Println(" 线上探测（走本项目真实 transport）")
	fmt.Println("=========================================================")

	client := trans.New(60 * time.Second)

	loginPayload := map[string]any{
		"email": "", "mobile": "", "password": password, "area_code": "",
		"device_id": "wire-capture-probe", "os": "web",
	}
	if email != "" {
		loginPayload["email"] = email
	} else {
		loginPayload["mobile"] = mobile
		loginPayload["area_code"] = "+86"
	}

	token := ""
	if body, resp := post(ctx, client, dsprotocol.DeepSeekLoginURL, dsprotocol.LoginHeaders(locale), loginPayload); resp != nil {
		reportResponse("login", resp, body)
		token = extractToken(body)
		if token == "" {
			fmt.Println("  !! 未取到 token，后续步骤跳过")
			return
		}
	} else {
		return
	}

	headers := dsprotocol.BaseHeadersFor(locale)
	headers["authorization"] = "Bearer " + token

	if body, resp := post(ctx, client, dsprotocol.DeepSeekCreateSessionURL, headers, map[string]any{}); resp != nil {
		reportResponse("create_session", resp, body)
	}
	if body, resp := post(ctx, client, dsprotocol.DeepSeekCreatePowURL, headers,
		map[string]any{"target_path": dsprotocol.DeepSeekCompletionTargetPath}); resp != nil {
		reportResponse("create_pow_challenge", resp, body)
	}

	fmt.Println("\n结论要点：")
	fmt.Println("  - 若上面出现 Set-Cookie，说明 cookie jar 确有内容可回放")
	fmt.Println("  - 若出现 Content-Encoding: br/zstd，说明新的解压路径确实被用上了")
	fmt.Println("  - 若 biz_code 非 0 或出现 captcha 字样，说明该账号已被风控盯上")
}

func post(ctx context.Context, client trans.Doer, url string, headers map[string]string, payload any) ([]byte, *http.Response) {
	raw, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("  marshal 失败: %v\n", err)
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		fmt.Printf("  构造请求失败: %v\n", err)
		return nil, nil
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("  请求失败: %v\n", err)
		return nil, nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return body, resp
}

func reportResponse(step string, resp *http.Response, body []byte) {
	fmt.Printf("\n[%s] HTTP %d\n", step, resp.StatusCode)
	if enc := resp.Header.Get("Content-Encoding"); enc != "" {
		fmt.Printf("  Content-Encoding : %s  (注意：解压后本应已清空，出现说明未走解压层)\n", enc)
	} else {
		fmt.Println("  Content-Encoding : <已解压或未压缩>")
	}
	if cookies := resp.Cookies(); len(cookies) > 0 {
		names := make([]string, 0, len(cookies))
		for _, c := range cookies {
			if showSecrets {
				names = append(names, c.Name+"="+c.Value)
			} else {
				names = append(names, c.Name)
			}
		}
		fmt.Printf("  Set-Cookie       : %s\n", strings.Join(names, ", "))
	} else {
		fmt.Println("  Set-Cookie       : <无>")
	}
	fmt.Printf("  body             : %s\n", truncate(strings.TrimSpace(string(body)), 300))
}

func extractToken(body []byte) string {
	var parsed struct {
		Data struct {
			BizData struct {
				User struct {
					Token string `json:"token"`
				} `json:"user"`
			} `json:"biz_data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	return parsed.Data.BizData.User.Token
}

// --------------------------------------------------------------------- shared

var secretHeaders = map[string]bool{
	"authorization":     true,
	"cookie":            true,
	"set-cookie":        true,
	"x-ds-pow-response": true,
}

func redact(name, value string) string {
	if showSecrets || !secretHeaders[strings.ToLower(name)] {
		return value
	}
	if value == "" {
		return ""
	}
	return fmt.Sprintf("<已脱敏 %d 字节>", len(value))
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
