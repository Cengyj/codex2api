package auth

import (
	"context"
	"log"
	"sync/atomic"
	"time"
)

const (
	defaultRefreshScanIntervalSeconds        = 120
	defaultRefreshPreExpireSeconds           = 300
	defaultRefreshMaxConcurrency             = 10
	defaultRefreshOnImportConcurrency        = 10
	defaultUsageProbeStaleAfterSeconds       = 600
	defaultUsageProbeMaxConcurrency          = 4
	defaultRecoveryProbeMinIntervalSeconds   = 1800
	defaultRecoveryProbeMaxConcurrency       = 2
	defaultImportRefreshWorkerCount          = 32
	defaultImportRefreshAcquireRetryInterval = 100 * time.Millisecond
)

type RefreshSchedulerStatus struct {
	Running        bool      `json:"running"`
	TotalAccounts  int       `json:"total_accounts"`
	TargetAccounts int       `json:"target_accounts"`
	Processed      int       `json:"processed"`
	Success        int       `json:"success"`
	Failure        int       `json:"failure"`
	NextScanAt     time.Time `json:"next_scan_at"`
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
}

func normalizeRefreshScanIntervalSeconds(v int) time.Duration {
	if v <= 0 {
		v = defaultRefreshScanIntervalSeconds
	}
	if v < 15 {
		v = 15
	}
	if v > 86400 {
		v = 86400
	}
	return time.Duration(v) * time.Second
}

func normalizeRefreshPreExpireSeconds(v int) time.Duration {
	if v <= 0 {
		v = defaultRefreshPreExpireSeconds
	}
	if v < 30 {
		v = 30
	}
	if v > 86400 {
		v = 86400
	}
	return time.Duration(v) * time.Second
}

func normalizeRefreshMaxConcurrency(v int) int {
	if v <= 0 {
		v = defaultRefreshMaxConcurrency
	}
	if v > 200 {
		v = 200
	}
	return v
}

func normalizeRefreshOnImportConcurrency(v int) int {
	if v <= 0 {
		v = defaultRefreshOnImportConcurrency
	}
	if v > 100 {
		v = 100
	}
	return v
}

func normalizeUsageProbeStaleAfterSeconds(v int) time.Duration {
	if v <= 0 {
		v = defaultUsageProbeStaleAfterSeconds
	}
	if v < 60 {
		v = 60
	}
	if v > 86400 {
		v = 86400
	}
	return time.Duration(v) * time.Second
}

func normalizeUsageProbeMaxConcurrency(v int) int {
	if v <= 0 {
		v = defaultUsageProbeMaxConcurrency
	}
	if v > 100 {
		v = 100
	}
	return v
}

func normalizeRecoveryProbeMinIntervalSeconds(v int) time.Duration {
	if v <= 0 {
		v = defaultRecoveryProbeMinIntervalSeconds
	}
	if v < 60 {
		v = 60
	}
	if v > 7*24*3600 {
		v = 7 * 24 * 3600
	}
	return time.Duration(v) * time.Second
}

func normalizeRecoveryProbeMaxConcurrency(v int) int {
	if v <= 0 {
		v = defaultRecoveryProbeMaxConcurrency
	}
	if v > 50 {
		v = 50
	}
	return v
}

func (s *Store) notifyBackgroundRefreshLoop() {
	if s == nil || s.backgroundRefreshWakeCh == nil {
		return
	}
	s.scheduleNextRefreshScan(time.Now().Add(s.GetRefreshScanInterval()))
	select {
	case s.backgroundRefreshWakeCh <- struct{}{}:
	default:
	}
}

func (s *Store) scheduleNextRefreshScan(at time.Time) {
	if s == nil {
		return
	}
	if at.IsZero() {
		atomic.StoreInt64(&s.nextRefreshScanAt, 0)
		return
	}
	atomic.StoreInt64(&s.nextRefreshScanAt, at.UnixNano())
}

func (s *Store) beginRefreshCycle(totalAccounts, targetAccounts int) {
	if s == nil {
		return
	}
	now := time.Now()
	s.refreshCycleRunning.Store(true)
	atomic.StoreInt64(&s.refreshCycleStartedAt, now.UnixNano())
	atomic.StoreInt64(&s.refreshCycleFinishedAt, 0)
	atomic.StoreInt64(&s.refreshCycleTotalAccounts, int64(totalAccounts))
	atomic.StoreInt64(&s.refreshCycleTargetAccounts, int64(targetAccounts))
	atomic.StoreInt64(&s.refreshCycleProcessed, 0)
	atomic.StoreInt64(&s.refreshCycleSuccess, 0)
	atomic.StoreInt64(&s.refreshCycleFailure, 0)
}

func (s *Store) recordRefreshCycleResult(success bool) {
	if s == nil {
		return
	}
	atomic.AddInt64(&s.refreshCycleProcessed, 1)
	if success {
		atomic.AddInt64(&s.refreshCycleSuccess, 1)
		return
	}
	atomic.AddInt64(&s.refreshCycleFailure, 1)
}

func (s *Store) finishRefreshCycle() {
	if s == nil {
		return
	}
	atomic.StoreInt64(&s.refreshCycleFinishedAt, time.Now().UnixNano())
	s.refreshCycleRunning.Store(false)
}

func (s *Store) countRefreshCandidates() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	accounts := make([]*Account, len(s.accounts))
	copy(accounts, s.accounts)
	s.mu.RUnlock()

	count := 0
	for _, acc := range accounts {
		if s.shouldRefreshAccount(acc) {
			count++
		}
	}
	return count
}

func (s *Store) GetRefreshSchedulerStatus() RefreshSchedulerStatus {
	if s == nil {
		return RefreshSchedulerStatus{}
	}
	totalAccounts := s.AccountCount()
	if totalAccounts <= 0 {
		totalAccounts = int(atomic.LoadInt64(&s.refreshCycleTotalAccounts))
	}
	status := RefreshSchedulerStatus{
		Running:        s.refreshCycleRunning.Load(),
		TotalAccounts:  totalAccounts,
		TargetAccounts: int(atomic.LoadInt64(&s.refreshCycleTargetAccounts)),
		Processed:      int(atomic.LoadInt64(&s.refreshCycleProcessed)),
		Success:        int(atomic.LoadInt64(&s.refreshCycleSuccess)),
		Failure:        int(atomic.LoadInt64(&s.refreshCycleFailure)),
	}
	if ts := atomic.LoadInt64(&s.nextRefreshScanAt); ts > 0 {
		status.NextScanAt = time.Unix(0, ts)
	}
	if ts := atomic.LoadInt64(&s.refreshCycleStartedAt); ts > 0 {
		status.StartedAt = time.Unix(0, ts)
	}
	if ts := atomic.LoadInt64(&s.refreshCycleFinishedAt); ts > 0 {
		status.FinishedAt = time.Unix(0, ts)
	}
	return status
}

func (s *Store) GetRefreshScanEnabled() bool {
	if s == nil {
		return true
	}
	return s.refreshScanEnabled.Load()
}

func (s *Store) SetRefreshScanEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.refreshScanEnabled.Store(enabled)
	s.notifyBackgroundRefreshLoop()
}

func (s *Store) GetRefreshScanInterval() time.Duration {
	if s == nil {
		return normalizeRefreshScanIntervalSeconds(defaultRefreshScanIntervalSeconds)
	}
	v := time.Duration(atomic.LoadInt64(&s.refreshScanInterval))
	if v <= 0 {
		return normalizeRefreshScanIntervalSeconds(defaultRefreshScanIntervalSeconds)
	}
	return v
}

func (s *Store) SetRefreshScanInterval(interval time.Duration) {
	if s == nil {
		return
	}
	atomic.StoreInt64(&s.refreshScanInterval, int64(normalizeRefreshScanIntervalSeconds(int(interval.Seconds()))))
	s.notifyBackgroundRefreshLoop()
}

func (s *Store) GetRefreshPreExpireWindow() time.Duration {
	if s == nil {
		return normalizeRefreshPreExpireSeconds(defaultRefreshPreExpireSeconds)
	}
	v := time.Duration(atomic.LoadInt64(&s.refreshPreExpireWindow))
	if v <= 0 {
		return normalizeRefreshPreExpireSeconds(defaultRefreshPreExpireSeconds)
	}
	return v
}

func (s *Store) SetRefreshPreExpireWindow(window time.Duration) {
	if s == nil {
		return
	}
	atomic.StoreInt64(&s.refreshPreExpireWindow, int64(normalizeRefreshPreExpireSeconds(int(window.Seconds()))))
}

func (s *Store) GetRefreshMaxConcurrency() int {
	if s == nil {
		return defaultRefreshMaxConcurrency
	}
	return normalizeRefreshMaxConcurrency(int(atomic.LoadInt64(&s.refreshMaxConcurrency)))
}

func (s *Store) SetRefreshMaxConcurrency(v int) {
	if s == nil {
		return
	}
	atomic.StoreInt64(&s.refreshMaxConcurrency, int64(normalizeRefreshMaxConcurrency(v)))
}

func (s *Store) GetRefreshOnImportEnabled() bool {
	if s == nil {
		return true
	}
	return s.refreshOnImportEnabled.Load()
}

func (s *Store) SetRefreshOnImportEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.refreshOnImportEnabled.Store(enabled)
}

func (s *Store) GetRefreshOnImportConcurrency() int {
	if s == nil {
		return defaultRefreshOnImportConcurrency
	}
	return normalizeRefreshOnImportConcurrency(int(atomic.LoadInt64(&s.refreshOnImportConcurrency)))
}

func (s *Store) SetRefreshOnImportConcurrency(v int) {
	if s == nil {
		return
	}
	atomic.StoreInt64(&s.refreshOnImportConcurrency, int64(normalizeRefreshOnImportConcurrency(v)))
}

func (s *Store) GetUsageProbeEnabled() bool {
	if s == nil {
		return true
	}
	return s.usageProbeEnabled.Load()
}

func (s *Store) SetUsageProbeEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.usageProbeEnabled.Store(enabled)
}

func (s *Store) GetUsageProbeStaleAfter() time.Duration {
	if s == nil {
		return normalizeUsageProbeStaleAfterSeconds(defaultUsageProbeStaleAfterSeconds)
	}
	v := time.Duration(atomic.LoadInt64(&s.usageProbeStaleAfter))
	if v <= 0 {
		return normalizeUsageProbeStaleAfterSeconds(defaultUsageProbeStaleAfterSeconds)
	}
	return v
}

func (s *Store) SetUsageProbeStaleAfter(v time.Duration) {
	if s == nil {
		return
	}
	atomic.StoreInt64(&s.usageProbeStaleAfter, int64(normalizeUsageProbeStaleAfterSeconds(int(v.Seconds()))))
}

func (s *Store) GetUsageProbeMaxConcurrency() int {
	if s == nil {
		return defaultUsageProbeMaxConcurrency
	}
	return normalizeUsageProbeMaxConcurrency(int(atomic.LoadInt64(&s.usageProbeMaxConcurrency)))
}

func (s *Store) SetUsageProbeMaxConcurrency(v int) {
	if s == nil {
		return
	}
	atomic.StoreInt64(&s.usageProbeMaxConcurrency, int64(normalizeUsageProbeMaxConcurrency(v)))
}

func (s *Store) GetRecoveryProbeEnabled() bool {
	if s == nil {
		return true
	}
	return s.recoveryProbeEnabled.Load()
}

func (s *Store) SetRecoveryProbeEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.recoveryProbeEnabled.Store(enabled)
}

func (s *Store) GetRecoveryProbeMinInterval() time.Duration {
	if s == nil {
		return normalizeRecoveryProbeMinIntervalSeconds(defaultRecoveryProbeMinIntervalSeconds)
	}
	v := time.Duration(atomic.LoadInt64(&s.recoveryProbeMinInterval))
	if v <= 0 {
		return normalizeRecoveryProbeMinIntervalSeconds(defaultRecoveryProbeMinIntervalSeconds)
	}
	return v
}

func (s *Store) SetRecoveryProbeMinInterval(v time.Duration) {
	if s == nil {
		return
	}
	atomic.StoreInt64(&s.recoveryProbeMinInterval, int64(normalizeRecoveryProbeMinIntervalSeconds(int(v.Seconds()))))
}

func (s *Store) GetRecoveryProbeMaxConcurrency() int {
	if s == nil {
		return defaultRecoveryProbeMaxConcurrency
	}
	return normalizeRecoveryProbeMaxConcurrency(int(atomic.LoadInt64(&s.recoveryProbeMaxConcurrency)))
}

func (s *Store) SetRecoveryProbeMaxConcurrency(v int) {
	if s == nil {
		return
	}
	atomic.StoreInt64(&s.recoveryProbeMaxConcurrency, int64(normalizeRecoveryProbeMaxConcurrency(v)))
}

func (s *Store) shouldRefreshAccount(acc *Account) bool {
	if s == nil || acc == nil {
		return false
	}
	if acc.Status == StatusError || acc.IsBanned() || acc.HasActiveCooldown() {
		return false
	}
	acc.mu.RLock()
	hasRT := acc.RefreshToken != ""
	acc.mu.RUnlock()
	if !hasRT {
		return false
	}
	return acc.NeedsRefreshWithin(s.GetRefreshPreExpireWindow())
}

func (s *Store) runPeriodicRefreshMaintenance(ctx context.Context) {
	if s == nil {
		return
	}
	if !s.refreshBatch.CompareAndSwap(false, true) {
		return
	}
	defer s.refreshBatch.Store(false)
	s.scheduleNextRefreshScan(time.Now().Add(s.GetRefreshScanInterval()))

	if s.GetRefreshScanEnabled() {
		s.beginRefreshCycle(s.AccountCount(), s.countRefreshCandidates())
		s.parallelRefreshAll(ctx)
		s.finishRefreshCycle()
	}
	if s.GetUsageProbeEnabled() {
		s.TriggerUsageProbeAsync()
	}
	if s.GetRecoveryProbeEnabled() {
		s.TriggerRecoveryProbeAsync()
	}
}

func (s *Store) EnqueueImportRefresh(dbID int64) {
	if s == nil || dbID <= 0 || !s.GetRefreshOnImportEnabled() || s.importRefreshQueue == nil {
		return
	}
	select {
	case s.importRefreshQueue <- dbID:
	case <-s.stopCh:
	}
}

func (s *Store) startImportRefreshWorkers() {
	if s == nil || s.importRefreshQueue == nil {
		return
	}
	for i := 0; i < defaultImportRefreshWorkerCount; i++ {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			for {
				select {
				case <-s.stopCh:
					return
				case dbID := <-s.importRefreshQueue:
					if dbID <= 0 || !s.acquireImportRefreshSlot() {
						continue
					}
					func(accountID int64) {
						defer s.releaseImportRefreshSlot()
						refreshCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						if err := s.RefreshSingle(refreshCtx, accountID); err != nil {
							log.Printf("导入账号 %d 刷新失败: %v", accountID, err)
						} else {
							log.Printf("导入账号 %d 刷新成功", accountID)
						}
					}(dbID)
				}
			}
		}()
	}
}

func (s *Store) acquireImportRefreshSlot() bool {
	if s == nil {
		return false
	}
	for {
		if !s.GetRefreshOnImportEnabled() {
			return false
		}
		limit := int64(s.GetRefreshOnImportConcurrency())
		current := atomic.LoadInt64(&s.importRefreshInFlight)
		if current < limit && atomic.CompareAndSwapInt64(&s.importRefreshInFlight, current, current+1) {
			return true
		}
		select {
		case <-s.stopCh:
			return false
		case <-time.After(defaultImportRefreshAcquireRetryInterval):
		}
	}
}

func (s *Store) releaseImportRefreshSlot() {
	if s == nil {
		return
	}
	atomic.AddInt64(&s.importRefreshInFlight, -1)
}
