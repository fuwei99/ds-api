package promptcompat

import (
	"fmt"
	"strings"

	"ds2api/internal/config"
)

// settingDefaultPrompts 把「哪个设置字段的内置提示词是哪段文本」记在提示词自己
// 的包里。设置接口拿它作为 UI 元数据（占位或预填）下发给前端，让用户看到自己
// 覆盖的是什么。
//
// 关联放在这里而不是 internal/config：文本归本包所有（其中一段还是由
// internal/toolcall 现算的），而 internal/config 不能反向依赖 prompt 层。设置接口
// 逐字段来问（见 SettingDefaultPrompt），因此新增一个带内置提示词的设置只改本文件
// 一行，handler、注册表与 WebUI 都不用动。
var settingDefaultPrompts = map[string]string{}

func init() {
	registerSettingDefaultPrompt("thinking_injection", "prompt", DefaultThinkingInjectionPrompt)
	registerSettingDefaultPrompt("bottom_format_injection", "prompt", DefaultBottomFormatInjectionPrompt)
}

// registerSettingDefaultPrompt 登记一个设置字段的内置提示词。字段不存在或文本为空
// 都是编程错误：前者会让元数据挂到一个不存在的控件上，后者会让前端的预填框空掉。
func registerSettingDefaultPrompt(settingKey, fieldName, text string) {
	if _, ok := config.LookupSettingField(settingKey, fieldName); !ok {
		panic(fmt.Sprintf("promptcompat: built-in prompt for %s.%s, which is not a registered setting field", settingKey, fieldName))
	}
	if strings.TrimSpace(text) == "" {
		panic(fmt.Sprintf("promptcompat: built-in prompt for %s.%s must not be empty", settingKey, fieldName))
	}
	settingDefaultPrompts[settingKey+"."+fieldName] = text
}

// SettingDefaultPrompt 返回某个设置字段的内置提示词，没有登记时返回 false。
//
// 这是设置接口唯一需要知道的接口：它逐字段询问，因此不必认识任何具体设置名。
func SettingDefaultPrompt(settingKey, fieldName string) (string, bool) {
	text, ok := settingDefaultPrompts[settingKey+"."+fieldName]
	return text, ok
}
