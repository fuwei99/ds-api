package devid

import (
	"math/rand"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

var testSeedCounter atomic.Uint64

// newTestRNG 每次返回互相独立的随机源：测试依赖两次调用得到不同的种子，
// 而 time.Now() 在 Windows 上粒度约 0.5ms，连续两次调用会取到同一个种子，
// 于是生成出两份完全相同的档案，让断言假失败。
func newTestRNG() *rand.Rand {
	return rand.New(rand.NewSource(int64(testSeedCounter.Add(1))))
}

// loadTestCatalog 读扩展包的画像目录；资产不入库，缺文件时跳过。
func loadTestCatalog(t *testing.T) *catalog {
	t.Helper()
	cat, err := loadCatalog(filepath.Join(findAssetDir(t), catalogFile))
	if err != nil {
		t.Skipf("profile catalog not available: %v", err)
	}
	return cat
}

func TestRemoveProfile(t *testing.T) {
	cat := loadTestCatalog(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "p", "profile.json")
	if _, err := loadOrMakeProfile(cat, path, newTestRNG()); err != nil {
		t.Fatal(err)
	}
	if err := RemoveProfile(path); err != nil {
		t.Fatalf("remove profile: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("profile should be gone, stat err=%v", err)
	}
	// 文件不存在时静默返回，空路径不报错。
	if err := RemoveProfile(path); err != nil {
		t.Fatalf("removing a missing profile must be silent, got %v", err)
	}
	if err := RemoveProfile(""); err != nil {
		t.Fatalf("empty path must be a no-op, got %v", err)
	}
}

// 删除档案后必须得到一份全新的档案：否则数美会按同一档案算出同一个 device_id。
func TestRemoveProfileRegeneratesFingerprint(t *testing.T) {
	cat := loadTestCatalog(t)
	path := filepath.Join(t.TempDir(), "profile.json")
	first, err := loadOrMakeProfile(cat, path, newTestRNG())
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveProfile(path); err != nil {
		t.Fatal(err)
	}
	second, err := loadOrMakeProfile(cat, path, newTestRNG())
	if err != nil {
		t.Fatal(err)
	}
	if first.Seed == second.Seed && first.CanvasSeed == second.CanvasSeed {
		t.Fatalf("regenerated profile reused the old seeds: %+v", second)
	}
}

// 画像分布只在扩展包里：没有目录又需要新建档案时必须如实报错，
// 不能拿仓库里一份兜底分布顶上（那等于把策略重新写回源码）。
func TestNewProfileRequiresCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	if _, err := loadOrMakeProfile(nil, path, newTestRNG()); err == nil {
		t.Fatal("creating a profile without the catalog must fail")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("no profile should be written without a catalog, stat err=%v", err)
	}
}

// 新档案必须落在目录给出的画像里，并且靠屏幕/语言等维度打散。
// 断言一律对着目录本身写（不写死具体分布），换一份目录也能跑。
func TestCatalogProfileVariety(t *testing.T) {
	cat := loadTestCatalog(t)
	rng := newTestRNG()
	seeds := map[uint32]bool{}
	screens := map[[2]int]bool{}
	locales := map[string]bool{}
	for i := 0; i < 40; i++ {
		p := cat.newProfile(rng)
		if p.ScreenW <= 0 || p.ScreenH <= 0 || p.Cores <= 0 || p.Dpr <= 0 {
			t.Fatalf("invalid profile: %+v", p)
		}
		fam, ok := cat.osFamilyByKey(p.OSFamily)
		if !ok {
			t.Fatalf("profile OS %q is not in the catalog", p.OSFamily)
		}
		// 同一条目要按 code+tz 找：同一个语言代码可以有多条不同时区的条目。
		loc, ok := cat.localeByCodeAndTz(p.Locale, p.TzName)
		if !ok {
			t.Fatalf("profile locale %q/%q is not in the catalog", p.Locale, p.TzName)
		}
		if p.AcceptLanguage != loc.AcceptLanguage {
			t.Fatalf("locale fields must come from the catalog entry: %+v", p)
		}
		if len(p.Langs) == 0 || p.Lang.Code != p.Locale || len(p.Lang.List) == 0 || p.Lang.List[0] != p.Langs[0] {
			t.Fatalf("language fields inconsistent: %+v", p)
		}
		if _, ok := TzOffsetMinutes(p.TzName); !ok {
			t.Fatalf("profile timezone %q is not loadable", p.TzName)
		}
		if p.AvailH > p.ScreenH || p.InnerH > p.ScreenH {
			t.Fatalf("available height must not exceed the screen: %+v", p)
		}
		if p.DeviceMemory <= 0 || p.ColorDepth <= 0 {
			t.Fatalf("missing scalar fields: %+v", p)
		}
		if p.Battery.Level <= 0 || p.Battery.Level > 1 {
			t.Fatalf("battery level out of range: %+v", p.Battery)
		}
		if !containsInt(fam.Cores, p.Cores) {
			t.Fatalf("cores %d not offered by family %q", p.Cores, fam.Key)
		}
		if !containsScreen(fam.Screens, p.ScreenW, p.ScreenH) {
			t.Fatalf("screen %dx%d not offered by family %q", p.ScreenW, p.ScreenH, fam.Key)
		}
		seeds[p.Seed] = true
		screens[[2]int{p.ScreenW, p.ScreenH}] = true
		locales[p.Locale] = true
	}
	if len(seeds) < 20 {
		t.Fatalf("profile seeds not varied enough: %d unique in 40", len(seeds))
	}
	// 打散轴：目录提供多少种，40 次抽样里就该出现不止一种。
	if len(cat.OSFamilies) > 1 && len(screens) < 2 {
		t.Fatalf("screens not varied: %v", screens)
	}
	if totalScreens(cat) > 1 && len(screens) < 2 {
		t.Fatalf("screen sizes not varied: %v", screens)
	}
	if len(cat.Locales) > 1 && len(locales) < 2 {
		t.Fatalf("locales not varied: %v", locales)
	}
}

func totalScreens(cat *catalog) int {
	total := 0
	for _, fam := range cat.OSFamilies {
		total += len(fam.Screens)
	}
	return total
}

func containsInt(values []int, want int) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func containsScreen(values [][2]int, w, h int) bool {
	for _, v := range values {
		if len(v) == 2 && v[0] == w && v[1] == h {
			return true
		}
	}
	return false
}
