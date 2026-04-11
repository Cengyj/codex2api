package admin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codex2api/auth"
	"github.com/codex2api/cache"
	"github.com/codex2api/database"
	"github.com/codex2api/proxy"
	"github.com/gin-gonic/gin"
)

func newProxyTestHandler() (*Handler, *auth.Store, *auth.Account) {
	store := auth.NewStore(nil, cache.NewMemory(1), &database.SystemSettings{
		MaxConcurrency:  2,
		TestConcurrency: 1,
		TestModel:       "gpt-5.4",
		ProxyURL:        "http://global:8080",
	})

	account := &auth.Account{
		DBID:        1,
		AccessToken: "access-token",
	}
	store.AddAccount(account)

	return &Handler{store: store}, store, account
}

func newHTTPResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestTestConnectionUsesEffectiveProxyURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, _, _ := newProxyTestHandler()
	originalExecute := executeRequest
	defer func() {
		executeRequest = originalExecute
	}()

	capturedProxy := make(chan string, 1)
	executeRequest = func(ctx context.Context, account *auth.Account, requestBody []byte, sessionID string, proxyOverride string, apiKey string, deviceCfg *proxy.DeviceProfileConfig, headers http.Header, useWebsocket ...bool) (*http.Response, error) {
		capturedProxy <- proxyOverride
		return newHTTPResponse(http.StatusOK, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"), nil
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "1"}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/accounts/1/test", nil)

	handler.TestConnection(ctx)

	select {
	case got := <-capturedProxy:
		if got != "http://global:8080" {
			t.Fatalf("proxy override = %q, want %q", got, "http://global:8080")
		}
	default:
		t.Fatal("executeRequest was not called")
	}

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

func TestBatchTestUsesEffectiveProxyURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, _, _ := newProxyTestHandler()
	originalExecute := executeRequest
	defer func() {
		executeRequest = originalExecute
	}()

	capturedProxy := make(chan string, 1)
	executeRequest = func(ctx context.Context, account *auth.Account, requestBody []byte, sessionID string, proxyOverride string, apiKey string, deviceCfg *proxy.DeviceProfileConfig, headers http.Header, useWebsocket ...bool) (*http.Response, error) {
		capturedProxy <- proxyOverride
		return newHTTPResponse(http.StatusOK, "ok"), nil
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/batch-test", nil)

	handler.BatchTest(ctx)

	select {
	case got := <-capturedProxy:
		if got != "http://global:8080" {
			t.Fatalf("proxy override = %q, want %q", got, "http://global:8080")
		}
	default:
		t.Fatal("executeRequest was not called")
	}

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

func TestProbeUsageSnapshotUsesEffectiveProxyURL(t *testing.T) {
	handler, _, account := newProxyTestHandler()
	originalExecute := executeRequest
	defer func() {
		executeRequest = originalExecute
	}()

	capturedProxy := make(chan string, 1)
	executeRequest = func(ctx context.Context, account *auth.Account, requestBody []byte, sessionID string, proxyOverride string, apiKey string, deviceCfg *proxy.DeviceProfileConfig, headers http.Header, useWebsocket ...bool) (*http.Response, error) {
		capturedProxy <- proxyOverride
		return newHTTPResponse(http.StatusOK, "ok"), nil
	}

	if err := handler.ProbeUsageSnapshot(context.Background(), account); err != nil {
		t.Fatalf("ProbeUsageSnapshot() error: %v", err)
	}

	select {
	case got := <-capturedProxy:
		if got != "http://global:8080" {
			t.Fatalf("proxy override = %q, want %q", got, "http://global:8080")
		}
	default:
		t.Fatal("executeRequest was not called")
	}
}

func TestTestConnectionSanitizesRequestError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, _, _ := newProxyTestHandler()
	originalExecute := executeRequest
	defer func() {
		executeRequest = originalExecute
	}()

	executeRequest = func(ctx context.Context, account *auth.Account, requestBody []byte, sessionID string, proxyOverride string, apiKey string, deviceCfg *proxy.DeviceProfileConfig, headers http.Header, useWebsocket ...bool) (*http.Response, error) {
		return nil, errors.New("dial tcp 1.2.3.4:443: connect: connection refused")
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "1"}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/accounts/1/test", nil)

	handler.TestConnection(ctx)

	body := recorder.Body.String()
	if !strings.Contains(body, "请求失败") {
		t.Fatalf("body = %q, want generic request failure", body)
	}
	if strings.Contains(body, "dial tcp") {
		t.Fatalf("body = %q, should not expose low-level network error", body)
	}
}

func TestTestConnectionSanitizesUpstreamErrorBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, _, _ := newProxyTestHandler()
	originalExecute := executeRequest
	defer func() {
		executeRequest = originalExecute
	}()

	executeRequest = func(ctx context.Context, account *auth.Account, requestBody []byte, sessionID string, proxyOverride string, apiKey string, deviceCfg *proxy.DeviceProfileConfig, headers http.Header, useWebsocket ...bool) (*http.Response, error) {
		return newHTTPResponse(http.StatusBadGateway, "sensitive upstream body"), nil
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "1"}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/accounts/1/test", nil)

	handler.TestConnection(ctx)

	body := recorder.Body.String()
	if !strings.Contains(body, "上游返回 502") {
		t.Fatalf("body = %q, want generic upstream status", body)
	}
	if strings.Contains(body, "sensitive upstream body") {
		t.Fatalf("body = %q, should not expose upstream body", body)
	}
}

func TestSendTestEventSkipsCanceledRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/accounts/1/test", nil).WithContext(reqCtx)

	if ok := sendTestEvent(ctx, testEvent{Type: "error", Error: "boom"}); ok {
		t.Fatal("sendTestEvent() = true, want false for canceled request")
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", recorder.Body.String())
	}
}
