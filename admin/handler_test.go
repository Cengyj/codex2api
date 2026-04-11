package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/cache"
	"github.com/codex2api/database"
	"github.com/codex2api/proxy"
	"github.com/codex2api/security"
	"github.com/gin-gonic/gin"
)

func TestRefreshAccountRejectsInvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := &Handler{
		refreshAccount: func(context.Context, int64) error {
			t.Fatal("refresh should not be called for invalid id")
			return nil
		},
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "bad-id"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/bad-id/refresh", nil)

	handler.RefreshAccount(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "无效的账号 ID" {
		t.Fatalf("error = %q, want %q", got, "无效的账号 ID")
	}
}

func TestRefreshAccountRunsSingleRefresh(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var called bool
	var gotID int64
	handler := &Handler{
		refreshAccount: func(_ context.Context, id int64) error {
			called = true
			gotID = id
			return nil
		},
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "42"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/42/refresh", nil)

	handler.RefreshAccount(ctx)

	if !called {
		t.Fatal("expected refresh to be called")
	}
	if gotID != 42 {
		t.Fatalf("refresh id = %d, want %d", gotID, 42)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["message"]; got != "账号刷新成功" {
		t.Fatalf("message = %q, want %q", got, "账号刷新成功")
	}
}

func TestRefreshAccountReturnsNotFoundForMissingAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := &Handler{
		refreshAccount: func(context.Context, int64) error {
			return errors.New("账号 7 不存在")
		},
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "7"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/7/refresh", nil)

	handler.RefreshAccount(ctx)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "账号 7 不存在" {
		t.Fatalf("error = %q, want %q", got, "账号 7 不存在")
	}
}

func TestRefreshAccountReturnsRefreshFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := &Handler{
		refreshAccount: func(context.Context, int64) error {
			return errors.New("upstream unavailable")
		},
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "9"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/9/refresh", nil)

	handler.RefreshAccount(ctx)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "刷新失败" {
		t.Fatalf("error = %q, want %q", got, "刷新失败")
	}
}

func TestValidateExternalServiceBaseURLRejectsQueryString(t *testing.T) {
	_, err := validateExternalServiceBaseURL(context.Background(), "http://example.com/base?token=1", "cpa_base_url")
	if err == nil {
		t.Fatal("expected service base URL with query string to be rejected")
	}
}

func TestValidateExternalServiceBaseURLAllowsLocalhost(t *testing.T) {
	got, err := validateExternalServiceBaseURL(context.Background(), "http://localhost:9090/base/", "cpa_base_url")
	if err != nil {
		t.Fatalf("validateExternalServiceBaseURL() error = %v", err)
	}
	if got != "http://localhost:9090/base" {
		t.Fatalf("validateExternalServiceBaseURL() = %q, want %q", got, "http://localhost:9090/base")
	}
}

func TestValidateExternalTargetURLRejectsUserInfo(t *testing.T) {
	_, err := validateExternalTargetURL(context.Background(), "http://user:pass@example.com/ping", "mihomo_delay_test_url")
	if err == nil {
		t.Fatal("expected target URL with embedded credentials to be rejected")
	}
}

func TestValidateMigrationRemoteURLRejectsLocalhost(t *testing.T) {
	_, err := validateMigrationRemoteURL(context.Background(), "http://localhost:8080")
	if err == nil {
		t.Fatal("expected migration remote URL localhost to be rejected")
	}
}

func TestBuildProxyConfigInputRejectsInvalidStaticProxyURL(t *testing.T) {
	_, err := buildProxyConfigInput(auth.ProxyModeStatic, "http://proxy.example.com:99999", "", "http", false, false)
	if err == nil {
		t.Fatal("expected invalid proxy URL to be rejected")
	}
}

func TestBuildProxyConfigInputNormalizesGlobalProxyMode(t *testing.T) {
	cfg, err := buildProxyConfigInput(auth.ProxyModeStatic, "", "", "http", false, false)
	if err != nil {
		t.Fatalf("buildProxyConfigInput() error = %v", err)
	}
	if cfg.Mode != auth.ProxyModeNone {
		t.Fatalf("cfg.Mode = %q, want %q", cfg.Mode, auth.ProxyModeNone)
	}
}

func TestAddATAccountReturnsReadableProxyConfigValidationError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	t.Cleanup(func() { _ = db.Close() })

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/admin/accounts/at",
		bytes.NewBufferString(`{"name":"demo","access_token":"token","proxy_mode":"static","proxy_url":"http://proxy.example.com:99999"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.AddATAccount(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	got := payload["error"]
	if !strings.Contains(got, "代理配置无效") {
		t.Fatalf("error = %q, want prefix containing %q", got, "代理配置无效")
	}
	if strings.Contains(got, "??????") {
		t.Fatalf("error = %q, should not contain placeholder text", got)
	}
}

func TestAddProxiesRejectsWhitespaceOnlyURLs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	t.Cleanup(func() { _ = db.Close() })

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/admin/proxies",
		bytes.NewBufferString(`{"urls":["   ","\n"]}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.AddProxies(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "请提供至少一个代理 URL" {
		t.Fatalf("error = %q, want %q", got, "请提供至少一个代理 URL")
	}
}

func newSettingsTestHandler(t *testing.T) (*Handler, *database.DB) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := database.New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("database.New(sqlite) error: %v", err)
	}

	settings := &database.SystemSettings{
		MaxConcurrency:  2,
		GlobalRPM:       120,
		TestConcurrency: 1,
		TestModel:       "gpt-5.4",
	}
	if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
		t.Fatalf("UpdateSystemSettings() error: %v", err)
	}

	store := auth.NewStore(db, cache.NewMemory(1), settings)
	handler := NewHandler(store, db, cache.NewMemory(1), proxy.NewRateLimiter(settings.GlobalRPM), "")
	t.Cleanup(func() {
		if handler.adminAuthLimiter != nil {
			handler.adminAuthLimiter.Stop()
		}
	})
	handler.SetPoolSizes(50, 30)
	return handler, db
}

func TestAdminAuthMiddlewareRejectsRequestsWhenAdminSecretMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	t.Cleanup(func() { _ = db.Close() })

	router := gin.New()
	handler.RegisterRoutes(router)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestAdminAuthMiddlewareAllowsConfiguredAdminSecret(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	t.Cleanup(func() { _ = db.Close() })

	settings, err := db.GetSystemSettings(context.Background())
	if err != nil {
		t.Fatalf("GetSystemSettings() error: %v", err)
	}
	settings.AdminSecret = "secret-value"
	if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
		t.Fatalf("UpdateSystemSettings() error: %v", err)
	}

	router := gin.New()
	handler.RegisterRoutes(router)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	req.Header.Set("X-Admin-Key", "secret-value")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAdminAuthMiddlewareRateLimitsRepeatedInvalidSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	t.Cleanup(func() { _ = db.Close() })
	if handler.adminAuthLimiter != nil {
		handler.adminAuthLimiter.Stop()
	}
	handler.adminAuthLimiter = security.NewIPRateLimiter(1, time.Minute)

	settings, err := db.GetSystemSettings(context.Background())
	if err != nil {
		t.Fatalf("GetSystemSettings() error: %v", err)
	}
	settings.AdminSecret = "secret-value"
	if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
		t.Fatalf("UpdateSystemSettings() error: %v", err)
	}

	router := gin.New()
	handler.RegisterRoutes(router)

	firstReq := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	firstReq.Header.Set("X-Admin-Key", "wrong-secret")
	firstRec := httptest.NewRecorder()
	router.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusUnauthorized {
		t.Fatalf("first status = %d, want %d", firstRec.Code, http.StatusUnauthorized)
	}

	secondReq := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	secondReq.Header.Set("X-Admin-Key", "wrong-secret")
	secondRec := httptest.NewRecorder()
	router.ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want %d", secondRec.Code, http.StatusTooManyRequests)
	}
}

func TestGetSettingsReturnsServiceUnavailableWhenSettingsReadFails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error: %v", err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil)

	handler.GetSettings(ctx)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestUpdateSettingsReturnsServiceUnavailableWhenSettingsReadFails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	original := handler.store.GetMaxConcurrency()
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error: %v", err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/settings", bytes.NewBufferString(`{"max_concurrency":5}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(ctx)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "系统设置不可用" {
		t.Fatalf("error = %q, want %q", got, "系统设置不可用")
	}
	if got := handler.store.GetMaxConcurrency(); got != original {
		t.Fatalf("max concurrency = %d, want %d", got, original)
	}
}

func TestUpdateSettingsRejectsInvalidJSONWithReadableError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	t.Cleanup(func() { _ = db.Close() })

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/settings", bytes.NewBufferString(`{"max_concurrency":`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "请求格式错误" {
		t.Fatalf("error = %q, want %q", got, "请求格式错误")
	}
}

func TestUpdateSettingsDoesNotPartiallyApplyWhenValidationFails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	t.Cleanup(func() { _ = db.Close() })
	original := handler.store.GetMaxConcurrency()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/admin/settings",
		bytes.NewBufferString(`{"max_concurrency":5,"cpa_base_url":"http://example.com/base?token=1"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if got := handler.store.GetMaxConcurrency(); got != original {
		t.Fatalf("max concurrency = %d, want %d", got, original)
	}
}

func TestUpdateSettingsRequiresAdminSecretBeforeEnablingRemoteMigration(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	t.Cleanup(func() { _ = db.Close() })

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/admin/settings",
		bytes.NewBufferString(`{"allow_remote_migration":true}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "启用远程迁移前请先设置管理员密钥" {
		t.Fatalf("error = %q, want %q", got, "启用远程迁移前请先设置管理员密钥")
	}
}

func TestUpdateSettingsReappliesEnvOverridesAfterSave(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("MAX_CONCURRENCY", "9")

	handler, db := newSettingsTestHandler(t)
	t.Cleanup(func() { _ = db.Close() })

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/admin/settings",
		bytes.NewBufferString(`{"max_concurrency":5}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := handler.store.GetMaxConcurrency(); got != 9 {
		t.Fatalf("runtime max concurrency = %d, want %d", got, 9)
	}

	settings, err := db.GetSystemSettings(context.Background())
	if err != nil {
		t.Fatalf("GetSystemSettings() error: %v", err)
	}
	if settings.MaxConcurrency != 5 {
		t.Fatalf("persisted max concurrency = %d, want %d", settings.MaxConcurrency, 5)
	}

	var payload settingsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.MaxConcurrency != 9 {
		t.Fatalf("response max concurrency = %d, want %d", payload.MaxConcurrency, 9)
	}
}

func TestUpdateSettingsKeepsDatabaseFallbackForUnchangedEnvControlledFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("MAX_CONCURRENCY", "9")

	handler, db := newSettingsTestHandler(t)
	t.Cleanup(func() { _ = db.Close() })

	if err := db.UpdateSystemSettings(context.Background(), &database.SystemSettings{
		MaxConcurrency:                  3,
		GlobalRPM:                       60,
		TestModel:                       "gpt-5.4",
		TestConcurrency:                 50,
		PgMaxConns:                      50,
		RedisPoolSize:                   30,
		RefreshScanEnabled:              true,
		RefreshScanIntervalSeconds:      120,
		RefreshPreExpireSeconds:         300,
		RefreshMaxConcurrency:           10,
		RefreshOnImportEnabled:          true,
		RefreshOnImportConcurrency:      10,
		UsageProbeEnabled:               true,
		UsageProbeStaleAfterSeconds:     600,
		UsageProbeMaxConcurrency:        4,
		RecoveryProbeEnabled:            true,
		RecoveryProbeMinIntervalSeconds: 1800,
		RecoveryProbeMaxConcurrency:     2,
	}); err != nil {
		t.Fatalf("UpdateSystemSettings() error: %v", err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/admin/settings",
		bytes.NewBufferString(`{"global_rpm":120}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := handler.store.GetMaxConcurrency(); got != 9 {
		t.Fatalf("runtime max concurrency = %d, want %d", got, 9)
	}
	if got := handler.rateLimiter.GetRPM(); got != 120 {
		t.Fatalf("runtime global rpm = %d, want %d", got, 120)
	}

	settings, err := db.GetSystemSettings(context.Background())
	if err != nil {
		t.Fatalf("GetSystemSettings() error: %v", err)
	}
	if settings.MaxConcurrency != 3 {
		t.Fatalf("persisted max concurrency = %d, want %d", settings.MaxConcurrency, 3)
	}
	if settings.GlobalRPM != 120 {
		t.Fatalf("persisted global rpm = %d, want %d", settings.GlobalRPM, 120)
	}

	var payload settingsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.MaxConcurrency != 9 {
		t.Fatalf("response max concurrency = %d, want %d", payload.MaxConcurrency, 9)
	}
	if payload.GlobalRPM != 120 {
		t.Fatalf("response global rpm = %d, want %d", payload.GlobalRPM, 120)
	}
}

type wrappedAccountNotFoundError struct{}

func (wrappedAccountNotFoundError) Error() string {
	return "璐﹀彿 7 涓嶅瓨鍦?"
}

func (wrappedAccountNotFoundError) Unwrap() error {
	return auth.ErrAccountNotFound
}

func TestRefreshAccountReturnsNotFoundForWrappedMissingAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := &Handler{
		refreshAccount: func(context.Context, int64) error {
			return wrappedAccountNotFoundError{}
		},
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "7"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/7/refresh", nil)

	handler.RefreshAccount(ctx)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestDeleteAccountReturnsNotFoundWhenAccountDoesNotExist(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	t.Cleanup(func() { _ = db.Close() })

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "999"}}
	ctx.Request = httptest.NewRequest(http.MethodDelete, "/api/admin/accounts/999", nil)

	handler.DeleteAccount(ctx)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestToggleAccountLockReturnsNotFoundWhenAccountDoesNotExist(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	t.Cleanup(func() { _ = db.Close() })

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "999"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/999/lock", bytes.NewBufferString(`{"locked":true}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.ToggleAccountLock(ctx)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestDeleteAccountReturnsGenericInternalErrorWhenDeleteFails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	id, err := db.InsertAccount(context.Background(), "delete-fail", "rt-delete-fail", "")
	if err != nil {
		t.Fatalf("InsertAccount() error: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error: %v", err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "1"}}
	ctx.Request = httptest.NewRequest(http.MethodDelete, "/api/admin/accounts/1", nil)
	ctx.Params[0].Value = strconv.FormatInt(id, 10)

	handler.DeleteAccount(ctx)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "删除失败" {
		t.Fatalf("error = %q, want %q", got, "删除失败")
	}
}

func TestToggleAccountLockReturnsGenericInternalErrorWhenUpdateFails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	id, err := db.InsertAccount(context.Background(), "lock-fail", "rt-lock-fail", "")
	if err != nil {
		t.Fatalf("InsertAccount() error: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error: %v", err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(id, 10)}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/1/lock", bytes.NewBufferString(`{"locked":true}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.ToggleAccountLock(ctx)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "更新锁定状态失败" {
		t.Fatalf("error = %q, want %q", got, "更新锁定状态失败")
	}
}

func TestExchangeOAuthCodeSanitizesRequestFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	t.Cleanup(func() { _ = db.Close() })

	sessionID := "session-request-fail"
	globalOAuthStore.set(sessionID, &oauthSession{
		State:        "oauth-state",
		CodeVerifier: "oauth-verifier",
		RedirectURI:  "http://localhost:1455/auth/callback",
		ProxyURL:     "http://127.0.0.1:1",
		CreatedAt:    time.Now(),
	})
	t.Cleanup(func() { globalOAuthStore.delete(sessionID) })

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/admin/oauth/exchange-code",
		bytes.NewBufferString(`{"session_id":"session-request-fail","code":"bad-code","state":"oauth-state"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.ExchangeOAuthCode(ctx)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadGateway)
	}

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "授权码兑换失败" {
		t.Fatalf("error = %q, want %q", got, "授权码兑换失败")
	}
	body := recorder.Body.String()
	if strings.Contains(body, "connect") || strings.Contains(body, "refused") || strings.Contains(body, "127.0.0.1") {
		t.Fatalf("body = %q, should not expose low-level request detail", body)
	}
}

func TestTestProxySanitizesInvalidProxyURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := &Handler{}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/admin/proxies/test",
		bytes.NewBufferString(`{"url":"bad-proxy-url"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.TestProxy(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, _ := payload["error"].(string); got != "代理 URL 格式错误" {
		t.Fatalf("error = %q, want %q", got, "代理 URL 格式错误")
	}
}

func TestTestProxySanitizesConnectionFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := &Handler{}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/admin/proxies/test",
		bytes.NewBufferString(`{"url":"http://127.0.0.1:1"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.TestProxy(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, _ := payload["error"].(string); got != "连接失败" {
		t.Fatalf("error = %q, want %q", got, "连接失败")
	}
	body := recorder.Body.String()
	if strings.Contains(body, "127.0.0.1") || strings.Contains(body, "refused") || strings.Contains(body, "connect") {
		t.Fatalf("body = %q, should not expose connection detail", body)
	}
}

func TestTestProxySanitizesUpstreamFailureMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"fail","message":"sensitive upstream detail"}`))
	}))
	defer proxyServer.Close()

	handler := &Handler{}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/admin/proxies/test",
		bytes.NewBufferString(`{"url":"`+proxyServer.URL+`"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.TestProxy(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, _ := payload["error"].(string); got != "查询出口信息失败" {
		t.Fatalf("error = %q, want %q", got, "查询出口信息失败")
	}
	if strings.Contains(recorder.Body.String(), "sensitive upstream detail") {
		t.Fatalf("body = %q, should not expose upstream detail", recorder.Body.String())
	}
}

func TestDeleteProxyReturnsNotFoundWhenProxyDoesNotExist(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	t.Cleanup(func() { _ = db.Close() })

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "999"}}
	ctx.Request = httptest.NewRequest(http.MethodDelete, "/api/admin/proxies/999", nil)

	handler.DeleteProxy(ctx)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestUpdateProxyReturnsNotFoundWhenProxyDoesNotExist(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	t.Cleanup(func() { _ = db.Close() })

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "999"}}
	ctx.Request = httptest.NewRequest(http.MethodPatch, "/api/admin/proxies/999", bytes.NewBufferString(`{"label":"new-label"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateProxy(ctx)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestSendImportEventSkipsCanceledRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/accounts/import", nil).WithContext(reqCtx)

	if ok := sendImportEvent(ctx, importEvent{Type: "progress", Current: 1, Total: 2}); ok {
		t.Fatal("sendImportEvent() = true, want false for canceled request")
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", recorder.Body.String())
	}
}

func TestExportAccountsReturnsGenericInternalErrorWhenQueryFails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error: %v", err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/accounts/export", nil)

	handler.ExportAccounts(ctx)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "查询账号失败" {
		t.Fatalf("error = %q, want %q", got, "查询账号失败")
	}
}

func TestGetStatsReturnsGenericInternalErrorWhenQueryFails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error: %v", err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/stats", nil)

	handler.GetStats(ctx)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "获取统计信息失败" {
		t.Fatalf("error = %q, want %q", got, "获取统计信息失败")
	}
}

func TestListAccountsReturnsGenericInternalErrorWhenQueryFails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error: %v", err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/accounts", nil)

	handler.ListAccounts(ctx)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "获取账号列表失败" {
		t.Fatalf("error = %q, want %q", got, "获取账号列表失败")
	}
}

func TestGetAccountEventTrendReturnsGenericInternalErrorWhenQueryFails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error: %v", err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodGet,
		"/api/admin/accounts/event-trend?start=2026-04-11T00:00:00Z&end=2026-04-11T01:00:00Z",
		nil,
	)

	handler.GetAccountEventTrend(ctx)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "获取账号趋势失败" {
		t.Fatalf("error = %q, want %q", got, "获取账号趋势失败")
	}
}

func TestMigrateAccountsSanitizesRemoteErrorBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.UpdateSystemSettings(context.Background(), &database.SystemSettings{
		MaxConcurrency:       2,
		GlobalRPM:            120,
		TestConcurrency:      1,
		TestModel:            "gpt-5.4",
		AdminSecret:          "admin-secret",
		AllowRemoteMigration: true,
	}); err != nil {
		t.Fatalf("UpdateSystemSettings() error: %v", err)
	}

	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream sensitive detail"))
	}))
	defer remoteServer.Close()
	remoteParsed, err := neturl.Parse(remoteServer.URL)
	if err != nil {
		t.Fatalf("parse remote server url: %v", err)
	}
	oldTransport := http.DefaultTransport
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == "example.com:80" {
			addr = remoteParsed.Host
		}
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	http.DefaultTransport = transport
	t.Cleanup(func() {
		http.DefaultTransport = oldTransport
		transport.CloseIdleConnections()
	})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/migrate", bytes.NewBufferString(
		`{"url":"http://example.com","admin_key":"remote-admin"}`,
	))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.MigrateAccounts(ctx)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadGateway)
	}
	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "远程实例返回错误 (502)" {
		t.Fatalf("error = %q, want %q", got, "远程实例返回错误 (502)")
	}
}

func TestMigrateAccountsSanitizesRemoteDecodeFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, db := newSettingsTestHandler(t)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.UpdateSystemSettings(context.Background(), &database.SystemSettings{
		MaxConcurrency:       2,
		GlobalRPM:            120,
		TestConcurrency:      1,
		TestModel:            "gpt-5.4",
		AdminSecret:          "admin-secret",
		AllowRemoteMigration: true,
	}); err != nil {
		t.Fatalf("UpdateSystemSettings() error: %v", err)
	}

	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{not-json"))
	}))
	defer remoteServer.Close()
	remoteParsed, err := neturl.Parse(remoteServer.URL)
	if err != nil {
		t.Fatalf("parse remote server url: %v", err)
	}
	oldTransport := http.DefaultTransport
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == "example.com:80" {
			addr = remoteParsed.Host
		}
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	http.DefaultTransport = transport
	t.Cleanup(func() {
		http.DefaultTransport = oldTransport
		transport.CloseIdleConnections()
	})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/migrate", bytes.NewBufferString(
		`{"url":"http://example.com","admin_key":"remote-admin"}`,
	))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.MigrateAccounts(ctx)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadGateway)
	}
	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "解析远程数据失败" {
		t.Fatalf("error = %q, want %q", got, "解析远程数据失败")
	}
}
