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
	if status.State.HourlyUploadCount != 1 {
		t.Fatalf("HourlyUploadCount = %d, want 1", status.State.HourlyUploadCount)
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
	if !strings.Contains(status.State.LastErrorSummary, "refresh CPA auth files after cleanup failed") {
		t.Fatalf("LastErrorSummary = %q, want refresh failure summary", status.State.LastErrorSummary)
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
