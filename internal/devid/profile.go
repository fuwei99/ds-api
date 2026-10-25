package devid

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
)

// Battery 电池状态。数美指纹会调用 getBattery()，桌面设备绝大多数时间在充电。
type Battery struct {
	Charging bool    `json:"charging"`
	Level    float64 `json:"level"`
}

// LangPref 语言偏好：Code 是 UI 语言（navigator.language），List 是
// navigator.languages（真实浏览器 UI 语言排第一）。
type LangPref struct {
	Code string   `json:"code"`
	List []string `json:"list"`
}

// UnmarshalJSON 兼容 v1 档案里 `"lang": "zh-CN"` 的写法。旧档案不能因为格式
// 升级就被当成损坏档案重建——重建等于给账号换了一台设备。
func (l *LangPref) UnmarshalJSON(data []byte) error {
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '"' {
		var code string
		if err := json.Unmarshal(trimmed, &code); err != nil {
			return err
		}
		*l = LangPref{Code: strings.TrimSpace(code)}
		return nil
	}
	type plain LangPref
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*l = LangPref(v)
	return nil
}

// Profile 是一份设备档案：同一份档案必须稳定算出同一台"设备"。
//
// 字段与 data/devid/browser_env.js 读取的 profile 一一对应；具体取值由扩展包的
// 目录（profiles.json）决定，这里只是数据模型与读写。
type Profile struct {
	Seed         uint32   `json:"seed"`
	ScreenW      int      `json:"screenW"`
	ScreenH      int      `json:"screenH"`
	AvailW       int      `json:"availW"`
	AvailH       int      `json:"availH"`
	Dpr          float64  `json:"dpr"`
	ColorDepth   int      `json:"colorDepth"`
	InnerW       int      `json:"innerW"`
	InnerH       int      `json:"innerH"`
	OuterH       int      `json:"outerH"`
	Cores        int      `json:"cores"`
	DeviceMemory int      `json:"deviceMemory"`
	Rtt          int      `json:"rtt"`
	Downlink     float64  `json:"downlink"`
	EffType      string   `json:"effType"`
	Battery      Battery  `json:"battery"`
	CanvasSeed   uint32   `json:"canvasSeed"`
	Lang         LangPref `json:"lang"`
	OSFamily     string   `json:"osFamily"`
	Locale       string   `json:"locale"`
	Langs        []string `json:"langs"`
	TzName       string   `json:"tzName"`
	TzOffset     int      `json:"tzOffset"`
	// AcceptLanguage 是上报请求的 Accept-Language，必须与 Langs 同源：
	// 请求头声称的语言和指纹里的 navigator.languages 不一致是白送的破绽。
	AcceptLanguage string `json:"acceptLanguage"`
	// SmidV2 是 SDK 首次运行生成的本地设备标识。真实浏览器二次访问会从
	// localStorage 复用它，因此必须落回档案：否则每次生成都等于一台全新的设备。
	SmidV2 string `json:"smidV2,omitempty"`
}

// LoadProfile 从文件读取设备档案。只做解析，画像补齐由目录在生成路径上完成
// （normalizeWith）——没有目录时也要能读出档案。
func LoadProfile(path string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	profile := &Profile{}
	if err := json.Unmarshal(data, profile); err != nil {
		return nil, fmt.Errorf("parse profile %s: %w", path, err)
	}
	if profile.ScreenW <= 0 || profile.ScreenH <= 0 {
		return nil, errors.New("profile missing screen dimensions")
	}
	return profile, nil
}

// SaveProfile 将设备档案写入文件。
func SaveProfile(path string, profile *Profile) error {
	out, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// RemoveProfile 删除设备档案文件；文件不存在时静默返回。
// 删除后下一次生成会重建一份全新的随机档案，数美据此换算出新的 device_id。
func RemoveProfile(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// loadOrMakeProfile 复用已有档案；不存在则按目录新建一份并在给出路径时持久化。
// 新建需要目录（profiles.json）：没有它就没有画像策略可用，如实报错。
func loadOrMakeProfile(cat *catalog, path string, rng *rand.Rand) (*Profile, error) {
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			return LoadProfile(path)
		}
	}
	if cat == nil {
		return nil, errors.New("devid: profile catalog is not loaded")
	}
	profile := cat.newProfile(rng)
	if path != "" {
		if err := SaveProfile(path, profile); err != nil {
			return nil, fmt.Errorf("save profile: %w", err)
		}
	}
	return profile, nil
}
