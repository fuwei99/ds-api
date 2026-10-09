package settings

import (
	"testing"

	"ds2api/internal/config"
)

// applySectionUpdate runs a settings update's section part exactly as the write
// handler does: the fields the request carries are applied to the config, then
// the touched sections are validated on the merged config.
func applySectionUpdate(cfg *config.Config, req map[string]any) error {
	updates := config.SectionUpdates(req)
	if err := config.ApplySections(cfg, updates); err != nil {
		return err
	}
	return config.ValidateAppliedSections(*cfg, updates)
}

func TestSectionUpdateDeviceIDMode(t *testing.T) {
	cfg := config.Config{}
	if err := applySectionUpdate(&cfg, map[string]any{
		"runtime": map[string]any{"device_id_mode": "auto"},
	}); err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.DeviceIDMode != config.DeviceIDTypeAuto {
		t.Fatalf("expected device_id_mode=auto, got %q", cfg.Runtime.DeviceIDMode)
	}

	if err := applySectionUpdate(&cfg, map[string]any{
		"runtime": map[string]any{"device_id_mode": "bogus"},
	}); err == nil {
		t.Fatal("expected error for invalid device_id_mode")
	}

	cfg = config.Config{}
	if err := applySectionUpdate(&cfg, map[string]any{
		"runtime": map[string]any{"account_max_inflight": 3},
	}); err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.DeviceIDMode != "" {
		t.Fatalf("device_id_mode must be untouched when absent, got %q", cfg.Runtime.DeviceIDMode)
	}
}

func TestSectionUpdateNormalizesDeviceIDModeCase(t *testing.T) {
	cfg := config.Config{}
	if err := applySectionUpdate(&cfg, map[string]any{
		"runtime": map[string]any{"device_id_mode": "AUTO"},
	}); err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.DeviceIDMode != config.DeviceIDTypeAuto {
		t.Fatalf("expected normalized auto, got %q", cfg.Runtime.DeviceIDMode)
	}
}

// 旧名 real 在写入侧迁移为 auto，而不是被枚举校验拒绝：仍发旧名的客户端与
// 携带旧配置的合并导入都按 auto 生效。
func TestSectionUpdateMigratesLegacyRealDeviceIDMode(t *testing.T) {
	cfg := config.Config{}
	if err := applySectionUpdate(&cfg, map[string]any{
		"runtime": map[string]any{"device_id_mode": "REAL"},
	}); err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.DeviceIDMode != config.DeviceIDTypeAuto {
		t.Fatalf("expected legacy real migrated to auto, got %q", cfg.Runtime.DeviceIDMode)
	}
}

func TestSectionUpdateAcceptsManualDeviceIDMode(t *testing.T) {
	cfg := config.Config{}
	if err := applySectionUpdate(&cfg, map[string]any{
		"runtime": map[string]any{"device_id_mode": "manual"},
	}); err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.DeviceIDMode != config.DeviceIDTypeManual {
		t.Fatalf("expected manual, got %q", cfg.Runtime.DeviceIDMode)
	}
}

func TestSectionUpdateMuteReport(t *testing.T) {
	cfg := config.Config{}
	if err := applySectionUpdate(&cfg, map[string]any{
		"runtime": map[string]any{
			"mute_report": map[string]any{"enabled": true, "url": " http://127.0.0.1:8100 "},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.MuteReport.Enabled == nil || !*cfg.Runtime.MuteReport.Enabled {
		t.Fatalf("expected mute_report.enabled=true, got %+v", cfg.Runtime.MuteReport)
	}
	if cfg.Runtime.MuteReport.URL != "http://127.0.0.1:8100" {
		t.Fatalf("expected trimmed url, got %q", cfg.Runtime.MuteReport.URL)
	}

	if err := applySectionUpdate(&cfg, map[string]any{
		"runtime": map[string]any{
			"mute_report": map[string]any{"url": "not-a-url"},
		},
	}); err == nil {
		t.Fatal("expected error for invalid mute_report.url")
	}

	cfg = config.Config{}
	if err := applySectionUpdate(&cfg, map[string]any{
		"runtime": map[string]any{"account_max_inflight": 3},
	}); err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.MuteReport.Enabled != nil || cfg.Runtime.MuteReport.URL != "" {
		t.Fatalf("mute_report must be untouched when absent, got %+v", cfg.Runtime.MuteReport)
	}
}

func TestSectionUpdateAccountRateLimit(t *testing.T) {
	cfg := config.Config{}
	if err := applySectionUpdate(&cfg, map[string]any{
		"runtime": map[string]any{
			"account_rate_limit": map[string]any{"enabled": true, "max_per_minute": 10},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.AccountRateLimit.Enabled == nil || !*cfg.Runtime.AccountRateLimit.Enabled {
		t.Fatalf("expected account_rate_limit.enabled=true, got %+v", cfg.Runtime.AccountRateLimit)
	}
	if cfg.Runtime.AccountRateLimit.MaxPerMinute != 10 {
		t.Fatalf("expected max_per_minute=10, got %d", cfg.Runtime.AccountRateLimit.MaxPerMinute)
	}

	if err := applySectionUpdate(&cfg, map[string]any{
		"runtime": map[string]any{
			"account_rate_limit": map[string]any{"max_per_minute": -1},
		},
	}); err == nil {
		t.Fatal("expected error for invalid account_rate_limit.max_per_minute")
	}
}

// A partial update must not reset the fields it does not mention — including the
// fields of a nested group.
func TestSectionUpdateLeavesUnmentionedFieldsAlone(t *testing.T) {
	cfg := config.Config{}
	cfg.Runtime.AccountMaxInflight = 4
	enabled := true
	cfg.Runtime.MuteReport.Enabled = &enabled
	cfg.Runtime.MuteReport.URL = "http://127.0.0.1:9999"

	if err := applySectionUpdate(&cfg, map[string]any{
		"runtime": map[string]any{"account_max_queue": 20},
	}); err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.AccountMaxInflight != 4 || cfg.Runtime.AccountMaxQueue != 20 {
		t.Fatalf("account limits: %+v", cfg.Runtime)
	}
	if cfg.Runtime.MuteReport.Enabled == nil || !*cfg.Runtime.MuteReport.Enabled || cfg.Runtime.MuteReport.URL != "http://127.0.0.1:9999" {
		t.Fatalf("mute_report must survive a partial update, got %+v", cfg.Runtime.MuteReport)
	}

	// A group the request does mention only applies the fields it carries.
	if err := applySectionUpdate(&cfg, map[string]any{
		"runtime": map[string]any{"mute_report": map[string]any{"enabled": false}},
	}); err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.MuteReport.Enabled == nil || *cfg.Runtime.MuteReport.Enabled {
		t.Fatalf("expected mute_report.enabled=false, got %+v", cfg.Runtime.MuteReport)
	}
	if cfg.Runtime.MuteReport.URL != "http://127.0.0.1:9999" {
		t.Fatalf("mute_report.url must be untouched, got %q", cfg.Runtime.MuteReport.URL)
	}
}
