package promptcompat

import (
	"strings"
	"testing"

	"ds2api/internal/config"
)

// 内置提示词的关联必须覆盖每一个声明了 PrefillDefault 的字段：前端会用它预填
// 输入框，漏登记就是空框。这条检查是派生的——注册表里新加一个 PrefillDefault
// 字段而忘了在 setting_default_prompts.go 登记时，这里会先失败。
func TestPrefillDefaultFieldsHaveARegisteredPrompt(t *testing.T) {
	for _, spec := range config.SettingSpecs() {
		for _, f := range spec.Fields {
			if !f.PrefillDefault {
				continue
			}
			text, ok := SettingDefaultPrompt(spec.Key, f.Name)
			if !ok {
				t.Errorf("%s.%s declares PrefillDefault but has no built-in prompt registered; the web UI would prefill an empty box", spec.Key, f.Name)
				continue
			}
			if strings.TrimSpace(text) == "" {
				t.Errorf("%s.%s has an empty built-in prompt", spec.Key, f.Name)
			}
		}
	}
}

// 登记的内置提示词必须指向真实存在的设置字段，且文本非空——否则它既不会出现在
// 接口里，也不会被任何控件读到。
func TestRegisteredDefaultPromptsPointAtRealFields(t *testing.T) {
	if len(settingDefaultPrompts) == 0 {
		t.Fatal("no built-in prompt registered; the settings API would report none")
	}
	for composite, text := range settingDefaultPrompts {
		key, field, ok := strings.Cut(composite, ".")
		if !ok {
			t.Errorf("registered default prompt key %q is not <setting>.<field>", composite)
			continue
		}
		if _, ok := config.LookupSettingField(key, field); !ok {
			t.Errorf("registered default prompt for %s, which is not a registered setting field", composite)
		}
		if strings.TrimSpace(text) == "" {
			t.Errorf("registered default prompt for %s is empty", composite)
		}
	}
}

// 没登记的字段必须回答"没有"，否则设置接口会给每个字段都挂上 default_prompt。
func TestSettingDefaultPromptReportsAbsentFields(t *testing.T) {
	if _, ok := SettingDefaultPrompt("thinking_injection", "enabled"); ok {
		t.Error("a bool field must not report a built-in prompt")
	}
	if _, ok := SettingDefaultPrompt("no_such_setting", "prompt"); ok {
		t.Error("an unknown setting must not report a built-in prompt")
	}
}
