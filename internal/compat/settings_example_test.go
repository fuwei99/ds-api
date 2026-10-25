package compat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"ds2api/internal/config"
)

// config.example.json 是用户复制起步的样例，也是「有哪些设置」的唯一离线文档。
// 它不在注册表里派生，所以会漂移：新加一个注册设置而忘了写进样例，用户就看不到
// 它；删掉一个字段而样例还留着，用户复制的就是被静默丢弃的配置。
//
// 本测试把样例钉在注册表上：
//   - 每个注册设置（含 Hidden）都要在样例里出现；
//   - 设置段里的字段集合与声明完全一致，不多不少；
//   - bool / int 字段的取值等于声明默认值——样例是给用户抄的，抄到的数字或开关
//     必须就是代码里的默认，否则用户以为改了默认值。
//
// 字符串字段只检查类型：样例可以给一个有说明意义的值（如 embeddings.provider
// 写成 "deterministic" 来展示可用取值），而字符串的默认值往往是空串。
func TestConfigExampleMatchesRegisteredSettings(t *testing.T) {
	root := filepath.Join("..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "config.example.json"))
	if err != nil {
		t.Fatalf("read config.example.json: %v", err)
	}
	var example map[string]json.RawMessage
	if err := json.Unmarshal(raw, &example); err != nil {
		t.Fatalf("config.example.json is not a JSON object: %v", err)
	}

	for _, spec := range config.SettingSpecs() {
		sectionRaw, ok := example[spec.Key]
		if !ok {
			t.Errorf("config.example.json has no %q section; add it so users can discover the setting", spec.Key)
			continue
		}
		var section map[string]json.RawMessage
		if err := json.Unmarshal(sectionRaw, &section); err != nil {
			t.Errorf("config.example.json section %q is not an object: %v", spec.Key, err)
			continue
		}

		declared := map[string]config.FieldSpec{}
		for _, field := range spec.Fields {
			declared[field.Name] = field
		}

		for _, field := range spec.Fields {
			valueRaw, ok := section[field.Name]
			if !ok {
				t.Errorf("config.example.json section %q omits field %q", spec.Key, field.Name)
				continue
			}
			checkExampleFieldValue(t, spec.Key, field, valueRaw)
		}
		for name := range section {
			if _, ok := declared[name]; !ok {
				t.Errorf("config.example.json section %q carries undeclared field %q; the codec drops it on load, so the example documents a setting that does nothing", spec.Key, name)
			}
		}
	}
}

func checkExampleFieldValue(t *testing.T, key string, field config.FieldSpec, valueRaw json.RawMessage) {
	t.Helper()
	switch field.Kind {
	case config.FieldBool:
		var got bool
		if err := json.Unmarshal(valueRaw, &got); err != nil {
			t.Errorf("config.example.json %s.%s is not a bool: %v", key, field.Name, err)
			return
		}
		if want, _ := field.Default.(bool); got != want {
			t.Errorf("config.example.json %s.%s = %v, want the declared default %v", key, field.Name, got, want)
		}
	case config.FieldInt:
		var got float64
		if err := json.Unmarshal(valueRaw, &got); err != nil {
			t.Errorf("config.example.json %s.%s is not a number: %v", key, field.Name, err)
			return
		}
		if want, ok := field.Default.(int); ok && int(got) != want {
			t.Errorf("config.example.json %s.%s = %v, want the declared default %d", key, field.Name, got, want)
		}
	case config.FieldString:
		var got string
		if err := json.Unmarshal(valueRaw, &got); err != nil {
			t.Errorf("config.example.json %s.%s is not a string: %v", key, field.Name, err)
		}
	}
}

// 样例里每个形如设置的顶层段都必须真的注册过。没注册的段会在加载时被当作未知
// 字段丢掉（见 internal/config/codec.go 的 reservedConfigKeys 之外的分支），
// 于是样例教用户写一个不生效的配置。
func TestConfigExampleHasNoUnregisteredSettingSections(t *testing.T) {
	root := filepath.Join("..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "config.example.json"))
	if err != nil {
		t.Fatalf("read config.example.json: %v", err)
	}
	var example map[string]json.RawMessage
	if err := json.Unmarshal(raw, &example); err != nil {
		t.Fatalf("config.example.json is not a JSON object: %v", err)
	}

	// 说明性元数据键，以及注册段（internal/config/sections.go）声明的顶层键。
	// 后者由注册表派生，所以新增一段不需要改这里——样例跟着注册表走。
	metadata := map[string]bool{"_comment": true, "_doc": true}
	sections := map[string]bool{}
	for _, key := range config.SectionKeys() {
		sections[key] = true
	}
	registered := map[string]bool{}
	for _, spec := range config.SettingSpecs() {
		registered[spec.Key] = true
	}

	var unknown []string
	for key := range example {
		if metadata[key] || sections[key] || registered[key] {
			continue
		}
		unknown = append(unknown, key)
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		t.Errorf("config.example.json documents sections that are neither registered settings nor hand-written codec sections: %s", strings.Join(unknown, ", "))
	}
}
