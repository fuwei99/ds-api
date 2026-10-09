package compat

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// promptTextOwners lists the files allowed to produce the final prompt text for a
// request, keyed by path from the repository root. The OpenAI path is not listed
// because it normalizes inside internal/promptcompat itself; a new protocol
// surface has to be added here, which is the tripwire this list exists for.
var promptTextOwners = []string{
	"internal/httpapi/claude/standard_request.go",
	"internal/httpapi/gemini/convert_request.go",
}

// The tool prompt, the EPSE format spec and the final rendering belong to
// internal/promptcompat, so a change to them reaches every surface at once.
// A surface that spells the prompt text itself — as the Claude adapter did
// before this convergence — silently forks that text and needs its own edit for
// every prompt-affecting setting.
//
// The check is on the sources because the property is about where the text is
// built, not about a value any single request computes.
func TestPromptTextIsBuiltOnlyInPromptcompat(t *testing.T) {
	root := filepath.Join("..", "..")
	httpapi := filepath.Join(root, "internal", "httpapi")

	// Text and constructors that identify a surface-owned tool prompt. These are
	// forbidden anywhere under internal/httpapi, not only in exit packages: an
	// admin or helper path that forks the tool prompt is the same drift.
	forbidden := []string{
		"You have access to these tools",
		"Tool: %s\\nDescription: %s\\nParameters: %s",
		"toolcall.BuildToolCallInstructions(",
		"toolcall.ToolCallFormatSpec(",
		// The final rendering step: surfaces hand messages to promptcompat and
		// take the prompt back, rather than rendering the message list here.
		"prompt.MessagesPrepareWithThinking(",
	}

	err := filepath.WalkDir(httpapi, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		src := string(raw)
		for _, needle := range forbidden {
			if strings.Contains(src, needle) {
				t.Errorf("%s builds prompt text itself (%q); the tool prompt, the format spec and the final rendering belong to internal/promptcompat", rel, needle)
			}
		}
		// 反向扫描：调用 promptcompat 的 prompt 构造器却不登记为 owner 的文件，
		// 说明出口集合里多了一个没被跟踪的渲染点——新增出口时这条会先失败。
		if !containsString(promptTextOwners, rel) && strings.Contains(src, "promptcompat.BuildOpenAIPrompt(") {
			t.Errorf("%s builds a prompt through promptcompat.BuildOpenAIPrompt but is not in promptTextOwners; add it so the next prompt-affecting change knows this surface exists", rel)
		}
		if !containsString(promptTextOwners, rel) {
			return nil
		}
		if !strings.Contains(src, "promptcompat.BuildOpenAIPrompt") {
			t.Errorf("%s is a prompt-text owner but no longer calls a promptcompat prompt builder", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s failed: %v", httpapi, err)
	}

	for _, rel := range promptTextOwners {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("prompt text owner %s is missing: %v", rel, err)
		}
	}
}

// 出口包不得自行渲染消息列表。判定用 exitPackages 从源码派生（见
// surface_packages_test.go），因此新增一个出口就会自动被这条约束覆盖。
//
// 单独检查 prompt.MessagesPrepare 是因为它没有 "WithThinking" 后缀，看起来像
// 与思考无关的普通渲染；出口一旦用它，prompt 就不再来自共享路径，而
// internal/httpapi/admin 下的账号自检工具用它渲染测试消息是合理的，所以这条
// 只约束出口包。
func TestExitPackagesDoNotRenderMessagesThemselves(t *testing.T) {
	for dir, files := range exitPackages(t) {
		if !exitPackageContains(files, "prompt.MessagesPrepare(") {
			continue
		}
		t.Errorf("exit package %s renders the message list itself; hand the messages to promptcompat and take the prompt back (files: %s)",
			dir, strings.Join(exitPackageFileNames(files), ", "))
	}
}
