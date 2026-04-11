package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/codex2api/database"
)

const (
	ProxyModeNone    = "none"
	ProxyModeStatic  = "static"
	ProxyModeDynamic = "dynamic"
	ProxyModeAuto    = "auto"
	ProxyModeInherit = "inherit"

	defaultProxyScheme        = "http"
	defaultProxyRotationHours = 24

	dynamicProxyFetchTimeout         = 10 * time.Second
	maxDynamicProxyPayloadBytes      = 1 << 20 // 1MB
	maxDynamicProxyErrorBodyBytes    = 64 * 1024
	proxyResolutionPersistTimeout    = 3 * time.Second
	proxyAssignmentPersistTimeout    = 3 * time.Second
	maxProxyResolutionErrorMsgLength = 512
)

var errDynamicProxyBodyTooLarge = errors.New("dynamic proxy provider response too large")

type dynamicProxyResponse struct {
	Code    int                   `json:"code"`
	Success bool                  `json:"success"`
	Msg     string                `json:"msg"`
	Data    []dynamicProxyPayload `json:"data"`
}

type dynamicProxyPayload struct {
	URL      string `json:"url"`
	IP       string `json:"ip"`
	Port     int    `json:"port"`
	Type     string `json:"type"`
	Scheme   string `json:"scheme"`
	Protocol string `json:"protocol"`
}

func NormalizeProxyMode(mode, staticURL, providerURL string, poolEnabled bool, allowInherit bool) string {
	return normalizeProxyMode(mode, staticURL, providerURL, poolEnabled, allowInherit)
}

func NormalizeProxyScheme(scheme string) string {
	return normalizeProxyScheme(scheme)
}

func normalizeProxyMode(mode, staticURL, providerURL string, poolEnabled bool, allowInherit bool) string {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	switch normalized {
	case ProxyModeNone, ProxyModeStatic, ProxyModeDynamic, ProxyModeAuto, ProxyModeInherit:
		return normalized
	}
	if strings.TrimSpace(providerURL) != "" {
		return ProxyModeDynamic
	}
	if strings.TrimSpace(staticURL) != "" {
		return ProxyModeStatic
	}
	if poolEnabled {
		return ProxyModeAuto
	}
	if allowInherit {
		return ProxyModeInherit
	}
	return ProxyModeNone
}

func normalizeProxyScheme(scheme string) string {
	normalized := strings.ToLower(strings.TrimSpace(scheme))
	switch normalized {
	case "http", "https", "socks4", "socks5", "socks5h":
		return normalized
	default:
		return defaultProxyScheme
	}
}

func (s *Store) GetProxyMode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.globalProxyMode
}

func (s *Store) GetProxyProviderURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.globalProxyProvider
}

func (s *Store) GetProxySchemeDefault() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.globalProxyScheme
}

func (s *Store) GetAutoAssignProxyIfMissing() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.autoAssignProxyIfMissing
}

func (s *Store) GetSwitchProxyOnNetworkError() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.switchProxyOnNetworkError
}

func (s *Store) GetProxyRotationHours() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.proxyRotationHours <= 0 {
		return defaultProxyRotationHours
	}
	return s.proxyRotationHours
}

func (s *Store) GetDisableProxyDuringImport() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.disableProxyDuringImport
}

func (s *Store) SetProxyMode(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.globalProxyMode = normalizeProxyMode(mode, s.globalProxy, s.globalProxyProvider, s.proxyPoolEnabled, false)
}

func (s *Store) SetProxyProviderURL(providerURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.globalProxyProvider = strings.TrimSpace(providerURL)
	s.globalProxyMode = normalizeProxyMode(s.globalProxyMode, s.globalProxy, s.globalProxyProvider, s.proxyPoolEnabled, false)
}

func (s *Store) SetProxySchemeDefault(scheme string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.globalProxyScheme = normalizeProxyScheme(scheme)
}

func (s *Store) SetAutoAssignProxyIfMissing(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.autoAssignProxyIfMissing = enabled
}

func (s *Store) SetSwitchProxyOnNetworkError(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.switchProxyOnNetworkError = enabled
}

func (s *Store) SetProxyRotationHours(hours int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if hours <= 0 {
		hours = defaultProxyRotationHours
	}
	s.proxyRotationHours = hours
}

func (s *Store) SetDisableProxyDuringImport(disabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disableProxyDuringImport = disabled
}

func (s *Store) ResolveMaintenanceProxy(ctx context.Context, acc *Account) string {
	return s.resolveProxy(ctx, acc)
}

func (s *Store) ResolveRuntimeProxy(ctx context.Context, acc *Account) string {
	return s.resolveProxy(ctx, acc)
}

func (s *Store) resolveProxy(ctx context.Context, acc *Account) string {
	if s == nil {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if acc == nil {
		return s.resolveGlobalProxy(ctx)
	}

	acc.mu.RLock()
	mode := normalizeProxyMode(acc.ProxyMode, acc.ProxyURL, acc.ProxyProviderURL, false, true)
	staticURL := strings.TrimSpace(acc.ProxyURL)
	providerURL := strings.TrimSpace(acc.ProxyProviderURL)
	scheme := normalizeProxyScheme(acc.ProxySchemeDefault)
	assigned := strings.TrimSpace(acc.AssignedProxyURL)
	rotateAt := acc.ProxyNextRotationAt
	lastReason := strings.TrimSpace(acc.ProxyLastSwitchReason)
	acc.mu.RUnlock()

	switch mode {
	case ProxyModeNone:
		return ""
	case ProxyModeStatic:
		if assigned != "" && !shouldRotateAssignedProxy(rotateAt) {
			return assigned
		}
		if staticURL != "" && !shouldBypassStaticProxy(lastReason, rotateAt) {
			return staticURL
		}
		if resolved := s.assignFallbackProxy(ctx, acc, providerURL, scheme); resolved != "" {
			return resolved
		}
		return staticURL
	case ProxyModeDynamic:
		if assigned != "" && !shouldRotateAssignedProxy(rotateAt) {
			return assigned
		}
		if providerURL != "" {
			resolved, err := s.fetchDynamicProxy(ctx, providerURL, scheme, 0)
			if err == nil {
				now := time.Now()
				s.persistAssignedProxy(ctx, acc, resolved, now, nextProxyRotationTime(now, s.GetProxyRotationHours()), "dynamic_assigned", "")
				return resolved
			}
			s.persistAssignedProxy(ctx, acc, "", time.Time{}, time.Time{}, "dynamic_resolve_failed", err.Error())
		}
		if resolved := s.assignFallbackProxy(ctx, acc, "", scheme); resolved != "" {
			return resolved
		}
		return ""
	case ProxyModeAuto, ProxyModeInherit:
		if assigned != "" && !shouldRotateAssignedProxy(rotateAt) {
			return assigned
		}
		if mode == ProxyModeInherit && acc.DBID == 0 {
			if resolved := s.resolveDynamicFallback(ctx, providerURL, scheme); resolved != "" {
				return resolved
			}
			if row := s.nextProxySource(); row != nil {
				return s.resolveProxyRow(ctx, row)
			}
			return s.resolveGlobalStaticProxy()
		}
		if resolved := s.assignFallbackProxy(ctx, acc, providerURL, scheme); resolved != "" {
			return resolved
		}
		if mode == ProxyModeAuto {
			return ""
		}
		return s.resolveGlobalStaticProxy()
	default:
		return s.resolveGlobalProxy(ctx)
	}
}

func shouldBypassStaticProxy(reason string, rotateAt time.Time) bool {
	if strings.TrimSpace(reason) != "network_error" {
		return false
	}
	if rotateAt.IsZero() {
		return true
	}
	return time.Now().Before(rotateAt)
}

func shouldRotateAssignedProxy(rotateAt time.Time) bool {
	return rotateAt.IsZero() || !time.Now().Before(rotateAt)
}

func nextProxyRotationTime(now time.Time, hours int) time.Time {
	if hours <= 0 {
		hours = defaultProxyRotationHours
	}
	return now.Add(time.Duration(hours) * time.Hour)
}

func (s *Store) resolveGlobalProxy(ctx context.Context) string {
	if resolved := s.resolveDynamicFallback(ctx, "", ""); resolved != "" {
		return resolved
	}
	if row := s.nextProxySource(); row != nil {
		return s.resolveProxyRow(ctx, row)
	}
	return s.resolveGlobalStaticProxy()
}

func (s *Store) assignFallbackProxy(ctx context.Context, acc *Account, providerURL, scheme string) string {
	now := time.Now()
	if resolved, reason := s.resolveDynamicFallbackWithReason(ctx, providerURL, scheme); resolved != "" {
		s.persistAssignedProxy(ctx, acc, resolved, now, nextProxyRotationTime(now, s.GetProxyRotationHours()), reason, "")
		return resolved
	}

	if row := s.nextProxySource(); row != nil {
		resolved := s.resolveProxyRow(ctx, row)
		if resolved != "" {
			s.persistAssignedProxy(ctx, acc, resolved, now, nextProxyRotationTime(now, s.GetProxyRotationHours()), "pool_assigned", "")
			return resolved
		}
	}

	if resolved := s.resolveGlobalStaticProxy(); resolved != "" {
		s.persistAssignedProxy(ctx, acc, resolved, now, nextProxyRotationTime(now, s.GetProxyRotationHours()), "global_static_assigned", "")
		return resolved
	}
	return ""
}

func (s *Store) resolveDynamicFallback(ctx context.Context, providerURL, scheme string) string {
	resolved, _ := s.resolveDynamicFallbackWithReason(ctx, providerURL, scheme)
	return resolved
}

func (s *Store) resolveDynamicFallbackWithReason(ctx context.Context, providerURL, scheme string) (string, string) {
	providerURL = strings.TrimSpace(providerURL)
	scheme = normalizeProxyScheme(scheme)
	if providerURL != "" {
		resolved, err := s.fetchDynamicProxy(ctx, providerURL, scheme, 0)
		if err == nil {
			return resolved, "account_dynamic_assigned"
		}
	}

	s.mu.RLock()
	globalProvider := strings.TrimSpace(s.globalProxyProvider)
	globalScheme := normalizeProxyScheme(s.globalProxyScheme)
	s.mu.RUnlock()
	if globalProvider == "" {
		return "", ""
	}

	resolved, err := s.fetchDynamicProxy(ctx, globalProvider, firstNonEmpty(scheme, globalScheme, defaultProxyScheme), 0)
	if err != nil {
		return "", ""
	}
	return resolved, "global_dynamic_assigned"
}

func (s *Store) resolveGlobalStaticProxy() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.globalProxy)
}

func (s *Store) nextProxySource() *database.ProxyRow {
	s.mu.RLock()
	enabled := s.proxyPoolEnabled
	rows := append([]*database.ProxyRow(nil), s.proxyPoolRows...)
	legacyPool := append([]string(nil), s.proxyPool...)
	s.mu.RUnlock()
	if !enabled {
		return nil
	}
	if len(rows) == 0 && len(legacyPool) == 0 {
		return nil
	}
	idx := atomic.AddUint64(&s.proxyRoundRobin, 1)
	if len(rows) == 0 {
		return &database.ProxyRow{
			SourceType: ProxyModeStatic,
			URL:        legacyPool[idx%uint64(len(legacyPool))],
		}
	}
	return rows[idx%uint64(len(rows))]
}

func (s *Store) resolveProxyRow(ctx context.Context, row *database.ProxyRow) string {
	if row == nil {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(row.SourceType), ProxyModeDynamic) {
		resolved, err := s.fetchDynamicProxy(ctx, row.ProviderURL, row.SchemeDefault, row.ID)
		if err != nil {
			return ""
		}
		return resolved
	}
	return strings.TrimSpace(row.URL)
}

func (s *Store) fetchDynamicProxy(ctx context.Context, providerURL, scheme string, sourceID int64) (string, error) {
	providerURL = strings.TrimSpace(providerURL)
	if providerURL == "" {
		return "", fmt.Errorf("empty proxy provider url")
	}

	requestCtx, cancel := context.WithTimeout(nonNilContext(ctx), dynamicProxyFetchTimeout)
	defer cancel()

	validatedURL, err := validateDynamicProviderURL(providerURL)
	if err != nil {
		s.persistProxyResolution(sourceID, "", time.Time{}, err.Error())
		return "", err
	}

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, validatedURL, nil)
	if err != nil {
		s.persistProxyResolution(sourceID, "", time.Time{}, err.Error())
		return "", err
	}
	client := &http.Client{
		Timeout: dynamicProxyFetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		s.persistProxyResolution(sourceID, "", time.Time{}, err.Error())
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := readLimitedBody(resp.Body, maxDynamicProxyErrorBodyBytes)
		detail := strings.TrimSpace(string(body))
		if detail != "" {
			err = fmt.Errorf("provider returned http %d: %s", resp.StatusCode, detail)
		} else {
			err = fmt.Errorf("provider returned http %d", resp.StatusCode)
		}
		s.persistProxyResolution(sourceID, "", time.Time{}, err.Error())
		return "", err
	}

	payloadBytes, err := readLimitedBody(resp.Body, maxDynamicProxyPayloadBytes)
	if err != nil {
		if errors.Is(err, errDynamicProxyBodyTooLarge) {
			err = fmt.Errorf("provider response too large")
		}
		s.persistProxyResolution(sourceID, "", time.Time{}, err.Error())
		return "", err
	}

	var payload dynamicProxyResponse
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		s.persistProxyResolution(sourceID, "", time.Time{}, err.Error())
		return "", err
	}

	resolved, err := buildResolvedProxyURL(payload, scheme)
	if err != nil {
		s.persistProxyResolution(sourceID, "", time.Time{}, err.Error())
		return "", err
	}

	s.persistProxyResolution(sourceID, resolved, time.Now(), "")
	return resolved, nil
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

func readLimitedBody(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = maxDynamicProxyPayloadBytes
	}
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errDynamicProxyBodyTooLarge
	}
	return data, nil
}

func (s *Store) persistProxyResolution(sourceID int64, resolved string, resolvedAt time.Time, lastError string) {
	if sourceID <= 0 || s == nil || s.db == nil {
		return
	}
	persistCtx, cancel := context.WithTimeout(context.Background(), proxyResolutionPersistTimeout)
	defer cancel()
	_ = s.db.UpdateProxyResolution(persistCtx, sourceID, strings.TrimSpace(resolved), resolvedAt, truncateProxyResolutionError(lastError))
}

func truncateProxyResolutionError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= maxProxyResolutionErrorMsgLength {
		return message
	}
	return message[:maxProxyResolutionErrorMsgLength]
}

func validateDynamicProviderURL(raw string) (string, error) {
	parsed, err := neturl.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return "", fmt.Errorf("invalid proxy provider url")
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("proxy provider url must use http or https")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("proxy provider url must not contain user info")
	}
	host := parsed.Hostname()
	if host == "" {
		return "", fmt.Errorf("proxy provider url missing host")
	}
	return parsed.String(), nil
}

func buildResolvedProxyURL(payload dynamicProxyResponse, defaultScheme string) (string, error) {
	if len(payload.Data) == 0 {
		return "", fmt.Errorf("provider returned no proxy data")
	}
	item := payload.Data[0]
	if rawURL := strings.TrimSpace(item.URL); rawURL != "" {
		return validateResolvedProxyURL(rawURL, defaultScheme)
	}
	host := strings.TrimSpace(item.IP)
	if host == "" || item.Port <= 0 || item.Port > 65535 {
		return "", fmt.Errorf("provider response missing ip or port")
	}
	scheme := normalizeProxyScheme(firstNonEmpty(item.Type, item.Scheme, item.Protocol, defaultScheme))
	return validateResolvedProxyURL(fmt.Sprintf("%s://%s:%d", scheme, host, item.Port), defaultScheme)
}

func validateResolvedProxyURL(raw, defaultScheme string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("provider returned empty proxy url")
	}

	if !strings.Contains(raw, "://") {
		raw = fmt.Sprintf("%s://%s", normalizeProxyScheme(defaultScheme), raw)
	}

	parsed, err := neturl.Parse(raw)
	if err != nil || parsed == nil {
		return "", fmt.Errorf("provider returned invalid proxy url")
	}

	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	switch scheme {
	case "http", "https", "socks4", "socks5", "socks5h":
	default:
		return "", fmt.Errorf("provider returned unsupported proxy scheme")
	}
	if strings.TrimSpace(parsed.Hostname()) == "" {
		return "", fmt.Errorf("provider returned proxy url missing host")
	}
	if port := strings.TrimSpace(parsed.Port()); port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber <= 0 || portNumber > 65535 {
			return "", fmt.Errorf("provider returned invalid proxy port")
		}
	}

	parsed.Scheme = scheme
	return parsed.String(), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (s *Store) persistAssignedProxy(ctx context.Context, acc *Account, assignedURL string, switchedAt, rotateAt time.Time, reason, lastError string) {
	if acc == nil {
		return
	}
	acc.mu.Lock()
	acc.AssignedProxyURL = strings.TrimSpace(assignedURL)
	acc.ProxyLastSwitchedAt = switchedAt
	acc.ProxyNextRotationAt = rotateAt
	acc.ProxyLastSwitchReason = strings.TrimSpace(reason)
	acc.ProxyLastError = strings.TrimSpace(lastError)
	acc.mu.Unlock()
	if s.db != nil && acc.DBID > 0 {
		persistCtx, cancel := context.WithTimeout(context.Background(), proxyAssignmentPersistTimeout)
		defer cancel()
		_ = s.db.UpdateAccountProxyRuntime(
			persistCtx,
			acc.DBID,
			strings.TrimSpace(assignedURL),
			switchedAt,
			rotateAt,
			strings.TrimSpace(reason),
			truncateProxyResolutionError(lastError),
		)
	}
}

func (s *Store) InvalidateProxyAssignment(ctx context.Context, acc *Account, reason, lastError string) {
	if acc == nil {
		return
	}
	acc.mu.RLock()
	mode := normalizeProxyMode(acc.ProxyMode, acc.ProxyURL, acc.ProxyProviderURL, false, true)
	acc.mu.RUnlock()
	if mode == ProxyModeStatic {
		now := time.Now()
		s.persistAssignedProxy(ctx, acc, "", now, nextProxyRotationTime(now, s.GetProxyRotationHours()), reason, lastError)
		return
	}
	s.persistAssignedProxy(ctx, acc, "", time.Time{}, time.Time{}, reason, lastError)
}
