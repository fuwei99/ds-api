package devid

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
)

func isBase64Rune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '+' || r == '/' || r == '='
}

// verifyFingerprint 用泄露的 priId 本地解密 payload，校验指纹内容完整性。
// 仅在 Debug 模式下执行，任何失败都只记日志不影响主流程。
func (g *Generator) verifyFingerprint(priId, dataHex string) {
	if priId == "" {
		g.logf("[devid] verify skipped: priId not leaked")
		return
	}
	ciphertext, err := hex.DecodeString(dataHex)
	if err != nil {
		g.logf("[devid] verify skipped: %v", err)
		return
	}
	block, err := aes.NewCipher([]byte(priId))
	if err != nil {
		g.logf("[devid] verify skipped: %v", err)
		return
	}
	dec := cipher.NewCBCDecrypter(block, []byte("0102030405060708"))
	pt := make([]byte, len(ciphertext))
	dec.CryptBlocks(pt, ciphertext)
	if n := len(pt); n > 0 {
		pad := int(pt[n-1])
		if pad >= 1 && pad <= 16 && pad <= n {
			ok := true
			for i := n - pad; i < n; i++ {
				if int(pt[i]) != pad {
					ok = false
					break
				}
			}
			if ok {
				pt = pt[:n-pad]
			}
		}
	}
	b64 := strings.TrimRight(string(pt), "\x00\r\n \t")
	b64 = strings.TrimRightFunc(b64, func(r rune) bool {
		return !isBase64Rune(r)
	})
	if i := strings.IndexByte(b64, 0); i >= 0 {
		b64 = b64[:i]
	}
	gzBytes, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		g.logf("[devid] verify skipped: base64: %v", err)
		return
	}
	gz, err := gzip.NewReader(bytes.NewReader(gzBytes))
	if err != nil {
		g.logf("[devid] verify skipped: gzip: %v", err)
		return
	}
	raw, err := io.ReadAll(gz)
	if err != nil {
		g.logf("[devid] verify skipped: gzip: %v", err)
		return
	}
	var fp map[string]any
	if err := json.Unmarshal(raw, &fp); err != nil {
		g.logf("[devid] verify skipped: json: %v", err)
		return
	}
	fpJSON, _ := json.Marshal(fp)
	g.logf("[devid] fingerprint: %s", string(fpJSON))
	if _, ok := fp["kb"]; !ok {
		g.logf("[devid] warning: plugins empty")
	}
	if v, ok := fp["cdp"].(float64); !ok || v != 0 {
		g.logf("[devid] warning: cdp=%v (expect 0)", fp["cdp"])
	}
	if v, ok := fp["ui"].(string); !ok || v != "MO2KwALGGzU=" {
		g.logf("[devid] warning: unexpected status cipher %v", fp["ui"])
	}
}
