package config

import (
	"testing"

	"github.com/codex2api/database"
)

func TestApplySystemSettingsEnvOverridesPrefersEnv(t *testing.T) {
	settings := &database.SystemSettings{
		MaxConcurrency:             2,
		GlobalRPM:                  60,
		TestModel:                  "gpt-5.4",
		TestConcurrency:            5,
		ProxyURL:                   "http://db-proxy:8080",
		AutoCleanUnauthorized:      true,
		RefreshScanIntervalSeconds: 120,
		AllowRemoteMigration:       false,
	}

	t.Setenv("MAX_CONCURRENCY", "9")
	t.Setenv("GLOBAL_RPM", "180")
	t.Setenv("TEST_MODEL", "gpt-5.5")
	t.Setenv("TEST_CONCURRENCY", "12")
	t.Setenv("CODEX_PROXY_URL", "http://env-proxy:7890")
	t.Setenv("AUTO_CLEAN_UNAUTHORIZED", "false")
	t.Setenv("REFRESH_SCAN_INTERVAL_SECONDS", "30")
	t.Setenv("ALLOW_REMOTE_MIGRATION", "true")

	applied := ApplySystemSettingsEnvOverrides(settings)

	if settings.MaxConcurrency != 9 {
		t.Fatalf("MaxConcurrency = %d, want %d", settings.MaxConcurrency, 9)
	}
	if settings.GlobalRPM != 180 {
		t.Fatalf("GlobalRPM = %d, want %d", settings.GlobalRPM, 180)
	}
	if settings.TestModel != "gpt-5.5" {
		t.Fatalf("TestModel = %q, want %q", settings.TestModel, "gpt-5.5")
	}
	if settings.TestConcurrency != 12 {
		t.Fatalf("TestConcurrency = %d, want %d", settings.TestConcurrency, 12)
	}
	if settings.ProxyURL != "http://env-proxy:7890" {
		t.Fatalf("ProxyURL = %q, want %q", settings.ProxyURL, "http://env-proxy:7890")
	}
	if settings.AutoCleanUnauthorized {
		t.Fatal("AutoCleanUnauthorized = true, want false")
	}
	if settings.RefreshScanIntervalSeconds != 30 {
		t.Fatalf("RefreshScanIntervalSeconds = %d, want %d", settings.RefreshScanIntervalSeconds, 30)
	}
	if !settings.AllowRemoteMigration {
		t.Fatal("AllowRemoteMigration = false, want true")
	}
	if len(applied) == 0 {
		t.Fatal("expected applied env overrides")
	}
}

func TestApplySystemSettingsEnvOverridesAllowsClearingProxyURL(t *testing.T) {
	settings := &database.SystemSettings{
		ProxyURL: "http://db-proxy:8080",
	}

	t.Setenv("CODEX_PROXY_URL", "")

	ApplySystemSettingsEnvOverrides(settings)

	if settings.ProxyURL != "" {
		t.Fatalf("ProxyURL = %q, want empty string", settings.ProxyURL)
	}
}
