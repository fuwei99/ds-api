package compat

import (
	"strings"
	"testing"
)

// 每个协议出口都必须应用「思考注入」设置，且只应用一次、在「当前输入文件」步骤
// 之前。该设置是全局的：它曾经只接在 OpenAI 出口上，于是打开它对 Claude 与
// Gemini 毫无影响，用户看到的是"开关没生效"。
//
// 出口集合由 exitPackages 从源码派生（见 surface_packages_test.go），所以新增一个
// 出口不需要改这份测试：只要它记录了工具存在性，就会被要求调用共享步骤。这正是
// 原先手写清单缺的那一半——旧版本只检查清单里的四个文件，新出口静默漏过。
//
// 检查落在源码上，因为这里约束的是接线形状而不是某次请求算出的值；行为本身由各
// 出口自己的测试覆盖。
func TestEveryProtocolSurfaceAppliesThinkingInjection(t *testing.T) {
	for dir, files := range exitPackages(t) {
		if exitPackageContains(files, "promptcompat.ApplyThinkingInjection(") {
			continue
		}
		t.Errorf("exit package %s never applies the thinking-injection setting; every exit calls promptcompat.ApplyThinkingInjection once, before the current-input-file step (files: %s)",
			dir, strings.Join(exitPackageFileNames(files), ", "))
	}
}
