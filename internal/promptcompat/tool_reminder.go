package promptcompat

import (
	"strings"

	"ds2api/internal/config"
	"ds2api/internal/toolcall"
)

// DefaultBottomFormatInjectionPrompt 是「底部格式提示注入」的内置提示词，即
// 末尾工具调用格式复述（trailing reminder）的原文。它按规范形态 EPSE 渲染，
// 出站时由 toolcall.ApplyToolMarker 在最终 prompt 字符串上统一替换成调用者
// 专属标识，因此这里不需要感知 marker。
var DefaultBottomFormatInjectionPrompt = toolcall.ToolCallFormatReminder()

// ToolReminderConfig 是「底部格式提示注入」行为设置在请求期的解析结果。
type ToolReminderConfig struct {
	// Enabled 为 false 时完全不注入末尾的格式提醒。
	Enabled bool
	// Prompt 非空时覆盖内置提醒文本；为空则回退到内置提醒。
	Prompt string
}

// DefaultToolReminderConfig 返回内置行为：注入内置提醒文本。未接线的调用方
// （例如只关心路由的轻量 ConfigReader 桩）会退化为该行为，与历史版本一致。
func DefaultToolReminderConfig() ToolReminderConfig {
	return ToolReminderConfig{Enabled: true}
}

// ReminderText 解析出本次请求要注入的提醒文本；返回空字符串表示不注入。
func (c ToolReminderConfig) ReminderText() string {
	if !c.Enabled {
		return ""
	}
	if prompt := strings.TrimSpace(c.Prompt); prompt != "" {
		return prompt
	}
	return DefaultBottomFormatInjectionPrompt
}

// ToolReminderReader 由配置存储实现，提供「底部格式提示注入」设置。它以窄接口
// 的形式在请求路径上按需断言：真实实现只有 config.Store，而各协议包里的轻量
// ConfigReader 桩无需实现它（未实现即退化为 DefaultToolReminderConfig）。
type ToolReminderReader interface {
	BottomFormatInjectionEnabled() bool
	BottomFormatInjectionPrompt() string
}

// ResolveToolReminder 从配置存储解析本次请求的提醒设置。store 未实现
// ToolReminderReader 时退化为内置行为（开启 + 内置提醒）。
func ResolveToolReminder(store any) ToolReminderConfig {
	reader, ok := store.(ToolReminderReader)
	if !ok {
		return DefaultToolReminderConfig()
	}
	return ToolReminderConfig{
		Enabled: reader.BottomFormatInjectionEnabled(),
		Prompt:  reader.BottomFormatInjectionPrompt(),
	}
}

// 真实配置存储必须实现该能力，否则请求路径会静默退化为「始终注入内置提醒」。
var _ ToolReminderReader = (*config.Store)(nil)
