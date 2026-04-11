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
	"github.com/codex2api/proxy"
	"github.com/gin-gonic/gin"
)

const (
	defaultCPASyncInterval    = 5 * time.Minute
	minCPASyncInterval        = 30 * time.Second
	maxCPASyncInterval        = 24 * time.Hour
	cpaSyncRequestTimeout     = 45 * time.Second
	cpaSyncStatusReadTimeout  = 15 * time.Second
	cpaSyncMaxResponseBody    = 2 * 1024 * 1024
	cpaSyncMaxRecentEvents    = 30
	defaultMihomoDelayTestURL = "https://cp.cloudflare.com/generate_204"
	cpaSwitchReasonThreshold  = "banned_delete_threshold"

	cpaErrorKindUsageLimitReached  = "usage_limit_reached"
	cpaErrorKindAccountDeactivated = "account_deactivated"
	cpaErrorKindTokenInvalidated   = "token_invalidated"
)

var errCPASyncBusy = errors.New("CPA sync is already running")

type requestValidationError struct {
	message string
}

func (e *requestValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func newRequestValidationError(err error) error {
	if err == nil {
		return nil
	}
	return &requestValidationError{message: err.Error()}
}

type CPASyncService struct {
	store       *auth.Store
	db          *database.DB
	client      *http.Client
	uploadProbe func(context.Context, *auth.Account) uploadCandidateProbeResult
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

type cpaUploadCandidate struct {
	DBID  int64
	Entry cpaExportEntry
}

type uploadCandidateProbeState string

const (
	uploadCandidateProbeActive       uploadCandidateProbeState = "active"
	uploadCandidateProbeUnauthorized uploadCandidateProbeState = "unauthorized"
	uploadCandidateProbeRateLimited  uploadCandidateProbeState = "rate_limited"
	uploadCandidateProbeUnknown      uploadCandidateProbeState = "unknown"
)

type uploadCandidateProbeResult struct {
	State   uploadCandidateProbeState
	Message string
}

type cpaRunMetrics struct {
	ProcessedErrors   int
	UploadedCount     int
	FirstErrorSummary string
	DeletedRemote     bool
	UploadedRemote    bool
}

type cpaLocalAccountCache struct {
	ctx    context.Context
	db     *database.DB
	rows   []*database.AccountRow
	loaded bool
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
	service := &CPASyncService{
		store:    store,
		db:       db,
		client:   &http.Client{Timeout: cpaSyncRequestTimeout},
		stopCh:   make(chan struct{}),
		configCh: make(chan struct{}, 1),
	}
	service.uploadProbe = service.probeUploadCandidate
	return service
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
	statusCtx, cancel := context.WithTimeout(context.Background(), cpaSyncStatusReadTimeout)
	defer cancel()
	return s.Status(statusCtx)
}

func (s *CPASyncService) persistState(ctx context.Context, state *database.CPASyncState) error {
	if err := s.db.UpdateCPASyncState(ctx, state); err != nil {
		return fmt.Errorf("persist CPA sync state: %w", err)
	}
	return nil
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
	sanitizedState := sanitizeCPASyncStateForAPI(state)

	return &cpaSyncStatusResponse{
		Config:           settings.summary(),
		State:            sanitizedState,
		CPATestStatus:    sanitizedState.CPATestStatus,
		MihomoTestStatus: sanitizedState.MihomoTestStatus,
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
		if err := s.persistState(ctx, state); err != nil {
			s.running.Store(false)
			return nil, err
		}
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
	if err := s.persistState(ctx, state); err != nil {
		s.running.Store(false)
		return nil, err
	}
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
	if err := s.persistState(ctx, state); err != nil {
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
	if err := s.persistState(ctx, state); err != nil {
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
		if err := s.persistState(ctx, state); err != nil {
			s.running.Store(false)
			return nil, err
		}
		return s.statusAfterRun(ctx)
	}
	if missing := settings.missingCPAConfig(); len(missing) > 0 {
		s.recordStateFailure(state, "skipped", fmt.Sprintf("missing config: %s", strings.Join(missing, ", ")))
		if err := s.persistState(ctx, state); err != nil {
			s.running.Store(false)
			return nil, err
		}
		return s.statusAfterRun(ctx)
	}

	records, err := s.listCPAAuthFiles(ctx, settings)
	if err != nil {
		s.recordStateFailure(state, "error", fmt.Sprintf("list CPA auth files failed: %v", err))
		if persistErr := s.persistState(ctx, state); persistErr != nil {
			s.running.Store(false)
			return nil, persistErr
		}
		return s.statusAfterRun(ctx)
	}

	metrics := &cpaRunMetrics{}
	downloadedTokens := make(map[string]struct{})
	records = s.processCPAErrorRecords(ctx, settings, state, records, downloadedTokens, metrics)
	effectiveRecords := filterEffectiveCPAAuthFileRecords(records)
	state.LastCPAAccountCount = len(effectiveRecords)

	targetCount := resolveCPATargetCount(settings)
	effectiveRecords = s.uploadMissingCPAAccounts(ctx, settings, state, effectiveRecords, downloadedTokens, targetCount, metrics)
	records, effectiveRecords = s.refreshCPARecordsAfterRemoteChanges(ctx, settings, state, records, effectiveRecords, metrics)
	state.LastCPAAccountCount = len(effectiveRecords)
	s.syncCPAAccountCountSnapshot(state, state.LastCPAAccountCount, len(records), time.Now().UTC())

	if eligible, reason := s.shouldAutoSwitchForBannedDeleteThreshold(state, settings); eligible {
		if err := s.switchMihomo(ctx, settings, state, cpaSwitchReasonThreshold); err != nil {
			s.recordAction(state, "switch", "error", fmt.Sprintf("switch failed: %v", err), settings.MihomoStrategyGroup)
			metrics.setFirstErrorf("switch Mihomo failed: %v", err)
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
	if metrics.FirstErrorSummary != "" {
		state.LastRunStatus = "partial_success"
		state.LastErrorSummary = metrics.FirstErrorSummary
	} else {
		state.LastRunStatus = "success"
		state.LastErrorSummary = ""
	}
	state.LastRunSummary = fmt.Sprintf("trigger=%s, cpa_count=%d, processed_errors=%d, uploaded=%d", trigger, state.LastCPAAccountCount, metrics.ProcessedErrors, metrics.UploadedCount)
	if err := s.persistState(ctx, state); err != nil {
		s.running.Store(false)
		return nil, err
	}

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

	cpaBaseURL, err := validateExternalServiceBaseURL(ctx, raw.CPABaseURL, "cpa_base_url")
	if err != nil {
		log.Printf("[CPA Sync] 忽略无效 cpa_base_url: %v", err)
		cpaBaseURL = ""
	}
	mihomoBaseURL, err := validateExternalServiceBaseURL(ctx, raw.MihomoBaseURL, "mihomo_base_url")
	if err != nil {
		log.Printf("[CPA Sync] 忽略无效 mihomo_base_url: %v", err)
		mihomoBaseURL = ""
	}
	mihomoDelayTestURL, err := validateExternalTargetURL(ctx, raw.MihomoDelayTestURL, "mihomo_delay_test_url")
	if err != nil {
		log.Printf("[CPA Sync] 忽略无效 mihomo_delay_test_url: %v", err)
		mihomoDelayTestURL = ""
	}

	return &cpaSyncSettings{
		Enabled:             raw.CPASyncEnabled,
		CPABaseURL:          cpaBaseURL,
		CPAAdminKey:         strings.TrimSpace(raw.CPAAdminKey),
		MinAccounts:         raw.CPAMinAccounts,
		MaxAccounts:         raw.CPAMaxAccounts,
		MaxUploadsPerHour:   raw.CPAMaxUploadsPerHour,
		SwitchAfterUploads:  raw.CPASwitchAfterUploads,
		Interval:            time.Duration(intervalSeconds) * time.Second,
		MihomoBaseURL:       mihomoBaseURL,
		MihomoSecret:        strings.TrimSpace(raw.MihomoSecret),
		MihomoStrategyGroup: strings.TrimSpace(raw.MihomoStrategyGroup),
		MihomoDelayTestURL:  mihomoDelayTestURL,
		MihomoDelayTimeout:  delayTimeout,
	}, nil
}

func (m *cpaRunMetrics) setFirstErrorf(format string, args ...any) {
	if m == nil || m.FirstErrorSummary != "" {
		return
	}
	m.FirstErrorSummary = fmt.Sprintf(format, args...)
}

func (m *cpaRunMetrics) appendErrorf(format string, args ...any) {
	if m == nil {
		return
	}
	message := fmt.Sprintf(format, args...)
	if m.FirstErrorSummary == "" {
		m.FirstErrorSummary = message
		return
	}
	m.FirstErrorSummary = fmt.Sprintf("%s; %s", m.FirstErrorSummary, message)
}

func (c *cpaLocalAccountCache) load() ([]*database.AccountRow, error) {
	if c == nil {
		return nil, errors.New("local account cache is nil")
	}
	if c.loaded {
		return c.rows, nil
	}
	rows, err := c.db.ListAllAccounts(c.ctx)
	if err != nil {
		return nil, err
	}
	c.rows = rows
	c.loaded = true
	return c.rows, nil
}

func (c *cpaLocalAccountCache) invalidate() {
	if c == nil {
		return
	}
	c.rows = nil
	c.loaded = false
}

func resolveCPATargetCount(settings *cpaSyncSettings) int {
	if settings == nil {
		return 0
	}
	targetCount := settings.MinAccounts
	if settings.MaxAccounts > 0 && (targetCount == 0 || settings.MaxAccounts < targetCount) {
		targetCount = settings.MaxAccounts
	}
	if targetCount < 0 {
		return 0
	}
	return targetCount
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
		sanitized, err := validateExternalServiceBaseURL(ctx, *req.CPABaseURL, "cpa_base_url")
		if err != nil {
			return nil, newRequestValidationError(err)
		}
		settings.CPABaseURL = sanitized
	}
	if req.CPAAdminKey != nil {
		settings.CPAAdminKey = strings.TrimSpace(*req.CPAAdminKey)
	}
	if req.MihomoBaseURL != nil {
		sanitized, err := validateExternalServiceBaseURL(ctx, *req.MihomoBaseURL, "mihomo_base_url")
		if err != nil {
			return nil, newRequestValidationError(err)
		}
		settings.MihomoBaseURL = sanitized
	}
	if req.MihomoSecret != nil {
		settings.MihomoSecret = strings.TrimSpace(*req.MihomoSecret)
	}
	if req.MihomoStrategyGroup != nil {
		settings.MihomoStrategyGroup = strings.TrimSpace(*req.MihomoStrategyGroup)
	}
	if req.MihomoDelayTestURL != nil {
		sanitized, err := validateExternalTargetURL(ctx, *req.MihomoDelayTestURL, "mihomo_delay_test_url")
		if err != nil {
			return nil, newRequestValidationError(err)
		}
		settings.MihomoDelayTestURL = sanitized
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

func (s *CPASyncService) processCPAErrorRecords(
	ctx context.Context,
	settings *cpaSyncSettings,
	state *database.CPASyncState,
	records []cpaAuthFileRecord,
	downloadedTokens map[string]struct{},
	metrics *cpaRunMetrics,
) []cpaAuthFileRecord {
	cache := &cpaLocalAccountCache{ctx: ctx, db: s.db}
	remainingRecords := append([]cpaAuthFileRecord{}, records...)
	for _, record := range records {
		kind := detectCPAErrorKind(record.StatusMessage)
		if kind == "" {
			continue
		}
		metrics.ProcessedErrors++

		remote, downloadErr := s.downloadCPAAuthFile(ctx, settings, record.Name)
		if downloadErr != nil {
			s.recordAction(state, "download", "error", fmt.Sprintf("download failed: %v", downloadErr), record.Name)
			metrics.setFirstErrorf("download %s failed: %v", record.Name, downloadErr)
		} else {
			addDownloadedAccountTokens(downloadedTokens, remote)
			s.reconcileCPAErrorRecord(ctx, record, kind, remote, cache, state, metrics)
		}

		if err := s.deleteCPAAuthFile(ctx, settings, record.Name); err != nil {
			s.recordAction(state, "delete", "error", fmt.Sprintf("delete failed: %v", err), record.Name)
			metrics.setFirstErrorf("delete %s failed: %v", record.Name, err)
			continue
		}

		metrics.DeletedRemote = true
		if isCPABannedErrorKind(kind) {
			// HourlyUploadCount is kept for API compatibility, but its effective
			// meaning is now "banned deletes in the current switch window".
			state.HourlyUploadCount++
		}
		s.recordAction(state, "delete", "success", "deleted remote CPA account", record.Name)
		remainingRecords = removeCPAAuthFileRecordByName(remainingRecords, record.Name)
	}
	return remainingRecords
}

func (s *CPASyncService) reconcileCPAErrorRecord(
	ctx context.Context,
	record cpaAuthFileRecord,
	kind string,
	remote *cpaDownloadedAccount,
	cache *cpaLocalAccountCache,
	state *database.CPASyncState,
	metrics *cpaRunMetrics,
) {
	if remote == nil {
		return
	}
	localRows, err := cache.load()
	if err != nil {
		s.recordAction(state, "reconcile", "error", fmt.Sprintf("load local accounts failed: %v", err), record.Name)
		metrics.setFirstErrorf("load local accounts failed: %v", err)
		return
	}

	if matched, matchKind := matchLocalAccount(localRows, remote); matched != nil {
		s.applyCPAErrorToMatchedAccount(ctx, record, kind, remote, matched, matchKind, cache, state, metrics)
		return
	}

	if isCPABannedErrorKind(kind) {
		s.recordAction(state, "reconcile", "warning", "no unique local account match found", record.Name)
		return
	}

	if localAccountCandidateCount(localRows, remote) != 0 {
		s.recordAction(state, "reconcile", "warning", "no unique local account match found", record.Name)
		return
	}

	importedID, importName, err := s.importRemoteAccount(ctx, remote)
	if err != nil {
		s.recordAction(state, "reconcile", "error", fmt.Sprintf("create local account failed: %v", err), record.Name)
		metrics.setFirstErrorf("create local account failed: %v", err)
		return
	}
	s.recordAction(state, "reconcile", "success", fmt.Sprintf("created local account %s from CPA", importName), record.Name)
	s.db.InsertAccountEventAsync(importedID, "added", "cpa_sync")
	if isCPARateLimitedErrorKind(kind) {
		s.markRateLimited(importedID, record.StatusMessage)
	}
	cache.invalidate()
}

func (s *CPASyncService) applyCPAErrorToMatchedAccount(
	ctx context.Context,
	record cpaAuthFileRecord,
	kind string,
	remote *cpaDownloadedAccount,
	matched *database.AccountRow,
	matchKind string,
	cache *cpaLocalAccountCache,
	state *database.CPASyncState,
	metrics *cpaRunMetrics,
) {
	delta := buildCredentialDelta(matched, remote)
	if len(delta) > 0 {
		if err := s.db.UpdateCredentials(ctx, matched.ID, delta); err != nil {
			s.recordAction(state, "reconcile", "error", fmt.Sprintf("update local credentials failed: %v", err), record.Name)
			metrics.setFirstErrorf("update local credentials failed: %v", err)
			return
		}
		s.applyInMemoryCredentials(matched.ID, remote)
		s.recordAction(state, "reconcile", "success", fmt.Sprintf("updated local credentials via %s", matchKind), record.Name)
		cache.invalidate()
	}
	if isCPABannedErrorKind(kind) {
		s.markUnauthorized(matched.ID)
		return
	}
	if isCPARateLimitedErrorKind(kind) {
		s.markRateLimited(matched.ID, record.StatusMessage)
	}
}

func (s *CPASyncService) uploadMissingCPAAccounts(
	ctx context.Context,
	settings *cpaSyncSettings,
	state *database.CPASyncState,
	effectiveRecords []cpaAuthFileRecord,
	downloadedTokens map[string]struct{},
	targetCount int,
	metrics *cpaRunMetrics,
) []cpaAuthFileRecord {
	if len(effectiveRecords) >= targetCount {
		return effectiveRecords
	}

	remaining := targetCount - len(effectiveRecords)
	if settings.MaxAccounts > 0 {
		room := settings.MaxAccounts - len(effectiveRecords)
		if room < remaining {
			remaining = room
		}
	}
	if remaining <= 0 {
		return effectiveRecords
	}

	candidates, err := s.selectVerifiedUploadCandidates(ctx, effectiveRecords, downloadedTokens, remaining, state)
	if err != nil {
		s.recordAction(state, "upload", "error", fmt.Sprintf("select candidates failed: %v", err), "")
		metrics.setFirstErrorf("select upload candidates failed: %v", err)
		return effectiveRecords
	}

	for _, candidate := range candidates {
		name := buildCPAAuthFileName(candidate.Entry)
		if err := s.uploadCPAAuthFile(ctx, settings, name, candidate.Entry); err != nil {
			s.recordAction(state, "upload", "error", fmt.Sprintf("upload failed: %v", err), name)
			metrics.setFirstErrorf("upload %s failed: %v", name, err)
			continue
		}
		metrics.UploadedRemote = true
		metrics.UploadedCount++
		effectiveRecords = append(effectiveRecords, cpaAuthFileRecord{
			Name:   name,
			Email:  candidate.Entry.Email,
			Status: "active",
		})
		s.recordAction(state, "upload", "success", fmt.Sprintf("uploaded %s", candidate.Entry.Email), name)
	}

	return effectiveRecords
}

func (s *CPASyncService) refreshCPARecordsAfterRemoteChanges(
	ctx context.Context,
	settings *cpaSyncSettings,
	state *database.CPASyncState,
	records []cpaAuthFileRecord,
	effectiveRecords []cpaAuthFileRecord,
	metrics *cpaRunMetrics,
) ([]cpaAuthFileRecord, []cpaAuthFileRecord) {
	if metrics == nil || (!metrics.DeletedRemote && !metrics.UploadedRemote) {
		return records, effectiveRecords
	}

	refreshed, err := s.listCPAAuthFiles(ctx, settings)
	if err != nil {
		s.recordAction(state, "run", "warning", fmt.Sprintf("final CPA auth file recount failed: %v", err), "")
		metrics.appendErrorf("final CPA auth file recount failed: %v", err)
		return records, effectiveRecords
	}

	return refreshed, filterEffectiveCPAAuthFileRecords(refreshed)
}

func addDownloadedAccountTokens(downloadedTokens map[string]struct{}, remote *cpaDownloadedAccount) {
	if downloadedTokens == nil || remote == nil {
		return
	}
	if remote.RefreshToken != "" {
		downloadedTokens["rt:"+remote.RefreshToken] = struct{}{}
	}
	if remote.AccessToken != "" {
		downloadedTokens["at:"+remote.AccessToken] = struct{}{}
	}
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

func (c *cpaSyncSettings) bannedDeleteSwitchWindow() time.Duration {
	if c == nil || c.SwitchAfterUploads <= 0 {
		return 0
	}
	return time.Duration(c.SwitchAfterUploads) * time.Minute
}

func (c *cpaSyncSettings) effectiveSwitchWindow() time.Duration {
	if window := c.bannedDeleteSwitchWindow(); window > 0 {
		return window
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
		windowDuration = settings.effectiveSwitchWindow()
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

func (s *CPASyncService) shouldAutoSwitchForBannedDeleteThreshold(state *database.CPASyncState, settings *cpaSyncSettings) (bool, string) {
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

func (s *CPASyncService) probeUploadCandidate(ctx context.Context, account *auth.Account) uploadCandidateProbeResult {
	if s == nil || account == nil {
		return uploadCandidateProbeResult{State: uploadCandidateProbeUnknown, Message: "account is nil"}
	}

	payload := buildTestPayload(s.store.GetTestModel())
	proxyOverride := s.store.ResolveMaintenanceProxy(ctx, account)
	resp, err := proxy.ExecuteRequest(ctx, account, payload, "", proxyOverride, "", nil, nil)
	if err != nil {
		return uploadCandidateProbeResult{State: uploadCandidateProbeUnknown, Message: err.Error()}
	}
	defer resp.Body.Close()

	usagePct, hasUsage := proxy.ParseCodexUsageHeaders(resp, account)
	if hasUsage {
		s.store.PersistUsageSnapshot(account, usagePct)
	}
	_, _ = io.Copy(io.Discard, resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		s.store.ReportRequestSuccess(account, 0)
		if !hasUsage || usagePct < 100 {
			s.store.ClearCooldown(account)
		}
		return uploadCandidateProbeResult{State: uploadCandidateProbeActive}
	case http.StatusUnauthorized:
		s.store.ReportRequestFailure(account, "client", 0)
		return uploadCandidateProbeResult{State: uploadCandidateProbeUnauthorized, Message: "unauthorized"}
	case http.StatusTooManyRequests:
		s.store.ReportRequestFailure(account, "client", 0)
		return uploadCandidateProbeResult{State: uploadCandidateProbeRateLimited, Message: "rate_limited"}
	default:
		if resp.StatusCode >= 500 {
			s.store.ReportRequestFailure(account, "server", 0)
		} else if resp.StatusCode >= 400 {
			s.store.ReportRequestFailure(account, "client", 0)
		}
		return uploadCandidateProbeResult{
			State:   uploadCandidateProbeUnknown,
			Message: fmt.Sprintf("probe returned HTTP %d", resp.StatusCode),
		}
	}
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
		log.Printf("[CPA Sync] build CPA test request failed: %v", err)
		return database.ConnectionTestStatus{
			Ok:       boolPtr(false),
			Message:  "build CPA request failed",
			TestedAt: testedAt,
			Details:  map[string]any{},
		}
	}
	s.applyCPAHeaders(req, settings.CPAAdminKey)
	resp, err := s.client.Do(req)
	if err != nil {
		log.Printf("[CPA Sync] CPA test request failed: %v", err)
		return database.ConnectionTestStatus{
			Ok:       boolPtr(false),
			Message:  "request CPA failed",
			TestedAt: testedAt,
			Details:  map[string]any{"duration_ms": time.Since(start).Milliseconds()},
		}
	}
	defer resp.Body.Close()
	body, readErr := readBodyLimited(resp.Body, cpaSyncMaxResponseBody)
	durationMs := time.Since(start).Milliseconds()
	httpStatus := resp.StatusCode
	if readErr != nil {
		log.Printf("[CPA Sync] read CPA test response failed: %v", readErr)
		return database.ConnectionTestStatus{
			Ok:         boolPtr(false),
			Message:    "read CPA response failed",
			HTTPStatus: intPtr(httpStatus),
			TestedAt:   testedAt,
			Details:    map[string]any{"duration_ms": durationMs},
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("[CPA Sync] CPA test upstream failed status=%d body=%s", resp.StatusCode, truncate(strings.TrimSpace(string(body)), 500))
		return database.ConnectionTestStatus{
			Ok:         boolPtr(false),
			Message:    fmt.Sprintf("HTTP %d", resp.StatusCode),
			HTTPStatus: intPtr(httpStatus),
			TestedAt:   testedAt,
			Details:    map[string]any{"duration_ms": durationMs},
		}
	}
	records, err := parseCPAAuthFiles(body)
	if err != nil {
		log.Printf("[CPA Sync] parse CPA test response failed: %v", err)
		return database.ConnectionTestStatus{
			Ok:         boolPtr(false),
			Message:    "parse CPA response failed",
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
		log.Printf("[CPA Sync] build Mihomo test request failed: %v", err)
		return database.ConnectionTestStatus{
			Ok:       boolPtr(false),
			Message:  "build Mihomo request failed",
			TestedAt: testedAt,
			Details:  map[string]any{},
		}
	}
	req.Header.Set("Authorization", "Bearer "+settings.MihomoSecret)
	resp, err := s.client.Do(req)
	if err != nil {
		log.Printf("[CPA Sync] Mihomo test request failed: %v", err)
		return database.ConnectionTestStatus{
			Ok:       boolPtr(false),
			Message:  "request Mihomo failed",
			TestedAt: testedAt,
			Details:  map[string]any{"duration_ms": time.Since(start).Milliseconds()},
		}
	}
	defer resp.Body.Close()
	body, readErr := readBodyLimited(resp.Body, cpaSyncMaxResponseBody)
	durationMs := time.Since(start).Milliseconds()
	httpStatus := resp.StatusCode
	if readErr != nil {
		log.Printf("[CPA Sync] read Mihomo test response failed: %v", readErr)
		return database.ConnectionTestStatus{
			Ok:         boolPtr(false),
			Message:    "read Mihomo response failed",
			HTTPStatus: intPtr(httpStatus),
			TestedAt:   testedAt,
			Details:    map[string]any{"duration_ms": durationMs},
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("[CPA Sync] Mihomo test upstream failed status=%d body=%s", resp.StatusCode, truncate(strings.TrimSpace(string(body)), 500))
		return database.ConnectionTestStatus{
			Ok:         boolPtr(false),
			Message:    fmt.Sprintf("HTTP %d", resp.StatusCode),
			HTTPStatus: intPtr(httpStatus),
			TestedAt:   testedAt,
			Details:    map[string]any{"duration_ms": durationMs},
		}
	}
	var detail mihomoSelectorDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		log.Printf("[CPA Sync] parse Mihomo test response failed: %v", err)
		return database.ConnectionTestStatus{
			Ok:         boolPtr(false),
			Message:    "parse Mihomo response failed",
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
	body, readErr := readBodyLimited(resp.Body, cpaSyncMaxResponseBody)
	if readErr != nil {
		return nil, fmt.Errorf("read delay response failed: %w", readErr)
	}
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
	body, readErr := readBodyLimited(resp.Body, cpaSyncMaxResponseBody)
	if readErr != nil {
		return nil, fmt.Errorf("read CPA auth files failed: %w", readErr)
	}
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
	body, readErr := readBodyLimited(resp.Body, cpaSyncMaxResponseBody)
	if readErr != nil {
		return nil, fmt.Errorf("read CPA auth file failed: %w", readErr)
	}
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
	body, readErr := readBodyLimited(resp.Body, cpaSyncMaxResponseBody)
	if readErr != nil {
		return fmt.Errorf("read CPA delete response failed: %w", readErr)
	}
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
		body, readErr := readBodyLimited(resp.Body, cpaSyncMaxResponseBody)
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("read CPA upload response failed: %w", readErr)
			continue
		}
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

func (s *CPASyncService) selectVerifiedUploadCandidates(
	ctx context.Context,
	records []cpaAuthFileRecord,
	downloadedTokens map[string]struct{},
	limit int,
	state *database.CPASyncState,
) ([]cpaUploadCandidate, error) {
	if limit <= 0 {
		return nil, nil
	}

	excluded := make(map[int64]struct{})
	selected := make([]cpaUploadCandidate, 0, limit)
	for len(selected) < limit {
		batch, err := s.selectUploadCandidates(ctx, records, downloadedTokens, limit-len(selected), excluded)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}

		for _, candidate := range batch {
			excluded[candidate.DBID] = struct{}{}
			account := s.runtimeAccountByDBID(candidate.DBID)
			if account == nil {
				continue
			}

			probe := s.uploadProbe(ctx, account)
			switch probe.State {
			case uploadCandidateProbeUnauthorized:
				s.markUnauthorized(candidate.DBID)
				s.recordAction(state, "upload", "warning", fmt.Sprintf("skip banned account %s during upload selection", candidate.Entry.Email), candidate.Entry.Email)
				continue
			case uploadCandidateProbeRateLimited:
				s.markRateLimited(candidate.DBID, probe.Message)
				s.recordAction(state, "upload", "warning", fmt.Sprintf("skip rate limited account %s during upload selection", candidate.Entry.Email), candidate.Entry.Email)
				continue
			case uploadCandidateProbeUnknown:
				if probe.Message != "" {
					s.recordAction(state, "upload", "warning", fmt.Sprintf("probe account %s inconclusive: %s", candidate.Entry.Email, probe.Message), candidate.Entry.Email)
				}
			}

			selected = append(selected, candidate)
			if len(selected) >= limit {
				break
			}
		}
	}

	return selected, nil
}

func (s *CPASyncService) selectUploadCandidates(
	ctx context.Context,
	records []cpaAuthFileRecord,
	downloadedTokens map[string]struct{},
	limit int,
	excluded map[int64]struct{},
) ([]cpaUploadCandidate, error) {
	rows, err := s.db.ListAllAccounts(ctx)
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

	selected := make([]cpaUploadCandidate, 0, limit)
	for _, row := range rows {
		if len(selected) >= limit {
			break
		}
		if _, skip := excluded[row.ID]; skip {
			continue
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
		selected = append(selected, cpaUploadCandidate{
			DBID: row.ID,
			Entry: cpaExportEntry{
				Type:         "codex",
				Email:        email,
				Expired:      row.GetCredential("expires_at"),
				IDToken:      row.GetCredential("id_token"),
				AccountID:    row.GetCredential("account_id"),
				AccessToken:  accessToken,
				LastRefresh:  row.UpdatedAt.UTC().Format(time.RFC3339),
				RefreshToken: refreshToken,
			},
		})
		existingEmails[strings.ToLower(email)] = struct{}{}
		downloadedTokens["rt:"+refreshToken] = struct{}{}
		if accessToken != "" {
			downloadedTokens["at:"+accessToken] = struct{}{}
		}
	}
	return selected, nil
}

func (s *CPASyncService) runtimeAccountByDBID(dbID int64) *auth.Account {
	if s == nil || dbID == 0 {
		return nil
	}
	for _, account := range s.store.Accounts() {
		if account != nil && account.DBID == dbID {
			return account
		}
	}
	return nil
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
		if reason == cpaSwitchReasonThreshold {
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
	respBody, readErr := readBodyLimited(resp.Body, cpaSyncMaxResponseBody)
	if readErr != nil {
		return fmt.Errorf("read Mihomo switch response failed: %w", readErr)
	}
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
	body, readErr := readBodyLimited(resp.Body, cpaSyncMaxResponseBody)
	if readErr != nil {
		return nil, fmt.Errorf("read Mihomo selector failed: %w", readErr)
	}
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
	body, readErr := readBodyLimited(resp.Body, cpaSyncMaxResponseBody)
	if readErr != nil {
		return nil, fmt.Errorf("read Mihomo proxies failed: %w", readErr)
	}
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

func sanitizeCPASyncPublicMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}

	if strings.Contains(message, "; ") {
		parts := strings.Split(message, "; ")
		for i, part := range parts {
			parts[i] = sanitizeCPASyncPublicMessage(part)
		}
		return strings.Join(parts, "; ")
	}

	if strings.HasPrefix(message, "missing config: ") {
		return message
	}
	if strings.HasPrefix(message, "HTTP ") {
		if idx := strings.Index(message, ":"); idx > 0 {
			return strings.TrimSpace(message[:idx])
		}
		return message
	}
	if idx := strings.Index(message, " inconclusive:"); idx >= 0 {
		return strings.TrimSpace(message[:idx+len(" inconclusive")])
	}
	if idx := strings.Index(message, " failed:"); idx >= 0 {
		return strings.TrimSpace(message[:idx+len(" failed")])
	}

	return message
}

func sanitizeConnectionTestStatusForAPI(status database.ConnectionTestStatus) database.ConnectionTestStatus {
	status = normalizeConnectionTestStatus(status)
	status.Message = sanitizeCPASyncPublicMessage(status.Message)
	if status.Details != nil {
		cloned := make(map[string]any, len(status.Details))
		for key, value := range status.Details {
			cloned[key] = value
		}
		status.Details = cloned
	}
	return status
}

func sanitizeCPASyncStateForAPI(state *database.CPASyncState) *database.CPASyncState {
	if state == nil {
		return nil
	}

	cloned := *state
	cloned.LastRunSummary = sanitizeCPASyncPublicMessage(state.LastRunSummary)
	cloned.LastErrorSummary = sanitizeCPASyncPublicMessage(state.LastErrorSummary)
	cloned.CPATestStatus = sanitizeConnectionTestStatusForAPI(state.CPATestStatus)
	cloned.MihomoTestStatus = sanitizeConnectionTestStatusForAPI(state.MihomoTestStatus)

	if len(state.RecentActions) > 0 {
		cloned.RecentActions = make([]database.CPASyncAction, len(state.RecentActions))
		for i, action := range state.RecentActions {
			cloned.RecentActions[i] = action
			cloned.RecentActions[i].Message = sanitizeCPASyncPublicMessage(action.Message)
		}
	} else {
		cloned.RecentActions = []database.CPASyncAction{}
	}

	return &cloned
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
		if firstString(errorObj, "type") == cpaErrorKindUsageLimitReached {
			return cpaErrorKindUsageLimitReached
		}
		if firstString(errorObj, "code") == cpaErrorKindAccountDeactivated {
			return cpaErrorKindAccountDeactivated
		}
		if firstString(errorObj, "code") == cpaErrorKindTokenInvalidated {
			return cpaErrorKindTokenInvalidated
		}
	}
	lower := strings.ToLower(statusMessage)
	switch {
	case strings.Contains(lower, cpaErrorKindUsageLimitReached):
		return cpaErrorKindUsageLimitReached
	case strings.Contains(lower, cpaErrorKindAccountDeactivated):
		return cpaErrorKindAccountDeactivated
	case strings.Contains(lower, cpaErrorKindTokenInvalidated),
		strings.Contains(lower, "authentication token has been invalidated"),
		strings.Contains(lower, "signing in again"):
		return cpaErrorKindTokenInvalidated
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
	if firstString(errorObj, "type") != cpaErrorKindUsageLimitReached {
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

func isCPABannedErrorKind(kind string) bool {
	switch kind {
	case cpaErrorKindAccountDeactivated, cpaErrorKindTokenInvalidated:
		return true
	default:
		return false
	}
}

func isCPARateLimitedErrorKind(kind string) bool {
	return kind == cpaErrorKindUsageLimitReached
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
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
			if cleanupErr := s.db.SetError(cleanupCtx, id, "deleted"); cleanupErr != nil {
				log.Printf("[CPA Sync] rollback imported account %d failed: %v", id, cleanupErr)
			}
			cleanupCancel()
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

func readBodyLimited(body io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = cpaSyncMaxResponseBody
	}
	limited := io.LimitReader(body, maxBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maxBytes {
		return nil, fmt.Errorf("response body too large: limit=%d", maxBytes)
	}
	return payload, nil
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
		writeLoggedInternalError(c, "获取 CPA 联动状态失败", err)
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
		writeLoggedInternalError(c, "执行 CPA 联动失败", err)
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
		writeLoggedInternalError(c, "切换 Mihomo 节点失败", err)
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
		var validationErr *requestValidationError
		if errors.As(err, &validationErr) {
			writeError(c, http.StatusBadRequest, validationErr.Error())
			return
		}
		writeLoggedInternalError(c, "测试 CPA 连接失败", err)
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
		var validationErr *requestValidationError
		if errors.As(err, &validationErr) {
			writeError(c, http.StatusBadRequest, validationErr.Error())
			return
		}
		writeLoggedInternalError(c, "测试 Mihomo 连接失败", err)
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
		var validationErr *requestValidationError
		if errors.As(err, &validationErr) {
			writeError(c, http.StatusBadRequest, validationErr.Error())
			return
		}
		writeLoggedInternalError(c, "加载 Mihomo 策略组失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"groups": groups})
}
