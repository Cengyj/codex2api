package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/codex2api/database"
)

// ApplySystemSettingsEnvOverrides makes runtime/system settings prefer .env/environment
// values over persisted database values. It mutates settings in place and returns the
// env keys that were applied.
func ApplySystemSettingsEnvOverrides(settings *database.SystemSettings) []string {
	if settings == nil {
		return nil
	}

	applied := make([]string, 0, 16)

	applyInt := func(target *int, minValue int, allowZero bool, keys ...string) {
		value, key, ok := lookupEnvInt(keys...)
		if !ok {
			return
		}
		if value == 0 && allowZero {
			*target = 0
			applied = append(applied, key)
			return
		}
		if value < minValue {
			return
		}
		*target = value
		applied = append(applied, key)
	}

	applyBool := func(target *bool, keys ...string) {
		value, key, ok := lookupEnvBool(keys...)
		if !ok {
			return
		}
		*target = value
		applied = append(applied, key)
	}

	applyString := func(target *string, trim bool, keys ...string) {
		value, key, ok := lookupEnvString(keys...)
		if !ok {
			return
		}
		if trim {
			value = strings.TrimSpace(value)
		}
		*target = value
		applied = append(applied, key)
	}

	applyInt(&settings.MaxConcurrency, 1, false, "MAX_CONCURRENCY")
	applyInt(&settings.GlobalRPM, 0, true, "GLOBAL_RPM")
	applyString(&settings.TestModel, true, "TEST_MODEL")
	applyInt(&settings.TestConcurrency, 1, false, "TEST_CONCURRENCY")
	applyString(&settings.ProxyURL, true, "CODEX_PROXY_URL", "PROXY_URL")
	applyString(&settings.ProxyMode, true, "PROXY_DEFAULT_MODE")
	applyString(&settings.ProxyProviderURL, true, "PROXY_DYNAMIC_PROVIDER_URL")
	applyString(&settings.ProxySchemeDefault, true, "PROXY_DEFAULT_PROTOCOL")
	applyInt(&settings.ProxyRotationHours, 1, false, "PROXY_ROTATION_HOURS")
	applyInt(&settings.PgMaxConns, 1, false, "PG_MAX_CONNS")
	applyInt(&settings.RedisPoolSize, 1, false, "REDIS_POOL_SIZE")
	applyBool(&settings.AutoCleanUnauthorized, "AUTO_CLEAN_UNAUTHORIZED")
	applyBool(&settings.AutoCleanRateLimited, "AUTO_CLEAN_RATE_LIMITED")
	applyBool(&settings.AutoCleanFullUsage, "AUTO_CLEAN_FULL_USAGE")
	applyBool(&settings.AutoCleanError, "AUTO_CLEAN_ERROR")
	applyBool(&settings.AutoCleanExpired, "AUTO_CLEAN_EXPIRED")
	applyBool(&settings.ProxyPoolEnabled, "PROXY_POOL_ENABLED")
	applyInt(&settings.MaxRetries, 0, true, "MAX_RETRIES")
	applyBool(&settings.RefreshScanEnabled, "REFRESH_SCAN_ENABLED")
	applyInt(&settings.RefreshScanIntervalSeconds, 0, true, "REFRESH_SCAN_INTERVAL_SECONDS")
	applyInt(&settings.RefreshPreExpireSeconds, 0, true, "REFRESH_PRE_EXPIRE_SECONDS")
	applyInt(&settings.RefreshMaxConcurrency, 1, false, "REFRESH_MAX_CONCURRENCY")
	applyBool(&settings.RefreshOnImportEnabled, "REFRESH_ON_IMPORT_ENABLED")
	applyInt(&settings.RefreshOnImportConcurrency, 0, true, "REFRESH_ON_IMPORT_CONCURRENCY")
	applyBool(&settings.UsageProbeEnabled, "USAGE_PROBE_ENABLED")
	applyInt(&settings.UsageProbeStaleAfterSeconds, 0, true, "USAGE_PROBE_STALE_AFTER_SECONDS")
	applyInt(&settings.UsageProbeMaxConcurrency, 1, false, "USAGE_PROBE_MAX_CONCURRENCY")
	applyBool(&settings.RecoveryProbeEnabled, "RECOVERY_PROBE_ENABLED")
	applyInt(&settings.RecoveryProbeMinIntervalSeconds, 0, true, "RECOVERY_PROBE_MIN_INTERVAL_SECONDS")
	applyInt(&settings.RecoveryProbeMaxConcurrency, 1, false, "RECOVERY_PROBE_MAX_CONCURRENCY")
	applyBool(&settings.AllowRemoteMigration, "ALLOW_REMOTE_MIGRATION")

	return applied
}

func lookupEnvString(keys ...string) (string, string, bool) {
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			return value, key, true
		}
	}
	return "", "", false
}

func lookupEnvInt(keys ...string) (int, string, bool) {
	for _, key := range keys {
		value, ok := os.LookupEnv(key)
		if !ok {
			continue
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, "", false
		}
		return parsed, key, true
	}
	return 0, "", false
}

func lookupEnvBool(keys ...string) (bool, string, bool) {
	for _, key := range keys {
		value, ok := os.LookupEnv(key)
		if !ok {
			continue
		}
		normalized := strings.ToLower(strings.TrimSpace(value))
		switch normalized {
		case "1", "true", "yes", "on", "enable", "enabled":
			return true, key, true
		case "0", "false", "no", "off", "disable", "disabled":
			return false, key, true
		default:
			return false, "", false
		}
	}
	return false, "", false
}
