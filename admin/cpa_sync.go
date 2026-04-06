package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/database"
	"github.com/gin-gonic/gin"
)

const (
	defaultCPASyncInterval    = 5 * time.Minute
	minCPASyncInterval        = 30 * time.Second
	maxCPASyncInterval        = 24 * time.Hour
	cpaSyncRequestTimeout     = 45 * time.Second
	cpaSyncMaxRecentEvents    = 30
	defaultMihomoDelayTestURL = "https://cp.cloudflare.com/generate_204"
)

var errCPASyncBusy = errors.New("CPA sync is already running")

type CPASyncService struct {
	store       *auth.Store
	db          *database.DB
	client      *http.Client
	stopCh      chan struct{}
	configCh    chan struct{}
	wg          sync.WaitGroup
	running     atomic.Bool
	nextRunUnix atomic.Int64
}

type cpaSyncSettings struct {
	Enabled             bool
	CPABaseURL          string
	CPAAdminKey         string
	MinAccounts         int
	MaxAccounts         int
	MaxUploadsPerHour   int
	SwitchAfterUploads  int
	Interval            time.Duration
	MihomoBaseURL       string
	MihomoSecret        string
	MihomoStrategyGroup string
	MihomoDelayTestURL  string
	MihomoDelayTimeout  int
}

type cpaSyncConfigSummary struct {
	Enabled               bool     `json:"enabled"`
	IntervalSeconds       int      `json:"interval_seconds"`
	CPABaseURL            string   `json:"cpa_base_url"`
	CPAMinAccounts        int      `json:"cpa_min_accounts"`
	CPAMaxAccounts        int      `json:"cpa_max_accounts"`
	CPAMaxUploadsPerHour  int      `json:"cpa_max_uploads_per_hour"`
	CPASwitchAfterUploads int      `json:"cpa_switch_after_uploads"`
	MihomoBaseURL         string   `json:"mihomo_base_url"`
	MihomoStrategyGroup   string   `json:"mihomo_strategy_group"`
	MihomoDelayTestURL    string   `json:"mihomo_delay_test_url"`
	MihomoDelayTimeoutMs  int      `json:"mihomo_delay_timeout_ms"`
	MissingConfig         []string `json:"missing_config"`
}

type cpaSyncStatusResponse struct {
	Config           cpaSyncConfigSummary          `json:"config"`
	State            *database.CPASyncState        `json:"state"`
	CPATestStatus    database.ConnectionTestStatus `json:"cpa_test_status"`
	MihomoTestStatus database.ConnectionTestStatus `json:"mihomo_test_status"`
	Running          bool                          `json:"running"`
	NextRunAt        string                        `json:"next_run_at,omitempty"`
}

type cpaAuthFileRecord struct {
	Name          string
	Email         string
	Status        string
	StatusMessage string
	RefreshToken  string
	AccessToken   string
	Disabled      bool
	Unavailable   bool
}

type cpaDownloadedAccount struct {
	RefreshToken string
	AccessToken  string
	IDToken      string
	ExpiresAt    string
	AccountID    string
	Email        string
	PlanType     string
}

type mihomoSelectorDetail struct {
	Now string   `json:"now"`
	All []string `json:"all"`
}

type mihomoProxyDetail struct {
	Type string   `json:"type"`
	Now  string   `json:"now"`
	All  []string `json:"all"`
}

type mihomoProxyListResponse struct {
	Proxies map[string]mihomoProxyDetail `json:"proxies"`
}

type mihomoStrategyGroupOption struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	Current        string `json:"current"`
	CandidateCount int    `json:"candidate_count"`
}

type cpaSyncConnectionTestRequest struct {
	CPABaseURL          *string `json:"cpa_base_url"`
	CPAAdminKey         *string `json:"cpa_admin_key"`
	MihomoBaseURL       *string `json:"mihomo_base_url"`
	MihomoSecret        *string `json:"mihomo_secret"`
	MihomoStrategyGroup *string `json:"mihomo_strategy_group"`
	MihomoDelayTestURL  *string `json:"mihomo_delay_test_url"`
	MihomoDelayTimeout  *int    `json:"mihomo_delay_timeout_ms"`
}

func NewCPASyncService(store *auth.Store, db *database.DB) *CPASyncService {
	return &CPASyncService{
		store:    store,
		db:       db,
		client:   &http.Client{Timeout: cpaSyncRequestTimeout},
		stopCh:   make(chan struct{}),
		configCh: make(chan struct{}, 1),
	}
}

func (s *CPASyncService) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			settings, err := s.loadSettings(context.Background())
			if err != nil {
				log.Printf("[CPA Sync] load settings failed: %v", err)
				s.setNextRunAt(time.Now().UTC().Add(defaultCPASyncInterval))
				timer := time.NewTimer(defaultCPASyncInterval)
				select {
				case <-timer.C:
				case <-s.configCh:
					if !timer.Stop() {
						<-timer.C
					}
					continue
				case <-s.stopCh:
					if !timer.Stop() {
						<-timer.C
					}
					s.setNextRunAt(time.Time{})
					return
				}
			}

			if settings == nil || !settings.Enabled {
				s.setNextRunAt(time.Time{})
				select {
				case <-s.configCh:
					continue
				case <-s.stopCh:
					return
				}
			}

			interval := settings.interval()
			nextRunAt := time.Now().UTC().Add(interval)
			s.setNextRunAt(nextRunAt)
			timer := time.NewTimer(interval)
			select {
			case <-timer.C:
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				if _, err := s.runOnce(ctx, "auto", false); err != nil && !errors.Is(err, errCPASyncBusy) {
					log.Printf("[CPA Sync] scheduled run failed: %v", err)
				}
				cancel()
			case <-s.configCh:
				if !timer.Stop() {
					<-timer.C
				}
				continue
			case <-s.stopCh:
				if !timer.Stop() {
					<-timer.C
				}
				s.setNextRunAt(time.Time{})
				return
			}
		}
	}()
}

func (s *CPASyncService) Stop() {
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
	s.wg.Wait()
}

func (s *CPASyncService) NotifyConfigChanged() {
	select {
	case s.configCh <- struct{}{}:
	default:
	}
}

func (s *CPASyncService) setNextRunAt(next time.Time) {
	if next.IsZero() {
		s.nextRunUnix.Store(0)
		return
	}
	s.nextRunUnix.Store(next.UTC().Unix())
}

func (s *CPASyncService) nextRunAt() string {
	unix := s.nextRunUnix.Load()
	if unix <= 0 {
		return ""
	}
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

func (s *CPASyncService) statusAfterRun(ctx context.Context) (*cpaSyncStatusResponse, error) {
	s.running.Store(false)
	return s.Status(ctx)
}

func (s *CPASyncService) Status(ctx context.Context) (*cpaSyncStatusResponse, error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	state, err := s.db.GetCPASyncState(ctx)
	if err != nil {
		return nil, err
	}
	s.normalizeState(state, settings, time.Now().UTC())

	return &cpaSyncStatusResponse{
		Config:           settings.summary(),
		State:            state,
		CPATestStatus:    normalizeConnectionTestStatus(state.CPATestStatus),
		MihomoTestStatus: normalizeConnectionTestStatus(state.MihomoTestStatus),
		Running:          s.running.Load(),
		NextRunAt:        s.nextRunAt(),
	}, nil
}

func (s *CPASyncService) RunOnce(ctx context.Context) (*cpaSyncStatusResponse, error) {
	return s.runOnce(ctx, "manual", true)
}

func (s *CPASyncService) ForceSwitch(ctx context.Context) (*cpaSyncStatusResponse, error) {
	if !s.running.CompareAndSwap(false, true) {
		return nil, errCPASyncBusy
	}

	settings, err := s.loadSettings(ctx)
	if err != nil {
		s.running.Store(false)
		return nil, err
	}
	state, err := s.db.GetCPASyncState(ctx)
	if err != nil {
		s.running.Store(false)
		return nil, err
	}
	now := time.Now().UTC()
	s.normalizeState(state, settings, now)
	if missing := settings.missingMihomoConfig(); len(missing) > 0 {
		s.recordStateFailure(state, "switch_error", fmt.Sprintf("missing config: %s", strings.Join(missing, ", ")))
		_ = s.db.UpdateCPASyncState(ctx, state)
		return s.statusAfterRun(ctx)
	}

	if err := s.switchMihomo(ctx, settings, state, "manual_switch"); err != nil {
		s.recordStateFailure(state, "switch_error", err.Error())
	} else {
		completedAt := time.Now().UTC()
		if refreshErr := s.refreshMihomoSnapshot(ctx, settings, state, completedAt); refreshErr != nil && settings.hasMihomoConfig() {
			s.recordAction(state, "switch", "warning", fmt.Sprintf("refresh Mihomo snapshot failed: %v", refreshErr), settings.MihomoStrategyGroup)
		}
		s.recordAction(state, "switch", "success", fmt.Sprintf("switched selector to %s", state.CurrentMihomoNode), settings.MihomoStrategyGroup)
		state.LastRunAt = completedAt.Format(time.RFC3339)
		state.LastRunStatus = "switch_success"
		state.LastRunSummary = fmt.Sprintf("switched selector to %s", state.CurrentMihomoNode)
		state.LastErrorSummary = ""
	}
	_ = s.db.UpdateCPASyncState(ctx, state)
	return s.statusAfterRun(ctx)
}

func (s *CPASyncService) TestCPA(ctx context.Context, req *cpaSyncConnectionTestRequest) (*database.ConnectionTestStatus, error) {
	settings, err := s.loadSettingsWithOverrides(ctx, req)
	if err != nil {
		return nil, err
	}
	result := s.runCPAConnectionTest(ctx, settings)
	normalized := normalizeConnectionTestStatus(result)
	state, err := s.db.GetCPASyncState(ctx)
	if err != nil {
		return nil, err
	}
	s.syncCPAStateFromTestStatus(state, normalized)
	if err := s.db.UpdateCPASyncState(ctx, state); err != nil {
		return nil, err
	}
	return &normalized, nil
}

func (s *CPASyncService) TestMihomo(ctx context.Context, req *cpaSyncConnectionTestRequest) (*database.ConnectionTestStatus, error) {
	settings, err := s.loadSettingsWithOverrides(ctx, req)
	if err != nil {
		return nil, err
	}
	result := s.runMihomoConnectionTest(ctx, settings)
	normalized := normalizeConnectionTestStatus(result)
	state, err := s.db.GetCPASyncState(ctx)
	if err != nil {
		return nil, err
	}
	s.syncMihomoStateFromTestStatus(state, normalized)
	if err := s.db.UpdateCPASyncState(ctx, state); err != nil {
		return nil, err
	}
	return &normalized, nil
}

func (s *CPASyncService) ListMihomoStrategyGroups(ctx context.Context, req *cpaSyncConnectionTestRequest) ([]mihomoStrategyGroupOption, error) {
	settings, err := s.loadSettingsWithOverrides(ctx, req)
	if err != nil {
		return nil, err
	}
	if missing := settings.missingMihomoListConfig(); len(missing) > 0 {
		return nil, fmt.Errorf("missing config: %s", strings.Join(missing, ", "))
	}
	return s.fetchMihomoStrategyGroups(ctx, settings)
}

func (s *CPASyncService) runOnce(ctx context.Context, trigger string, allowWhenDisabled bool) (*cpaSyncStatusResponse, error) {
	if !s.running.CompareAndSwap(false, true) {
		return nil, errCPASyncBusy
	}

	settings, err := s.loadSettings(ctx)
	if err != nil {
		s.running.Store(false)
		return nil, err
	}
	state, err := s.db.GetCPASyncState(ctx)
	if err != nil {
		s.running.Store(false)
		return nil, err
	}
	now := time.Now().UTC()
	s.normalizeState(state, settings, now)

	if !allowWhenDisabled && !settings.Enabled {
		s.recordSkip(state, "disabled")
		_ = s.db.UpdateCPASyncState(ctx, state)
		return s.statusAfterRun(ctx)
	}
	if missing := settings.missingCPAConfig(); len(missing) > 0 {
		s.recordStateFailure(state, "skipped", fmt.Sprintf("missing config: %s", strings.Join(missing, ", ")))
		_ = s.db.UpdateCPASyncState(ctx, state)
		return s.statusAfterRun(ctx)
	}

	records, err := s.listCPAAuthFiles(ctx, settings)
	if err != nil {
		s.recordStateFailure(state, "error", fmt.Sprintf("list CPA auth files failed: %v", err))
		_ = s.db.UpdateCPASyncState(ctx, state)
		return s.statusAfterRun(ctx)
	}

	processedErrors := 0
	uploadedCount := 0
	firstErrorSummary := ""
	downloadedTokens := make(map[string]struct{})
	deletedRemote := false
	uploadedRemote := false
	remainingRecords := append([]cpaAuthFileRecord{}, records...)
	for _, record := range records {
		kind := detectCPAErrorKind(record.StatusMessage)
		if kind == "" {
			continue
		}
		processedErrors++
		remote, downloadErr := s.downloadCPAAuthFile(ctx, settings, record.Name)
		if downloadErr != nil {
			s.recordAction(state, "download", "error", fmt.Sprintf("download failed: %v", downloadErr), record.Name)
			if firstErrorSummary == "" {
				firstErrorSummary = fmt.Sprintf("download %s failed: %v", record.Name, downloadErr)
			}
		} else {
			if remote.RefreshToken != "" {
				downloadedTokens["rt:"+remote.RefreshToken] = struct{}{}
			}
			if remote.AccessToken != "" {
				downloadedTokens["at:"+remote.AccessToken] = struct{}{}
			}
		}

		localRows, listErr := s.db.ListAllAccounts(ctx)
		if listErr != nil {
			s.recordAction(state, "reconcile", "error", fmt.Sprintf("load local accounts failed: %v", listErr), record.Name)
			if firstErrorSummary == "" {
				firstErrorSummary = fmt.Sprintf("load local accounts failed: %v", listErr)
			}
		} else if downloadErr == nil {
			if matched, matchKind := matchLocalAccount(localRows, remote); matched != nil {
				delta := buildCredentialDelta(matched, remote)
				if len(delta) > 0 {
					if err := s.db.UpdateCredentials(ctx, matched.ID, delta); err != nil {
						s.recordAction(state, "reconcile", "error", fmt.Sprintf("update local credentials failed: %v", err), record.Name)
						if firstErrorSummary == "" {
							firstErrorSummary = fmt.Sprintf("update local credentials failed: %v", err)
						}
					} else {
						s.applyInMemoryCredentials(matched.ID, remote)
						s.recordAction(state, "reconcile", "success", fmt.Sprintf("updated local credentials via %s", matchKind), record.Name)
					}
				}
				if kind == "account_deactivated" || kind == "token_invalidated" {
					s.markUnauthorized(matched.ID)
				} else if kind == "usage_limit_reached" {
					s.markRateLimited(matched.ID, record.StatusMessage)
				}
			} else if kind != "account_deactivated" && kind != "token_invalidated" && localAccountCandidateCount(localRows, remote) == 0 {
				importedID, importName, err := s.importRemoteAccount(ctx, remote)
				if err != nil {
					s.recordAction(state, "reconcile", "error", fmt.Sprintf("create local account failed: %v", err), record.Name)
					if firstErrorSummary == "" {
						firstErrorSummary = fmt.Sprintf("create local account failed: %v", err)
					}
				} else {
					s.recordAction(state, "reconcile", "success", fmt.Sprintf("created local account %s from CPA", importName), record.Name)
					s.db.InsertAccountEventAsync(importedID, "added", "cpa_sync")
					if kind == "usage_limit_reached" {
						s.markRateLimited(importedID, record.StatusMessage)
					}
				}
			} else {
				s.recordAction(state, "reconcile", "warning", "no unique local account match found", record.Name)
			}
		}

		if err := s.deleteCPAAuthFile(ctx, settings, record.Name); err != nil {
			s.recordAction(state, "delete", "error", fmt.Sprintf("delete failed: %v", err), record.Name)
			if firstErrorSummary == "" {
				firstErrorSummary = fmt.Sprintf("delete %s failed: %v", record.Name, err)
			}
		} else {
			deletedRemote = true
			s.recordAction(state, "delete", "success", "deleted remote CPA account", record.Name)
			remainingRecords = removeCPAAuthFileRecordByName(remainingRecords, record.Name)
		}
	}

	records = remainingRecords
	effectiveRecords := filterEffectiveCPAAuthFileRecords(records)
	state.LastCPAAccountCount = len(effectiveRecords)

	targetCount := settings.MinAccounts
	if settings.MaxAccounts > 0 && (targetCount == 0 || settings.MaxAccounts < targetCount) {
		targetCount = settings.MaxAccounts
	}
	if targetCount < 0 {
		targetCount = 0
	}

	if state.LastCPAAccountCount < targetCount {
		remaining := targetCount - state.LastCPAAccountCount
		if settings.MaxUploadsPerHour > 0 {
			allowed := settings.MaxUploadsPerHour - state.HourlyUploadCount
			if allowed < remaining {
				remaining = allowed
			}
		}
		if settings.MaxAccounts > 0 {
			room := settings.MaxAccounts - state.LastCPAAccountCount
			if room < remaining {
				remaining = room
			}
		}
		if remaining > 0 {
			candidates, err := s.selectUploadCandidates(ctx, effectiveRecords, downloadedTokens, remaining)
			if err != nil {
				s.recordAction(state, "upload", "error", fmt.Sprintf("select candidates failed: %v", err), "")
				if firstErrorSummary == "" {
					firstErrorSummary = fmt.Sprintf("select upload candidates failed: %v", err)
				}
			} else {
				for _, candidate := range candidates {
					name := buildCPAAuthFileName(candidate)
					if err := s.uploadCPAAuthFile(ctx, settings, name, candidate); err != nil {
						s.recordAction(state, "upload", "error", fmt.Sprintf("upload failed: %v", err), name)
						if firstErrorSummary == "" {
							firstErrorSummary = fmt.Sprintf("upload %s failed: %v", name, err)
						}
						continue
					}
					uploadedRemote = true
					uploadedCount++
					state.HourlyUploadCount++
					effectiveRecords = append(effectiveRecords, cpaAuthFileRecord{
						Name:   name,
						Email:  candidate.Email,
						Status: "active",
					})
					s.recordAction(state, "upload", "success", fmt.Sprintf("uploaded %s", candidate.Email), name)
				}
			}
		}
	}
	if deletedRemote || uploadedRemote {
		refreshed, recountErr := s.listCPAAuthFiles(ctx, settings)
		if recountErr != nil {
			s.recordAction(state, "run", "warning", fmt.Sprintf("final CPA auth file recount failed: %v", recountErr), "")
			if firstErrorSummary == "" {
				firstErrorSummary = fmt.Sprintf("final CPA auth file recount failed: %v", recountErr)
			} else {
				firstErrorSummary = fmt.Sprintf("%s; final CPA auth file recount failed: %v", firstErrorSummary, recountErr)
			}
		} else {
			records = refreshed
			effectiveRecords = filterEffectiveCPAAuthFileRecords(records)
		}
	}
	state.LastCPAAccountCount = len(effectiveRecords)
	s.syncCPAAccountCountSnapshot(state, state.LastCPAAccountCount, len(records), time.Now().UTC())

	if eligible, reason := s.shouldAutoSwitchForHourlyLimit(state, settings, now); eligible {
		if err := s.switchMihomo(ctx, settings, state, "hourly_upload_limit"); err != nil {
			s.recordAction(state, "switch", "error", fmt.Sprintf("switch failed: %v", err), settings.MihomoStrategyGroup)
			if firstErrorSummary == "" {
				firstErrorSummary = fmt.Sprintf("switch Mihomo failed: %v", err)
			}
		} else {
			s.recordAction(state, "switch", "success", fmt.Sprintf("switched selector to %s", state.CurrentMihomoNode), settings.MihomoStrategyGroup)
		}
	} else if reason != "" && settings.MaxUploadsPerHour > 0 && state.HourlyUploadCount >= settings.MaxUploadsPerHour {
		s.recordAction(state, "switch", "info", reason, settings.MihomoStrategyGroup)
	}
	if err := s.refreshMihomoSnapshot(ctx, settings, state, time.Now().UTC()); err != nil && settings.hasMihomoConfig() {
		s.recordAction(state, "switch", "warning", fmt.Sprintf("refresh Mihomo snapshot failed: %v", err), settings.MihomoStrategyGroup)
	}

	state.LastRunAt = now.Format(time.RFC3339)
	if firstErrorSummary != "" {
		state.LastRunStatus = "partial_success"
		state.LastErrorSummary = firstErrorSummary
	} else {
		state.LastRunStatus = "success"
		state.LastErrorSummary = ""
	}
	state.LastRunSummary = fmt.Sprintf("trigger=%s, cpa_count=%d, processed_errors=%d, uploaded=%d", trigger, state.LastCPAAccountCount, processedErrors, uploadedCount)
	_ = s.db.UpdateCPASyncState(ctx, state)

	return s.statusAfterRun(ctx)
}

func (s *CPASyncService) loadSettings(ctx context.Context) (*cpaSyncSettings, error) {
	raw, err := s.db.GetSystemSettings(ctx)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		raw = &database.SystemSettings{}
	}
	delayTimeout := raw.MihomoDelayTimeoutMs
	if delayTimeout <= 0 {
		delayTimeout = 5000
	}
	intervalSeconds := raw.CPASyncIntervalSeconds
	if intervalSeconds <= 0 {
		intervalSeconds = int(defaultCPASyncInterval / time.Second)
	}
	return &cpaSyncSettings{
		Enabled:             raw.CPASyncEnabled,
		CPABaseURL:          strings.TrimRight(strings.TrimSpace(raw.CPABaseURL), "/"),
		CPAAdminKey:         strings.TrimSpace(raw.CPAAdminKey),
		MinAccounts:         raw.CPAMinAccounts,
		MaxAccounts:         raw.CPAMaxAccounts,
		MaxUploadsPerHour:   raw.CPAMaxUploadsPerHour,
		SwitchAfterUploads:  raw.CPASwitchAfterUploads,
		Interval:            time.Duration(intervalSeconds) * time.Second,
		MihomoBaseURL:       strings.TrimRight(strings.TrimSpace(raw.MihomoBaseURL), "/"),
		MihomoSecret:        strings.TrimSpace(raw.MihomoSecret),
		MihomoStrategyGroup: strings.TrimSpace(raw.MihomoStrategyGroup),
		MihomoDelayTestURL:  strings.TrimSpace(raw.MihomoDelayTestURL),
		MihomoDelayTimeout:  delayTimeout,
	}, nil
}

func (s *CPASyncService) loadSettingsWithOverrides(ctx context.Context, req *cpaSyncConnectionTestRequest) (*cpaSyncSettings, error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return settings, nil
	}
	if req.CPABaseURL != nil {
		settings.CPABaseURL = strings.TrimRight(strings.TrimSpace(*req.CPABaseURL), "/")
	}
	if req.CPAAdminKey != nil {
		settings.CPAAdminKey = strings.TrimSpace(*req.CPAAdminKey)
	}
	if req.MihomoBaseURL != nil {
		settings.MihomoBaseURL = strings.TrimRight(strings.TrimSpace(*req.MihomoBaseURL), "/")
	}
	if req.MihomoSecret != nil {
		settings.MihomoSecret = strings.TrimSpace(*req.MihomoSecret)
	}
	if req.MihomoStrategyGroup != nil {
		settings.MihomoStrategyGroup = strings.TrimSpace(*req.MihomoStrategyGroup)
	}
	if req.MihomoDelayTestURL != nil {
		settings.MihomoDelayTestURL = strings.TrimSpace(*req.MihomoDelayTestURL)
	}
	if req.MihomoDelayTimeout != nil {
		settings.MihomoDelayTimeout = *req.MihomoDelayTimeout
		if settings.MihomoDelayTimeout <= 0 {
			settings.MihomoDelayTimeout = 5000
		}
	}
	return settings, nil
}

func (c *cpaSyncSettings) missingCPAConfig() []string {
	var missing []string
	if c.CPABaseURL == "" {
		missing = append(missing, "cpa_base_url")
	}
	if c.CPAAdminKey == "" {
		missing = append(missing, "cpa_admin_key")
	}
	return missing
}

func (c *cpaSyncSettings) missingMihomoConfig() []string {
	var missing []string
	if c.MihomoBaseURL == "" {
		missing = append(missing, "mihomo_base_url")
	}
	if c.MihomoSecret == "" {
		missing = append(missing, "mihomo_secret")
	}
	if c.MihomoStrategyGroup == "" {
		missing = append(missing, "mihomo_strategy_group")
	}
	return missing
}

func (c *cpaSyncSettings) missingMihomoListConfig() []string {
	var missing []string
	if c.MihomoBaseURL == "" {
		missing = append(missing, "mihomo_base_url")
	}
	if c.MihomoSecret == "" {
		missing = append(missing, "mihomo_secret")
	}
	return missing
}

func (c *cpaSyncSettings) hasCPAConfig() bool {
	return len(c.missingCPAConfig()) == 0
}

func (c *cpaSyncSettings) hasMihomoConfig() bool {
	return len(c.missingMihomoConfig()) == 0
}

func (c *cpaSyncSettings) interval() time.Duration {
	if c == nil || c.Interval <= 0 {
		return defaultCPASyncInterval
	}
	if c.Interval < minCPASyncInterval {
		return minCPASyncInterval
	}
	if c.Interval > maxCPASyncInterval {
		return maxCPASyncInterval
	}
	return c.Interval
}

func (c *cpaSyncSettings) switchCooldown() time.Duration {
	if c == nil || c.SwitchAfterUploads <= 0 {
		return 0
	}
	return time.Duration(c.SwitchAfterUploads) * time.Minute
}

func (c *cpaSyncSettings) uploadSwitchWindow() time.Duration {
	if cooldown := c.switchCooldown(); cooldown > 0 {
		return cooldown
	}
	return time.Hour
}

func (c *cpaSyncSettings) resolvedMihomoDelayTestURL() string {
	if c == nil || strings.TrimSpace(c.MihomoDelayTestURL) == "" {
		return defaultMihomoDelayTestURL
	}
	return strings.TrimSpace(c.MihomoDelayTestURL)
}

func (c *cpaSyncSettings) summary() cpaSyncConfigSummary {
	missing := append([]string{}, c.missingCPAConfig()...)
	missing = append(missing, c.missingMihomoConfig()...)
	return cpaSyncConfigSummary{
		Enabled:               c.Enabled,
		IntervalSeconds:       int(c.interval() / time.Second),
		CPABaseURL:            c.CPABaseURL,
		CPAMinAccounts:        c.MinAccounts,
		CPAMaxAccounts:        c.MaxAccounts,
		CPAMaxUploadsPerHour:  c.MaxUploadsPerHour,
		CPASwitchAfterUploads: c.SwitchAfterUploads,
		MihomoBaseURL:         c.MihomoBaseURL,
		MihomoStrategyGroup:   c.MihomoStrategyGroup,
		MihomoDelayTestURL:    c.MihomoDelayTestURL,
		MihomoDelayTimeoutMs:  c.MihomoDelayTimeout,
		MissingConfig:         missing,
	}
}

func (s *CPASyncService) normalizeState(state *database.CPASyncState, settings *cpaSyncSettings, now time.Time) bool {
	if state == nil {
		return false
	}
	changed := false
	windowStart := now.UTC()
	windowDuration := time.Hour
	if settings != nil {
		windowDuration = settings.uploadSwitchWindow()
	}
	if state.HourBucketStart != "" {
		if parsed, err := time.Parse(time.RFC3339, state.HourBucketStart); err == nil && !parsed.IsZero() {
			if now.Before(parsed.Add(windowDuration)) {
				windowStart = parsed
			}
		}
	}
	windowStartRaw := windowStart.Format(time.RFC3339)
	if state.HourBucketStart == "" || state.HourBucketStart != windowStartRaw {
		state.HourBucketStart = windowStartRaw
		state.HourlyUploadCount = 0
		state.ConsecutiveUploadCount = 0
		changed = true
	}
	if state.ConsecutiveUploadCount != 0 {
		state.ConsecutiveUploadCount = 0
		changed = true
	}
	if state.RecentActions == nil {
		state.RecentActions = []database.CPASyncAction{}
		changed = true
	}
	return changed
}

func (s *CPASyncService) shouldAutoSwitchForHourlyLimit(state *database.CPASyncState, settings *cpaSyncSettings, now time.Time) (bool, string) {
	if state == nil || settings == nil {
		return false, ""
	}
	if settings.MaxUploadsPerHour <= 0 || state.HourlyUploadCount < settings.MaxUploadsPerHour {
		return false, ""
	}
	return true, ""
}

func (s *CPASyncService) recordAction(state *database.CPASyncState, kind, status, message, target string) {
	if state == nil {
		return
	}
	state.RecentActions = append(state.RecentActions, database.CPASyncAction{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Kind:      kind,
		Status:    status,
		Message:   message,
		Target:    target,
	})
	if len(state.RecentActions) > cpaSyncMaxRecentEvents {
		state.RecentActions = append([]database.CPASyncAction{}, state.RecentActions[len(state.RecentActions)-cpaSyncMaxRecentEvents:]...)
	}
}

func removeCPAAuthFileRecordByName(records []cpaAuthFileRecord, name string) []cpaAuthFileRecord {
	if len(records) == 0 {
		return records
	}
	filtered := make([]cpaAuthFileRecord, 0, len(records))
	for _, record := range records {
		if record.Name == name {
			continue
		}
		filtered = append(filtered, record)
	}
	return filtered
}

func (s *CPASyncService) recordSkip(state *database.CPASyncState, reason string) {
	state.LastRunAt = time.Now().UTC().Format(time.RFC3339)
	state.LastRunStatus = "skipped"
	state.LastRunSummary = reason
	state.LastErrorSummary = ""
	s.recordAction(state, "run", "info", reason, "")
}

func (s *CPASyncService) recordStateFailure(state *database.CPASyncState, status, message string) {
	state.LastRunAt = time.Now().UTC().Format(time.RFC3339)
	state.LastRunStatus = status
	state.LastRunSummary = message
	state.LastErrorSummary = message
	s.recordAction(state, "run", "error", message, "")
}

func (s *CPASyncService) runCPAConnectionTest(ctx context.Context, settings *cpaSyncSettings) database.ConnectionTestStatus {
	testedAt := time.Now().UTC().Format(time.RFC3339)
	if missing := settings.missingCPAConfig(); len(missing) > 0 {
		return database.ConnectionTestStatus{
			Ok:       boolPtr(false),
			Message:  fmt.Sprintf("missing config: %s", strings.Join(missing, ", ")),
			TestedAt: testedAt,
			Details:  map[string]any{},
		}
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, settings.CPABaseURL+"/v0/management/auth-files", nil)
	if err != nil {
		return database.ConnectionTestStatus{
			Ok:       boolPtr(false),
			Message:  err.Error(),
			TestedAt: testedAt,
			Details:  map[string]any{},
		}
	}
	s.applyCPAHeaders(req, settings.CPAAdminKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return database.ConnectionTestStatus{
			Ok:       boolPtr(false),
			Message:  err.Error(),
			TestedAt: testedAt,
			Details:  map[string]any{"duration_ms": time.Since(start).Milliseconds()},
		}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	durationMs := time.Since(start).Milliseconds()
	httpStatus := resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return database.ConnectionTestStatus{
			Ok:         boolPtr(false),
			Message:    fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
			HTTPStatus: intPtr(httpStatus),
			TestedAt:   testedAt,
			Details:    map[string]any{"duration_ms": durationMs},
		}
	}
	records, err := parseCPAAuthFiles(body)
	if err != nil {
		return database.ConnectionTestStatus{
			Ok:         boolPtr(false),
			Message:    fmt.Sprintf("parse CPA response failed: %v", err),
			HTTPStatus: intPtr(httpStatus),
			TestedAt:   testedAt,
			Details:    map[string]any{"duration_ms": durationMs},
		}
	}
	effectiveCount := len(filterEffectiveCPAAuthFileRecords(records))
	return database.ConnectionTestStatus{
		Ok:         boolPtr(true),
		Message:    fmt.Sprintf("CPA connection OK, found %d effective auth files", effectiveCount),
		HTTPStatus: intPtr(httpStatus),
		TestedAt:   testedAt,
		Details: map[string]any{
			"account_count":     effectiveCount,
			"raw_account_count": len(records),
			"duration_ms":       durationMs,
		},
	}
}

func (s *CPASyncService) runMihomoConnectionTest(ctx context.Context, settings *cpaSyncSettings) database.ConnectionTestStatus {
	testedAt := time.Now().UTC().Format(time.RFC3339)
	if missing := settings.missingMihomoConfig(); len(missing) > 0 {
		return database.ConnectionTestStatus{
			Ok:       boolPtr(false),
			Message:  fmt.Sprintf("missing config: %s", strings.Join(missing, ", ")),
			TestedAt: testedAt,
			Details:  map[string]any{},
		}
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/proxies/%s", settings.MihomoBaseURL, url.PathEscape(settings.MihomoStrategyGroup)), nil)
	if err != nil {
		return database.ConnectionTestStatus{
			Ok:       boolPtr(false),
			Message:  err.Error(),
			TestedAt: testedAt,
			Details:  map[string]any{},
		}
	}
	req.Header.Set("Authorization", "Bearer "+settings.MihomoSecret)
	resp, err := s.client.Do(req)
	if err != nil {
		return database.ConnectionTestStatus{
			Ok:       boolPtr(false),
			Message:  err.Error(),
			TestedAt: testedAt,
			Details:  map[string]any{"duration_ms": time.Since(start).Milliseconds()},
		}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	durationMs := time.Since(start).Milliseconds()
	httpStatus := resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return database.ConnectionTestStatus{
			Ok:         boolPtr(false),
			Message:    fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
			HTTPStatus: intPtr(httpStatus),
			TestedAt:   testedAt,
			Details:    map[string]any{"duration_ms": durationMs},
		}
	}
	var detail mihomoSelectorDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return database.ConnectionTestStatus{
			Ok:         boolPtr(false),
			Message:    fmt.Sprintf("parse Mihomo response failed: %v", err),
			HTTPStatus: intPtr(httpStatus),
			TestedAt:   testedAt,
			Details:    map[string]any{"duration_ms": durationMs},
		}
	}
	details := map[string]any{
		"current_node":    detail.Now,
		"candidate_count": len(detail.All),
		"duration_ms":     durationMs,
	}
	message := fmt.Sprintf("Mihomo connection OK, strategy group has %d candidates", len(detail.All))
	if settings.MihomoDelayTestURL != "" {
		if delayDetails, delayErr := s.runMihomoDelayTest(ctx, settings); delayErr != nil {
			details["delay_message"] = delayErr.Error()
			message = message + "; delay test failed"
		} else {
			for key, value := range delayDetails {
				details[key] = value
			}
		}
	}
	return database.ConnectionTestStatus{
		Ok:         boolPtr(true),
		Message:    message,
		HTTPStatus: intPtr(httpStatus),
		TestedAt:   testedAt,
		Details:    details,
	}
}

func (s *CPASyncService) runMihomoDelayTest(ctx context.Context, settings *cpaSyncSettings) (map[string]any, error) {
	testURL := settings.resolvedMihomoDelayTestURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/proxies/%s/delay?url=%s&timeout=%d",
		settings.MihomoBaseURL,
		url.PathEscape(settings.MihomoStrategyGroup),
		url.QueryEscape(testURL),
		settings.MihomoDelayTimeout,
	), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+settings.MihomoSecret)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("delay test failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return map[string]any{
			"delay_message": "delay test ok",
			"tested_url":    testURL,
		}, nil
	}
	result := map[string]any{
		"delay_message": "delay test ok",
		"tested_url":    testURL,
	}
	for _, key := range []string{"delay", "meanDelay"} {
		if value, ok := decoded[key]; ok {
			if delay, ok := int64FromAny(value); ok && delay <= 0 {
				return nil, fmt.Errorf("delay test failed: invalid %s=%d", key, delay)
			}
			result["delay_value"] = value
			break
		}
	}
	return result, nil
}

func (s *CPASyncService) listCPAAuthFiles(ctx context.Context, settings *cpaSyncSettings) ([]cpaAuthFileRecord, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, settings.CPABaseURL+"/v0/management/auth-files", nil)
	if err != nil {
		return nil, err
	}
	s.applyCPAHeaders(req, settings.CPAAdminKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseCPAAuthFiles(body)
}

func (s *CPASyncService) downloadCPAAuthFile(ctx context.Context, settings *cpaSyncSettings, name string) (*cpaDownloadedAccount, error) {
	endpoint := fmt.Sprintf("%s/v0/management/auth-files/download?name=%s", settings.CPABaseURL, url.QueryEscape(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	s.applyCPAHeaders(req, settings.CPAAdminKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseCPADownloadedAccount(body)
}

func (s *CPASyncService) deleteCPAAuthFile(ctx context.Context, settings *cpaSyncSettings, name string) error {
	endpoint := fmt.Sprintf("%s/v0/management/auth-files?name=%s", settings.CPABaseURL, url.QueryEscape(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	s.applyCPAHeaders(req, settings.CPAAdminKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (s *CPASyncService) uploadCPAAuthFile(ctx context.Context, settings *cpaSyncSettings, name string, entry cpaExportEntry) error {
	payload, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	type uploadAttempt struct {
		url         string
		contentType string
		body        []byte
	}
	attempts := []uploadAttempt{
		{
			url:         fmt.Sprintf("%s/v0/management/auth-files?name=%s", settings.CPABaseURL, url.QueryEscape(name)),
			contentType: "application/json",
			body:        payload,
		},
		{
			url:         fmt.Sprintf("%s/v0/management/auth-files/upload?name=%s", settings.CPABaseURL, url.QueryEscape(name)),
			contentType: "application/json",
			body:        payload,
		},
	}
	if mpBody, mpContentType, mpErr := buildMultipartCPAUpload(name, payload); mpErr == nil {
		attempts = append(attempts,
			uploadAttempt{url: settings.CPABaseURL + "/v0/management/auth-files", contentType: mpContentType, body: mpBody},
			uploadAttempt{url: settings.CPABaseURL + "/v0/management/auth-files/upload", contentType: mpContentType, body: mpBody},
		)
	}

	var lastErr error
	for _, attempt := range attempts {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, attempt.url, bytes.NewReader(attempt.body))
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", attempt.contentType)
		s.applyCPAHeaders(req, settings.CPAAdminKey)
		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if lastErr == nil {
		lastErr = errors.New("unknown CPA upload error")
	}
	return lastErr
}

func (s *CPASyncService) selectUploadCandidates(ctx context.Context, records []cpaAuthFileRecord, downloadedTokens map[string]struct{}, limit int) ([]cpaExportEntry, error) {
	rows, err := s.db.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	runtimeMap := make(map[int64]*auth.Account)
	for _, account := range s.store.Accounts() {
		runtimeMap[account.DBID] = account
	}

	existingEmails := make(map[string]struct{})
	for _, record := range records {
		if record.Email != "" {
			existingEmails[strings.ToLower(strings.TrimSpace(record.Email))] = struct{}{}
		}
		if record.RefreshToken != "" {
			downloadedTokens["rt:"+record.RefreshToken] = struct{}{}
		}
		if record.AccessToken != "" {
			downloadedTokens["at:"+record.AccessToken] = struct{}{}
		}
	}

	selected := make([]cpaExportEntry, 0, limit)
	for _, row := range rows {
		if len(selected) >= limit {
			break
		}
		account, ok := runtimeMap[row.ID]
		if !ok || account.RuntimeStatus() != "active" {
			continue
		}
		email := strings.TrimSpace(row.GetCredential("email"))
		refreshToken := strings.TrimSpace(row.GetCredential("refresh_token"))
		if email == "" || refreshToken == "" {
			continue
		}
		if _, exists := existingEmails[strings.ToLower(email)]; exists {
			continue
		}
		accessToken := strings.TrimSpace(row.GetCredential("access_token"))
		if _, exists := downloadedTokens["rt:"+refreshToken]; exists {
			continue
		}
		if accessToken != "" {
			if _, exists := downloadedTokens["at:"+accessToken]; exists {
				continue
			}
		}
		selected = append(selected, cpaExportEntry{
			Type:         "codex",
			Email:        email,
			Expired:      row.GetCredential("expires_at"),
			IDToken:      row.GetCredential("id_token"),
			AccountID:    row.GetCredential("account_id"),
			AccessToken:  accessToken,
			LastRefresh:  row.UpdatedAt.UTC().Format(time.RFC3339),
			RefreshToken: refreshToken,
		})
		existingEmails[strings.ToLower(email)] = struct{}{}
		downloadedTokens["rt:"+refreshToken] = struct{}{}
		if accessToken != "" {
			downloadedTokens["at:"+accessToken] = struct{}{}
		}
	}
	return selected, nil
}

func (s *CPASyncService) switchMihomo(ctx context.Context, settings *cpaSyncSettings, state *database.CPASyncState, reason string) error {
	if !settings.hasMihomoConfig() {
		return fmt.Errorf("missing Mihomo config")
	}

	detail, err := s.getMihomoSelector(ctx, settings)
	if err != nil {
		return err
	}
	if len(detail.All) == 0 {
		return errors.New("selector has no candidate nodes")
	}

	candidates := buildMihomoSwitchCandidates(detail)
	var lastErr error
	for _, candidate := range candidates {
		if err := s.setMihomoSelector(ctx, settings, candidate); err != nil {
			lastErr = fmt.Errorf("switch to %s failed: %w", candidate, err)
			continue
		}
		if err := s.triggerMihomoDelay(ctx, settings); err != nil {
			lastErr = fmt.Errorf("candidate %s delay test failed: %w", candidate, err)
			continue
		}

		switchAt := time.Now().UTC()
		state.LastSwitchAt = switchAt.Format(time.RFC3339)
		s.syncMihomoSnapshot(state, settings, &mihomoSelectorDetail{
			Now: candidate,
			All: append([]string{}, detail.All...),
		}, switchAt)
		if reason == "hourly_upload_limit" {
			state.HourBucketStart = switchAt.Format(time.RFC3339)
			state.HourlyUploadCount = 0
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("no Mihomo candidate available")
	}
	return lastErr
}

func buildMihomoSwitchCandidates(detail *mihomoSelectorDetail) []string {
	if detail == nil || len(detail.All) == 0 {
		return nil
	}
	if len(detail.All) == 1 {
		return append([]string{}, detail.All...)
	}

	currentIndex := -1
	for idx, candidate := range detail.All {
		if candidate == detail.Now {
			currentIndex = idx
			break
		}
	}
	if currentIndex < 0 {
		return append([]string{}, detail.All...)
	}

	candidates := make([]string, 0, len(detail.All))
	for offset := 1; offset <= len(detail.All); offset++ {
		candidates = append(candidates, detail.All[(currentIndex+offset)%len(detail.All)])
	}
	return candidates
}

func (s *CPASyncService) setMihomoSelector(ctx context.Context, settings *cpaSyncSettings, candidate string) error {
	body, _ := json.Marshal(map[string]string{"name": candidate})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, fmt.Sprintf("%s/proxies/%s", settings.MihomoBaseURL, url.PathEscape(settings.MihomoStrategyGroup)), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+settings.MihomoSecret)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func (s *CPASyncService) triggerMihomoDelay(ctx context.Context, settings *cpaSyncSettings) error {
	_, err := s.runMihomoDelayTest(ctx, settings)
	return err
}

func (s *CPASyncService) getMihomoSelector(ctx context.Context, settings *cpaSyncSettings) (*mihomoSelectorDetail, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/proxies/%s", settings.MihomoBaseURL, url.PathEscape(settings.MihomoStrategyGroup)), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+settings.MihomoSecret)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var detail mihomoSelectorDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return nil, err
	}
	return &detail, nil
}

func (s *CPASyncService) fetchMihomoStrategyGroups(ctx context.Context, settings *cpaSyncSettings) ([]mihomoStrategyGroupOption, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, settings.MihomoBaseURL+"/proxies", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+settings.MihomoSecret)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var decoded mihomoProxyListResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("parse Mihomo proxies failed: %w", err)
	}

	groups := make([]mihomoStrategyGroupOption, 0, len(decoded.Proxies))
	for name, detail := range decoded.Proxies {
		if len(detail.All) == 0 {
			continue
		}
		groups = append(groups, mihomoStrategyGroupOption{
			Name:           name,
			Type:           detail.Type,
			Current:        detail.Now,
			CandidateCount: len(detail.All),
		})
	}
	sort.Slice(groups, func(i, j int) bool {
		return strings.ToLower(groups[i].Name) < strings.ToLower(groups[j].Name)
	})
	return groups, nil
}

func normalizeConnectionTestStatus(status database.ConnectionTestStatus) database.ConnectionTestStatus {
	if status.Details == nil {
		status.Details = map[string]any{}
	}
	return status
}

func (s *CPASyncService) syncCPAStateFromTestStatus(state *database.CPASyncState, status database.ConnectionTestStatus) {
	if state == nil {
		return
	}
	normalized := normalizeConnectionTestStatus(status)
	state.CPATestStatus = normalized
	if accountCount, ok := int64FromAny(normalized.Details["account_count"]); ok && accountCount >= 0 {
		state.LastCPAAccountCount = int(accountCount)
	}
}

func (s *CPASyncService) syncCPAAccountCountSnapshot(state *database.CPASyncState, effectiveCount, rawCount int, testedAt time.Time) {
	s.syncCPAStateFromTestStatus(state, database.ConnectionTestStatus{
		Ok:         boolPtr(true),
		Message:    fmt.Sprintf("CPA connection OK, found %d effective auth files", effectiveCount),
		HTTPStatus: intPtr(http.StatusOK),
		TestedAt:   formatConnectionTestedAt(testedAt),
		Details: map[string]any{
			"account_count":     effectiveCount,
			"raw_account_count": rawCount,
		},
	})
}

func (s *CPASyncService) refreshMihomoSnapshot(ctx context.Context, settings *cpaSyncSettings, state *database.CPASyncState, testedAt time.Time) error {
	if state == nil || settings == nil || !settings.hasMihomoConfig() {
		return nil
	}
	detail, err := s.getMihomoSelector(ctx, settings)
	if err != nil {
		return err
	}
	s.syncMihomoSnapshot(state, settings, detail, testedAt)
	return nil
}

func (s *CPASyncService) syncMihomoStateFromTestStatus(state *database.CPASyncState, status database.ConnectionTestStatus) {
	if state == nil {
		return
	}
	normalized := normalizeConnectionTestStatus(status)
	state.MihomoTestStatus = normalized
	if currentNode := strings.TrimSpace(firstString(normalized.Details, "current_node")); currentNode != "" {
		state.CurrentMihomoNode = currentNode
	}
}

func (s *CPASyncService) syncMihomoSnapshot(state *database.CPASyncState, settings *cpaSyncSettings, detail *mihomoSelectorDetail, testedAt time.Time) {
	if settings == nil || detail == nil {
		return
	}
	s.syncMihomoStateFromTestStatus(state, database.ConnectionTestStatus{
		Ok:         boolPtr(true),
		Message:    fmt.Sprintf("Mihomo connection OK, strategy group has %d candidates", len(detail.All)),
		HTTPStatus: intPtr(http.StatusOK),
		TestedAt:   formatConnectionTestedAt(testedAt),
		Details: map[string]any{
			"current_node":    detail.Now,
			"candidate_count": len(detail.All),
			"strategy_group":  settings.MihomoStrategyGroup,
		},
	})
}

func boolPtr(value bool) *bool {
	return &value
}

func intPtr(value int) *int {
	return &value
}

func formatConnectionTestedAt(testedAt time.Time) string {
	if testedAt.IsZero() {
		return ""
	}
	return testedAt.UTC().Format(time.RFC3339)
}

func (s *CPASyncService) applyCPAHeaders(req *http.Request, adminKey string) {
	if adminKey == "" {
		return
	}
	req.Header.Set("Authorization", "Bearer "+adminKey)
	req.Header.Set("X-Management-Key", adminKey)
}

func (s *CPASyncService) applyInMemoryCredentials(dbID int64, remote *cpaDownloadedAccount) {
	if remote == nil {
		return
	}
	for _, account := range s.store.Accounts() {
		if account.DBID != dbID {
			continue
		}
		account.Mu().Lock()
		if remote.RefreshToken != "" {
			account.RefreshToken = remote.RefreshToken
		}
		if remote.AccessToken != "" {
			account.AccessToken = remote.AccessToken
		}
		if remote.AccountID != "" {
			account.AccountID = remote.AccountID
		}
		if remote.Email != "" {
			account.Email = remote.Email
		}
		if remote.PlanType != "" {
			account.PlanType = remote.PlanType
		}
		if remote.ExpiresAt != "" {
			if parsed, err := time.Parse(time.RFC3339, remote.ExpiresAt); err == nil {
				account.ExpiresAt = parsed
			}
		}
		account.Mu().Unlock()
		return
	}
}

func (s *CPASyncService) markUnauthorized(dbID int64) {
	for _, account := range s.store.Accounts() {
		if account.DBID == dbID {
			s.store.MarkCooldown(account, 6*time.Hour, "unauthorized")
			return
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.db.SetCooldown(ctx, dbID, "unauthorized", time.Now().UTC().Add(24*time.Hour))
}

func (s *CPASyncService) markRateLimited(dbID int64, statusMessage string) {
	now := time.Now().UTC()
	until := now.Add(5 * time.Minute)
	if parsed, ok := parseCPAUsageLimitResetAt(statusMessage, now); ok && parsed.After(now) {
		until = parsed
	}
	duration := until.Sub(now)
	if duration <= 0 {
		duration = 5 * time.Minute
		until = now.Add(duration)
	}

	for _, account := range s.store.Accounts() {
		if account.DBID == dbID {
			s.store.MarkCooldown(account, duration, "rate_limited")
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.db.SetCooldown(ctx, dbID, "rate_limited", until)
}

func parseCPAAuthFiles(body []byte) ([]cpaAuthFileRecord, error) {
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}

	items := unwrapArray(decoded)
	records := make([]cpaAuthFileRecord, 0, len(items))
	for _, item := range items {
		record := cpaAuthFileRecord{
			Name:          firstString(item, "name", "file_name", "filename", "id"),
			Email:         firstString(item, "email"),
			Status:        firstString(item, "status"),
			StatusMessage: extractCPAStatusMessage(item),
			RefreshToken:  firstString(item, "refresh_token"),
			AccessToken:   firstString(item, "access_token"),
			Disabled:      boolFromAny(item["disabled"]),
			Unavailable:   boolFromAny(item["unavailable"]),
		}
		if record.Name == "" && record.Email == "" {
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

func extractCPAStatusMessage(item map[string]any) string {
	if item == nil {
		return ""
	}
	if message := firstString(item, "status_message", "statusMessage", "status", "message"); message != "" {
		return message
	}
	if errorObj := unwrapObject(item["error"]); errorObj != nil {
		if encoded, err := json.Marshal(map[string]any{"error": errorObj}); err == nil {
			return string(encoded)
		}
	}
	for _, key := range []string{"details", "meta", "data", "auth_file"} {
		if nested := unwrapObject(item[key]); nested != nil {
			if message := extractCPAStatusMessage(nested); message != "" {
				return message
			}
		}
	}
	return ""
}

func parseCPADownloadedAccount(body []byte) (*cpaDownloadedAccount, error) {
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	obj := unwrapObject(decoded)
	if obj == nil {
		obj = map[string]any{}
	}
	if nested := unwrapObject(obj["credentials"]); nested != nil {
		for key, value := range nested {
			if _, exists := obj[key]; !exists {
				obj[key] = value
			}
		}
	}
	return &cpaDownloadedAccount{
		RefreshToken: firstString(obj, "refresh_token"),
		AccessToken:  firstString(obj, "access_token"),
		IDToken:      firstString(obj, "id_token"),
		ExpiresAt:    firstString(obj, "expires_at", "expired"),
		AccountID:    firstString(obj, "account_id"),
		Email:        firstString(obj, "email"),
		PlanType:     firstString(obj, "plan_type"),
	}, nil
}

func detectCPAErrorKind(statusMessage string) string {
	statusMessage = strings.TrimSpace(statusMessage)
	if statusMessage == "" {
		return ""
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(statusMessage), &decoded) == nil {
		errorObj := unwrapObject(decoded["error"])
		if firstString(errorObj, "type") == "usage_limit_reached" {
			return "usage_limit_reached"
		}
		if firstString(errorObj, "code") == "account_deactivated" {
			return "account_deactivated"
		}
		if firstString(errorObj, "code") == "token_invalidated" {
			return "token_invalidated"
		}
	}
	lower := strings.ToLower(statusMessage)
	switch {
	case strings.Contains(lower, "usage_limit_reached"):
		return "usage_limit_reached"
	case strings.Contains(lower, "account_deactivated"):
		return "account_deactivated"
	case strings.Contains(lower, "token_invalidated"),
		strings.Contains(lower, "authentication token has been invalidated"),
		strings.Contains(lower, "signing in again"):
		return "token_invalidated"
	default:
		return ""
	}
}

func parseCPAUsageLimitResetAt(statusMessage string, now time.Time) (time.Time, bool) {
	statusMessage = strings.TrimSpace(statusMessage)
	if statusMessage == "" {
		return time.Time{}, false
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(statusMessage), &decoded) != nil {
		return time.Time{}, false
	}
	errorObj := unwrapObject(decoded["error"])
	if firstString(errorObj, "type") != "usage_limit_reached" {
		return time.Time{}, false
	}
	if unix, ok := int64FromAny(errorObj["resets_at"]); ok && unix > 0 {
		return time.Unix(unix, 0).UTC(), true
	}
	if seconds, ok := int64FromAny(errorObj["resets_in_seconds"]); ok && seconds > 0 {
		return now.Add(time.Duration(seconds) * time.Second), true
	}
	return time.Time{}, false
}

func boolFromAny(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		default:
			return false
		}
	case int:
		return v != 0
	case int32:
		return v != 0
	case int64:
		return v != 0
	case float32:
		return v != 0
	case float64:
		return v != 0
	case json.Number:
		parsed, err := v.Int64()
		return err == nil && parsed != 0
	default:
		return false
	}
}

func int64FromAny(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case float32:
		return int64(v), true
	case float64:
		return int64(v), true
	case json.Number:
		parsed, err := v.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func filterEffectiveCPAAuthFileRecords(records []cpaAuthFileRecord) []cpaAuthFileRecord {
	effective := make([]cpaAuthFileRecord, 0, len(records))
	for _, record := range records {
		if !isEffectiveCPAAuthFileRecord(record) {
			continue
		}
		effective = append(effective, record)
	}
	return effective
}

func isEffectiveCPAAuthFileRecord(record cpaAuthFileRecord) bool {
	if record.Disabled || record.Unavailable {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(record.Status)) {
	case "disabled", "unavailable":
		return false
	}
	return detectCPAErrorKind(record.StatusMessage) == ""
}

func matchLocalAccount(rows []*database.AccountRow, remote *cpaDownloadedAccount) (*database.AccountRow, string) {
	if remote == nil {
		return nil, ""
	}
	if remote.RefreshToken != "" {
		if matched := uniqueAccountMatch(rows, func(row *database.AccountRow) bool {
			return row.GetCredential("refresh_token") == remote.RefreshToken
		}); matched != nil {
			return matched, "refresh_token"
		}
	}
	if remote.AccessToken != "" {
		if matched := uniqueAccountMatch(rows, func(row *database.AccountRow) bool {
			return row.GetCredential("access_token") == remote.AccessToken
		}); matched != nil {
			return matched, "access_token"
		}
	}
	if remote.Email != "" && remote.AccountID != "" {
		if matched := uniqueAccountMatch(rows, func(row *database.AccountRow) bool {
			return strings.EqualFold(row.GetCredential("email"), remote.Email) && row.GetCredential("account_id") == remote.AccountID
		}); matched != nil {
			return matched, "email+account_id"
		}
	}
	if remote.Email != "" {
		if matched := uniqueAccountMatch(rows, func(row *database.AccountRow) bool {
			return strings.EqualFold(row.GetCredential("email"), remote.Email)
		}); matched != nil {
			return matched, "email"
		}
	}
	return nil, ""
}

func uniqueAccountMatch(rows []*database.AccountRow, predicate func(*database.AccountRow) bool) *database.AccountRow {
	var matched *database.AccountRow
	for _, row := range rows {
		if !predicate(row) {
			continue
		}
		if matched != nil {
			return nil
		}
		matched = row
	}
	return matched
}

func localAccountCandidateCount(rows []*database.AccountRow, remote *cpaDownloadedAccount) int {
	if remote == nil {
		return 0
	}
	seen := make(map[int64]struct{})
	addIfMatch := func(predicate func(*database.AccountRow) bool) {
		for _, row := range rows {
			if !predicate(row) {
				continue
			}
			seen[row.ID] = struct{}{}
		}
	}

	if remote.RefreshToken != "" {
		addIfMatch(func(row *database.AccountRow) bool {
			return row.GetCredential("refresh_token") == remote.RefreshToken
		})
	}
	if remote.AccessToken != "" {
		addIfMatch(func(row *database.AccountRow) bool {
			return row.GetCredential("access_token") == remote.AccessToken
		})
	}
	if remote.Email != "" && remote.AccountID != "" {
		addIfMatch(func(row *database.AccountRow) bool {
			return strings.EqualFold(row.GetCredential("email"), remote.Email) && row.GetCredential("account_id") == remote.AccountID
		})
	}
	if remote.Email != "" {
		addIfMatch(func(row *database.AccountRow) bool {
			return strings.EqualFold(row.GetCredential("email"), remote.Email)
		})
	}
	return len(seen)
}

func buildImportedAccountName(remote *cpaDownloadedAccount) string {
	if remote == nil {
		return fmt.Sprintf("cpa-import-%d", time.Now().UTC().UnixNano())
	}
	if email := strings.TrimSpace(remote.Email); email != "" {
		return email
	}
	if accountID := strings.TrimSpace(remote.AccountID); accountID != "" {
		return accountID
	}
	return fmt.Sprintf("cpa-import-%d", time.Now().UTC().UnixNano())
}

func buildRemoteCredentialPayload(remote *cpaDownloadedAccount) map[string]interface{} {
	if remote == nil {
		return map[string]interface{}{}
	}
	credentials := make(map[string]interface{})
	if remote.RefreshToken != "" {
		credentials["refresh_token"] = remote.RefreshToken
	}
	if remote.AccessToken != "" {
		credentials["access_token"] = remote.AccessToken
	}
	if remote.IDToken != "" {
		credentials["id_token"] = remote.IDToken
	}
	if remote.ExpiresAt != "" {
		credentials["expires_at"] = remote.ExpiresAt
	}
	if remote.AccountID != "" {
		credentials["account_id"] = remote.AccountID
	}
	if remote.Email != "" {
		credentials["email"] = remote.Email
	}
	if remote.PlanType != "" {
		credentials["plan_type"] = remote.PlanType
	}
	return credentials
}

func (s *CPASyncService) importRemoteAccount(ctx context.Context, remote *cpaDownloadedAccount) (int64, string, error) {
	if remote == nil {
		return 0, "", errors.New("remote account is nil")
	}
	if remote.RefreshToken == "" && remote.AccessToken == "" {
		return 0, "", errors.New("remote account has no refresh_token or access_token")
	}

	name := buildImportedAccountName(remote)
	var (
		id  int64
		err error
	)
	if remote.RefreshToken != "" {
		id, err = s.db.InsertAccount(ctx, name, remote.RefreshToken, "")
	} else {
		id, err = s.db.InsertATAccount(ctx, name, remote.AccessToken, "")
	}
	if err != nil {
		return 0, "", err
	}

	credentials := buildRemoteCredentialPayload(remote)
	if len(credentials) > 0 {
		if err := s.db.UpdateCredentials(ctx, id, credentials); err != nil {
			return 0, "", err
		}
	}

	account := &auth.Account{
		DBID:         id,
		RefreshToken: remote.RefreshToken,
		AccessToken:  remote.AccessToken,
		AccountID:    remote.AccountID,
		Email:        remote.Email,
		PlanType:     remote.PlanType,
		ProxyURL:     "",
	}
	if remote.ExpiresAt != "" {
		if parsed, err := time.Parse(time.RFC3339, remote.ExpiresAt); err == nil {
			account.ExpiresAt = parsed
		}
	}
	if account.AccessToken == "" && !account.ExpiresAt.IsZero() {
		account.ExpiresAt = time.Time{}
	}
	s.store.AddAccount(account)

	return id, name, nil
}

func buildCredentialDelta(row *database.AccountRow, remote *cpaDownloadedAccount) map[string]interface{} {
	delta := make(map[string]interface{})
	if remote.RefreshToken != "" && row.GetCredential("refresh_token") != remote.RefreshToken {
		delta["refresh_token"] = remote.RefreshToken
	}
	if remote.AccessToken != "" && row.GetCredential("access_token") != remote.AccessToken {
		delta["access_token"] = remote.AccessToken
	}
	if remote.IDToken != "" && row.GetCredential("id_token") != remote.IDToken {
		delta["id_token"] = remote.IDToken
	}
	if remote.ExpiresAt != "" && row.GetCredential("expires_at") != remote.ExpiresAt {
		delta["expires_at"] = remote.ExpiresAt
	}
	if remote.AccountID != "" && row.GetCredential("account_id") != remote.AccountID {
		delta["account_id"] = remote.AccountID
	}
	if remote.Email != "" && row.GetCredential("email") != remote.Email {
		delta["email"] = remote.Email
	}
	if remote.PlanType != "" && row.GetCredential("plan_type") != remote.PlanType {
		delta["plan_type"] = remote.PlanType
	}
	return delta
}

func buildCPAAuthFileName(entry cpaExportEntry) string {
	base := entry.Email
	if base == "" {
		base = entry.AccountID
	}
	base = strings.ToLower(strings.TrimSpace(base))
	base = strings.NewReplacer("@", "_", "/", "_", "\\", "_", ":", "_").Replace(base)
	if base == "" {
		base = "account"
	}
	return fmt.Sprintf("%s-%d.json", base, time.Now().UTC().UnixNano())
}

func buildMultipartCPAUpload(name string, payload []byte) ([]byte, string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("name", name); err != nil {
		return nil, "", err
	}
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(payload); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), writer.FormDataContentType(), nil
}

func unwrapArray(value any) []map[string]any {
	switch typed := value.(type) {
	case []any:
		result := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if record := unwrapObject(item); record != nil {
				result = append(result, record)
			}
		}
		return result
	case map[string]any:
		for _, key := range []string{"data", "items", "auth_files", "files", "results"} {
			if nested, ok := typed[key]; ok {
				return unwrapArray(nested)
			}
		}
	}
	return nil
}

func unwrapObject(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"data", "item", "auth_file"} {
			if nested, ok := typed[key]; ok {
				if unwrapped := unwrapObject(nested); unwrapped != nil {
					return unwrapped
				}
			}
		}
		return typed
	case string:
		var nested map[string]any
		if json.Unmarshal([]byte(typed), &nested) == nil {
			return unwrapObject(nested)
		}
	}
	return nil
}

func firstString(obj map[string]any, keys ...string) string {
	if obj == nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := obj[key]; ok {
			switch typed := value.(type) {
			case string:
				return typed
			default:
				if encoded, err := json.Marshal(typed); err == nil {
					return strings.TrimSpace(string(encoded))
				}
			}
		}
	}
	return ""
}

func (h *Handler) GetCPASyncStatus(c *gin.Context) {
	if h.cpaSync == nil {
		writeError(c, http.StatusServiceUnavailable, "CPA sync service is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	status, err := h.cpaSync.Status(ctx)
	if err != nil {
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, status)
}

func (h *Handler) RunCPASync(c *gin.Context) {
	if h.cpaSync == nil {
		writeError(c, http.StatusServiceUnavailable, "CPA sync service is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	status, err := h.cpaSync.RunOnce(ctx)
	if err != nil {
		if errors.Is(err, errCPASyncBusy) {
			writeError(c, http.StatusConflict, err.Error())
			return
		}
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, status)
}

func (h *Handler) SwitchCPASyncMihomo(c *gin.Context) {
	if h.cpaSync == nil {
		writeError(c, http.StatusServiceUnavailable, "CPA sync service is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	status, err := h.cpaSync.ForceSwitch(ctx)
	if err != nil {
		if errors.Is(err, errCPASyncBusy) {
			writeError(c, http.StatusConflict, err.Error())
			return
		}
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, status)
}

func (h *Handler) TestCPASyncCPA(c *gin.Context) {
	if h.cpaSync == nil {
		writeError(c, http.StatusServiceUnavailable, "CPA sync service is unavailable")
		return
	}
	var req cpaSyncConnectionTestRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			writeError(c, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	status, err := h.cpaSync.TestCPA(ctx, &req)
	if err != nil {
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, status)
}

func (h *Handler) TestCPASyncMihomo(c *gin.Context) {
	if h.cpaSync == nil {
		writeError(c, http.StatusServiceUnavailable, "CPA sync service is unavailable")
		return
	}
	var req cpaSyncConnectionTestRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			writeError(c, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	status, err := h.cpaSync.TestMihomo(ctx, &req)
	if err != nil {
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, status)
}

func (h *Handler) ListCPASyncMihomoGroups(c *gin.Context) {
	if h.cpaSync == nil {
		writeError(c, http.StatusServiceUnavailable, "CPA sync service is unavailable")
		return
	}
	var req cpaSyncConnectionTestRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			writeError(c, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	groups, err := h.cpaSync.ListMihomoStrategyGroups(ctx, &req)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"groups": groups})
}
