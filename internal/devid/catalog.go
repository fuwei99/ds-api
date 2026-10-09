package devid

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"
)

// catalog 是设备档案目录（data/devid/profiles.json）。
//
// 分布本身——有哪些 OS 画像、语言/时区组合、各自权重、电池与网络怎么取值——
// 是"怎么打散同一批设备"的策略，属于扩展包内容；仓库里只有这份解释代码。
// 表项顺序即默认值顺序：档案缺字段时按第一条兜底。
type catalog struct {
	Version    int             `json:"version"`
	OSFamilies []catalogOS     `json:"osFamilies"`
	Locales    []catalogLocale `json:"locales"`
	Battery    struct {
		ChargingProbability float64   `json:"chargingProbability"`
		Levels              []float64 `json:"levels"`
	} `json:"battery"`
	Network struct {
		RTT           []int     `json:"rtt"`
		Downlink      []float64 `json:"downlink"`
		EffectiveType string    `json:"effectiveType"`
	} `json:"network"`
	Memory struct {
		Buckets        []int `json:"buckets"`
		LowCoreBuckets []int `json:"lowCoreBuckets"`
		LowCoreMax     int   `json:"lowCoreMax"`
	} `json:"memory"`
	Defaults struct {
		ColorDepth   int `json:"colorDepth"`
		DeviceMemory int `json:"deviceMemory"`
	} `json:"defaults"`
}

type catalogOS struct {
	Key        string    `json:"key"`
	OS         string    `json:"os"`
	Platform   string    `json:"platform"`
	UAPlatform string    `json:"uaPlatform"`
	UA         string    `json:"ua"` // 含 %s 版本占位
	Screens    [][2]int  `json:"screens"`
	Dprs       []float64 `json:"dprs"`
	Cores      []int     `json:"cores"`
	Taskbar    []int     `json:"taskbar"`
	Weight     int       `json:"weight"`
}

type catalogLocale struct {
	Code           string   `json:"code"`
	Langs          []string `json:"langs"`
	Tz             string   `json:"tz"`
	TzOffset       int      `json:"tzOffset"` // JS getTimezoneOffset 语义，仅作档案记录
	AcceptLanguage string   `json:"acceptLanguage"`
	Weight         int      `json:"weight"`
}

func loadCatalog(path string) (*catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cat := &catalog{}
	if err := json.Unmarshal(data, cat); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := cat.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cat, nil
}

func (c *catalog) validate() error {
	if c == nil || len(c.OSFamilies) == 0 {
		return errors.New("no osFamilies")
	}
	if len(c.Locales) == 0 {
		return errors.New("no locales")
	}
	for i, fam := range c.OSFamilies {
		if strings.TrimSpace(fam.Key) == "" || strings.TrimSpace(fam.UA) == "" {
			return fmt.Errorf("osFamilies[%d]: key/ua required", i)
		}
		if len(fam.Screens) == 0 || len(fam.Dprs) == 0 || len(fam.Cores) == 0 {
			return fmt.Errorf("osFamilies[%d]: screens/dprs/cores required", i)
		}
	}
	for i, loc := range c.Locales {
		if strings.TrimSpace(loc.Code) == "" || strings.TrimSpace(loc.Tz) == "" {
			return fmt.Errorf("locales[%d]: code/tz required", i)
		}
	}
	return nil
}

// pickWeighted 按权重挑选，权重非正时退回第一项。
func pickWeighted[T any](rng *rand.Rand, items []T, weight func(T) int) T {
	total := 0
	for _, it := range items {
		if w := weight(it); w > 0 {
			total += w
		}
	}
	if total <= 0 {
		return items[0]
	}
	r := rng.Intn(total)
	for _, it := range items {
		r -= weight(it)
		if r < 0 {
			return it
		}
	}
	return items[len(items)-1]
}

func pick[T any](rng *rand.Rand, arr []T) T {
	return arr[rng.Intn(len(arr))]
}

func (c *catalog) pickOS(rng *rand.Rand) catalogOS {
	return pickWeighted(rng, c.OSFamilies, func(f catalogOS) int { return f.Weight })
}

func (c *catalog) pickLocale(rng *rand.Rand) catalogLocale {
	return pickWeighted(rng, c.Locales, func(l catalogLocale) int { return l.Weight })
}

func (c *catalog) localeByCode(code string) (catalogLocale, bool) {
	code = strings.TrimSpace(code)
	for _, loc := range c.Locales {
		if loc.Code == code {
			return loc, true
		}
	}
	return catalogLocale{}, false
}

// localeByCodeAndTz 先按 code+tz 精确匹配（同一语言代码可以有多条不同时区的条目，
// 环境脚本也是这么找的），找不到再退回只按 code。
func (c *catalog) localeByCodeAndTz(code, tz string) (catalogLocale, bool) {
	code, tz = strings.TrimSpace(code), strings.TrimSpace(tz)
	if tz != "" {
		for _, loc := range c.Locales {
			if loc.Code == code && loc.Tz == tz {
				return loc, true
			}
		}
	}
	return c.localeByCode(code)
}

func (c *catalog) osFamilyByKey(key string) (catalogOS, bool) {
	key = strings.TrimSpace(key)
	for _, fam := range c.OSFamilies {
		if fam.Key == key {
			return fam, true
		}
	}
	return catalogOS{}, false
}

// newProfile 按目录挑一份自洽的设备画像建档案。
func (c *catalog) newProfile(rng *rand.Rand) *Profile {
	fam := c.pickOS(rng)
	loc := c.pickLocale(rng)
	scr := pick(rng, fam.Screens)
	w, h := scr[0], scr[1]
	taskbar := 0
	if len(fam.Taskbar) > 0 {
		taskbar = pick(rng, fam.Taskbar)
	}
	cores := pick(rng, fam.Cores)
	profile := &Profile{
		Seed:    rng.Uint32(),
		ScreenW: w, ScreenH: h, AvailW: w, AvailH: h - taskbar,
		Dpr: pick(rng, fam.Dprs), ColorDepth: c.colorDepth(),
		InnerW: w, InnerH: h - taskbar, OuterH: h,
		Cores:        cores,
		DeviceMemory: c.deviceMemory(cores),
		Rtt:          pick(rng, c.Network.RTT),
		Downlink:     pick(rng, c.Network.Downlink),
		EffType:      c.Network.EffectiveType,
		Battery: Battery{
			Charging: rng.Float64() < c.Battery.ChargingProbability,
			Level:    pick(rng, c.Battery.Levels),
		},
		CanvasSeed:     rng.Uint32(),
		OSFamily:       fam.Key,
		Locale:         loc.Code,
		Langs:          append([]string(nil), loc.Langs...),
		TzName:         loc.Tz,
		TzOffset:       loc.TzOffset,
		Lang:           LangPref{Code: loc.Code, List: append([]string(nil), loc.Langs...)},
		AcceptLanguage: loc.AcceptLanguage,
	}
	if off, ok := TzOffsetMinutes(loc.Tz); ok {
		profile.TzOffset = off
	}
	return profile
}

func (c *catalog) colorDepth() int {
	if c.Defaults.ColorDepth > 0 {
		return c.Defaults.ColorDepth
	}
	return 24
}

func (c *catalog) deviceMemory(cores int) int {
	buckets := c.Memory.Buckets
	if c.Memory.LowCoreMax > 0 && cores <= c.Memory.LowCoreMax && len(c.Memory.LowCoreBuckets) > 0 {
		buckets = c.Memory.LowCoreBuckets
	}
	if len(buckets) == 0 {
		if c.Defaults.DeviceMemory > 0 {
			return c.Defaults.DeviceMemory
		}
		return 8
	}
	return buckets[0]
}

// normalizeWith 用目录补齐档案缺字段，让 v1 档案（无画像字段）也能生成出一致的设备。
// 只填空缺、不覆盖已有值：档案是可复现设备的唯一依据。
func (c *catalog) normalizeWith(p *Profile) {
	if c == nil || p == nil {
		return
	}
	// 目录里没有的 OS 画像一律换成目录第一条：画像决定了 UA/platform，留着目录外的
	// 值等于让指纹与传输层（预设、登录 UA）各说一套。
	if _, ok := c.osFamilyByKey(p.OSFamily); !ok {
		p.OSFamily = c.OSFamilies[0].Key
	}
	if _, ok := c.localeByCode(p.Locale); !ok || strings.TrimSpace(p.Locale) == "" {
		// v1 档案只有 lang 一个字段，别把它丢掉换成默认语言：
		// 语言决定时区与 Accept-Language，换掉等于换了台设备。
		if code := strings.TrimSpace(p.Lang.Code); code != "" {
			if _, ok := c.localeByCode(code); ok {
				p.Locale = code
			}
		}
	}
	fallback := c.Locales[0]
	if loc, ok := c.localeByCodeAndTz(p.Locale, p.TzName); ok {
		fallback = loc
	} else {
		p.Locale = fallback.Code
	}
	if len(p.Langs) == 0 {
		p.Langs = append([]string(nil), fallback.Langs...)
	}
	if strings.TrimSpace(p.Lang.Code) == "" {
		p.Lang.Code = p.Locale
	}
	if len(p.Lang.List) == 0 {
		p.Lang.List = append([]string(nil), p.Langs...)
	}
	if strings.TrimSpace(p.TzName) == "" {
		p.TzName = fallback.Tz
	}
	if strings.TrimSpace(p.AcceptLanguage) == "" {
		p.AcceptLanguage = fallback.AcceptLanguage
	}
	if p.DeviceMemory <= 0 {
		p.DeviceMemory = c.deviceMemory(p.Cores)
	}
	if p.ColorDepth <= 0 {
		p.ColorDepth = c.colorDepth()
	}
	if off, ok := TzOffsetMinutes(p.TzName); ok {
		p.TzOffset = off
	} else if p.TzOffset == 0 && strings.TrimSpace(p.TzName) != "UTC" {
		p.TzOffset = fallback.TzOffset
	}
}

// TzOffsetMinutes 返回 IANA 时区当前的 UTC 偏移，取 JS getTimezoneOffset 的
// 语义（UTC 以西为正）。按名字实时换算而不是写死表值：带夏令时的地区一年里
// 有半年是另一个偏移，写死的值有半年是错的。
func TzOffsetMinutes(name string) (int, bool) {
	loc, err := time.LoadLocation(strings.TrimSpace(name))
	if err != nil {
		return 0, false
	}
	_, offSec := time.Now().In(loc).Zone()
	return -offSec / 60, true
}
