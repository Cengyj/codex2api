package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codex2api/cache"
	"github.com/codex2api/database"
)

func newDynamicProxyStore(providerURL, globalProxy string, pool []string) *Store {
	store := NewStore(nil, cache.NewMemory(1), &database.SystemSettings{
		MaxConcurrency:   2,
		TestConcurrency:  1,
		TestModel:        "gpt-5.4",
		ProxyURL:         globalProxy,
		ProxyProviderURL: providerURL,
		ProxyMode:        ProxyModeDynamic,
		ProxyPoolEnabled: len(pool) > 0,
	})
	store.proxyPoolEnabled = len(pool) > 0
	store.proxyPool = append([]string(nil), pool...)
	return store
}

func TestAccountsWithoutProxyPreferDynamicThenPoolThenGlobal(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"success":true,"data":[{"ip":"104.160.167.56","port":18111}]}`))
	}))
	defer provider.Close()

	store := newDynamicProxyStore(provider.URL, "http://global:8080", []string{"http://pool:8080"})

	got := store.ResolveMaintenanceProxy(context.Background(), &Account{})
	want := "http://104.160.167.56:18111"
	if got != want {
		t.Fatalf("ResolveMaintenanceProxy() = %q, want %q", got, want)
	}
}

func TestStaticAccountFallsBackToDynamicOnNetworkError(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"success":true,"data":[{"ip":"104.160.167.56","port":18111}]}`))
	}))
	defer provider.Close()

	store := newDynamicProxyStore(provider.URL, "http://global:8080", []string{"http://pool:8080"})
	acc := &Account{
		DBID:      123,
		ProxyURL:  "http://file-proxy:8080",
		ProxyMode: ProxyModeStatic,
	}

	if got := store.ResolveMaintenanceProxy(context.Background(), acc); got != "http://file-proxy:8080" {
		t.Fatalf("initial ResolveMaintenanceProxy() = %q, want file proxy", got)
	}

	store.InvalidateProxyAssignment(context.Background(), acc, "network_error", "dial tcp timeout")

	got := store.ResolveMaintenanceProxy(context.Background(), acc)
	want := "http://104.160.167.56:18111"
	if got != want {
		t.Fatalf("ResolveMaintenanceProxy() after invalidation = %q, want %q", got, want)
	}
	if acc.AssignedProxyURL != want {
		t.Fatalf("AssignedProxyURL = %q, want %q", acc.AssignedProxyURL, want)
	}
}

func TestBuildResolvedProxyURLNormalizesHostPortPayload(t *testing.T) {
	got, err := buildResolvedProxyURL(dynamicProxyResponse{
		Data: []dynamicProxyPayload{{IP: "104.160.167.56", Port: 18111}},
	}, "socks5")
	if err != nil {
		t.Fatalf("buildResolvedProxyURL() error = %v", err)
	}
	if got != "socks5://104.160.167.56:18111" {
		t.Fatalf("buildResolvedProxyURL() = %q, want %q", got, "socks5://104.160.167.56:18111")
	}
}

func TestBuildResolvedProxyURLNormalizesHostPortPayloadWithSOCKS4(t *testing.T) {
	got, err := buildResolvedProxyURL(dynamicProxyResponse{
		Data: []dynamicProxyPayload{{IP: "104.160.167.56", Port: 18111}},
	}, "socks4")
	if err != nil {
		t.Fatalf("buildResolvedProxyURL() error = %v", err)
	}
	if got != "socks4://104.160.167.56:18111" {
		t.Fatalf("buildResolvedProxyURL() = %q, want %q", got, "socks4://104.160.167.56:18111")
	}
}

func TestBuildResolvedProxyURLRejectsUnsupportedScheme(t *testing.T) {
	_, err := buildResolvedProxyURL(dynamicProxyResponse{
		Data: []dynamicProxyPayload{{URL: "ftp://104.160.167.56:18111"}},
	}, "http")
	if err == nil {
		t.Fatal("buildResolvedProxyURL() error = nil, want unsupported scheme error")
	}
}

func TestBuildResolvedProxyURLRejectsMissingHost(t *testing.T) {
	_, err := buildResolvedProxyURL(dynamicProxyResponse{
		Data: []dynamicProxyPayload{{URL: "http://"}},
	}, "http")
	if err == nil {
		t.Fatal("buildResolvedProxyURL() error = nil, want missing host error")
	}
}

func TestBuildResolvedProxyURLRejectsInvalidPort(t *testing.T) {
	_, err := buildResolvedProxyURL(dynamicProxyResponse{
		Data: []dynamicProxyPayload{{URL: "http://104.160.167.56:99999"}},
	}, "http")
	if err == nil {
		t.Fatal("buildResolvedProxyURL() error = nil, want invalid port error")
	}
}
