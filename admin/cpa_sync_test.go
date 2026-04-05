package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/cache"
	"github.com/codex2api/database"
)

func newCPASyncTestService(t *testing.T, cpaBaseURL string) (*CPASyncService, *database.DB, *auth.Store) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := database.New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("database.New(sqlite) error: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	settings := &database.SystemSettings{
		MaxConcurrency:       2,
		TestConcurrency:      1,
		TestModel:            "gpt-5.4",
		CPASyncEnabled:       true,
		CPABaseURL:           cpaBaseURL,
		CPAAdminKey:          "test-key",
		MihomoDelayTimeoutMs: 5000,
	}
	if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
		t.Fatalf("UpdateSystemSettings() error: %v", err)
	}

	store := auth.NewStore(db, cache.NewMemory(1), settings)
	service := NewCPASyncService(store, db)
	return service, db, store
}

func newCPATestServer(t *testing.T, statusMessage string, remote *cpaDownloadedAccount) *httptest.Server {
	t.Helper()

	var mu sync.Mutex
	deleted := false

	handler := http.NewServeMux()
	handler.HandleFunc("/v0/management/auth-files", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch r.Method {
		case http.MethodGet:
			if deleted {
				_ = json.NewEncoder(w).Encode([]map[string]any{})
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"name":           "remote-account.json",
					"email":          remote.Email,
					"status_message": statusMessage,
				},
			})
		case http.MethodDelete:
			deleted = true
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	handler.HandleFunc("/v0/management/auth-files/download", func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.URL.Query().Get("name")) == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"refresh_token": remote.RefreshToken,
			"access_token":  remote.AccessToken,
			"id_token":      remote.IDToken,
			"expires_at":    remote.ExpiresAt,
			"account_id":    remote.AccountID,
			"email":         remote.Email,
			"plan_type":     remote.PlanType,
		})
	})

	return httptest.NewServer(handler)
}

func TestRunOnceImportsMissingUsageLimitAccountIntoLocalDB(t *testing.T) {
	remote := &cpaDownloadedAccount{
		RefreshToken: "rt-usage-limit",
		AccessToken:  "at-usage-limit",
		IDToken:      "id-usage-limit",
		ExpiresAt:    time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
		AccountID:    "acct-usage-limit",
		Email:        "usage@example.com",
		PlanType:     "free",
	}
	server := newCPATestServer(t, `{"error":{"type":"usage_limit_reached","resets_in_seconds":3600}}`, remote)
	defer server.Close()

	service, db, store := newCPASyncTestService(t, server.URL)

	if _, err := service.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error: %v", err)
	}

	rows, err := db.ListAllAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAllAccounts() error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	row := rows[0]
	if got := row.GetCredential("refresh_token"); got != remote.RefreshToken {
		t.Fatalf("refresh_token = %q, want %q", got, remote.RefreshToken)
	}
	if got := row.GetCredential("access_token"); got != remote.AccessToken {
		t.Fatalf("access_token = %q, want %q", got, remote.AccessToken)
	}
	if got := row.GetCredential("email"); got != remote.Email {
		t.Fatalf("email = %q, want %q", got, remote.Email)
	}
	if got := row.GetCredential("account_id"); got != remote.AccountID {
		t.Fatalf("account_id = %q, want %q", got, remote.AccountID)
	}
	if got := row.GetCredential("plan_type"); got != remote.PlanType {
		t.Fatalf("plan_type = %q, want %q", got, remote.PlanType)
	}
	if row.CooldownReason != "rate_limited" {
		t.Fatalf("cooldown_reason = %q, want %q", row.CooldownReason, "rate_limited")
	}
	if !row.CooldownUntil.Valid {
		t.Fatal("cooldown_until is invalid, want active rate limit cooldown")
	}
	if time.Until(row.CooldownUntil.Time) < 30*time.Minute {
		t.Fatalf("cooldown_until = %s, want at least 30 minutes in the future", row.CooldownUntil.Time.Format(time.RFC3339))
	}

	accounts := store.Accounts()
	if len(accounts) != 1 {
		t.Fatalf("len(store.Accounts()) = %d, want 1", len(accounts))
	}
	if accounts[0].Email != remote.Email {
		t.Fatalf("runtime email = %q, want %q", accounts[0].Email, remote.Email)
	}
	if got := accounts[0].RuntimeStatus(); got != "rate_limited" {
		t.Fatalf("runtime status = %q, want %q", got, "rate_limited")
	}
}

func TestRunOnceDoesNotImportMissingDeactivatedAccount(t *testing.T) {
	remote := &cpaDownloadedAccount{
		RefreshToken: "rt-deactivated",
		AccessToken:  "at-deactivated",
		AccountID:    "acct-deactivated",
		Email:        "deactivated@example.com",
		PlanType:     "free",
	}
	server := newCPATestServer(t, `{"error":{"code":"account_deactivated"}}`, remote)
	defer server.Close()

	service, db, store := newCPASyncTestService(t, server.URL)

	if _, err := service.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error: %v", err)
	}

	rows, err := db.ListAllAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAllAccounts() error: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("len(rows) = %d, want 0", len(rows))
	}
	if len(store.Accounts()) != 0 {
		t.Fatalf("len(store.Accounts()) = %d, want 0", len(store.Accounts()))
	}
}

func TestRunOnceMarksMatchedUsageLimitAccountRateLimited(t *testing.T) {
	remote := &cpaDownloadedAccount{
		RefreshToken: "rt-existing",
		AccessToken:  "at-existing-new",
		AccountID:    "acct-existing",
		Email:        "existing@example.com",
		PlanType:     "free",
	}
	server := newCPATestServer(t, `{"error":{"type":"usage_limit_reached","resets_in_seconds":1800}}`, remote)
	defer server.Close()

	service, db, store := newCPASyncTestService(t, server.URL)

	accountID, err := db.InsertAccount(context.Background(), "existing@example.com", remote.RefreshToken, "")
	if err != nil {
		t.Fatalf("InsertAccount() error: %v", err)
	}
	if err := db.UpdateCredentials(context.Background(), accountID, map[string]interface{}{
		"email":      remote.Email,
		"account_id": remote.AccountID,
		"plan_type":  remote.PlanType,
	}); err != nil {
		t.Fatalf("UpdateCredentials() error: %v", err)
	}
	store.AddAccount(&auth.Account{
		DBID:         accountID,
		RefreshToken: remote.RefreshToken,
		AccountID:    remote.AccountID,
		Email:        remote.Email,
		PlanType:     remote.PlanType,
	})

	if _, err := service.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error: %v", err)
	}

	rows, err := db.ListAllAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAllAccounts() error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if got := rows[0].GetCredential("access_token"); got != remote.AccessToken {
		t.Fatalf("access_token = %q, want %q", got, remote.AccessToken)
	}
	if rows[0].CooldownReason != "rate_limited" {
		t.Fatalf("cooldown_reason = %q, want %q", rows[0].CooldownReason, "rate_limited")
	}

	accounts := store.Accounts()
	if len(accounts) != 1 {
		t.Fatalf("len(store.Accounts()) = %d, want 1", len(accounts))
	}
	if got := accounts[0].RuntimeStatus(); got != "rate_limited" {
		t.Fatalf("runtime status = %q, want %q", got, "rate_limited")
	}
}
