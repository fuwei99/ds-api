package compat

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// exitPackages 返回 internal/httpapi 下的协议出口包，键为相对 internal/httpapi
// 的包目录，值为该包内每个非测试 Go 文件的源码。
//
// 出口的判定信号是「记录本次请求是否携带工具定义」（调用 auth.WithToolsPresent），
// 这是每个出口都必须做的一步，见 internal/compat/caller_flag_action_test.go。集合
// 由源码派生而不是手写清单，因此新增一个出口时会自动进入集合：所有「出口必须经由
// 共享步骤」的检查都会立刻把它算进去，不需要谁记得去更新一份列表。
//
// 之所以按包而不是按文件聚合：出口与共享步骤的接线位置不固定——Claude 与 Gemini
// 在出口文件里直接调用，OpenAI Chat 与 Responses 放在同包的处理步骤文件里，Vercel
// 路径也在同一个包内。包是稳定的单位，文件不是。
func exitPackages(t *testing.T) map[string]map[string]string {
	t.Helper()
	root := filepath.Join("..", "httpapi")

	// 第一趟：找出记录工具存在性的包。
	isExit := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
		if !strings.Contains(string(raw), "auth.WithToolsPresent(") {
			return nil
		}
		dir, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		isExit[filepath.ToSlash(dir)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s failed: %v", root, err)
	}
	if len(isExit) == 0 {
		t.Fatal("no protocol exit package found; the derivation signal auth.WithToolsPresent( may have been renamed")
	}

	// 第二趟：把出口包的每个非测试文件都收进来。接线位置在包内不固定，只看命中
	// 标记的那个文件会漏掉同包的处理步骤文件。
	packages := map[string]map[string]string{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		dir, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		dir = filepath.ToSlash(dir)
		if !isExit[dir] {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if packages[dir] == nil {
			packages[dir] = map[string]string{}
		}
		packages[dir][filepath.Base(path)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s failed: %v", root, err)
	}
	return packages
}

// exitPackageContains 报告出口包里是否有任意一个非测试文件包含 needle。
func exitPackageContains(files map[string]string, needle string) bool {
	for _, src := range files {
		if strings.Contains(src, needle) {
			return true
		}
	}
	return false
}

// exitPackageFileNames 返回出口包内所有非测试文件名，便于报错时指明位置。
func exitPackageFileNames(files map[string]string) []string {
	out := make([]string, 0, len(files))
	for name := range files {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
