package config

import (
	"strings"
	"testing"
)

func TestValidateConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "admin",
			cfg:  Config{Admin: AdminConfig{JWTExpireHours: 721}},
			want: "admin.jwt_expire_hours",
		},
		{
			name: "runtime relation",
			cfg: Config{Runtime: RuntimeConfig{
				AccountMaxInflight: 8,
				GlobalMaxInflight:  4,
			}},
			want: "runtime.global_max_inflight must be >= runtime.account_max_inflight",
		},
		{
			name: "responses",
			cfg:  Config{Responses: ResponsesConfig{StoreTTLSeconds: 10}},
			want: "responses.store_ttl_seconds",
		},
		{
			name: "embeddings",
			cfg:  Config{Embeddings: EmbeddingsConfig{Provider: "   "}},
			want: "embeddings.provider",
		},
		{
			name: "auto delete",
			cfg:  Config{AutoDelete: AutoDeleteConfig{Mode: "maybe"}},
			want: "auto_delete.mode",
		},
		{
			name: "input file",
			cfg:  Config{InputFile: InputFileConfig{MinChars: -1}},
			want: "input_file.min_chars",
		},
		{
			name: "mute report scheme",
			cfg:  Config{Runtime: RuntimeConfig{MuteReport: MuteReportConfig{URL: "ftp://127.0.0.1:8100"}}},
			want: "runtime.mute_report.url",
		},
		{
			name: "mute report host",
			cfg:  Config{Runtime: RuntimeConfig{MuteReport: MuteReportConfig{URL: "http://"}}},
			want: "runtime.mute_report.url",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConfig(tc.cfg)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q in error, got %v", tc.want, err)
			}
		})
	}
}

func TestValidateConfigAcceptsLegacyAutoDeleteSessions(t *testing.T) {
	if err := ValidateConfig(Config{AutoDelete: AutoDeleteConfig{Sessions: true}}); err != nil {
		t.Fatalf("expected legacy auto_delete.sessions config to remain valid, got %v", err)
	}
}

func TestValidateMuteReportConfig(t *testing.T) {
	valid := []string{
		"",
		"http://127.0.0.1:8100",
		"https://example.com/hook",
		"http://example.com:8080/path?query=1",
	}
	for _, raw := range valid {
		if err := ValidateMuteReportConfig(MuteReportConfig{URL: raw}); err != nil {
			t.Fatalf("expected %q to be valid, got %v", raw, err)
		}
	}
	invalid := []string{
		"127.0.0.1:8100",
		"ftp://example.com",
		"://bad",
		"http://",
	}
	for _, raw := range invalid {
		if err := ValidateMuteReportConfig(MuteReportConfig{URL: raw}); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
}

func TestRuntimeMuteReportAccessors(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{"keys":["k1"],"accounts":[]}`)
	store := LoadStore()
	if store.RuntimeMuteReportEnabled() {
		t.Fatal("mute report must default to disabled")
	}
	if store.RuntimeMuteReportURL() != DefaultMuteReportURL {
		t.Fatalf("expected default url %q, got %q", DefaultMuteReportURL, store.RuntimeMuteReportURL())
	}

	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["k1"],
		"accounts":[],
		"runtime":{"mute_report":{"enabled":true,"url":"http://example.com/hook"}}
	}`)
	store = LoadStore()
	if !store.RuntimeMuteReportEnabled() {
		t.Fatal("expected mute report enabled")
	}
	if store.RuntimeMuteReportURL() != "http://example.com/hook" {
		t.Fatalf("unexpected url %q", store.RuntimeMuteReportURL())
	}
}
