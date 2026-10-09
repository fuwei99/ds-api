package config

import (
	"encoding/json"
	"testing"
)

// TestConfigMarshalKeepsMuteReport 确保仅配置 mute_report 时，runtime 段
// 仍会写入 JSON（否则保存配置会静默丢失该开关）。
func TestConfigMarshalKeepsMuteReport(t *testing.T) {
	enabled := true
	cfg := Config{
		Runtime: RuntimeConfig{
			MuteReport: MuteReportConfig{Enabled: &enabled, URL: "http://example.com/hook"},
		},
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Runtime struct {
			MuteReport MuteReportConfig `json:"mute_report"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	report := decoded.Runtime.MuteReport
	if report.Enabled == nil || !*report.Enabled {
		t.Fatalf("expected enabled flag to survive marshal, got %+v (raw=%s)", report, raw)
	}
	if report.URL != "http://example.com/hook" {
		t.Fatalf("expected url to survive marshal, got %q", report.URL)
	}
}

// TestConfigCloneDeepCopiesMuteReportEnabled 确保快照修改不会污染 Store 状态。
func TestConfigCloneDeepCopiesMuteReportEnabled(t *testing.T) {
	enabled := true
	cfg := Config{Runtime: RuntimeConfig{MuteReport: MuteReportConfig{Enabled: &enabled}}}

	clone := cfg.Clone()
	if clone.Runtime.MuteReport.Enabled == nil {
		t.Fatal("expected clone to keep enabled flag")
	}
	if clone.Runtime.MuteReport.Enabled == cfg.Runtime.MuteReport.Enabled {
		t.Fatal("expected clone to deep copy the enabled pointer")
	}
	*clone.Runtime.MuteReport.Enabled = false
	if !*cfg.Runtime.MuteReport.Enabled {
		t.Fatal("mutating the clone must not affect the original config")
	}
}
