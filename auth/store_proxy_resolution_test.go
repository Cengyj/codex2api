package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/codex2api/cache"
	"github.com/codex2api/database"
)

func newProxyResolutionStore(t *testing.T, db *database.DB, globalProxy string) *Store {
	t.Helper()

	return NewStore(db, cache.NewMemory(1), &database.SystemSettings{
		MaxConcurrency:  2,
		TestConcurrency: 1,
		TestModel:       "gpt-5.4",
		ProxyURL:        globalProxy,
	})
}

func newSQLiteTestDB(t *testing.T) *database.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := database.New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("database.New(sqlite) error: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func TestEffectiveProxyURLPrefersAccountProxy(t *testing.T) {
	store := newProxyResolutionStore(t, nil, "http://global:8080")
	acc := &Account{ProxyURL: "http://account:8080"}

	if got := store.EffectiveProxyURL(acc); got != "http://account:8080" {
		t.Fatalf("EffectiveProxyURL() = %q, want %q", got, "http://account:8080")
	}
}

func TestEffectiveProxyURLFallsBackToProxyPageRules(t *testing.T) {
	t.Run("proxy pool wins when enabled", func(t *testing.T) {
		store := newProxyResolutionStore(t, nil, "http://global:8080")
		store.proxyPoolEnabled = true
		store.proxyPool = []string{"http://pool:8080"}

		if got := store.EffectiveProxyURL(&Account{}); got != "http://pool:8080" {
			t.Fatalf("EffectiveProxyURL() = %q, want %q", got, "http://pool:8080")
		}
	})

	t.Run("global proxy used when pool disabled", func(t *testing.T) {
		store := newProxyResolutionStore(t, nil, "http://global:8080")

		if got := store.EffectiveProxyURL(&Account{}); got != "http://global:8080" {
			t.Fatalf("EffectiveProxyURL() = %q, want %q", got, "http://global:8080")
		}
	})

	t.Run("direct connection when no proxy configured", func(t *testing.T) {
		store := newProxyResolutionStore(t, nil, "")

		if got := store.EffectiveProxyURL(&Account{}); got != "" {
			t.Fatalf("EffectiveProxyURL() = %q, want empty", got)
		}
	})
}

func TestSetProxyURLAppliesImmediatelyToAccountsWithoutProxy(t *testing.T) {
	store := newProxyResolutionStore(t, nil, "http://old-global:8080")
	acc := &Account{}

	if got := store.EffectiveProxyURL(acc); got != "http://old-global:8080" {
		t.Fatalf("EffectiveProxyURL() before update = %q, want %q", got, "http://old-global:8080")
	}

	store.SetProxyURL("http://new-global:8080")

	if got := store.EffectiveProxyURL(acc); got != "http://new-global:8080" {
		t.Fatalf("EffectiveProxyURL() after update = %q, want %q", got, "http://new-global:8080")
	}
}

func TestLoadFromDBKeepsEmptyAccountProxyURL(t *testing.T) {
	db := newSQLiteTestDB(t)
	ctx := context.Background()

	accountID, err := db.InsertAccount(ctx, "load-empty-proxy", "refresh-token", "")
	if err != nil {
		t.Fatalf("InsertAccount() error: %v", err)
	}

	store := newProxyResolutionStore(t, db, "http://global:8080")
	if err := store.Init(ctx); err != nil {
		t.Fatalf("store.Init() error: %v", err)
	}

	acc := store.FindByID(accountID)
	if acc == nil {
		t.Fatalf("FindByID(%d) returned nil", accountID)
	}
	if acc.ProxyURL != "" {
		t.Fatalf("account.ProxyURL = %q, want empty", acc.ProxyURL)
	}
	if got := store.EffectiveProxyURL(acc); got != "http://global:8080" {
		t.Fatalf("EffectiveProxyURL() = %q, want %q", got, "http://global:8080")
	}
}

func TestRefreshSingleUsesEffectiveProxyURL(t *testing.T) {
	originalRefresh := refreshWithRetryFunc
	defer func() {
		refreshWithRetryFunc = originalRefresh
	}()

	tests := []struct {
		name         string
		accountProxy string
		globalProxy  string
		poolEnabled  bool
		pool         []string
		wantProxy    string
	}{
		{
			name:         "explicit account proxy wins",
			accountProxy: "http://account:8080",
			globalProxy:  "http://global:8080",
			poolEnabled:  true,
			pool:         []string{"http://pool:8080"},
			wantProxy:    "http://account:8080",
		},
		{
			name:        "pool proxy used when account proxy empty",
			globalProxy: "http://global:8080",
			poolEnabled: true,
			pool:        []string{"http://pool:8080"},
			wantProxy:   "http://pool:8080",
		},
		{
			name:        "global proxy used when pool disabled",
			globalProxy: "http://global:8080",
			wantProxy:   "http://global:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newSQLiteTestDB(t)
			ctx := context.Background()

			accountID, err := db.InsertAccount(ctx, tt.name, "refresh-token", tt.accountProxy)
			if err != nil {
				t.Fatalf("InsertAccount() error: %v", err)
			}

			store := newProxyResolutionStore(t, db, tt.globalProxy)
			store.proxyPoolEnabled = tt.poolEnabled
			store.proxyPool = append([]string(nil), tt.pool...)

			if err := store.Init(ctx); err != nil {
				t.Fatalf("store.Init() error: %v", err)
			}

			var capturedProxy string
			refreshWithRetryFunc = func(ctx context.Context, refreshToken string, proxyURL string) (*TokenData, *AccountInfo, error) {
				capturedProxy = proxyURL
				return &TokenData{
					AccessToken:  "access-token",
					RefreshToken: refreshToken,
					ExpiresAt:    time.Now().Add(1 * time.Hour),
				}, nil, nil
			}

			if err := store.RefreshSingle(ctx, accountID); err != nil {
				t.Fatalf("RefreshSingle() error: %v", err)
			}

			if capturedProxy != tt.wantProxy {
				t.Fatalf("refresh proxy = %q, want %q", capturedProxy, tt.wantProxy)
			}
		})
	}
}
