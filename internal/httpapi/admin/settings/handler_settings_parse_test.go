package settings

import (
	"testing"

	"ds2api/internal/config"
)

func TestParseSettingsUpdateRequestDeviceIDMode(t *testing.T) {
	req := map[string]any{
		"runtime": map[string]any{"device_id_mode": "real"},
	}
	_, runtimeCfg, _, _, _, _, _, _, _, _, _, err := parseSettingsUpdateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeCfg == nil || runtimeCfg.DeviceIDMode != config.DeviceIDTypeReal {
		t.Fatalf("expected device_id_mode=real, got %+v", runtimeCfg)
	}

	req = map[string]any{
		"runtime": map[string]any{"device_id_mode": "bogus"},
	}
	if _, _, _, _, _, _, _, _, _, _, _, err := parseSettingsUpdateRequest(req); err == nil {
		t.Fatal("expected error for invalid device_id_mode")
	}

	req = map[string]any{
		"runtime": map[string]any{"account_max_inflight": 3},
	}
	_, runtimeCfg, _, _, _, _, _, _, _, _, _, err = parseSettingsUpdateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeCfg.DeviceIDMode != "" {
		t.Fatalf("device_id_mode must be untouched when absent, got %q", runtimeCfg.DeviceIDMode)
	}
}

func TestParseSettingsUpdateRequestNormalizesDeviceIDModeCase(t *testing.T) {
	req := map[string]any{
		"runtime": map[string]any{"device_id_mode": "REAL"},
	}
	_, runtimeCfg, _, _, _, _, _, _, _, _, _, err := parseSettingsUpdateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeCfg.DeviceIDMode != config.DeviceIDTypeReal {
		t.Fatalf("expected normalized real, got %q", runtimeCfg.DeviceIDMode)
	}
}

func TestParseSettingsUpdateRequestAcceptsManualDeviceIDMode(t *testing.T) {
	req := map[string]any{
		"runtime": map[string]any{"device_id_mode": "manual"},
	}
	_, runtimeCfg, _, _, _, _, _, _, _, _, _, err := parseSettingsUpdateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeCfg.DeviceIDMode != config.DeviceIDTypeManual {
		t.Fatalf("expected manual, got %q", runtimeCfg.DeviceIDMode)
	}
}

func TestParseSettingsUpdateRequestMuteReport(t *testing.T) {
	req := map[string]any{
		"runtime": map[string]any{
			"mute_report": map[string]any{"enabled": true, "url": " http://127.0.0.1:8100 "},
		},
	}
	_, runtimeCfg, _, _, _, _, _, _, _, _, _, err := parseSettingsUpdateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeCfg == nil || runtimeCfg.MuteReport.Enabled == nil || !*runtimeCfg.MuteReport.Enabled {
		t.Fatalf("expected mute_report.enabled=true, got %+v", runtimeCfg)
	}
	if runtimeCfg.MuteReport.URL != "http://127.0.0.1:8100" {
		t.Fatalf("expected trimmed url, got %q", runtimeCfg.MuteReport.URL)
	}

	req = map[string]any{
		"runtime": map[string]any{
			"mute_report": map[string]any{"url": "not-a-url"},
		},
	}
	if _, _, _, _, _, _, _, _, _, _, _, err := parseSettingsUpdateRequest(req); err == nil {
		t.Fatal("expected error for invalid mute_report.url")
	}

	req = map[string]any{
		"runtime": map[string]any{"account_max_inflight": 3},
	}
	_, runtimeCfg, _, _, _, _, _, _, _, _, _, err = parseSettingsUpdateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeCfg.MuteReport.Enabled != nil || runtimeCfg.MuteReport.URL != "" {
		t.Fatalf("mute_report must be untouched when absent, got %+v", runtimeCfg.MuteReport)
	}
}

func TestParseSettingsUpdateRequestAccountRateLimit(t *testing.T) {
	req := map[string]any{
		"runtime": map[string]any{
			"account_rate_limit": map[string]any{"enabled": true, "max_per_minute": 10},
		},
	}
	_, runtimeCfg, _, _, _, _, _, _, _, _, _, err := parseSettingsUpdateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeCfg == nil || runtimeCfg.AccountRateLimit.Enabled == nil || !*runtimeCfg.AccountRateLimit.Enabled {
		t.Fatalf("expected account_rate_limit.enabled=true, got %+v", runtimeCfg)
	}
	if runtimeCfg.AccountRateLimit.MaxPerMinute != 10 {
		t.Fatalf("expected max_per_minute=10, got %d", runtimeCfg.AccountRateLimit.MaxPerMinute)
	}

	req = map[string]any{
		"runtime": map[string]any{
			"account_rate_limit": map[string]any{"max_per_minute": -1},
		},
	}
	if _, _, _, _, _, _, _, _, _, _, _, err := parseSettingsUpdateRequest(req); err == nil {
		t.Fatal("expected error for invalid account_rate_limit.max_per_minute")
	}
}
