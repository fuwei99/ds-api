package promptcompat

import (
	"regexp"
	"strings"
)

// FileRef 表示一个 <||file:name:email:id||> 文件引用。
type FileRef struct {
	Name  string
	Email string
	ID    string
}

// UploadTagsResult 保存从消息文本中解析出的上传控制标签。
type UploadTagsResult struct {
	// ForceUpload 对应 <||file-upload:True||>，强制把最终 prompt 上传为文件。
	ForceUpload bool
	// ReturnFileID 对应 <||fileid:True||>，要求在响应末尾返回文件 ID 和账号。
	ReturnFileID bool
	// ExistingFiles 是所有 <||file:name:email:id||> 解析出的文件引用（按出现顺序去重）。
	ExistingFiles []FileRef
	// PreferredAccount 是第一个文件引用里的 email，用于锁定指定账号。
	// 优先级高于 X-Ds2-Target-Account 请求头。
	PreferredAccount string
}

var (
	// fileRefPattern 匹配 <||file:name:email:id||>。
	// name/email/id 都不含 ':' 和 '|'，避免跨标签或字段错位。
	fileRefPattern = regexp.MustCompile(`<\|\|file:([^:|]+):([^:|]+):([^:|]+)\|\|>`)
	// anyTagPattern 匹配所有 <||xxx||> 控制标签，用于清理。
	// 标签内容不含 '|'，因此 [^|]* 不会跨越标签边界。
	anyTagPattern = regexp.MustCompile(`<\|\|[^|]*\|\|>`)
)

// ParseUploadTags 解析请求消息中的 <||xxx||> 上传控制标签，并原地清理所有标签。
//
// 支持的标签：
//   - <||file:name:email:id||>  已有文件引用，解析 email（指定账号）和 id（复用）
//   - <||fileid:True||>         回复中返回文件 ID 和 account-email
//   - <||file-upload:True||>    强制上传整个 prompt
//
// 标签优先级：<||file:...:email:...||> 里的 email 高于 X-Ds2-Target-Account 请求头。
// ParseUploadTags 只负责解析和清理，账号切换由调用方（chat handler）处理。
//
// 副作用：会原地修改 req["messages"] 中每条消息的 content，清除所有 <||xxx||> 标签。
// 这必须在 NormalizeOpenAIChatRequest 之前调用，确保标签不会进入最终 prompt。
func ParseUploadTags(req map[string]any) UploadTagsResult {
	var result UploadTagsResult
	if req == nil {
		return result
	}
	messages, ok := req["messages"].([]any)
	if !ok {
		return result
	}
	seenFileIDs := map[string]struct{}{}
	for _, item := range messages {
		msg, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch c := msg["content"].(type) {
		case string:
			msg["content"] = processUploadTagsInText(c, seenFileIDs, &result)
		case []any:
			// OpenAI 多模态 content：只处理 type=text 的 part。
			for _, part := range c {
				p, ok := part.(map[string]any)
				if !ok {
					continue
				}
				if txt, ok := p["text"].(string); ok {
					p["text"] = processUploadTagsInText(txt, seenFileIDs, &result)
				}
			}
		}
	}
	for _, f := range result.ExistingFiles {
		if f.Email != "" {
			result.PreferredAccount = f.Email
			break
		}
	}
	return result
}

// processUploadTagsInText 从单段文本里提取文件引用和控制标签，并清理所有 <||xxx||> 标签。
func processUploadTagsInText(text string, seen map[string]struct{}, result *UploadTagsResult) string {
	for _, m := range fileRefPattern.FindAllStringSubmatch(text, -1) {
		name := strings.TrimSpace(m[1])
		email := strings.TrimSpace(m[2])
		id := strings.TrimSpace(m[3])
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result.ExistingFiles = append(result.ExistingFiles, FileRef{Name: name, Email: email, ID: id})
	}
	if strings.Contains(text, "<||file-upload:True||>") {
		result.ForceUpload = true
	}
	if strings.Contains(text, "<||fileid:True||>") {
		result.ReturnFileID = true
	}
	return strings.TrimSpace(anyTagPattern.ReplaceAllString(text, ""))
}

// BuildFileTag 构造响应末尾追加的 <||file:name:email:id||> 标签文本。
// 当 <||fileid:True||> 被触发且本次上传成功后，由 chat handler 拼接到响应内容末尾。
func BuildFileTag(name, email, id string) string {
	return "\n---\n<||file:" + name + ":" + email + ":" + id + "||>"
}

// AppendUniqueFileID 把 fileID 追加到 ids（去重），返回新切片。
func AppendUniqueFileID(ids []string, fileID string) []string {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return ids
	}
	for _, existing := range ids {
		if existing == fileID {
			return ids
		}
	}
	return append(ids, fileID)
}
