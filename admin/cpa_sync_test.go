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

func newCPAMultiRecordTestServer(t *testing.T, records []map[string]any, downloads map[string]*cpaDownloadedAccount) *httptest.Server {
	t.Helper()

	var (
		mu      sync.Mutex
		deleted = make(map[string]bool)
	)

	handler := http.NewServeMux()
	handler.HandleFunc("/v0/management/auth-files", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch r.Method {
		case http.MethodGet:
			remaining := make([]map[string]any, 0, len(records))
			for _, record := range records {
				name := strings.TrimSpace(record["name"].(string))
				if deleted[name] {
					continue
				}
				remaining = append(remaining, record)
			}
			_ = json.NewEncoder(w).Encode(remaining)
		case http.MethodDelete:
			name := strings.TrimSpace(r.URL.Query().Get("name"))
			if name == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			deleted[name] = true
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	handler.HandleFunc("/v0/management/auth-files/download", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if name == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		remote, ok := downloads[name]
		if !ok || remote == nil {
			w.WriteHeader(http.StatusNotFound)
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

func newMihomoTestServer(t *testing.T, current string, candidates []string, badNodes map[string]bool) (*httptest.Server, *string) {
	t.Helper()

	var (
		mu      sync.Mutex
		nowNode = current
	)

	handler := http.NewServeMux()
	handler.HandleFunc("/proxies/Selector", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"now": nowNode,
				"all": candidates,
			})
		case http.MethodPut:
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if next := strings.TrimSpace(payload["name"]); next != "" {
				nowNode = next
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	handler.HandleFunc("/proxies/Selector/delay", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if badNodes != nil && badNodes[nowNode] {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": "network unreachable",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"delay": 120,
		})
	})

	server := httptest.NewServer(handler)
	return server, &nowNode
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

func TestRunOnceMarksMatchedTokenInvalidatedAccountUnauthorized(t *testing.T) {
	remote := &cpaDownloadedAccount{
		RefreshToken: "rt-token-invalidated",
		AccessToken:  "at-token-invalidated-new",
		AccountID:    "acct-token-invalidated",
		Email:        "tokeninvalidated@example.com",
		PlanType:     "free",
	}
	server := newCPATestServer(t, `{"error":{"code":"token_invalidated","message":"Your authentication token has been invalidated. Please try signing in again."}}`, remote)
	defer server.Close()

	service, db, store := newCPASyncTestService(t, server.URL)

	accountID, err := db.InsertAccount(context.Background(), remote.Email, remote.RefreshToken, "")
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
	if rows[0].CooldownReason != "unauthorized" {
		t.Fatalf("cooldown_reason = %q, want %q", rows[0].CooldownReason, "unauthorized")
	}

	accounts := store.Accounts()
	if len(accounts) != 1 {
		t.Fatalf("len(store.Accounts()) = %d, want 1", len(accounts))
	}
	if got := accounts[0].RuntimeStatus(); got != "unauthorized" {
		t.Fatalf("runtime status = %q, want %q", got, "unauthorized")
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

func TestRunOnceStillProcessesErrorAccountsWhenCPACountAlreadyMeetsMinimum(t *testing.T) {
	healthyName := "healthy.json"
	errorName := "error.json"
	errorRemote := &cpaDownloadedAccount{
		RefreshToken: "rt-enough-but-error",
		AccessToken:  "at-enough-but-error",
		AccountID:    "acct-enough-but-error",
		Email:        "error@example.com",
		PlanType:     "free",
	}
	server := newCPAMultiRecordTestServer(t,
		[]map[string]any{
			{
				"name":           healthyName,
				"email":          "healthy@example.com",
				"status_message": "",
			},
			{
				"name":  errorName,
				"email": errorRemote.Email,
				"error": map[string]any{
					"type": "usage_limit_reached",
				},
			},
		},
		map[string]*cpaDownloadedAccount{
			errorName: errorRemote,
		},
	)
	defer server.Close()

	service, db, _ := newCPASyncTestService(t, server.URL)
	if err := db.UpdateSystemSettings(context.Background(), &database.SystemSettings{
		MaxConcurrency:         2,
		TestConcurrency:        1,
		TestModel:              "gpt-5.4",
		CPASyncEnabled:         true,
		CPABaseURL:             server.URL,
		CPAAdminKey:            "test-key",
		CPAMinAccounts:         1,
		MihomoDelayTimeoutMs:   5000,
		CPASyncIntervalSeconds: 300,
	}); err != nil {
		t.Fatalf("UpdateSystemSettings() error: %v", err)
	}

	status, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error: %v", err)
	}

	settings, err := service.loadSettings(context.Background())
	if err != nil {
		t.Fatalf("loadSettings() error: %v", err)
	}
	remaining, err := service.listCPAAuthFiles(context.Background(), settings)
	if err != nil {
		t.Fatalf("listCPAAuthFiles() error: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("len(remaining) = %d, want 1", len(remaining))
	}
	if remaining[0].Name != healthyName {
		t.Fatalf("remaining[0].Name = %q, want %q", remaining[0].Name, healthyName)
	}
	if status.State.LastCPAAccountCount != 1 {
		t.Fatalf("LastCPAAccountCount = %d, want 1", status.State.LastCPAAccountCount)
	}
	if !strings.Contains(status.State.LastRunSummary, "processed_errors=1") {
		t.Fatalf("LastRunSummary = %q, want processed_errors=1", status.State.LastRunSummary)
	}
	if !strings.Contains(status.State.LastRunSummary, "uploaded=0") {
		t.Fatalf("LastRunSummary = %q, want uploaded=0", status.State.LastRunSummary)
	}
}

func TestRunOnceSwitchesMihomoWhenHourlyUploadCountReachesLimit(t *testing.T) {
	cpaHandler := http.NewServeMux()
	cpaHandler.HandleFunc("/v0/management/auth-files", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		case http.MethodPost:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	cpaHandler.HandleFunc("/v0/management/auth-files/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	cpaServer := httptest.NewServer(cpaHandler)
	defer cpaServer.Close()

	mihomoServer, currentNode := newMihomoTestServer(t, "node-a", []string{"node-a", "node-b"}, nil)
	defer mihomoServer.Close()

	service, db, store := newCPASyncTestService(t, cpaServer.URL)
	if err := db.UpdateSystemSettings(context.Background(), &database.SystemSettings{
		MaxConcurrency:         2,
		TestConcurrency:        1,
		TestModel:              "gpt-5.4",
		CPASyncEnabled:         true,
		CPABaseURL:             cpaServer.URL,
		CPAAdminKey:            "test-key",
		CPAMinAccounts:         1,
		CPAMaxUploadsPerHour:   1,
		CPASwitchAfterUploads:  30,
		CPASyncIntervalSeconds: 300,
		MihomoBaseURL:          mihomoServer.URL,
		MihomoSecret:           "mihomo-secret",
		MihomoStrategyGroup:    "Selector",
		MihomoDelayTestURL:     "https://example.com/ping",
		MihomoDelayTimeoutMs:   5000,
	}); err != nil {
		t.Fatalf("UpdateSystemSettings() error: %v", err)
	}

	accountID, err := db.InsertAccount(context.Background(), "upload@example.com", "rt-upload", "")
	if err != nil {
		t.Fatalf("InsertAccount() error: %v", err)
	}
	if err := db.UpdateCredentials(context.Background(), accountID, map[string]interface{}{
		"email":        "upload@example.com",
		"account_id":   "acct-upload",
		"access_token": "at-upload",
	}); err != nil {
		t.Fatalf("UpdateCredentials() error: %v", err)
	}
	store.AddAccount(&auth.Account{
		DBID:         accountID,
		RefreshToken: "rt-upload",
		AccessToken:  "at-upload",
		Email:        "upload@example.com",
		AccountID:    "acct-upload",
	})

	status, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error: %v", err)
	}

	if *currentNode != "node-b" {
		t.Fatalf("current Mihomo node = %q, want %q; actions=%+v summary=%q", *currentNode, "node-b", status.State.RecentActions, status.State.LastRunSummary)
	}
	if status.State.CurrentMihomoNode != "node-b" {
		t.Fatalf("status current node = %q, want %q", status.State.CurrentMihomoNode, "node-b")
	}
	if status.State.LastSwitchAt == "" {
		t.Fatal("LastSwitchAt is empty, want recorded switch time")
	}
	if status.State.HourlyUploadCount != 0 {
		t.Fatalf("HourlyUploadCount = %d, want 0 after successful threshold switch reset", status.State.HourlyUploadCount)
	}
}

func TestShouldAutoSwitchForHourlyLimitIgnoresTimeCooldownWhenThresholdReached(t *testing.T) {
	service, _, _ := newCPASyncTestService(t, "http://example.invalid")

	now := time.Date(2026, 4, 6, 10, 10, 0, 0, time.UTC)
	state := &database.CPASyncState{
		HourBucketStart:   now.Add(-5 * time.Minute).Format(time.RFC3339),
		HourlyUploadCount: 100,
		LastSwitchAt:      now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	settings := &cpaSyncSettings{
		MaxUploadsPerHour:  100,
		SwitchAfterUploads: 30,
	}

	ok, reason := service.shouldAutoSwitchForHourlyLimit(state, settings, now)
	if !ok {
		t.Fatalf("ok = %t, want true when threshold reached before configured time window; reason=%q", ok, reason)
	}
}

func TestSwitchMihomoRetriesNextCandidateWhenDelayTestFails(t *testing.T) {
	mihomoServer, currentNode := newMihomoTestServer(t, "node-a", []string{"node-a", "node-b", "node-c"}, map[string]bool{
		"node-b": true,
	})
	defer mihomoServer.Close()

	service, db, _ := newCPASyncTestService(t, "http://example.invalid")
	if err := db.UpdateSystemSettings(context.Background(), &database.SystemSettings{
		MaxConcurrency:       2,
		TestConcurrency:      1,
		TestModel:            "gpt-5.4",
		CPASyncEnabled:       true,
		CPABaseURL:           "http://example.invalid",
		CPAAdminKey:          "test-key",
		MihomoBaseURL:        mihomoServer.URL,
		MihomoSecret:         "mihomo-secret",
		MihomoStrategyGroup:  "Selector",
		MihomoDelayTestURL:   "https://example.com/ping",
		MihomoDelayTimeoutMs: 5000,
	}); err != nil {
		t.Fatalf("UpdateSystemSettings() error: %v", err)
	}

	settings, err := service.loadSettings(context.Background())
	if err != nil {
		t.Fatalf("loadSettings() error: %v", err)
	}
	state := &database.CPASyncState{}
	if err := service.switchMihomo(context.Background(), settings, state, "manual_switch"); err != nil {
		t.Fatalf("switchMihomo() error: %v", err)
	}

	if *currentNode != "node-c" {
		t.Fatalf("current Mihomo node = %q, want %q", *currentNode, "node-c")
	}
	if state.CurrentMihomoNode != "node-c" {
		t.Fatalf("state current node = %q, want %q", state.CurrentMihomoNode, "node-c")
	}
	if state.LastSwitchAt == "" {
		t.Fatal("LastSwitchAt is empty, want recorded switch time")
	}
}

func TestStatusDoesNotPollExternalServicesOrPersistNormalizedState(t *testing.T) {
	var (
		mu                 sync.Mutex
		cpaRequestCount    int
		mihomoRequestCount int
	)

	cpaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		cpaRequestCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer cpaServer.Close()

	mihomoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		mihomoRequestCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"now": "node-a",
			"all": []string{"node-a", "node-b"},
		})
	}))
	defer mihomoServer.Close()

	service, db, _ := newCPASyncTestService(t, cpaServer.URL)
	if err := db.UpdateSystemSettings(context.Background(), &database.SystemSettings{
		MaxConcurrency:       2,
		TestConcurrency:      1,
		TestModel:            "gpt-5.4",
		CPASyncEnabled:       true,
		CPABaseURL:           cpaServer.URL,
		CPAAdminKey:          "test-key",
		MihomoBaseURL:        mihomoServer.URL,
		MihomoSecret:         "mihomo-secret",
		MihomoStrategyGroup:  "Selector",
		MihomoDelayTimeoutMs: 5000,
	}); err != nil {
		t.Fatalf("UpdateSystemSettings() error: %v", err)
	}

	staleState := &database.CPASyncState{
		HourBucketStart:   time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour).Format(time.RFC3339),
		HourlyUploadCount: 7,
		RecentActions:     []database.CPASyncAction{},
	}
	if err := db.UpdateCPASyncState(context.Background(), staleState); err != nil {
		t.Fatalf("UpdateCPASyncState() error: %v", err)
	}

	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if status.State.HourlyUploadCount != 0 {
		t.Fatalf("status hourly_upload_count = %d, want 0 after in-memory normalization", status.State.HourlyUploadCount)
	}

	persisted, err := db.GetCPASyncState(context.Background())
	if err != nil {
		t.Fatalf("GetCPASyncState() error: %v", err)
	}
	if persisted.HourlyUploadCount != 7 {
		t.Fatalf("persisted hourly_upload_count = %d, want 7 without status writeback", persisted.HourlyUploadCount)
	}

	mu.Lock()
	defer mu.Unlock()
	if cpaRequestCount != 0 {
		t.Fatalf("CPA request count = %d, want 0", cpaRequestCount)
	}
	if mihomoRequestCount != 0 {
		t.Fatalf("Mihomo request count = %d, want 0", mihomoRequestCount)
	}
}

func TestRunOnceFallsBackToPostDeleteCountWhenCPARefreshFails(t *testing.T) {
	remote := &cpaDownloadedAccount{
		RefreshToken: "rt-refresh-fail",
		AccessToken:  "at-refresh-fail",
		AccountID:    "acct-refresh-fail",
		Email:        "refresh-fail@example.com",
		PlanType:     "free",
	}

	var listCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v0/management/auth-files" && r.Method == http.MethodGet:
			listCalls++
			if listCalls == 1 {
				_ = json.NewEncoder(w).Encode([]map[string]any{
					{
						"name":           "healthy.json",
						"email":          "healthy@example.com",
						"status_message": "",
					},
					{
						"name":           "error.json",
						"email":          remote.Email,
						"status_message": `{"error":{"type":"usage_limit_reached","resets_in_seconds":1200}}`,
					},
				})
				return
			}
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`refresh failed`))
		case r.URL.Path == "/v0/management/auth-files/download" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"refresh_token": remote.RefreshToken,
				"access_token":  remote.AccessToken,
				"id_token":      remote.IDToken,
				"expires_at":    remote.ExpiresAt,
				"account_id":    remote.AccountID,
				"email":         remote.Email,
				"plan_type":     remote.PlanType,
			})
		case r.URL.Path == "/v0/management/auth-files" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	service, db, _ := newCPASyncTestService(t, server.URL)
	if err := db.UpdateSystemSettings(context.Background(), &database.SystemSettings{
		MaxConcurrency:       2,
		TestConcurrency:      1,
		TestModel:            "gpt-5.4",
		CPASyncEnabled:       true,
		CPABaseURL:           server.URL,
		CPAAdminKey:          "test-key",
		CPAMinAccounts:       1,
		MihomoDelayTimeoutMs: 5000,
	}); err != nil {
		t.Fatalf("UpdateSystemSettings() error: %v", err)
	}

	status, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error: %v", err)
	}
	if status.State.LastCPAAccountCount != 1 {
		t.Fatalf("LastCPAAccountCount = %d, want 1 after successful delete fallback", status.State.LastCPAAccountCount)
	}
	if status.State.LastRunStatus != "partial_success" {
		t.Fatalf("LastRunStatus = %q, want %q", status.State.LastRunStatus, "partial_success")
	}
	if !strings.Contains(status.State.LastErrorSummary, "final CPA auth file recount failed") {
		t.Fatalf("LastErrorSummary = %q, want final recount failure summary", status.State.LastErrorSummary)
	}
}

func TestRunOnceCountsUnknownCPAErrorsWithoutUploadingWhenMinimumAlreadyMet(t *testing.T) {
	var (
		mu            sync.Mutex
		listCalls     int
		records       []map[string]any
		uploadedNames []string
		deletedNames  []string
	)
	for i := 0; i < 25; i++ {
		records = append(records, map[string]any{
			"name":           "proxy-error-" + strings.TrimSpace(time.Unix(int64(i), 0).UTC().Format("150405")) + ".json",
			"email":          "broken-" + strings.TrimSpace(time.Unix(int64(i), 0).UTC().Format("150405")) + "@example.com",
			"status":         "error",
			"status_message": `Post "https://chatgpt.com/backend-api/codex/responses": proxyconnect tcp: dial tcp 1.2.3.4:443: connect: connection refused`,
			"disabled":       false,
			"unavailable":    false,
		})
	}

	handler := http.NewServeMux()
	handler.HandleFunc("/v0/management/auth-files", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch r.Method {
		case http.MethodGet:
			listCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"files": records})
		case http.MethodDelete:
			name := strings.TrimSpace(r.URL.Query().Get("name"))
			if name == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			deletedNames = append(deletedNames, name)
			filtered := make([]map[string]any, 0, len(records))
			for _, record := range records {
				if strings.TrimSpace(firstString(record, "name")) == name {
					continue
				}
				filtered = append(filtered, record)
			}
			records = filtered
			w.WriteHeader(http.StatusOK)
		case http.MethodPost:
			var entry cpaExportEntry
			if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			name := strings.TrimSpace(r.URL.Query().Get("name"))
			if name == "" {
				name = buildCPAAuthFileName(entry)
			}
			uploadedNames = append(uploadedNames, name)
			records = append(records, map[string]any{
				"name":           name,
				"email":          entry.Email,
				"status":         "active",
				"status_message": "",
				"disabled":       false,
				"unavailable":    false,
			})
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	handler.HandleFunc("/v0/management/auth-files/upload", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	service, db, store := newCPASyncTestService(t, server.URL)
	if err := db.UpdateSystemSettings(context.Background(), &database.SystemSettings{
		MaxConcurrency:         2,
		TestConcurrency:        1,
		TestModel:              "gpt-5.4",
		CPASyncEnabled:         true,
		CPABaseURL:             server.URL,
		CPAAdminKey:            "test-key",
		CPAMinAccounts:         25,
		CPASyncIntervalSeconds: 300,
		MihomoDelayTimeoutMs:   5000,
	}); err != nil {
		t.Fatalf("UpdateSystemSettings() error: %v", err)
	}

	accountID, err := db.InsertAccount(context.Background(), "upload@example.com", "rt-upload-healthy", "")
	if err != nil {
		t.Fatalf("InsertAccount() error: %v", err)
	}
	if err := db.UpdateCredentials(context.Background(), accountID, map[string]interface{}{
		"email":        "upload@example.com",
		"account_id":   "acct-upload-healthy",
		"access_token": "at-upload-healthy",
	}); err != nil {
		t.Fatalf("UpdateCredentials() error: %v", err)
	}
	store.AddAccount(&auth.Account{
		DBID:         accountID,
		RefreshToken: "rt-upload-healthy",
		AccessToken:  "at-upload-healthy",
		Email:        "upload@example.com",
		AccountID:    "acct-upload-healthy",
	})

	status, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(deletedNames) != 0 {
		t.Fatalf("deletedNames = %v, want no deletion for unknown CPA error", deletedNames)
	}
	if len(uploadedNames) != 0 {
		t.Fatalf("len(uploadedNames) = %d, want 0 successful uploads when 25 unknown errors already count", len(uploadedNames))
	}
	if listCalls != 1 {
		t.Fatalf("listCalls = %d, want 1 when no remote changes occur", listCalls)
	}
	if status.State.LastCPAAccountCount != 25 {
		t.Fatalf("LastCPAAccountCount = %d, want 25 unknown CPA errors counted as existing accounts", status.State.LastCPAAccountCount)
	}
	if !strings.Contains(status.State.LastRunSummary, "processed_errors=0") {
		t.Fatalf("LastRunSummary = %q, want processed_errors=0 for unknown error", status.State.LastRunSummary)
	}
	if !strings.Contains(status.State.LastRunSummary, "uploaded=0") {
		t.Fatalf("LastRunSummary = %q, want uploaded=0", status.State.LastRunSummary)
	}
	if !strings.Contains(status.State.LastRunSummary, "cpa_count=25") {
		t.Fatalf("LastRunSummary = %q, want cpa_count=25", status.State.LastRunSummary)
	}
}

func TestRunOnceUsesFinalCPARecountAfterDeletesAndUploads(t *testing.T) {
	var (
		mu            sync.Mutex
		listCalls     int
		deletedNames  []string
		uploadedNames []string
		initialFiles  []map[string]any
		finalFiles    []map[string]any
		downloads     = map[string]*cpaDownloadedAccount{}
	)

	for i := 0; i < 22; i++ {
		email := "unknown-" + strings.TrimSpace(time.Unix(int64(i), 0).UTC().Format("150405")) + "@example.com"
		initialFiles = append(initialFiles, map[string]any{
			"name":           "unknown-" + strings.TrimSpace(time.Unix(int64(i), 0).UTC().Format("150405")) + ".json",
			"email":          email,
			"status":         "error",
			"status_message": `proxyconnect tcp: dial tcp 1.2.3.4:443: connect: connection refused`,
			"disabled":       false,
			"unavailable":    false,
		})
		finalFiles = append(finalFiles, map[string]any{
			"name":           "unknown-final-" + strings.TrimSpace(time.Unix(int64(i), 0).UTC().Format("150405")) + ".json",
			"email":          email,
			"status":         "error",
			"status_message": `proxyconnect tcp: dial tcp 1.2.3.4:443: connect: connection refused`,
			"disabled":       false,
			"unavailable":    false,
		})
	}
	for i := 0; i < 3; i++ {
		name := "target-error-" + strings.TrimSpace(time.Unix(int64(i), 0).UTC().Format("150405")) + ".json"
		email := "target-" + strings.TrimSpace(time.Unix(int64(i), 0).UTC().Format("150405")) + "@example.com"
		initialFiles = append(initialFiles, map[string]any{
			"name":           name,
			"email":          email,
			"status":         "error",
			"status_message": `{"error":{"type":"usage_limit_reached","resets_in_seconds":1200}}`,
			"disabled":       false,
			"unavailable":    false,
		})
		downloads[name] = &cpaDownloadedAccount{
			RefreshToken: "rt-" + strings.TrimSpace(time.Unix(int64(i), 0).UTC().Format("150405")),
			AccessToken:  "at-" + strings.TrimSpace(time.Unix(int64(i), 0).UTC().Format("150405")),
			AccountID:    "acct-" + strings.TrimSpace(time.Unix(int64(i), 0).UTC().Format("150405")),
			Email:        email,
			PlanType:     "free",
		}
	}
	for i := 0; i < 2; i++ {
		finalFiles = append(finalFiles, map[string]any{
			"name":           "uploaded-final-" + strings.TrimSpace(time.Unix(int64(i+100), 0).UTC().Format("150405")) + ".json",
			"email":          "uploaded-final-" + strings.TrimSpace(time.Unix(int64(i+100), 0).UTC().Format("150405")) + "@example.com",
			"status":         "active",
			"status_message": "",
			"disabled":       false,
			"unavailable":    false,
		})
	}

	handler := http.NewServeMux()
	handler.HandleFunc("/v0/management/auth-files", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch r.Method {
		case http.MethodGet:
			listCalls++
			if listCalls == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{"files": initialFiles})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"files": finalFiles})
		case http.MethodDelete:
			name := strings.TrimSpace(r.URL.Query().Get("name"))
			if name == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			deletedNames = append(deletedNames, name)
			w.WriteHeader(http.StatusOK)
		case http.MethodPost:
			var entry cpaExportEntry
			if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			uploadedNames = append(uploadedNames, strings.TrimSpace(entry.Email))
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	handler.HandleFunc("/v0/management/auth-files/download", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		remote, ok := downloads[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"refresh_token": remote.RefreshToken,
			"access_token":  remote.AccessToken,
			"account_id":    remote.AccountID,
			"email":         remote.Email,
			"plan_type":     remote.PlanType,
		})
	})
	handler.HandleFunc("/v0/management/auth-files/upload", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	service, db, store := newCPASyncTestService(t, server.URL)
	if err := db.UpdateSystemSettings(context.Background(), &database.SystemSettings{
		MaxConcurrency:         2,
		TestConcurrency:        1,
		TestModel:              "gpt-5.4",
		CPASyncEnabled:         true,
		CPABaseURL:             server.URL,
		CPAAdminKey:            "test-key",
		CPAMinAccounts:         25,
		CPASyncIntervalSeconds: 300,
		MihomoDelayTimeoutMs:   5000,
	}); err != nil {
		t.Fatalf("UpdateSystemSettings() error: %v", err)
	}

	for i := 0; i < 3; i++ {
		email := "candidate-" + strings.TrimSpace(time.Unix(int64(i+200), 0).UTC().Format("150405")) + "@example.com"
		accountID, err := db.InsertAccount(context.Background(), email, "rt-candidate-"+strings.TrimSpace(time.Unix(int64(i+200), 0).UTC().Format("150405")), "")
		if err != nil {
			t.Fatalf("InsertAccount() error: %v", err)
		}
		if err := db.UpdateCredentials(context.Background(), accountID, map[string]interface{}{
			"email":        email,
			"account_id":   "acct-candidate-" + strings.TrimSpace(time.Unix(int64(i+200), 0).UTC().Format("150405")),
			"access_token": "at-candidate-" + strings.TrimSpace(time.Unix(int64(i+200), 0).UTC().Format("150405")),
		}); err != nil {
			t.Fatalf("UpdateCredentials() error: %v", err)
		}
		store.AddAccount(&auth.Account{
			DBID:         accountID,
			RefreshToken: "rt-candidate-" + strings.TrimSpace(time.Unix(int64(i+200), 0).UTC().Format("150405")),
			AccessToken:  "at-candidate-" + strings.TrimSpace(time.Unix(int64(i+200), 0).UTC().Format("150405")),
			Email:        email,
			AccountID:    "acct-candidate-" + strings.TrimSpace(time.Unix(int64(i+200), 0).UTC().Format("150405")),
		})
	}

	status, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(deletedNames) != 3 {
		t.Fatalf("len(deletedNames) = %d, want 3 deleted target errors", len(deletedNames))
	}
	if len(uploadedNames) != 3 {
		t.Fatalf("len(uploadedNames) = %d, want 3 uploads to refill from 22 to 25", len(uploadedNames))
	}
	if listCalls != 2 {
		t.Fatalf("listCalls = %d, want 2 with final recount after remote changes", listCalls)
	}
	if status.State.LastCPAAccountCount != 24 {
		t.Fatalf("LastCPAAccountCount = %d, want 24 from final CPA recount instead of optimistic local count 25", status.State.LastCPAAccountCount)
	}
	if !strings.Contains(status.State.LastRunSummary, "processed_errors=3") {
		t.Fatalf("LastRunSummary = %q, want processed_errors=3", status.State.LastRunSummary)
	}
	if !strings.Contains(status.State.LastRunSummary, "uploaded=3") {
		t.Fatalf("LastRunSummary = %q, want uploaded=3", status.State.LastRunSummary)
	}
	if !strings.Contains(status.State.LastRunSummary, "cpa_count=24") {
		t.Fatalf("LastRunSummary = %q, want cpa_count=24 from final recount", status.State.LastRunSummary)
	}
}

func TestRunOnceSecondFetchOnlyRecountsWithoutDeletingNewTargetErrors(t *testing.T) {
	var (
		mu          sync.Mutex
		listCalls   int
		deleteCalls int
	)

	handler := http.NewServeMux()
	handler.HandleFunc("/v0/management/auth-files", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch r.Method {
		case http.MethodGet:
			listCalls++
			if listCalls == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"files": []map[string]any{
						{
							"name":           "target-error.json",
							"email":          "target@example.com",
							"status":         "error",
							"status_message": `{"error":{"type":"usage_limit_reached","resets_in_seconds":1200}}`,
							"disabled":       false,
							"unavailable":    false,
						},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"files": []map[string]any{
					{
						"name":           "still-target-error.json",
						"email":          "target@example.com",
						"status":         "error",
						"status_message": `{"error":{"type":"usage_limit_reached","resets_in_seconds":1200}}`,
						"disabled":       false,
						"unavailable":    false,
					},
				},
			})
		case http.MethodDelete:
			deleteCalls++
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	handler.HandleFunc("/v0/management/auth-files/download", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"refresh_token": "rt-target",
			"access_token":  "at-target",
			"account_id":    "acct-target",
			"email":         "target@example.com",
			"plan_type":     "free",
		})
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	service, db, _ := newCPASyncTestService(t, server.URL)
	if err := db.UpdateSystemSettings(context.Background(), &database.SystemSettings{
		MaxConcurrency:         2,
		TestConcurrency:        1,
		TestModel:              "gpt-5.4",
		CPASyncEnabled:         true,
		CPABaseURL:             server.URL,
		CPAAdminKey:            "test-key",
		CPAMinAccounts:         0,
		CPASyncIntervalSeconds: 300,
		MihomoDelayTimeoutMs:   5000,
	}); err != nil {
		t.Fatalf("UpdateSystemSettings() error: %v", err)
	}

	status, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if listCalls != 2 {
		t.Fatalf("listCalls = %d, want 2 with one initial fetch and one final recount", listCalls)
	}
	if deleteCalls != 1 {
		t.Fatalf("deleteCalls = %d, want 1 because second fetch should not trigger another delete", deleteCalls)
	}
	if status.State.LastCPAAccountCount != 0 {
		t.Fatalf("LastCPAAccountCount = %d, want 0 because target errors from final recount are not counted as effective", status.State.LastCPAAccountCount)
	}
	if !strings.Contains(status.State.LastRunSummary, "processed_errors=1") {
		t.Fatalf("LastRunSummary = %q, want processed_errors=1 from the first fetch only", status.State.LastRunSummary)
	}
	if !strings.Contains(status.State.LastRunSummary, "uploaded=0") {
		t.Fatalf("LastRunSummary = %q, want uploaded=0", status.State.LastRunSummary)
	}
	if !strings.Contains(status.State.LastRunSummary, "cpa_count=0") {
		t.Fatalf("LastRunSummary = %q, want cpa_count=0 after final recount", status.State.LastRunSummary)
	}
}

func TestParseCPAAuthFilesMarksDisabledAndUnavailableRecordsIneffective(t *testing.T) {
	body := []byte(`{"files":[
		{"name":"disabled.json","email":"disabled@example.com","status":"active","disabled":true},
		{"name":"unavailable.json","email":"unavailable@example.com","status":"active","unavailable":true},
		{"name":"healthy.json","email":"healthy@example.com","status":"active"}
	]}`)

	records, err := parseCPAAuthFiles(body)
	if err != nil {
		t.Fatalf("parseCPAAuthFiles() error: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("len(records) = %d, want 3", len(records))
	}

	effective := filterEffectiveCPAAuthFileRecords(records)
	if len(effective) != 1 {
		t.Fatalf("len(effective) = %d, want 1", len(effective))
	}
	if effective[0].Name != "healthy.json" {
		t.Fatalf("effective[0].Name = %q, want %q", effective[0].Name, "healthy.json")
	}
}

func TestSwitchMihomoUsesDefaultDelayURLWhenUnset(t *testing.T) {
	var (
		mu          sync.Mutex
		nowNode     = "node-a"
		delayCalls  int
		lastTestURL string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case r.URL.Path == "/proxies/Selector" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"now": nowNode,
				"all": []string{"node-a", "node-b", "node-c"},
			})
		case r.URL.Path == "/proxies/Selector" && r.Method == http.MethodPut:
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			nowNode = payload["name"]
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/proxies/Selector/delay" && r.Method == http.MethodGet:
			delayCalls++
			lastTestURL = r.URL.Query().Get("url")
			if nowNode == "node-b" {
				w.WriteHeader(http.StatusBadGateway)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "network unreachable"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"delay": 120})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	service, db, _ := newCPASyncTestService(t, "http://example.invalid")
	if err := db.UpdateSystemSettings(context.Background(), &database.SystemSettings{
		MaxConcurrency:       2,
		TestConcurrency:      1,
		TestModel:            "gpt-5.4",
		CPASyncEnabled:       true,
		CPABaseURL:           "http://example.invalid",
		CPAAdminKey:          "test-key",
		MihomoBaseURL:        server.URL,
		MihomoSecret:         "mihomo-secret",
		MihomoStrategyGroup:  "Selector",
		MihomoDelayTimeoutMs: 5000,
	}); err != nil {
		t.Fatalf("UpdateSystemSettings() error: %v", err)
	}

	settings, err := service.loadSettings(context.Background())
	if err != nil {
		t.Fatalf("loadSettings() error: %v", err)
	}
	state := &database.CPASyncState{}
	if err := service.switchMihomo(context.Background(), settings, state, "manual_switch"); err != nil {
		t.Fatalf("switchMihomo() error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if nowNode != "node-c" {
		t.Fatalf("current Mihomo node = %q, want %q", nowNode, "node-c")
	}
	if delayCalls < 2 {
		t.Fatalf("delayCalls = %d, want at least 2 to verify retry path", delayCalls)
	}
	if lastTestURL != defaultMihomoDelayTestURL {
		t.Fatalf("delay test url = %q, want %q", lastTestURL, defaultMihomoDelayTestURL)
	}
}

func TestTestMihomoPersistsCurrentNodeToSyncState(t *testing.T) {
	mihomoServer, currentNode := newMihomoTestServer(t, "node-b", []string{"node-a", "node-b", "node-c"}, nil)
	defer mihomoServer.Close()

	service, db, _ := newCPASyncTestService(t, "http://example.invalid")
	if err := db.UpdateSystemSettings(context.Background(), &database.SystemSettings{
		MaxConcurrency:       2,
		TestConcurrency:      1,
		TestModel:            "gpt-5.4",
		CPASyncEnabled:       true,
		CPABaseURL:           "http://example.invalid",
		CPAAdminKey:          "test-key",
		MihomoBaseURL:        mihomoServer.URL,
		MihomoSecret:         "mihomo-secret",
		MihomoStrategyGroup:  "Selector",
		MihomoDelayTimeoutMs: 5000,
	}); err != nil {
		t.Fatalf("UpdateSystemSettings() error: %v", err)
	}

	result, err := service.TestMihomo(context.Background(), nil)
	if err != nil {
		t.Fatalf("TestMihomo() error: %v", err)
	}
	if result == nil {
		t.Fatal("TestMihomo() returned nil result")
	}
	if got := strings.TrimSpace(firstString(result.Details, "current_node")); got != *currentNode {
		t.Fatalf("current_node detail = %q, want %q", got, *currentNode)
	}

	state, err := db.GetCPASyncState(context.Background())
	if err != nil {
		t.Fatalf("GetCPASyncState() error: %v", err)
	}
	if state.CurrentMihomoNode != *currentNode {
		t.Fatalf("CurrentMihomoNode = %q, want %q", state.CurrentMihomoNode, *currentNode)
	}
	if state.MihomoTestStatus.TestedAt == "" {
		t.Fatal("MihomoTestStatus.TestedAt is empty, want persisted test status")
	}
}

func TestTestCPAPersistsAccountCountToSyncState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/management/auth-files" || r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"files": []map[string]any{
				{"name": "a.json", "email": "a@example.com", "status": "active"},
				{"name": "b.json", "email": "b@example.com", "status": "active"},
				{"name": "c.json", "email": "c@example.com", "status": "active"},
			},
		})
	}))
	defer server.Close()

	service, db, _ := newCPASyncTestService(t, server.URL)

	result, err := service.TestCPA(context.Background(), nil)
	if err != nil {
		t.Fatalf("TestCPA() error: %v", err)
	}
	if result == nil {
		t.Fatal("TestCPA() returned nil result")
	}
	if got, ok := int64FromAny(result.Details["account_count"]); !ok || got != 3 {
		t.Fatalf("account_count detail = %v, ok=%t, want 3,true", result.Details["account_count"], ok)
	}

	state, err := db.GetCPASyncState(context.Background())
	if err != nil {
		t.Fatalf("GetCPASyncState() error: %v", err)
	}
	if state.LastCPAAccountCount != 3 {
		t.Fatalf("LastCPAAccountCount = %d, want 3", state.LastCPAAccountCount)
	}
	if state.CPATestStatus.TestedAt == "" {
		t.Fatal("CPATestStatus.TestedAt is empty, want persisted test status")
	}
}
