package devid

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
)

type Battery struct {
	Charging bool    `json:"charging"`
	Level    float64 `json:"level"`
}

type Profile struct {
	Seed       uint32  `json:"seed"`
	ScreenW    int     `json:"screenW"`
	ScreenH    int     `json:"screenH"`
	AvailW     int     `json:"availW"`
	AvailH     int     `json:"availH"`
	Dpr        float64 `json:"dpr"`
	ColorDepth int     `json:"colorDepth"`
	InnerW     int     `json:"innerW"`
	InnerH     int     `json:"innerH"`
	OuterH     int     `json:"outerH"`
	Cores      int     `json:"cores"`
	Rtt        int     `json:"rtt"`
	Downlink   float64 `json:"downlink"`
	EffType    string  `json:"effType"`
	Battery    Battery `json:"battery"`
	CanvasSeed uint32  `json:"canvasSeed"`
	Lang       string  `json:"lang"`
}

func pick[T any](rng *rand.Rand, arr []T) T {
	return arr[rng.Intn(len(arr))]
}

func randUint32(rng *rand.Rand) uint32 {
	return rng.Uint32()
}

// NewRandomProfile 生成一份全新的随机设备档案。
func NewRandomProfile(rng *rand.Rand) *Profile {
	type res struct {
		w, h int
		dpr  float64
	}
	r := pick(rng, []res{
		{1920, 1080, 1}, {1920, 1080, 1}, {1920, 1080, 1},
		{1536, 864, 1.25}, {2560, 1440, 1}, {1366, 768, 1},
		{1600, 900, 1}, {1440, 900, 1.5}, {1280, 720, 1}, {1680, 1050, 1},
	})
	w, h := r.w, r.h
	availH := h - pick(rng, []int{40, 40, 48, 60})
	var lang string
	switch pick(rng, []int{0, 1, 2}) {
	case 2:
		lang = "en-US"
	default:
		lang = "zh-CN"
	}
	return &Profile{
		Seed:    randUint32(rng),
		ScreenW: w, ScreenH: h, AvailW: w, AvailH: availH,
		Dpr: r.dpr, ColorDepth: 24,
		InnerW: w, InnerH: availH, OuterH: h,
		Cores:      pick(rng, []int{4, 8, 8, 12, 16, 16, 20, 24}),
		Rtt:        pick(rng, []int{0, 25, 25, 50, 50, 100}),
		Downlink:   pick(rng, []float64{2.3, 4.6, 5.2, 7.8, 10, 10, 11.5}),
		EffType:    "4g",
		Battery:    Battery{Charging: rng.Float64() < 0.75, Level: pick(rng, []float64{1, 1, 1, 0.87, 0.72, 0.55, 0.31})},
		CanvasSeed: randUint32(rng),
		Lang:       lang,
	}
}

// LoadProfile 从文件读取设备档案。
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

// loadOrMakeProfile 复用已有档案；不存在则新建并在给出路径时持久化。
func loadOrMakeProfile(path string, rng *rand.Rand) (*Profile, error) {
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			return LoadProfile(path)
		}
	}
	profile := NewRandomProfile(rng)
	if path != "" {
		if err := SaveProfile(path, profile); err != nil {
			return nil, fmt.Errorf("save profile: %w", err)
		}
	}
	return profile, nil
}
