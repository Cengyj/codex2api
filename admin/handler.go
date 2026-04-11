package admin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/codex2api/auth"
	"github.com/codex2api/cache"
	"github.com/codex2api/database"
	"github.com/codex2api/proxy"
	"github.com/codex2api/security"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Handler 管理后台 API 处理器
type Handler struct {
	store            *auth.Store
	cache            cache.TokenCache
	db               *database.DB
	rateLimiter      *proxy.RateLimiter
	refreshAccount   func(context.Context, int64) error
	cpuSampler       *cpuSampler
	startedAt        time.Time
	pgMaxConns       int
	redisPoolSize    int
	databaseDriver   string
	databaseLabel    string
	cacheDriver      string
	cacheLabel       string
	adminSecretEnv   string
	adminAuthLimiter *security.IPRateLimiter
	cpaSync          *CPASyncService

	// 图表聚合内存缓存（10秒 TTL）
	chartCacheMu   sync.RWMutex
	chartCacheData map[string]*chartCacheEntry

	// 账号请求统计缓存（30秒 TTL）
	reqCountMu        sync.RWMutex
	reqCountCache     map[int64]*database.AccountRequestCount
	reqCountExpiresAt time.Time
}

type chartCacheEntry struct {
	data      *database.ChartAggregation
	expiresAt time.Time
}

// NewHandler 创建管理后台处理器
func NewHandler(store *auth.Store, db *database.DB, tc cache.TokenCache, rl *proxy.RateLimiter, adminSecretEnv string) *Handler {
	handler := &Handler{
		store:            store,
		cache:            tc,
		db:               db,
		rateLimiter:      rl,
		cpuSampler:       newCPUSampler(),
		startedAt:        time.Now(),
		databaseDriver:   db.Driver(),
		databaseLabel:    db.Label(),
		cacheDriver:      tc.Driver(),
		cacheLabel:       tc.Label(),
		adminSecretEnv:   adminSecretEnv,
		adminAuthLimiter: security.NewIPRateLimiter(20, time.Minute),
		chartCacheData:   make(map[string]*chartCacheEntry),
	}
	handler.refreshAccount = handler.refreshSingleAccount
	return handler
}

// SetPoolSizes 设置连接池大小跟踪值（由 main.go 在启动时调用）
func (h *Handler) SetPoolSizes(pgMaxConns, redisPoolSize int) {
	h.pgMaxConns = pgMaxConns
	h.redisPoolSize = redisPoolSize
}

func (h *Handler) SetCPASyncService(service *CPASyncService) {
	h.cpaSync = service
}

// RegisterRoutes 注册管理 API 路由
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	api := r.Group("/api/admin")
	api.Use(h.adminAuthMiddleware())
	api.GET("/stats", h.GetStats)
	api.GET("/accounts", h.ListAccounts)
	api.POST("/accounts", h.AddAccount)
	api.POST("/accounts/at", h.AddATAccount)
	api.POST("/accounts/import", h.ImportAccounts)
	api.DELETE("/accounts/:id", h.DeleteAccount)
	api.POST("/accounts/:id/refresh", h.RefreshAccount)
	api.POST("/accounts/:id/lock", h.ToggleAccountLock)
	api.GET("/accounts/:id/test", h.TestConnection)
	api.POST("/accounts/batch-test", h.BatchTest)
	api.POST("/accounts/clean-banned", h.CleanBanned)
	api.POST("/accounts/clean-rate-limited", h.CleanRateLimited)
	api.POST("/accounts/clean-error", h.CleanError)
	api.GET("/accounts/export", h.ExportAccounts)
	api.POST("/accounts/migrate", h.MigrateAccounts)
	api.GET("/accounts/event-trend", h.GetAccountEventTrend)
	api.GET("/health", h.GetHealth)
	api.GET("/ops/overview", h.GetOpsOverview)
	api.GET("/settings", h.GetSettings)
	api.PUT("/settings", h.UpdateSettings)
	api.GET("/cpa-sync/status", h.GetCPASyncStatus)
	api.POST("/cpa-sync/run", h.RunCPASync)
	api.POST("/cpa-sync/switch", h.SwitchCPASyncMihomo)
	api.POST("/cpa-sync/test-cpa", h.TestCPASyncCPA)
	api.POST("/cpa-sync/test-mihomo", h.TestCPASyncMihomo)
	api.POST("/cpa-sync/mihomo-groups", h.ListCPASyncMihomoGroups)
	api.GET("/models", h.ListModels)
	api.GET("/proxies", h.ListProxies)
	api.POST("/proxies", h.AddProxies)
	api.DELETE("/proxies/:id", h.DeleteProxy)
	api.PATCH("/proxies/:id", h.UpdateProxy)
	api.POST("/proxies/batch-delete", h.BatchDeleteProxies)
	api.POST("/proxies/test", h.TestProxy)

	// OAuth 授权流程
	api.POST("/oauth/generate-auth-url", h.GenerateOAuthURL)
	api.POST("/oauth/exchange-code", h.ExchangeOAuthCode)
}

// adminAuthMiddleware 管理接口鉴权中间件（增强版，增加安全审计日志）
func (h *Handler) adminAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		adminSecret, source := h.resolveAdminSecret(c.Request.Context())
		if adminSecret == "" {
			if source == "error" {
				security.SecurityAuditLog("ADMIN_AUTH_ERROR", fmt.Sprintf("path=%s ip=%s source=%s", c.Request.URL.Path, c.ClientIP(), source))
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"error": "管理鉴权暂时不可用，请稍后重试",
				})
				c.Abort()
				return
			}
			// 未配置管理密钥，跳过鉴权
			security.SecurityAuditLog("ADMIN_AUTH_MISCONFIGURED", fmt.Sprintf("path=%s ip=%s source=%s", c.Request.URL.Path, c.ClientIP(), source))
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": "????????????????",
			})
			c.Abort()
			return
		}

		adminKey := c.GetHeader("X-Admin-Key")
		if adminKey == "" {
			// 兼容 Authorization: Bearer 方式
			authHeader := c.GetHeader("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				adminKey = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}

		// 清理输入
		adminKey = security.SanitizeInput(adminKey)

		// 使用安全比较防止时序攻击
		if !security.SecureCompare(adminKey, adminSecret) {
			if h.adminAuthLimiter != nil && !h.adminAuthLimiter.Allow(c.ClientIP()) {
				security.SecurityAuditLog("ADMIN_AUTH_RATE_LIMITED", fmt.Sprintf("path=%s ip=%s source=%s", c.Request.URL.Path, c.ClientIP(), source))
				c.JSON(http.StatusTooManyRequests, gin.H{
					"error": "管理密钥重试过于频繁，请稍后再试",
				})
				c.Abort()
				return
			}
			// 记录安全审计日志
			security.SecurityAuditLog("ADMIN_AUTH_FAILED", fmt.Sprintf("path=%s ip=%s source=%s", c.Request.URL.Path, c.ClientIP(), source))
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "管理密钥无效或缺失",
			})
			c.Abort()
			return
		}

		// 成功认证，记录审计日志
		if security.IsSensitiveEndpoint(c.Request.URL.Path) {
			security.SecurityAuditLog("ADMIN_ACCESS", fmt.Sprintf("path=%s ip=%s method=%s", c.Request.URL.Path, c.ClientIP(), c.Request.Method))
		}

		c.Next()
	}
}

func (h *Handler) resolveAdminSecret(ctx context.Context) (string, string) {
	if h.adminSecretEnv != "" {
		return h.adminSecretEnv, "env"
	}

	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	settings, err := h.db.GetSystemSettings(readCtx)
	if err != nil {
		return "", "error"
	}
	if settings == nil || settings.AdminSecret == "" {
		return "", "disabled"
	}
	return settings.AdminSecret, "database"
}

func (h *Handler) hasConfiguredAdminSecret(ctx context.Context) bool {
	adminSecret, _ := h.resolveAdminSecret(ctx)
	return strings.TrimSpace(adminSecret) != ""
}

// ==================== Stats ====================

// GetStats 获取仪表盘统计
func (h *Handler) GetStats(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	accounts, err := h.db.ListActive(ctx)
	if err != nil {
		writeLoggedInternalError(c, "获取统计信息失败", err)
		return
	}

	total := len(accounts)
	available := h.store.AvailableCount()
	errCount := 0
	for _, acc := range accounts {
		if acc.Status == "error" {
			errCount++
		}
	}

	c.JSON(http.StatusOK, statsResponse{
		Total:            total,
		Available:        available,
		Error:            errCount,
		RefreshScheduler: h.getRefreshSchedulerResponse(),
		RefreshConfig:    h.getRefreshConfigResponse(),
	})
}

// ==================== Accounts ====================

type accountResponse struct {
	ID                  int64                      `json:"id"`
	Name                string                     `json:"name"`
	Email               string                     `json:"email"`
	PlanType            string                     `json:"plan_type"`
	Status              string                     `json:"status"`
	ATOnly              bool                       `json:"at_only"`
	HealthTier          string                     `json:"health_tier"`
	SchedulerScore      float64                    `json:"scheduler_score"`
	ConcurrencyCap      int64                      `json:"dynamic_concurrency_limit"`
	ProxyURL            string                     `json:"proxy_url"`
	ProxyMode           string                     `json:"proxy_mode,omitempty"`
	ProxyProviderURL    string                     `json:"proxy_provider_url,omitempty"`
	ProxyProtocol       string                     `json:"proxy_protocol,omitempty"`
	ProxyAssignedURL    string                     `json:"proxy_assigned_url,omitempty"`
	ProxyAssignedAt     string                     `json:"proxy_assigned_at,omitempty"`
	ProxyLastSwitchedAt string                     `json:"proxy_last_switched_at,omitempty"`
	ProxyLastError      string                     `json:"proxy_last_error,omitempty"`
	CreatedAt           string                     `json:"created_at"`
	UpdatedAt           string                     `json:"updated_at"`
	ActiveRequests      int64                      `json:"active_requests"`
	TotalRequests       int64                      `json:"total_requests"`
	LastUsedAt          string                     `json:"last_used_at"`
	SuccessRequests     int64                      `json:"success_requests"`
	ErrorRequests       int64                      `json:"error_requests"`
	UsagePercent7d      *float64                   `json:"usage_percent_7d"`
	UsagePercent5h      *float64                   `json:"usage_percent_5h"`
	Reset5hAt           string                     `json:"reset_5h_at,omitempty"`
	Reset7dAt           string                     `json:"reset_7d_at,omitempty"`
	ScoreBreakdown      schedulerBreakdownResponse `json:"scheduler_breakdown"`
	LastUnauthorizedAt  string                     `json:"last_unauthorized_at,omitempty"`
	LastRateLimitedAt   string                     `json:"last_rate_limited_at,omitempty"`
	LastTimeoutAt       string                     `json:"last_timeout_at,omitempty"`
	LastServerErrorAt   string                     `json:"last_server_error_at,omitempty"`
	Locked              bool                       `json:"locked"`
}

type schedulerBreakdownResponse struct {
	UnauthorizedPenalty float64 `json:"unauthorized_penalty"`
	RateLimitPenalty    float64 `json:"rate_limit_penalty"`
	TimeoutPenalty      float64 `json:"timeout_penalty"`
	ServerPenalty       float64 `json:"server_penalty"`
	FailurePenalty      float64 `json:"failure_penalty"`
	SuccessBonus        float64 `json:"success_bonus"`
	UsagePenalty7d      float64 `json:"usage_penalty_7d"`
	LatencyPenalty      float64 `json:"latency_penalty"`
	SuccessRatePenalty  float64 `json:"success_rate_penalty"`
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sanitizeProxyProviderURL(raw string) (string, error) {
	raw = strings.TrimSpace(security.SanitizeInput(raw))
	if raw == "" {
		return "", nil
	}
	parsed, err := neturl.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("动态代理 URL 无效")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("动态代理 URL 仅支持 http/https")
	}
	return raw, nil
}

func buildProxyConfigInput(mode, proxyURL, providerURL, scheme string, poolEnabled bool, allowInherit bool) (database.ProxyConfigInput, error) {
	proxyURL = strings.TrimSpace(security.SanitizeInput(proxyURL))
	if err := security.ValidateProxyURL(proxyURL); err != nil {
		return database.ProxyConfigInput{}, err
	}

	sanitizedProviderURL, err := sanitizeProxyProviderURL(providerURL)
	if err != nil {
		return database.ProxyConfigInput{}, err
	}

	cfg := database.ProxyConfigInput{
		Mode:        auth.NormalizeProxyMode(mode, proxyURL, sanitizedProviderURL, poolEnabled, allowInherit),
		URL:         proxyURL,
		ProviderURL: sanitizedProviderURL,
		Scheme:      auth.NormalizeProxyScheme(scheme),
	}

	if cfg.Mode == auth.ProxyModeDynamic && cfg.ProviderURL == "" {
		return database.ProxyConfigInput{}, fmt.Errorf("动态代理模式必须提供 provider URL")
	}
	if cfg.Mode == auth.ProxyModeStatic && cfg.URL == "" && !allowInherit {
		cfg.Mode = auth.ProxyModeNone
	}
	return cfg, nil
}

func (h *Handler) buildProxyConfigInput(mode, proxyURL, providerURL, protocol, schemeDefault string) (database.ProxyConfigInput, error) {
	return buildProxyConfigInput(mode, proxyURL, providerURL, firstNonEmptyString(protocol, schemeDefault), h.store.GetProxyPoolEnabled(), true)
}

func isPrivateOrLocalIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	if ip.IsPrivate() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		// 100.64.0.0/10 carrier-grade NAT
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
	}
	return false
}

type externalURLValidationOptions struct {
	allowEmpty        bool
	trimTrailingSlash bool
	rejectQuery       bool
	rejectFragment    bool
	rejectPrivate     bool
}

func validateExternalURL(ctx context.Context, raw string, fieldName string, opts externalURLValidationOptions) (string, error) {
	raw = strings.TrimSpace(security.SanitizeInput(raw))
	if raw == "" {
		if opts.allowEmpty {
			return "", nil
		}
		return "", fmt.Errorf("%s 不能为空", fieldName)
	}

	parsed, err := neturl.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("%s 格式无效", fieldName)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("%s 仅支持 http/https 协议", fieldName)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("%s 不允许包含用户信息", fieldName)
	}
	if opts.rejectQuery && parsed.RawQuery != "" {
		return "", fmt.Errorf("%s 不允许包含查询参数", fieldName)
	}
	if opts.rejectFragment && parsed.Fragment != "" {
		return "", fmt.Errorf("%s 不允许包含片段", fieldName)
	}

	hostname := strings.TrimSpace(parsed.Hostname())
	if hostname == "" {
		return "", fmt.Errorf("%s 主机无效", fieldName)
	}

	if opts.rejectPrivate {
		switch strings.ToLower(hostname) {
		case "localhost", "localhost.localdomain":
			return "", fmt.Errorf("不允许使用本地地址")
		}
		if ctx == nil {
			ctx = context.Background()
		}
		if ip := net.ParseIP(hostname); ip != nil {
			if isPrivateOrLocalIP(ip) {
				return "", fmt.Errorf("不允许访问内网或本地地址")
			}
		} else {
			resolveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			addrs, err := net.DefaultResolver.LookupIPAddr(resolveCtx, hostname)
			if err != nil {
				return "", fmt.Errorf("解析远程地址失败: %w", err)
			}
			if len(addrs) == 0 {
				return "", fmt.Errorf("远程地址无可用 IP")
			}
			for _, addr := range addrs {
				if isPrivateOrLocalIP(addr.IP) {
					return "", fmt.Errorf("不允许访问内网或本地地址")
				}
			}
		}
	}

	if opts.trimTrailingSlash {
		return strings.TrimRight(raw, "/"), nil
	}
	return raw, nil
}

func validateExternalServiceBaseURL(ctx context.Context, raw string, fieldName string) (string, error) {
	return validateExternalURL(ctx, raw, fieldName, externalURLValidationOptions{
		allowEmpty:        true,
		trimTrailingSlash: true,
		rejectQuery:       true,
		rejectFragment:    true,
	})
}

func validateExternalTargetURL(ctx context.Context, raw string, fieldName string) (string, error) {
	return validateExternalURL(ctx, raw, fieldName, externalURLValidationOptions{
		allowEmpty: true,
	})
}

func validateMigrationRemoteURL(ctx context.Context, raw string) (string, error) {
	return validateExternalURL(ctx, raw, "url", externalURLValidationOptions{
		trimTrailingSlash: true,
		rejectQuery:       true,
		rejectFragment:    true,
		rejectPrivate:     true,
	})
}

// ListAccounts 获取账号列表
func (h *Handler) ListAccounts(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	h.store.TriggerUsageProbeAsync()
	h.store.TriggerRecoveryProbeAsync()

	rows, err := h.db.ListActive(ctx)
	if err != nil {
		writeLoggedInternalError(c, "获取账号列表失败", err)
		return
	}

	// 合并内存中的调度指标
	accountMap := make(map[int64]*auth.Account)
	for _, acc := range h.store.Accounts() {
		accountMap[acc.DBID] = acc
	}

	// 获取每账号近 7 天请求统计（带 30 秒内存缓存）
	reqCounts := h.getCachedRequestCounts()

	accounts := make([]accountResponse, 0, len(rows))
	for _, row := range rows {
		resp := accountResponse{
			ID:               row.ID,
			Name:             row.Name,
			Email:            row.GetCredential("email"),
			PlanType:         row.GetCredential("plan_type"),
			Status:           row.Status,
			ATOnly:           row.GetCredential("refresh_token") == "" && row.GetCredential("access_token") != "",
			ProxyURL:         row.ProxyURL,
			ProxyMode:        auth.NormalizeProxyMode(row.ProxyMode, row.ProxyURL, row.ProxyProviderURL, false, true),
			ProxyProviderURL: row.ProxyProviderURL,
			ProxyProtocol:    auth.NormalizeProxyScheme(row.ProxySchemeDefault),
			ProxyAssignedURL: row.AssignedProxyURL,
			ProxyLastError:   row.ProxyLastError,
			Locked:           row.Locked,
			CreatedAt:        row.CreatedAt.Format(time.RFC3339),
			UpdatedAt:        row.UpdatedAt.Format(time.RFC3339),
		}
		if !row.ProxyLastSwitchedAt.IsZero() {
			resp.ProxyAssignedAt = row.ProxyLastSwitchedAt.Format(time.RFC3339)
			resp.ProxyLastSwitchedAt = row.ProxyLastSwitchedAt.Format(time.RFC3339)
		}
		if acc, ok := accountMap[row.ID]; ok {
			acc.Mu().RLock()
			resp.ProxyMode = auth.NormalizeProxyMode(acc.ProxyMode, acc.ProxyURL, acc.ProxyProviderURL, false, true)
			resp.ProxyProviderURL = acc.ProxyProviderURL
			resp.ProxyProtocol = auth.NormalizeProxyScheme(acc.ProxySchemeDefault)
			if acc.AssignedProxyURL != "" {
				resp.ProxyAssignedURL = acc.AssignedProxyURL
			}
			if !acc.ProxyLastSwitchedAt.IsZero() {
				resp.ProxyAssignedAt = acc.ProxyLastSwitchedAt.Format(time.RFC3339)
				resp.ProxyLastSwitchedAt = acc.ProxyLastSwitchedAt.Format(time.RFC3339)
			}
			if acc.ProxyLastError != "" {
				resp.ProxyLastError = acc.ProxyLastError
			}
			acc.Mu().RUnlock()
			resp.ActiveRequests = acc.GetActiveRequests()
			resp.TotalRequests = acc.GetTotalRequests()
			debug := acc.GetSchedulerDebugSnapshot(int64(h.store.GetMaxConcurrency()))
			resp.HealthTier = debug.HealthTier
			resp.SchedulerScore = debug.SchedulerScore
			resp.ConcurrencyCap = debug.DynamicConcurrencyLimit
			resp.ScoreBreakdown = schedulerBreakdownResponse{
				UnauthorizedPenalty: debug.Breakdown.UnauthorizedPenalty,
				RateLimitPenalty:    debug.Breakdown.RateLimitPenalty,
				TimeoutPenalty:      debug.Breakdown.TimeoutPenalty,
				ServerPenalty:       debug.Breakdown.ServerPenalty,
				FailurePenalty:      debug.Breakdown.FailurePenalty,
				SuccessBonus:        debug.Breakdown.SuccessBonus,
				UsagePenalty7d:      debug.Breakdown.UsagePenalty7d,
				LatencyPenalty:      debug.Breakdown.LatencyPenalty,
				SuccessRatePenalty:  debug.Breakdown.SuccessRatePenalty,
			}
			if usagePct, ok := acc.GetUsagePercent7d(); ok {
				resp.UsagePercent7d = &usagePct
			}
			if usagePct5h, ok := acc.GetUsagePercent5h(); ok {
				resp.UsagePercent5h = &usagePct5h
			}
			if t := acc.GetReset5hAt(); !t.IsZero() {
				resp.Reset5hAt = t.Format(time.RFC3339)
			}
			if t := acc.GetReset7dAt(); !t.IsZero() {
				resp.Reset7dAt = t.Format(time.RFC3339)
			}
			if t := acc.GetLastUsedAt(); !t.IsZero() {
				resp.LastUsedAt = t.Format(time.RFC3339)
			}
			if !debug.LastUnauthorizedAt.IsZero() {
				resp.LastUnauthorizedAt = debug.LastUnauthorizedAt.Format(time.RFC3339)
			}
			if !debug.LastRateLimitedAt.IsZero() {
				resp.LastRateLimitedAt = debug.LastRateLimitedAt.Format(time.RFC3339)
			}
			if !debug.LastTimeoutAt.IsZero() {
				resp.LastTimeoutAt = debug.LastTimeoutAt.Format(time.RFC3339)
			}
			if !debug.LastServerErrorAt.IsZero() {
				resp.LastServerErrorAt = debug.LastServerErrorAt.Format(time.RFC3339)
			}
			// 使用运行时状态（优先于 DB 状态）
			resp.Status = acc.RuntimeStatus()
		}
		if rc, ok := reqCounts[row.ID]; ok {
			resp.SuccessRequests = rc.SuccessCount
			resp.ErrorRequests = rc.ErrorCount
		}
		accounts = append(accounts, resp)
	}

	c.JSON(http.StatusOK, accountsResponse{
		Accounts:         accounts,
		RefreshScheduler: h.getRefreshSchedulerResponse(),
	})
}

// getCachedRequestCounts 返回带 30 秒 TTL 的账号请求统计缓存
func (h *Handler) getCachedRequestCounts() map[int64]*database.AccountRequestCount {
	h.reqCountMu.RLock()
	if h.reqCountCache != nil && time.Now().Before(h.reqCountExpiresAt) {
		cached := h.reqCountCache
		h.reqCountMu.RUnlock()
		return cached
	}
	h.reqCountMu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	counts, err := h.db.GetAccountRequestCounts(ctx)
	if err != nil {
		log.Printf("获取账号请求统计失败: %v", err)
		return make(map[int64]*database.AccountRequestCount)
	}

	h.reqCountMu.Lock()
	h.reqCountCache = counts
	h.reqCountExpiresAt = time.Now().Add(30 * time.Second)
	h.reqCountMu.Unlock()

	return counts
}

type addAccountReq struct {
	Name             string `json:"name"`
	RefreshToken     string `json:"refresh_token"`
	ProxyURL         string `json:"proxy_url"`
	ProxyMode        string `json:"proxy_mode"`
	ProxyProviderURL string `json:"proxy_provider_url"`
	ProxyProtocol    string `json:"proxy_protocol"`
	ProxyScheme      string `json:"proxy_scheme_default"`
}

// AddAccount 添加新账号（支持批量：refresh_token 按行分割）
func (h *Handler) AddAccount(c *gin.Context) {
	var req addAccountReq
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求格式错误")
		return
	}

	// 输入验证和清理
	req.Name = security.SanitizeInput(req.Name)
	req.ProxyURL = security.SanitizeInput(req.ProxyURL)
	req.ProxyProviderURL = strings.TrimSpace(req.ProxyProviderURL)

	if req.RefreshToken == "" {
		writeError(c, http.StatusBadRequest, "refresh_token 是必填字段")
		return
	}

	// 检查XSS和SQL注入
	if security.ContainsXSS(req.Name) || security.ContainsSQLInjection(req.Name) {
		writeError(c, http.StatusBadRequest, "名称包含非法字符")
		return
	}

	// 验证名称长度
	if utf8.RuneCountInString(req.Name) > 100 {
		writeError(c, http.StatusBadRequest, "名称长度不能超过100字符")
		return
	}

	proxyCfg, err := buildProxyConfigInput(req.ProxyMode, req.ProxyURL, req.ProxyProviderURL, firstNonEmptyString(req.ProxyProtocol, req.ProxyScheme), h.store.GetProxyPoolEnabled(), true)
	if err != nil {
		writeError(c, http.StatusBadRequest, "代理配置无效: "+err.Error())
		return
	}

	// 按行分割，支持批量添加
	lines := strings.Split(req.RefreshToken, "\n")
	var tokens []string
	for _, line := range lines {
		t := strings.TrimSpace(security.SanitizeInput(line))
		if t != "" {
			tokens = append(tokens, t)
		}
	}

	if len(tokens) == 0 {
		writeError(c, http.StatusBadRequest, "未找到有效的 Refresh Token")
		return
	}

	// 限制批量添加数量
	if len(tokens) > 100 {
		writeError(c, http.StatusBadRequest, "单次最多添加100个账号")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	successCount := 0
	failCount := 0

	for i, rt := range tokens {
		name := req.Name
		if name == "" {
			name = fmt.Sprintf("account-%d", i+1)
		} else if len(tokens) > 1 {
			name = fmt.Sprintf("%s-%d", req.Name, i+1)
		}

		id, err := h.db.InsertAccountWithProxyConfig(ctx, name, rt, proxyCfg)
		if err != nil {
			log.Printf("批量添加账号 %d 失败: %v", i+1, err)
			failCount++
			continue
		}

		successCount++
		h.db.InsertAccountEventAsync(id, "added", "manual")

		// 热加载：直接加入内存池
		newAcc := &auth.Account{
			DBID:               id,
			RefreshToken:       rt,
			ProxyURL:           proxyCfg.URL,
			ProxyMode:          proxyCfg.Mode,
			ProxyProviderURL:   proxyCfg.ProviderURL,
			ProxySchemeDefault: proxyCfg.Scheme,
		}
		h.store.AddAccount(newAcc)
		h.store.EnqueueImportRefresh(id)
	}

	// 记录安全审计日志
	security.SecurityAuditLog("ACCOUNTS_ADDED", fmt.Sprintf("success=%d failed=%d ip=%s", successCount, failCount, c.ClientIP()))

	msg := fmt.Sprintf("成功添加 %d 个账号", successCount)
	if failCount > 0 {
		msg += fmt.Sprintf("，%d 个失败", failCount)
	}

	c.JSON(http.StatusOK, gin.H{
		"message": msg,
		"success": successCount,
		"failed":  failCount,
	})
}

// addATAccountReq AT 模式添加账号请求
type addATAccountReq struct {
	Name             string `json:"name"`
	AccessToken      string `json:"access_token"`
	ProxyURL         string `json:"proxy_url"`
	ProxyMode        string `json:"proxy_mode"`
	ProxyProviderURL string `json:"proxy_provider_url"`
	ProxyProtocol    string `json:"proxy_protocol"`
	ProxyScheme      string `json:"proxy_scheme_default"`
}

// AddATAccount 添加 AT-only 账号（支持批量：access_token 按行分割）
func (h *Handler) AddATAccount(c *gin.Context) {
	var req addATAccountReq
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求格式错误")
		return
	}

	req.Name = security.SanitizeInput(req.Name)
	req.ProxyURL = security.SanitizeInput(req.ProxyURL)
	req.ProxyProviderURL = strings.TrimSpace(req.ProxyProviderURL)

	if req.AccessToken == "" {
		writeError(c, http.StatusBadRequest, "access_token 是必填字段")
		return
	}

	if security.ContainsXSS(req.Name) || security.ContainsSQLInjection(req.Name) {
		writeError(c, http.StatusBadRequest, "名称包含非法字符")
		return
	}

	if utf8.RuneCountInString(req.Name) > 100 {
		writeError(c, http.StatusBadRequest, "名称长度不能超过100字符")
		return
	}

	proxyCfg, err := buildProxyConfigInput(req.ProxyMode, req.ProxyURL, req.ProxyProviderURL, firstNonEmptyString(req.ProxyProtocol, req.ProxyScheme), h.store.GetProxyPoolEnabled(), true)
	if err != nil {
		writeError(c, http.StatusBadRequest, "代理配置无效: "+err.Error())
		return
	}

	// ???????????
	lines := strings.Split(req.AccessToken, "\n")
	var tokens []string
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t != "" {
			tokens = append(tokens, t)
		}
	}

	if len(tokens) == 0 {
		writeError(c, http.StatusBadRequest, "未找到有效的 Access Token")
		return
	}

	if len(tokens) > 100 {
		writeError(c, http.StatusBadRequest, "单次最多添加100个账号")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	successCount := 0
	failCount := 0

	for i, at := range tokens {
		name := req.Name
		if name == "" {
			name = fmt.Sprintf("at-account-%d", i+1)
		} else if len(tokens) > 1 {
			name = fmt.Sprintf("%s-%d", req.Name, i+1)
		}

		id, err := h.db.InsertATAccountWithProxyConfig(ctx, name, at, proxyCfg)
		if err != nil {
			log.Printf("添加 AT 账号 %d 失败: %v", i+1, err)
			failCount++
			continue
		}

		successCount++
		h.db.InsertAccountEventAsync(id, "added", "manual_at")

		// 解析 AT JWT 提取账号信息（email、plan_type、account_id、过期时间）
		atInfo := auth.ParseAccessToken(at)

		// 热加载到内存池（AT-only，无 RT）
		newAcc := &auth.Account{
			DBID:               id,
			AccessToken:        at,
			ExpiresAt:          time.Now().Add(1 * time.Hour),
			ProxyURL:           proxyCfg.URL,
			ProxyMode:          proxyCfg.Mode,
			ProxyProviderURL:   proxyCfg.ProviderURL,
			ProxySchemeDefault: proxyCfg.Scheme,
		}
		if atInfo != nil {
			newAcc.Email = atInfo.Email
			newAcc.AccountID = atInfo.ChatGPTAccountID
			newAcc.PlanType = atInfo.PlanType
			if !atInfo.ExpiresAt.IsZero() {
				newAcc.ExpiresAt = atInfo.ExpiresAt
			}
		}
		h.store.AddAccount(newAcc)

		// 将解析到的信息持久化到数据库
		if atInfo != nil {
			creds := map[string]interface{}{
				"email":      atInfo.Email,
				"account_id": atInfo.ChatGPTAccountID,
				"plan_type":  atInfo.PlanType,
				"expires_at": newAcc.ExpiresAt.Format(time.RFC3339),
			}
			if err := h.db.UpdateCredentials(ctx, id, creds); err != nil {
				log.Printf("AT 账号 %d 更新 credentials 失败: %v", id, err)
			}
		}
		log.Printf("AT 账号 %d 已加入号池 (id=%d, email=%s)", i+1, id, newAcc.Email)
	}

	security.SecurityAuditLog("AT_ACCOUNTS_ADDED", fmt.Sprintf("success=%d failed=%d ip=%s", successCount, failCount, c.ClientIP()))

	msg := fmt.Sprintf("成功添加 %d 个 AT 账号", successCount)
	if failCount > 0 {
		msg += fmt.Sprintf("，%d 个失败", failCount)
	}

	c.JSON(http.StatusOK, gin.H{
		"message": msg,
		"success": successCount,
		"failed":  failCount,
	})
}

// importToken 导入时的统一 token 载体
type importToken struct {
	refreshToken string
	accessToken  string // AT-only 兼容路径
	name         string
}

// jsonAccountEntry CLIProxyAPI 凭证 JSON 条目
type jsonAccountEntry struct {
	RefreshToken string `json:"refresh_token"`
	AccessToken  string `json:"access_token"`
	Email        string `json:"email"`
}

type sub2apiImportPayload struct {
	Accounts []sub2apiAccountEntry `json:"accounts"`
}

type sub2apiAccountEntry struct {
	Name        string                    `json:"name"`
	Credentials sub2apiAccountCredentials `json:"credentials"`
}

type sub2apiAccountCredentials struct {
	RefreshToken string `json:"refresh_token"`
	AccessToken  string `json:"access_token"`
	Email        string `json:"email"`
}

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

func trimUTF8BOM(data []byte) []byte {
	return bytes.TrimPrefix(data, utf8BOM)
}

// parseImportJSONTokens 同时兼容现有扁平 JSON 和 Sub2Api 顶层对象。
func parseImportJSONTokens(data []byte) ([]importToken, error) {
	data = trimUTF8BOM(data)
	if !json.Valid(data) {
		return nil, fmt.Errorf("invalid import json")
	}

	if tokens := parseFlatJSONImportTokens(data); len(tokens) > 0 {
		return tokens, nil
	}

	if tokens := parseSub2APIJSONImportTokens(data); len(tokens) > 0 {
		return tokens, nil
	}

	return nil, nil
}

func parseFlatJSONImportTokens(data []byte) []importToken {
	var entries []jsonAccountEntry
	if err := json.Unmarshal(data, &entries); err == nil {
		return jsonAccountEntriesToTokens(entries)
	}

	var single jsonAccountEntry
	if err := json.Unmarshal(data, &single); err == nil {
		return jsonAccountEntriesToTokens([]jsonAccountEntry{single})
	}

	return nil
}

func jsonAccountEntriesToTokens(entries []jsonAccountEntry) []importToken {
	tokens := make([]importToken, 0, len(entries))
	for _, entry := range entries {
		rt := strings.TrimSpace(entry.RefreshToken)
		at := strings.TrimSpace(entry.AccessToken)
		email := strings.TrimSpace(entry.Email)

		if rt != "" {
			tokens = append(tokens, importToken{refreshToken: rt, name: email})
			continue
		}
		if at != "" {
			tokens = append(tokens, importToken{accessToken: at, name: email})
		}
	}
	return tokens
}

func parseSub2APIJSONImportTokens(data []byte) []importToken {
	var payload sub2apiImportPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}

	tokens := make([]importToken, 0, len(payload.Accounts))
	for _, account := range payload.Accounts {
		rt := strings.TrimSpace(account.Credentials.RefreshToken)
		at := strings.TrimSpace(account.Credentials.AccessToken)
		name := strings.TrimSpace(account.Name)
		email := strings.TrimSpace(account.Credentials.Email)

		if name == "" {
			name = email
		}

		if rt != "" {
			tokens = append(tokens, importToken{refreshToken: rt, name: name})
			continue
		}
		if at != "" {
			tokens = append(tokens, importToken{accessToken: at, name: name})
		}
	}

	return tokens
}

// ImportAccounts 批量导入账号（支持 TXT / JSON）
func (h *Handler) ImportAccounts(c *gin.Context) {
	format := c.DefaultPostForm("format", "txt")
	proxyCfg, err := h.buildProxyConfigInput(
		c.PostForm("proxy_mode"),
		c.PostForm("proxy_url"),
		c.PostForm("proxy_provider_url"),
		c.PostForm("proxy_protocol"),
		c.PostForm("proxy_scheme_default"),
	)
	if err != nil {
		writeError(c, http.StatusBadRequest, "代理配置无效: "+err.Error())
		return
	}

	switch format {
	case "json":
		h.importAccountsJSON(c, proxyCfg)
	case "at_txt":
		h.importAccountsATTXT(c, proxyCfg)
	default:
		h.importAccountsTXT(c, proxyCfg)
	}
}

// importAccountsTXT 通过 TXT 文件导入（每行一个 RT）
func (h *Handler) importAccountsTXT(c *gin.Context, proxyCfg database.ProxyConfigInput) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		writeError(c, http.StatusBadRequest, "请上传文件（字段名: file）")
		return
	}
	defer file.Close()

	if header.Size > 2*1024*1024 {
		writeError(c, http.StatusBadRequest, "文件大小不能超过 2MB")
		return
	}

	data, err := io.ReadAll(io.LimitReader(file, 2*1024*1024+1))
	if err != nil {
		writeError(c, http.StatusBadRequest, "读取文件失败")
		return
	}
	if len(data) > 2*1024*1024 {
		writeError(c, http.StatusBadRequest, "文件大小不能超过 2MB")
		return
	}

	// 按行分割，去重
	lines := strings.Split(string(data), "\n")
	seen := make(map[string]bool)
	var tokens []importToken
	for _, line := range lines {
		t := strings.TrimSpace(line)
		t = strings.TrimPrefix(t, "\xef\xbb\xbf") // 去除 UTF-8 BOM
		if t != "" && !seen[t] {
			seen[t] = true
			tokens = append(tokens, importToken{refreshToken: t})
		}
	}

	if len(tokens) == 0 {
		writeError(c, http.StatusBadRequest, "文件中未找到有效的 Refresh Token")
		return
	}

	h.importAccountsCommon(c, tokens, proxyCfg)
}

// importAccountsJSON 通过 JSON 文件导入（兼容 CLIProxyAPI 凭证格式）
func (h *Handler) importAccountsJSON(c *gin.Context, proxyCfg database.ProxyConfigInput) {
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		writeError(c, http.StatusBadRequest, "解析表单失败")
		return
	}

	files := c.Request.MultipartForm.File["file"]
	if len(files) == 0 {
		writeError(c, http.StatusBadRequest, "请上传至少一个 JSON 文件")
		return
	}

	var allTokens []importToken

	for _, fh := range files {
		if fh.Size > 2*1024*1024 {
			writeError(c, http.StatusBadRequest, fmt.Sprintf("文件 %s 大小超过 2MB", fh.Filename))
			return
		}

		f, err := fh.Open()
		if err != nil {
			writeError(c, http.StatusBadRequest, fmt.Sprintf("打开文件 %s 失败", fh.Filename))
			return
		}
		data, err := io.ReadAll(io.LimitReader(f, 2*1024*1024+1))
		f.Close()
		if err != nil {
			writeError(c, http.StatusBadRequest, fmt.Sprintf("读取文件 %s 失败", fh.Filename))
			return
		}
		if len(data) > 2*1024*1024 {
			writeError(c, http.StatusBadRequest, fmt.Sprintf("文件 %s 大小不能超过 2MB", fh.Filename))
			return
		}

		tokens, err := parseImportJSONTokens(data)
		if err != nil {
			writeError(c, http.StatusBadRequest, fmt.Sprintf("文件 %s 不是有效的 JSON 格式", fh.Filename))
			return
		}

		allTokens = append(allTokens, tokens...)
	}

	if len(allTokens) == 0 {
		writeError(c, http.StatusBadRequest, "JSON 文件中未找到有效的 refresh_token 或 access_token")
		return
	}

	h.importAccountsCommon(c, allTokens, proxyCfg)
}

// importEvent SSE 导入进度事件
type importEvent struct {
	Type      string `json:"type"` // progress | complete
	Current   int    `json:"current"`
	Total     int    `json:"total"`
	Success   int    `json:"success"`
	Duplicate int    `json:"duplicate"`
	Failed    int    `json:"failed"`
}

func canStreamImportEvent(c *gin.Context) bool {
	return c != nil && c.Request != nil && c.Request.Context().Err() == nil
}

func sendImportEvent(c *gin.Context, e importEvent) bool {
	if !canStreamImportEvent(c) {
		return false
	}
	data, _ := json.Marshal(e)
	fmt.Fprintf(c.Writer, "data: %s\n\n", data)
	c.Writer.Flush()
	return true
}

func setupSSE(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	if canStreamImportEvent(c) {
		c.Writer.Flush()
	}
}

// importAccountsCommon 公共的去重、并发插入、SSE 进度推送逻辑（支持 RT 和 AT-only 混合导入）
func (h *Handler) importAccountsCommon(c *gin.Context, tokens []importToken, proxyCfg database.ProxyConfigInput) {
	// 文件内去重（RT 和 AT 分别去重）
	seenRT := make(map[string]bool)
	seenAT := make(map[string]bool)
	var unique []importToken
	for _, t := range tokens {
		if t.accessToken != "" {
			if !seenAT[t.accessToken] {
				seenAT[t.accessToken] = true
				unique = append(unique, t)
			}
		} else {
			if !seenRT[t.refreshToken] {
				seenRT[t.refreshToken] = true
				unique = append(unique, t)
			}
		}
	}

	// 数据库去重（独立短超时）
	dedupeCtx, dedupeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dedupeCancel()

	existingRTs, err := h.db.GetAllRefreshTokens(dedupeCtx)
	if err != nil {
		log.Printf("查询已有 RT 失败: %v", err)
		existingRTs = make(map[string]bool)
	}

	// 存在 AT-only token 时额外查询已有 AT
	hasAT := len(seenAT) > 0
	var existingATs map[string]bool
	if hasAT {
		existingATs, err = h.db.GetAllAccessTokens(dedupeCtx)
		if err != nil {
			log.Printf("查询已有 AT 失败: %v", err)
			existingATs = make(map[string]bool)
		}
	}

	var newTokens []importToken
	duplicateCount := 0
	for _, t := range unique {
		if t.accessToken != "" {
			if existingATs[t.accessToken] {
				duplicateCount++
			} else {
				newTokens = append(newTokens, t)
			}
		} else {
			if existingRTs[t.refreshToken] {
				duplicateCount++
			} else {
				newTokens = append(newTokens, t)
			}
		}
	}

	total := len(unique)

	if len(newTokens) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"message":   fmt.Sprintf("所有 %d 个 Token 已存在，无需导入", total),
			"success":   0,
			"duplicate": duplicateCount,
			"failed":    0,
			"total":     total,
		})
		return
	}

	// 切换到 SSE 流式响应
	setupSSE(c)

	var successCount int64
	var failCount int64
	var current int64
	sem := make(chan struct{}, 20) // 并发插入上限
	var wg sync.WaitGroup

	// 进度推送 goroutine：定时发送，避免每条都写造成 IO 瓶颈
	done := make(chan struct{})
	reporterDone := make(chan struct{})
	go func() {
		defer close(reporterDone)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				cur := int(atomic.LoadInt64(&current))
				suc := int(atomic.LoadInt64(&successCount))
				fai := int(atomic.LoadInt64(&failCount))
				sendImportEvent(c, importEvent{
					Type: "progress", Current: cur + duplicateCount, Total: total,
					Success: suc, Duplicate: duplicateCount, Failed: fai,
				})
			case <-c.Request.Context().Done():
				return
			case <-done:
				return
			}
		}
	}()

	for i, t := range newTokens {
		sem <- struct{}{}
		wg.Add(1)
		go func(idx int, tok importToken) {
			defer wg.Done()
			defer func() { <-sem }()

			name := tok.name

			if tok.accessToken != "" {
				// AT-only 导入路径
				if name == "" {
					name = fmt.Sprintf("at-import-%d", idx+1)
				}

				insertCtx, insertCancel := context.WithTimeout(context.Background(), 5*time.Second)
				id, err := h.db.InsertATAccountWithProxyConfig(insertCtx, name, tok.accessToken, proxyCfg)
				insertCancel()

				if err != nil {
					log.Printf("导入 AT 账号 %d/%d 失败: %v", idx+1, len(newTokens), err)
					atomic.AddInt64(&failCount, 1)
					atomic.AddInt64(&current, 1)
					return
				}

				atomic.AddInt64(&successCount, 1)
				atomic.AddInt64(&current, 1)
				h.db.InsertAccountEventAsync(id, "added", "import_at")

				atInfo := auth.ParseAccessToken(tok.accessToken)
				newAcc := &auth.Account{
					DBID:               id,
					AccessToken:        tok.accessToken,
					ExpiresAt:          time.Now().Add(1 * time.Hour),
					ProxyURL:           proxyCfg.URL,
					ProxyMode:          proxyCfg.Mode,
					ProxyProviderURL:   proxyCfg.ProviderURL,
					ProxySchemeDefault: proxyCfg.Scheme,
				}
				if atInfo != nil {
					newAcc.Email = atInfo.Email
					newAcc.AccountID = atInfo.ChatGPTAccountID
					newAcc.PlanType = atInfo.PlanType
					if !atInfo.ExpiresAt.IsZero() {
						newAcc.ExpiresAt = atInfo.ExpiresAt
					}
					credCtx, credCancel := context.WithTimeout(context.Background(), 3*time.Second)
					_ = h.db.UpdateCredentials(credCtx, id, map[string]interface{}{
						"email":      atInfo.Email,
						"account_id": atInfo.ChatGPTAccountID,
						"plan_type":  atInfo.PlanType,
						"expires_at": newAcc.ExpiresAt.Format(time.RFC3339),
					})
					credCancel()
				}
				h.store.AddAccount(newAcc)
			} else {
				// RT 导入路径
				if name == "" {
					name = fmt.Sprintf("import-%d", idx+1)
				}

				insertCtx, insertCancel := context.WithTimeout(context.Background(), 5*time.Second)
				id, err := h.db.InsertAccountWithProxyConfig(insertCtx, name, tok.refreshToken, proxyCfg)
				insertCancel()

				if err != nil {
					log.Printf("导入账号 %d/%d 失败: %v", idx+1, len(newTokens), err)
					atomic.AddInt64(&failCount, 1)
					atomic.AddInt64(&current, 1)
					return
				}

				atomic.AddInt64(&successCount, 1)
				atomic.AddInt64(&current, 1)
				h.db.InsertAccountEventAsync(id, "added", "import")

				newAcc := &auth.Account{
					DBID:               id,
					RefreshToken:       tok.refreshToken,
					ProxyURL:           proxyCfg.URL,
					ProxyMode:          proxyCfg.Mode,
					ProxyProviderURL:   proxyCfg.ProviderURL,
					ProxySchemeDefault: proxyCfg.Scheme,
				}
				h.store.AddAccount(newAcc)
				h.store.EnqueueImportRefresh(id)
			}
		}(i, t)
	}

	wg.Wait()
	close(done)
	<-reporterDone

	// 发送完成事件
	suc := int(atomic.LoadInt64(&successCount))
	fai := int(atomic.LoadInt64(&failCount))
	sendImportEvent(c, importEvent{
		Type: "complete", Current: total, Total: total,
		Success: suc, Duplicate: duplicateCount, Failed: fai,
	})

	log.Printf("导入完成: success=%d, duplicate=%d, failed=%d, total=%d", suc, duplicateCount, fai, total)
}

// importAccountsATTXT 通过 TXT 文件导入 AT-only 账号（每行一个 Access Token）
func (h *Handler) importAccountsATTXT(c *gin.Context, proxyCfg database.ProxyConfigInput) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		writeError(c, http.StatusBadRequest, "请上传文件（字段名: file）")
		return
	}
	defer file.Close()

	if header.Size > 2*1024*1024 {
		writeError(c, http.StatusBadRequest, "文件大小不能超过 2MB")
		return
	}

	data, err := io.ReadAll(io.LimitReader(file, 2*1024*1024+1))
	if err != nil {
		writeError(c, http.StatusBadRequest, "读取文件失败")
		return
	}
	if len(data) > 2*1024*1024 {
		writeError(c, http.StatusBadRequest, "文件大小不能超过 2MB")
		return
	}

	// 按行分割，文件内去重
	lines := strings.Split(string(data), "\n")
	seen := make(map[string]bool)
	var atTokens []string
	for _, line := range lines {
		t := strings.TrimSpace(line)
		t = strings.TrimPrefix(t, "\xef\xbb\xbf")
		if t != "" && !seen[t] {
			seen[t] = true
			atTokens = append(atTokens, t)
		}
	}

	if len(atTokens) == 0 {
		writeError(c, http.StatusBadRequest, "文件中未找到有效的 Access Token")
		return
	}

	// 数据库去重
	dedupeCtx, dedupeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dedupeCancel()
	existingATs, err := h.db.GetAllAccessTokens(dedupeCtx)
	if err != nil {
		log.Printf("查询已有 AT 失败: %v", err)
		existingATs = make(map[string]bool)
	}

	var newTokens []string
	duplicateCount := 0
	for _, at := range atTokens {
		if existingATs[at] {
			duplicateCount++
		} else {
			newTokens = append(newTokens, at)
		}
	}

	total := len(atTokens)

	if len(newTokens) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"message":   fmt.Sprintf("所有 %d 个 AT 已存在，无需导入", total),
			"success":   0,
			"duplicate": duplicateCount,
			"failed":    0,
			"total":     total,
		})
		return
	}

	// SSE 流式响应
	setupSSE(c)

	var successCount int64
	var failCount int64
	var current int64
	sem := make(chan struct{}, 20)
	var wg sync.WaitGroup

	done := make(chan struct{})
	reporterDone := make(chan struct{})
	go func() {
		defer close(reporterDone)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				cur := int(atomic.LoadInt64(&current))
				suc := int(atomic.LoadInt64(&successCount))
				fai := int(atomic.LoadInt64(&failCount))
				sendImportEvent(c, importEvent{
					Type: "progress", Current: cur + duplicateCount, Total: total,
					Success: suc, Duplicate: duplicateCount, Failed: fai,
				})
			case <-c.Request.Context().Done():
				return
			case <-done:
				return
			}
		}
	}()

	for i, at := range newTokens {
		sem <- struct{}{}
		wg.Add(1)
		go func(idx int, accessToken string) {
			defer wg.Done()
			defer func() { <-sem }()

			name := fmt.Sprintf("at-import-%d", idx+1)

			insertCtx, insertCancel := context.WithTimeout(context.Background(), 5*time.Second)
			id, err := h.db.InsertATAccountWithProxyConfig(insertCtx, name, accessToken, proxyCfg)
			insertCancel()

			if err != nil {
				log.Printf("导入 AT 账号 %d/%d 失败: %v", idx+1, len(newTokens), err)
				atomic.AddInt64(&failCount, 1)
				atomic.AddInt64(&current, 1)
				return
			}

			atomic.AddInt64(&successCount, 1)
			atomic.AddInt64(&current, 1)
			h.db.InsertAccountEventAsync(id, "added", "import_at")

			// 解析 AT JWT 提取账号信息
			atInfo := auth.ParseAccessToken(accessToken)

			newAcc := &auth.Account{
				DBID:               id,
				AccessToken:        accessToken,
				ExpiresAt:          time.Now().Add(1 * time.Hour),
				ProxyURL:           proxyCfg.URL,
				ProxyMode:          proxyCfg.Mode,
				ProxyProviderURL:   proxyCfg.ProviderURL,
				ProxySchemeDefault: proxyCfg.Scheme,
			}
			if atInfo != nil {
				newAcc.Email = atInfo.Email
				newAcc.AccountID = atInfo.ChatGPTAccountID
				newAcc.PlanType = atInfo.PlanType
				if !atInfo.ExpiresAt.IsZero() {
					newAcc.ExpiresAt = atInfo.ExpiresAt
				}
				// 持久化解析到的账号信息
				credCtx, credCancel := context.WithTimeout(context.Background(), 3*time.Second)
				_ = h.db.UpdateCredentials(credCtx, id, map[string]interface{}{
					"email":      atInfo.Email,
					"account_id": atInfo.ChatGPTAccountID,
					"plan_type":  atInfo.PlanType,
					"expires_at": newAcc.ExpiresAt.Format(time.RFC3339),
				})
				credCancel()

				// 如果解析到邮箱，用邮箱替换默认名称
				if atInfo.Email != "" {
					name = atInfo.Email
				}
			}
			h.store.AddAccount(newAcc)
		}(i, at)
	}

	wg.Wait()
	close(done)
	<-reporterDone

	suc := int(atomic.LoadInt64(&successCount))
	fai := int(atomic.LoadInt64(&failCount))
	sendImportEvent(c, importEvent{
		Type: "complete", Current: total, Total: total,
		Success: suc, Duplicate: duplicateCount, Failed: fai,
	})

	log.Printf("AT 导入完成: success=%d, duplicate=%d, failed=%d, total=%d", suc, duplicateCount, fai, total)
}

// DeleteAccount 删除账号
func (h *Handler) DeleteAccount(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		writeError(c, http.StatusBadRequest, "无效的账号 ID")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	// 标记为 deleted 而非物理删除
	if err := h.db.SetError(ctx, id, "deleted"); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(c, http.StatusNotFound, "账号不存在")
			return
		}
		writeLoggedInternalError(c, "删除失败", err)
		return
	}

	// 从内存池移除
	h.store.RemoveAccount(id)
	h.db.InsertAccountEventAsync(id, "deleted", "manual")

	writeMessage(c, http.StatusOK, "账号已删除")
}

// RefreshAccount 手动刷新账号 AT
func (h *Handler) RefreshAccount(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		writeError(c, http.StatusBadRequest, "无效的账号 ID")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	refreshFn := h.refreshAccount
	if refreshFn == nil {
		refreshFn = h.refreshSingleAccount
	}
	if err := refreshFn(ctx, id); err != nil {
		if errors.Is(err, auth.ErrAccountNotFound) || strings.Contains(err.Error(), "不存在") {
			writeError(c, http.StatusNotFound, err.Error())
			return
		}
		writeLoggedInternalError(c, "刷新失败", err)
		return
	}

	writeMessage(c, http.StatusOK, "账号刷新成功")
}

// ToggleAccountLock 切换账号的锁定状态
func (h *Handler) ToggleAccountLock(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		writeError(c, http.StatusBadRequest, "无效的账号 ID")
		return
	}

	var req struct {
		Locked bool `json:"locked"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求格式错误")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	if err := h.db.SetAccountLocked(ctx, id, req.Locked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(c, http.StatusNotFound, "账号不存在")
			return
		}
		writeLoggedInternalError(c, "更新锁定状态失败", err)
		return
	}

	// 同步更新内存中的状态
	if acc := h.store.FindByID(id); acc != nil {
		if req.Locked {
			atomic.StoreInt32(&acc.Locked, 1)
		} else {
			atomic.StoreInt32(&acc.Locked, 0)
		}
	}

	if req.Locked {
		writeMessage(c, http.StatusOK, "账号已锁定")
	} else {
		writeMessage(c, http.StatusOK, "账号已解锁")
	}
}

func (h *Handler) refreshSingleAccount(ctx context.Context, id int64) error {
	if h == nil || h.store == nil {
		return fmt.Errorf("账号池未初始化")
	}
	return h.store.RefreshSingle(ctx, id)
}

// ==================== Health ====================

// GetHealth 系统健康检查（扩展版）
func (h *Handler) GetHealth(c *gin.Context) {
	c.JSON(http.StatusOK, healthResponse{
		Status:           "ok",
		Available:        h.store.AvailableCount(),
		Total:            h.store.AccountCount(),
		RefreshScheduler: h.getRefreshSchedulerResponse(),
	})
}

func (h *Handler) getRefreshSchedulerResponse() refreshStatusResponse {
	status := h.store.GetRefreshSchedulerStatus()
	resp := refreshStatusResponse{
		Running:        status.Running,
		TotalAccounts:  status.TotalAccounts,
		TargetAccounts: status.TargetAccounts,
		Processed:      status.Processed,
		Success:        status.Success,
		Failure:        status.Failure,
	}
	if !status.NextScanAt.IsZero() {
		resp.NextScanAt = status.NextScanAt.Format(time.RFC3339)
	}
	if !status.StartedAt.IsZero() {
		resp.StartedAt = status.StartedAt.Format(time.RFC3339)
	}
	if !status.FinishedAt.IsZero() {
		resp.FinishedAt = status.FinishedAt.Format(time.RFC3339)
	}
	return resp
}

func (h *Handler) getRefreshConfigResponse() refreshConfigResponse {
	return refreshConfigResponse{
		ScanEnabled:         h.store.GetRefreshScanEnabled(),
		ScanIntervalSeconds: int(h.store.GetRefreshScanInterval().Seconds()),
		PreExpireSeconds:    int(h.store.GetRefreshPreExpireWindow().Seconds()),
	}
}

type settingsResponse struct {
	MaxConcurrency                  int    `json:"max_concurrency"`
	GlobalRPM                       int    `json:"global_rpm"`
	TestModel                       string `json:"test_model"`
	TestConcurrency                 int    `json:"test_concurrency"`
	ProxyURL                        string `json:"proxy_url"`
	ProxyDefaultMode                string `json:"proxy_default_mode"`
	ProxyDynamicProviderURL         string `json:"proxy_dynamic_provider_url"`
	ProxyDefaultProtocol            string `json:"proxy_default_protocol"`
	ProxyRotationHours              int    `json:"proxy_rotation_hours"`
	PgMaxConns                      int    `json:"pg_max_conns"`
	RedisPoolSize                   int    `json:"redis_pool_size"`
	AutoCleanUnauthorized           bool   `json:"auto_clean_unauthorized"`
	AutoCleanRateLimited            bool   `json:"auto_clean_rate_limited"`
	AdminSecret                     string `json:"admin_secret"`
	AdminAuthSource                 string `json:"admin_auth_source"`
	AutoCleanFullUsage              bool   `json:"auto_clean_full_usage"`
	AutoCleanError                  bool   `json:"auto_clean_error"`
	AutoCleanExpired                bool   `json:"auto_clean_expired"`
	ProxyPoolEnabled                bool   `json:"proxy_pool_enabled"`
	MaxRetries                      int    `json:"max_retries"`
	RefreshScanEnabled              bool   `json:"refresh_scan_enabled"`
	RefreshScanIntervalSeconds      int    `json:"refresh_scan_interval_seconds"`
	RefreshPreExpireSeconds         int    `json:"refresh_pre_expire_seconds"`
	RefreshMaxConcurrency           int    `json:"refresh_max_concurrency"`
	RefreshOnImportEnabled          bool   `json:"refresh_on_import_enabled"`
	RefreshOnImportConcurrency      int    `json:"refresh_on_import_concurrency"`
	UsageProbeEnabled               bool   `json:"usage_probe_enabled"`
	UsageProbeStaleAfterSeconds     int    `json:"usage_probe_stale_after_seconds"`
	UsageProbeMaxConcurrency        int    `json:"usage_probe_max_concurrency"`
	RecoveryProbeEnabled            bool   `json:"recovery_probe_enabled"`
	RecoveryProbeMinIntervalSeconds int    `json:"recovery_probe_min_interval_seconds"`
	RecoveryProbeMaxConcurrency     int    `json:"recovery_probe_max_concurrency"`
	AllowRemoteMigration            bool   `json:"allow_remote_migration"`
	DatabaseDriver                  string `json:"database_driver"`
	DatabaseLabel                   string `json:"database_label"`
	CacheDriver                     string `json:"cache_driver"`
	CacheLabel                      string `json:"cache_label"`
	ExpiredCleaned                  int    `json:"expired_cleaned,omitempty"`
	CPASyncEnabled                  bool   `json:"cpa_sync_enabled"`
	CPABaseURL                      string `json:"cpa_base_url"`
	CPAAdminKey                     string `json:"cpa_admin_key"`
	CPAMinAccounts                  int    `json:"cpa_min_accounts"`
	CPAMaxAccounts                  int    `json:"cpa_max_accounts"`
	CPAMaxUploadsPerHour            int    `json:"cpa_max_uploads_per_hour"`
	CPASwitchAfterUploads           int    `json:"cpa_switch_after_uploads"`
	CPASyncIntervalSeconds          int    `json:"cpa_sync_interval_seconds"`
	MihomoBaseURL                   string `json:"mihomo_base_url"`
	MihomoSecret                    string `json:"mihomo_secret"`
	MihomoStrategyGroup             string `json:"mihomo_strategy_group"`
	MihomoDelayTestURL              string `json:"mihomo_delay_test_url"`
	MihomoDelayTimeoutMs            int    `json:"mihomo_delay_timeout_ms"`
}

type updateSettingsReq struct {
	MaxConcurrency                  *int    `json:"max_concurrency"`
	GlobalRPM                       *int    `json:"global_rpm"`
	TestModel                       *string `json:"test_model"`
	TestConcurrency                 *int    `json:"test_concurrency"`
	ProxyURL                        *string `json:"proxy_url"`
	ProxyDefaultMode                *string `json:"proxy_default_mode"`
	ProxyDynamicProviderURL         *string `json:"proxy_dynamic_provider_url"`
	ProxyDefaultProtocol            *string `json:"proxy_default_protocol"`
	ProxyRotationHours              *int    `json:"proxy_rotation_hours"`
	PgMaxConns                      *int    `json:"pg_max_conns"`
	RedisPoolSize                   *int    `json:"redis_pool_size"`
	AutoCleanUnauthorized           *bool   `json:"auto_clean_unauthorized"`
	AutoCleanRateLimited            *bool   `json:"auto_clean_rate_limited"`
	AdminSecret                     *string `json:"admin_secret"`
	AutoCleanFullUsage              *bool   `json:"auto_clean_full_usage"`
	AutoCleanError                  *bool   `json:"auto_clean_error"`
	AutoCleanExpired                *bool   `json:"auto_clean_expired"`
	ProxyPoolEnabled                *bool   `json:"proxy_pool_enabled"`
	MaxRetries                      *int    `json:"max_retries"`
	RefreshScanEnabled              *bool   `json:"refresh_scan_enabled"`
	RefreshScanIntervalSeconds      *int    `json:"refresh_scan_interval_seconds"`
	RefreshPreExpireSeconds         *int    `json:"refresh_pre_expire_seconds"`
	RefreshMaxConcurrency           *int    `json:"refresh_max_concurrency"`
	RefreshOnImportEnabled          *bool   `json:"refresh_on_import_enabled"`
	RefreshOnImportConcurrency      *int    `json:"refresh_on_import_concurrency"`
	UsageProbeEnabled               *bool   `json:"usage_probe_enabled"`
	UsageProbeStaleAfterSeconds     *int    `json:"usage_probe_stale_after_seconds"`
	UsageProbeMaxConcurrency        *int    `json:"usage_probe_max_concurrency"`
	RecoveryProbeEnabled            *bool   `json:"recovery_probe_enabled"`
	RecoveryProbeMinIntervalSeconds *int    `json:"recovery_probe_min_interval_seconds"`
	RecoveryProbeMaxConcurrency     *int    `json:"recovery_probe_max_concurrency"`
	AllowRemoteMigration            *bool   `json:"allow_remote_migration"`
	CPASyncEnabled                  *bool   `json:"cpa_sync_enabled"`
	CPABaseURL                      *string `json:"cpa_base_url"`
	CPAAdminKey                     *string `json:"cpa_admin_key"`
	CPAMinAccounts                  *int    `json:"cpa_min_accounts"`
	CPAMaxAccounts                  *int    `json:"cpa_max_accounts"`
	CPAMaxUploadsPerHour            *int    `json:"cpa_max_uploads_per_hour"`
	CPASwitchAfterUploads           *int    `json:"cpa_switch_after_uploads"`
	CPASyncIntervalSeconds          *int    `json:"cpa_sync_interval_seconds"`
	MihomoBaseURL                   *string `json:"mihomo_base_url"`
	MihomoSecret                    *string `json:"mihomo_secret"`
	MihomoStrategyGroup             *string `json:"mihomo_strategy_group"`
	MihomoDelayTestURL              *string `json:"mihomo_delay_test_url"`
	MihomoDelayTimeoutMs            *int    `json:"mihomo_delay_timeout_ms"`
}

type runtimeSettingsSnapshot struct {
	maxConcurrency              int
	globalRPM                   int
	testModel                   string
	testConcurrency             int
	proxyURL                    string
	proxyDefaultMode            string
	proxyDynamicProviderURL     string
	proxyDefaultProtocol        string
	proxyRotationHours          int
	pgMaxConns                  int
	redisPoolSize               int
	autoCleanUnauthorized       bool
	autoCleanRateLimited        bool
	autoCleanFullUsage          bool
	autoCleanError              bool
	autoCleanExpired            bool
	proxyPoolEnabled            bool
	maxRetries                  int
	refreshScanEnabled          bool
	refreshScanInterval         time.Duration
	refreshPreExpireWindow      time.Duration
	refreshMaxConcurrency       int
	refreshOnImportEnabled      bool
	refreshOnImportConcurrency  int
	usageProbeEnabled           bool
	usageProbeStaleAfter        time.Duration
	usageProbeMaxConcurrency    int
	recoveryProbeEnabled        bool
	recoveryProbeMinInterval    time.Duration
	recoveryProbeMaxConcurrency int
	allowRemoteMigration        bool
}

func (h *Handler) captureRuntimeSettingsSnapshot() runtimeSettingsSnapshot {
	return runtimeSettingsSnapshot{
		maxConcurrency:              h.store.GetMaxConcurrency(),
		globalRPM:                   h.rateLimiter.GetRPM(),
		testModel:                   h.store.GetTestModel(),
		testConcurrency:             h.store.GetTestConcurrency(),
		proxyURL:                    h.store.GetProxyURL(),
		proxyDefaultMode:            h.store.GetProxyMode(),
		proxyDynamicProviderURL:     h.store.GetProxyProviderURL(),
		proxyDefaultProtocol:        h.store.GetProxySchemeDefault(),
		proxyRotationHours:          h.store.GetProxyRotationHours(),
		pgMaxConns:                  h.pgMaxConns,
		redisPoolSize:               h.redisPoolSize,
		autoCleanUnauthorized:       h.store.GetAutoCleanUnauthorized(),
		autoCleanRateLimited:        h.store.GetAutoCleanRateLimited(),
		autoCleanFullUsage:          h.store.GetAutoCleanFullUsage(),
		autoCleanError:              h.store.GetAutoCleanError(),
		autoCleanExpired:            h.store.GetAutoCleanExpired(),
		proxyPoolEnabled:            h.store.GetProxyPoolEnabled(),
		maxRetries:                  h.store.GetMaxRetries(),
		refreshScanEnabled:          h.store.GetRefreshScanEnabled(),
		refreshScanInterval:         h.store.GetRefreshScanInterval(),
		refreshPreExpireWindow:      h.store.GetRefreshPreExpireWindow(),
		refreshMaxConcurrency:       h.store.GetRefreshMaxConcurrency(),
		refreshOnImportEnabled:      h.store.GetRefreshOnImportEnabled(),
		refreshOnImportConcurrency:  h.store.GetRefreshOnImportConcurrency(),
		usageProbeEnabled:           h.store.GetUsageProbeEnabled(),
		usageProbeStaleAfter:        h.store.GetUsageProbeStaleAfter(),
		usageProbeMaxConcurrency:    h.store.GetUsageProbeMaxConcurrency(),
		recoveryProbeEnabled:        h.store.GetRecoveryProbeEnabled(),
		recoveryProbeMinInterval:    h.store.GetRecoveryProbeMinInterval(),
		recoveryProbeMaxConcurrency: h.store.GetRecoveryProbeMaxConcurrency(),
		allowRemoteMigration:        h.store.GetAllowRemoteMigration(),
	}
}

func (h *Handler) restoreRuntimeSettingsSnapshot(snapshot runtimeSettingsSnapshot) {
	h.store.SetMaxConcurrency(snapshot.maxConcurrency)
	h.rateLimiter.UpdateRPM(snapshot.globalRPM)
	h.store.SetTestModel(snapshot.testModel)
	h.store.SetTestConcurrency(snapshot.testConcurrency)
	h.store.SetProxyURL(snapshot.proxyURL)
	h.store.SetProxyMode(snapshot.proxyDefaultMode)
	h.store.SetProxyProviderURL(snapshot.proxyDynamicProviderURL)
	h.store.SetProxySchemeDefault(snapshot.proxyDefaultProtocol)
	h.store.SetProxyRotationHours(snapshot.proxyRotationHours)
	h.db.SetMaxOpenConns(snapshot.pgMaxConns)
	h.pgMaxConns = snapshot.pgMaxConns
	h.cache.SetPoolSize(snapshot.redisPoolSize)
	h.redisPoolSize = snapshot.redisPoolSize
	h.store.SetAutoCleanUnauthorized(snapshot.autoCleanUnauthorized)
	h.store.SetAutoCleanRateLimited(snapshot.autoCleanRateLimited)
	h.store.SetAutoCleanFullUsage(snapshot.autoCleanFullUsage)
	h.store.SetAutoCleanError(snapshot.autoCleanError)
	h.store.SetAutoCleanExpired(snapshot.autoCleanExpired)
	h.store.SetProxyPoolEnabled(snapshot.proxyPoolEnabled)
	h.store.SetMaxRetries(snapshot.maxRetries)
	h.store.SetRefreshScanEnabled(snapshot.refreshScanEnabled)
	h.store.SetRefreshScanInterval(snapshot.refreshScanInterval)
	h.store.SetRefreshPreExpireWindow(snapshot.refreshPreExpireWindow)
	h.store.SetRefreshMaxConcurrency(snapshot.refreshMaxConcurrency)
	h.store.SetRefreshOnImportEnabled(snapshot.refreshOnImportEnabled)
	h.store.SetRefreshOnImportConcurrency(snapshot.refreshOnImportConcurrency)
	h.store.SetUsageProbeEnabled(snapshot.usageProbeEnabled)
	h.store.SetUsageProbeStaleAfter(snapshot.usageProbeStaleAfter)
	h.store.SetUsageProbeMaxConcurrency(snapshot.usageProbeMaxConcurrency)
	h.store.SetRecoveryProbeEnabled(snapshot.recoveryProbeEnabled)
	h.store.SetRecoveryProbeMinInterval(snapshot.recoveryProbeMinInterval)
	h.store.SetRecoveryProbeMaxConcurrency(snapshot.recoveryProbeMaxConcurrency)
	h.store.SetAllowRemoteMigration(snapshot.allowRemoteMigration)
}

// GetSettings 获取当前系统设置
func (h *Handler) GetSettings(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	dbSettings, err := h.db.GetSystemSettings(ctx)
	if err != nil {
		writeError(c, http.StatusServiceUnavailable, "读取系统设置失败")
		return
	}
	if dbSettings == nil {
		dbSettings = &database.SystemSettings{}
	}
	if dbSettings.MihomoDelayTimeoutMs <= 0 {
		dbSettings.MihomoDelayTimeoutMs = 5000
	}
	if dbSettings.CPASyncIntervalSeconds <= 0 {
		dbSettings.CPASyncIntervalSeconds = 300
	}
	_, adminAuthSource := h.resolveAdminSecret(c.Request.Context())
	adminSecret := ""
	if adminAuthSource != "env" {
		adminSecret = dbSettings.AdminSecret
	}
	c.JSON(http.StatusOK, settingsResponse{
		MaxConcurrency:                  h.store.GetMaxConcurrency(),
		GlobalRPM:                       h.rateLimiter.GetRPM(),
		TestModel:                       h.store.GetTestModel(),
		TestConcurrency:                 h.store.GetTestConcurrency(),
		ProxyURL:                        h.store.GetProxyURL(),
		ProxyDefaultMode:                h.store.GetProxyMode(),
		ProxyDynamicProviderURL:         h.store.GetProxyProviderURL(),
		ProxyDefaultProtocol:            h.store.GetProxySchemeDefault(),
		ProxyRotationHours:              h.store.GetProxyRotationHours(),
		PgMaxConns:                      h.pgMaxConns,
		RedisPoolSize:                   h.redisPoolSize,
		AutoCleanUnauthorized:           h.store.GetAutoCleanUnauthorized(),
		AutoCleanRateLimited:            h.store.GetAutoCleanRateLimited(),
		AdminSecret:                     adminSecret,
		AdminAuthSource:                 adminAuthSource,
		AutoCleanFullUsage:              h.store.GetAutoCleanFullUsage(),
		AutoCleanError:                  h.store.GetAutoCleanError(),
		AutoCleanExpired:                h.store.GetAutoCleanExpired(),
		ProxyPoolEnabled:                h.store.GetProxyPoolEnabled(),
		MaxRetries:                      h.store.GetMaxRetries(),
		RefreshScanEnabled:              h.store.GetRefreshScanEnabled(),
		RefreshScanIntervalSeconds:      int(h.store.GetRefreshScanInterval().Seconds()),
		RefreshPreExpireSeconds:         int(h.store.GetRefreshPreExpireWindow().Seconds()),
		RefreshMaxConcurrency:           h.store.GetRefreshMaxConcurrency(),
		RefreshOnImportEnabled:          h.store.GetRefreshOnImportEnabled(),
		RefreshOnImportConcurrency:      h.store.GetRefreshOnImportConcurrency(),
		UsageProbeEnabled:               h.store.GetUsageProbeEnabled(),
		UsageProbeStaleAfterSeconds:     int(h.store.GetUsageProbeStaleAfter().Seconds()),
		UsageProbeMaxConcurrency:        h.store.GetUsageProbeMaxConcurrency(),
		RecoveryProbeEnabled:            h.store.GetRecoveryProbeEnabled(),
		RecoveryProbeMinIntervalSeconds: int(h.store.GetRecoveryProbeMinInterval().Seconds()),
		RecoveryProbeMaxConcurrency:     h.store.GetRecoveryProbeMaxConcurrency(),
		AllowRemoteMigration:            h.store.GetAllowRemoteMigration() && adminAuthSource != "disabled",
		DatabaseDriver:                  h.databaseDriver,
		DatabaseLabel:                   h.databaseLabel,
		CacheDriver:                     h.cacheDriver,
		CacheLabel:                      h.cacheLabel,
		CPASyncEnabled:                  dbSettings.CPASyncEnabled,
		CPABaseURL:                      dbSettings.CPABaseURL,
		CPAAdminKey:                     dbSettings.CPAAdminKey,
		CPAMinAccounts:                  dbSettings.CPAMinAccounts,
		CPAMaxAccounts:                  dbSettings.CPAMaxAccounts,
		CPAMaxUploadsPerHour:            dbSettings.CPAMaxUploadsPerHour,
		CPASwitchAfterUploads:           dbSettings.CPASwitchAfterUploads,
		CPASyncIntervalSeconds:          dbSettings.CPASyncIntervalSeconds,
		MihomoBaseURL:                   dbSettings.MihomoBaseURL,
		MihomoSecret:                    dbSettings.MihomoSecret,
		MihomoStrategyGroup:             dbSettings.MihomoStrategyGroup,
		MihomoDelayTestURL:              dbSettings.MihomoDelayTestURL,
		MihomoDelayTimeoutMs:            dbSettings.MihomoDelayTimeoutMs,
	})
}

// UpdateSettings 更新系统设置（实时生效）
func (h *Handler) UpdateSettings(c *gin.Context) {
	var req updateSettingsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求格式错误")
		return
	}

	existingSettings, err := h.db.GetSystemSettings(c.Request.Context())
	if err != nil {
		writeError(c, http.StatusServiceUnavailable, "系统设置不可用")
		return
	}
	if existingSettings == nil {
		existingSettings = &database.SystemSettings{}
	}

	currentAdminSecret := existingSettings.AdminSecret
	if req.AdminSecret != nil {
		if h.adminSecretEnv == "" {
			currentAdminSecret = *req.AdminSecret
			log.Printf("?????: admin_secret (??=%d)", len(currentAdminSecret))
		} else {
			log.Printf("??????? ADMIN_SECRET???????? admin_secret")
		}
	}
	hasAdminSecret := strings.TrimSpace(currentAdminSecret) != "" || strings.TrimSpace(h.adminSecretEnv) != ""

	pendingProxyConfig := database.ProxyConfigInput{
		Mode:        h.store.GetProxyMode(),
		URL:         h.store.GetProxyURL(),
		ProviderURL: h.store.GetProxyProviderURL(),
		Scheme:      h.store.GetProxySchemeDefault(),
	}
	if req.ProxyURL != nil || req.ProxyDefaultMode != nil || req.ProxyDynamicProviderURL != nil || req.ProxyDefaultProtocol != nil || req.ProxyPoolEnabled != nil {
		pendingProxyURL := pendingProxyConfig.URL
		if req.ProxyURL != nil {
			pendingProxyURL = *req.ProxyURL
		}
		pendingProxyMode := pendingProxyConfig.Mode
		if req.ProxyDefaultMode != nil {
			pendingProxyMode = *req.ProxyDefaultMode
		}
		pendingProxyProviderURL := pendingProxyConfig.ProviderURL
		if req.ProxyDynamicProviderURL != nil {
			pendingProxyProviderURL = *req.ProxyDynamicProviderURL
		}
		pendingProxyScheme := pendingProxyConfig.Scheme
		if req.ProxyDefaultProtocol != nil {
			pendingProxyScheme = *req.ProxyDefaultProtocol
		}
		pendingProxyPoolEnabled := h.store.GetProxyPoolEnabled()
		if req.ProxyPoolEnabled != nil {
			pendingProxyPoolEnabled = *req.ProxyPoolEnabled
		}
		pendingProxyConfig, err = buildProxyConfigInput(pendingProxyMode, pendingProxyURL, pendingProxyProviderURL, pendingProxyScheme, pendingProxyPoolEnabled, false)
		if err != nil {
			writeError(c, http.StatusBadRequest, err.Error())
			return
		}
	}

	if req.AllowRemoteMigration != nil && *req.AllowRemoteMigration && !hasAdminSecret {
		writeError(c, http.StatusBadRequest, "启用远程迁移前请先设置管理员密钥")
		return
	}

	cpaSyncEnabled := existingSettings.CPASyncEnabled
	if req.CPASyncEnabled != nil {
		cpaSyncEnabled = *req.CPASyncEnabled
	}

	cpaBaseURL := existingSettings.CPABaseURL
	if req.CPABaseURL != nil {
		sanitized, err := validateExternalServiceBaseURL(c.Request.Context(), *req.CPABaseURL, "cpa_base_url")
		if err != nil {
			writeError(c, http.StatusBadRequest, err.Error())
			return
		}
		cpaBaseURL = sanitized
	}

	cpaAdminKey := existingSettings.CPAAdminKey
	if req.CPAAdminKey != nil {
		cpaAdminKey = strings.TrimSpace(*req.CPAAdminKey)
	}

	cpaMinAccounts := existingSettings.CPAMinAccounts
	if req.CPAMinAccounts != nil {
		cpaMinAccounts = *req.CPAMinAccounts
		if cpaMinAccounts < 0 {
			cpaMinAccounts = 0
		}
	}

	cpaMaxAccounts := existingSettings.CPAMaxAccounts
	if req.CPAMaxAccounts != nil {
		cpaMaxAccounts = *req.CPAMaxAccounts
		if cpaMaxAccounts < 0 {
			cpaMaxAccounts = 0
		}
	}

	cpaMaxUploadsPerHour := existingSettings.CPAMaxUploadsPerHour
	if req.CPAMaxUploadsPerHour != nil {
		cpaMaxUploadsPerHour = *req.CPAMaxUploadsPerHour
		if cpaMaxUploadsPerHour < 0 {
			cpaMaxUploadsPerHour = 0
		}
	}

	cpaSwitchAfterUploads := existingSettings.CPASwitchAfterUploads
	if req.CPASwitchAfterUploads != nil {
		cpaSwitchAfterUploads = *req.CPASwitchAfterUploads
		if cpaSwitchAfterUploads < 0 {
			cpaSwitchAfterUploads = 0
		}
	}

	cpaSyncIntervalSeconds := existingSettings.CPASyncIntervalSeconds
	if cpaSyncIntervalSeconds <= 0 {
		cpaSyncIntervalSeconds = 300
	}
	if req.CPASyncIntervalSeconds != nil {
		cpaSyncIntervalSeconds = *req.CPASyncIntervalSeconds
		if cpaSyncIntervalSeconds < 30 {
			cpaSyncIntervalSeconds = 30
		}
		if cpaSyncIntervalSeconds > 86400 {
			cpaSyncIntervalSeconds = 86400
		}
	}

	mihomoBaseURL := existingSettings.MihomoBaseURL
	if req.MihomoBaseURL != nil {
		sanitized, err := validateExternalServiceBaseURL(c.Request.Context(), *req.MihomoBaseURL, "mihomo_base_url")
		if err != nil {
			writeError(c, http.StatusBadRequest, err.Error())
			return
		}
		mihomoBaseURL = sanitized
	}

	mihomoSecret := existingSettings.MihomoSecret
	if req.MihomoSecret != nil {
		mihomoSecret = strings.TrimSpace(*req.MihomoSecret)
	}

	mihomoStrategyGroup := existingSettings.MihomoStrategyGroup
	if req.MihomoStrategyGroup != nil {
		mihomoStrategyGroup = strings.TrimSpace(*req.MihomoStrategyGroup)
	}

	mihomoDelayTestURL := existingSettings.MihomoDelayTestURL
	if req.MihomoDelayTestURL != nil {
		sanitized, err := validateExternalTargetURL(c.Request.Context(), *req.MihomoDelayTestURL, "mihomo_delay_test_url")
		if err != nil {
			writeError(c, http.StatusBadRequest, err.Error())
			return
		}
		mihomoDelayTestURL = sanitized
	}

	mihomoDelayTimeoutMs := existingSettings.MihomoDelayTimeoutMs
	if mihomoDelayTimeoutMs <= 0 {
		mihomoDelayTimeoutMs = 5000
	}
	if req.MihomoDelayTimeoutMs != nil {
		mihomoDelayTimeoutMs = *req.MihomoDelayTimeoutMs
		if mihomoDelayTimeoutMs < 100 {
			mihomoDelayTimeoutMs = 100
		}
		if mihomoDelayTimeoutMs > 120000 {
			mihomoDelayTimeoutMs = 120000
		}
	}

	runtimeSnapshot := h.captureRuntimeSettingsSnapshot()
	proxyPoolReloadNeeded := false
	expiredCleanRequested := false

	if req.MaxConcurrency != nil {
		v := *req.MaxConcurrency
		if v < 1 {
			v = 1
		}
		if v > 50 {
			v = 50
		}
		h.store.SetMaxConcurrency(v)
		log.Printf("?????: max_concurrency = %d", v)
	}

	if req.GlobalRPM != nil {
		v := *req.GlobalRPM
		if v < 0 {
			v = 0
		}
		h.rateLimiter.UpdateRPM(v)
		log.Printf("?????: global_rpm = %d", v)
	}

	if req.TestModel != nil && *req.TestModel != "" {
		h.store.SetTestModel(*req.TestModel)
		log.Printf("?????: test_model = %s", *req.TestModel)
	}

	if req.TestConcurrency != nil {
		v := *req.TestConcurrency
		if v < 1 {
			v = 1
		}
		if v > 200 {
			v = 200
		}
		h.store.SetTestConcurrency(v)
		log.Printf("?????: test_concurrency = %d", v)
	}

	if req.ProxyURL != nil || req.ProxyDefaultMode != nil || req.ProxyDynamicProviderURL != nil || req.ProxyDefaultProtocol != nil || req.ProxyPoolEnabled != nil {
		if req.ProxyURL != nil {
			h.store.SetProxyURL(pendingProxyConfig.URL)
			log.Printf("?????: proxy_url = %s", security.SanitizeLog(pendingProxyConfig.URL))
		}
		h.store.SetProxyMode(pendingProxyConfig.Mode)
		if req.ProxyDynamicProviderURL != nil {
			h.store.SetProxyProviderURL(pendingProxyConfig.ProviderURL)
		}
		if req.ProxyDefaultProtocol != nil {
			h.store.SetProxySchemeDefault(pendingProxyConfig.Scheme)
		}
	}

	if req.ProxyRotationHours != nil {
		v := *req.ProxyRotationHours
		if v < 1 {
			v = 1
		}
		if v > 24*30 {
			v = 24 * 30
		}
		h.store.SetProxyRotationHours(v)
	}

	if req.PgMaxConns != nil {
		v := *req.PgMaxConns
		if v < 5 {
			v = 5
		}
		if v > 500 {
			v = 500
		}
		h.db.SetMaxOpenConns(v)
		h.pgMaxConns = v
		log.Printf("?????: pg_max_conns = %d", v)
	}

	if req.RedisPoolSize != nil {
		v := *req.RedisPoolSize
		if v < 5 {
			v = 5
		}
		if v > 500 {
			v = 500
		}
		h.cache.SetPoolSize(v)
		h.redisPoolSize = v
		log.Printf("?????: redis_pool_size = %d", v)
	}

	if req.AutoCleanUnauthorized != nil {
		h.store.SetAutoCleanUnauthorized(*req.AutoCleanUnauthorized)
		log.Printf("?????: auto_clean_unauthorized = %t", *req.AutoCleanUnauthorized)
	}

	if req.AutoCleanRateLimited != nil {
		h.store.SetAutoCleanRateLimited(*req.AutoCleanRateLimited)
		log.Printf("?????: auto_clean_rate_limited = %t", *req.AutoCleanRateLimited)
	}

	if req.AutoCleanFullUsage != nil {
		h.store.SetAutoCleanFullUsage(*req.AutoCleanFullUsage)
		log.Printf("?????: auto_clean_full_usage = %t", *req.AutoCleanFullUsage)
	}

	if req.AutoCleanError != nil {
		h.store.SetAutoCleanError(*req.AutoCleanError)
		log.Printf("?????: auto_clean_error = %t", *req.AutoCleanError)
	}

	var expiredCleaned int
	if req.AutoCleanExpired != nil {
		h.store.SetAutoCleanExpired(*req.AutoCleanExpired)
		log.Printf("?????: auto_clean_expired = %t", *req.AutoCleanExpired)
		expiredCleanRequested = *req.AutoCleanExpired
	}

	if req.ProxyPoolEnabled != nil {
		h.store.SetProxyPoolEnabled(*req.ProxyPoolEnabled)
		proxyPoolReloadNeeded = *req.ProxyPoolEnabled
		log.Printf("?????: proxy_pool_enabled = %t", *req.ProxyPoolEnabled)
	}

	if req.MaxRetries != nil {
		v := *req.MaxRetries
		if v < 0 {
			v = 0
		}
		if v > 10 {
			v = 10
		}
		h.store.SetMaxRetries(v)
		log.Printf("?????: max_retries = %d", v)
	}

	if req.RefreshScanEnabled != nil {
		h.store.SetRefreshScanEnabled(*req.RefreshScanEnabled)
	}
	if req.RefreshScanIntervalSeconds != nil {
		h.store.SetRefreshScanInterval(time.Duration(*req.RefreshScanIntervalSeconds) * time.Second)
	}
	if req.RefreshPreExpireSeconds != nil {
		h.store.SetRefreshPreExpireWindow(time.Duration(*req.RefreshPreExpireSeconds) * time.Second)
	}
	if req.RefreshMaxConcurrency != nil {
		h.store.SetRefreshMaxConcurrency(*req.RefreshMaxConcurrency)
	}
	if req.RefreshOnImportEnabled != nil {
		h.store.SetRefreshOnImportEnabled(*req.RefreshOnImportEnabled)
	}
	if req.RefreshOnImportConcurrency != nil {
		h.store.SetRefreshOnImportConcurrency(*req.RefreshOnImportConcurrency)
	}
	if req.UsageProbeEnabled != nil {
		h.store.SetUsageProbeEnabled(*req.UsageProbeEnabled)
	}
	if req.UsageProbeStaleAfterSeconds != nil {
		h.store.SetUsageProbeStaleAfter(time.Duration(*req.UsageProbeStaleAfterSeconds) * time.Second)
	}
	if req.UsageProbeMaxConcurrency != nil {
		h.store.SetUsageProbeMaxConcurrency(*req.UsageProbeMaxConcurrency)
	}
	if req.RecoveryProbeEnabled != nil {
		h.store.SetRecoveryProbeEnabled(*req.RecoveryProbeEnabled)
	}
	if req.RecoveryProbeMinIntervalSeconds != nil {
		h.store.SetRecoveryProbeMinInterval(time.Duration(*req.RecoveryProbeMinIntervalSeconds) * time.Second)
	}
	if req.RecoveryProbeMaxConcurrency != nil {
		h.store.SetRecoveryProbeMaxConcurrency(*req.RecoveryProbeMaxConcurrency)
	}

	if req.AllowRemoteMigration != nil {
		h.store.SetAllowRemoteMigration(*req.AllowRemoteMigration)
		log.Printf("?????: allow_remote_migration = %t", *req.AllowRemoteMigration)
	} else if !hasAdminSecret {
		h.store.SetAllowRemoteMigration(false)
	}

	err = h.db.UpdateSystemSettings(c.Request.Context(), &database.SystemSettings{
		MaxConcurrency:                  h.store.GetMaxConcurrency(),
		GlobalRPM:                       h.rateLimiter.GetRPM(),
		TestModel:                       h.store.GetTestModel(),
		TestConcurrency:                 h.store.GetTestConcurrency(),
		ProxyURL:                        h.store.GetProxyURL(),
		ProxyMode:                       h.store.GetProxyMode(),
		ProxyProviderURL:                h.store.GetProxyProviderURL(),
		ProxySchemeDefault:              h.store.GetProxySchemeDefault(),
		ProxyRotationHours:              h.store.GetProxyRotationHours(),
		PgMaxConns:                      h.pgMaxConns,
		RedisPoolSize:                   h.redisPoolSize,
		AutoCleanUnauthorized:           h.store.GetAutoCleanUnauthorized(),
		AutoCleanRateLimited:            h.store.GetAutoCleanRateLimited(),
		AdminSecret:                     currentAdminSecret,
		AutoCleanFullUsage:              h.store.GetAutoCleanFullUsage(),
		AutoCleanError:                  h.store.GetAutoCleanError(),
		AutoCleanExpired:                h.store.GetAutoCleanExpired(),
		ProxyPoolEnabled:                h.store.GetProxyPoolEnabled(),
		MaxRetries:                      h.store.GetMaxRetries(),
		RefreshScanEnabled:              h.store.GetRefreshScanEnabled(),
		RefreshScanIntervalSeconds:      int(h.store.GetRefreshScanInterval().Seconds()),
		RefreshPreExpireSeconds:         int(h.store.GetRefreshPreExpireWindow().Seconds()),
		RefreshMaxConcurrency:           h.store.GetRefreshMaxConcurrency(),
		RefreshOnImportEnabled:          h.store.GetRefreshOnImportEnabled(),
		RefreshOnImportConcurrency:      h.store.GetRefreshOnImportConcurrency(),
		UsageProbeEnabled:               h.store.GetUsageProbeEnabled(),
		UsageProbeStaleAfterSeconds:     int(h.store.GetUsageProbeStaleAfter().Seconds()),
		UsageProbeMaxConcurrency:        h.store.GetUsageProbeMaxConcurrency(),
		RecoveryProbeEnabled:            h.store.GetRecoveryProbeEnabled(),
		RecoveryProbeMinIntervalSeconds: int(h.store.GetRecoveryProbeMinInterval().Seconds()),
		RecoveryProbeMaxConcurrency:     h.store.GetRecoveryProbeMaxConcurrency(),
		AllowRemoteMigration:            h.store.GetAllowRemoteMigration() && hasAdminSecret,
		ModelMapping:                    h.store.GetModelMapping(),
		CPASyncEnabled:                  cpaSyncEnabled,
		CPABaseURL:                      cpaBaseURL,
		CPAAdminKey:                     cpaAdminKey,
		CPAMinAccounts:                  cpaMinAccounts,
		CPAMaxAccounts:                  cpaMaxAccounts,
		CPAMaxUploadsPerHour:            cpaMaxUploadsPerHour,
		CPASwitchAfterUploads:           cpaSwitchAfterUploads,
		CPASyncIntervalSeconds:          cpaSyncIntervalSeconds,
		MihomoBaseURL:                   mihomoBaseURL,
		MihomoSecret:                    mihomoSecret,
		MihomoStrategyGroup:             mihomoStrategyGroup,
		MihomoDelayTestURL:              mihomoDelayTestURL,
		MihomoDelayTimeoutMs:            mihomoDelayTimeoutMs,
	})
	if err != nil {
		h.restoreRuntimeSettingsSnapshot(runtimeSnapshot)
		log.Printf("?????????: %v", err)
		writeError(c, http.StatusServiceUnavailable, "保存系统设置失败")
		return
	}

	if proxyPoolReloadNeeded {
		_ = h.store.ReloadProxyPool()
	}
	if expiredCleanRequested {
		expiredCleaned = h.store.CleanExpiredNow()
	}

	if h.store.GetAutoCleanUnauthorized() || h.store.GetAutoCleanRateLimited() || h.store.GetAutoCleanError() {
		h.store.TriggerAutoCleanupAsync()
	}
	if h.cpaSync != nil {
		h.cpaSync.NotifyConfigChanged()
	}

	adminSecretForDisplay := currentAdminSecret
	adminAuthSource := func() string {
		_, source := h.resolveAdminSecret(c.Request.Context())
		return source
	}()
	if adminAuthSource == "env" {
		adminSecretForDisplay = ""
	}

	c.JSON(http.StatusOK, settingsResponse{
		MaxConcurrency:                  h.store.GetMaxConcurrency(),
		GlobalRPM:                       h.rateLimiter.GetRPM(),
		TestModel:                       h.store.GetTestModel(),
		TestConcurrency:                 h.store.GetTestConcurrency(),
		ProxyURL:                        h.store.GetProxyURL(),
		ProxyDefaultMode:                h.store.GetProxyMode(),
		ProxyDynamicProviderURL:         h.store.GetProxyProviderURL(),
		ProxyDefaultProtocol:            h.store.GetProxySchemeDefault(),
		ProxyRotationHours:              h.store.GetProxyRotationHours(),
		PgMaxConns:                      h.pgMaxConns,
		RedisPoolSize:                   h.redisPoolSize,
		AutoCleanUnauthorized:           h.store.GetAutoCleanUnauthorized(),
		AutoCleanRateLimited:            h.store.GetAutoCleanRateLimited(),
		AdminSecret:                     adminSecretForDisplay,
		AdminAuthSource:                 adminAuthSource,
		AutoCleanFullUsage:              h.store.GetAutoCleanFullUsage(),
		AutoCleanError:                  h.store.GetAutoCleanError(),
		AutoCleanExpired:                h.store.GetAutoCleanExpired(),
		ProxyPoolEnabled:                h.store.GetProxyPoolEnabled(),
		MaxRetries:                      h.store.GetMaxRetries(),
		RefreshScanEnabled:              h.store.GetRefreshScanEnabled(),
		RefreshScanIntervalSeconds:      int(h.store.GetRefreshScanInterval().Seconds()),
		RefreshPreExpireSeconds:         int(h.store.GetRefreshPreExpireWindow().Seconds()),
		RefreshMaxConcurrency:           h.store.GetRefreshMaxConcurrency(),
		RefreshOnImportEnabled:          h.store.GetRefreshOnImportEnabled(),
		RefreshOnImportConcurrency:      h.store.GetRefreshOnImportConcurrency(),
		UsageProbeEnabled:               h.store.GetUsageProbeEnabled(),
		UsageProbeStaleAfterSeconds:     int(h.store.GetUsageProbeStaleAfter().Seconds()),
		UsageProbeMaxConcurrency:        h.store.GetUsageProbeMaxConcurrency(),
		RecoveryProbeEnabled:            h.store.GetRecoveryProbeEnabled(),
		RecoveryProbeMinIntervalSeconds: int(h.store.GetRecoveryProbeMinInterval().Seconds()),
		RecoveryProbeMaxConcurrency:     h.store.GetRecoveryProbeMaxConcurrency(),
		AllowRemoteMigration:            h.store.GetAllowRemoteMigration() && adminAuthSource != "disabled",
		DatabaseDriver:                  h.databaseDriver,
		DatabaseLabel:                   h.databaseLabel,
		CacheDriver:                     h.cacheDriver,
		CacheLabel:                      h.cacheLabel,
		ExpiredCleaned:                  expiredCleaned,
		CPASyncEnabled:                  cpaSyncEnabled,
		CPABaseURL:                      cpaBaseURL,
		CPAAdminKey:                     cpaAdminKey,
		CPAMinAccounts:                  cpaMinAccounts,
		CPAMaxAccounts:                  cpaMaxAccounts,
		CPAMaxUploadsPerHour:            cpaMaxUploadsPerHour,
		CPASwitchAfterUploads:           cpaSwitchAfterUploads,
		CPASyncIntervalSeconds:          cpaSyncIntervalSeconds,
		MihomoBaseURL:                   mihomoBaseURL,
		MihomoSecret:                    mihomoSecret,
		MihomoStrategyGroup:             mihomoStrategyGroup,
		MihomoDelayTestURL:              mihomoDelayTestURL,
		MihomoDelayTimeoutMs:            mihomoDelayTimeoutMs,
	})
}

type cpaExportEntry struct {
	Type         string `json:"type"`
	Email        string `json:"email"`
	Expired      string `json:"expired"`
	IDToken      string `json:"id_token"`
	AccountID    string `json:"account_id"`
	AccessToken  string `json:"access_token"`
	LastRefresh  string `json:"last_refresh"`
	RefreshToken string `json:"refresh_token"`
}

// ExportAccounts 导出账号（CPA JSON 格式）
func (h *Handler) ExportAccounts(c *gin.Context) {
	filter := c.DefaultQuery("filter", "healthy")
	idsParam := c.Query("ids")
	remote := c.Query("remote")

	// 远程调用需检查 allow_remote_migration
	if remote == "true" {
		if !h.hasConfiguredAdminSecret(c.Request.Context()) {
			writeError(c, http.StatusForbidden, "请先设置管理密钥，再启用远程迁移")
			return
		}
		if !h.store.GetAllowRemoteMigration() {
			writeError(c, http.StatusForbidden, "远程迁移未启用，请在系统设置中开启")
			return
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	rows, err := h.db.ListActive(ctx)
	if err != nil {
		writeLoggedInternalError(c, "查询账号失败", err)
		return
	}

	// 按指定 ID 过滤
	var idSet map[int64]bool
	if idsParam != "" {
		idSet = make(map[int64]bool)
		for _, s := range strings.Split(idsParam, ",") {
			if id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
				idSet[id] = true
			}
		}
	}

	// 构建运行时状态映射（用于健康过滤）
	runtimeMap := make(map[int64]*auth.Account)
	if filter == "healthy" {
		for _, acc := range h.store.Accounts() {
			runtimeMap[acc.DBID] = acc
		}
	}

	var entries []cpaExportEntry
	for _, row := range rows {
		if idSet != nil && !idSet[row.ID] {
			continue
		}
		if filter == "healthy" {
			acc, ok := runtimeMap[row.ID]
			if !ok || acc.RuntimeStatus() != "active" {
				continue
			}
		}
		rt := row.GetCredential("refresh_token")
		if rt == "" {
			continue
		}
		entries = append(entries, cpaExportEntry{
			Type:         "codex",
			Email:        row.GetCredential("email"),
			Expired:      row.GetCredential("expires_at"),
			IDToken:      row.GetCredential("id_token"),
			AccountID:    row.GetCredential("account_id"),
			AccessToken:  row.GetCredential("access_token"),
			LastRefresh:  row.UpdatedAt.Format(time.RFC3339),
			RefreshToken: rt,
		})
	}

	if entries == nil {
		entries = []cpaExportEntry{}
	}
	c.JSON(http.StatusOK, entries)
}

type migrateReq struct {
	URL      string `json:"url"`
	AdminKey string `json:"admin_key"`
}

// MigrateAccounts 从远程 codex2api 实例迁移健康账号（SSE 流式进度）
func (h *Handler) MigrateAccounts(c *gin.Context) {
	const (
		maxMigrateErrorBodyBytes = 256 * 1024
		maxMigratePayloadBytes   = 20 * 1024 * 1024
	)

	if !h.hasConfiguredAdminSecret(c.Request.Context()) {
		writeError(c, http.StatusForbidden, "请先设置管理密钥，再使用远程迁移")
		return
	}

	var req migrateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求格式错误")
		return
	}
	if req.URL == "" || req.AdminKey == "" {
		writeError(c, http.StatusBadRequest, "url 和 admin_key 是必填字段")
		return
	}

	remoteURL, err := validateMigrationRemoteURL(c.Request.Context(), req.URL)
	if err != nil {
		writeError(c, http.StatusBadRequest, "远程实例 URL 无效: "+err.Error())
		return
	}
	exportURL := remoteURL + "/api/admin/accounts/export?filter=healthy&remote=true"

	fetchCtx, fetchCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer fetchCancel()

	httpReq, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, exportURL, nil)
	if err != nil {
		writeLoggedInternalError(c, "构建远程迁移请求失败", err)
		return
	}
	httpReq.Header.Set("X-Admin-Key", req.AdminKey)

	client := &http.Client{
		Timeout: 60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return fmt.Errorf("禁止重定向")
		},
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		log.Printf("[admin] 连接远程实例失败: %v", err)
		writeError(c, http.StatusBadGateway, "连接远程实例失败")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxMigrateErrorBodyBytes))
		log.Printf("[admin] 远程实例返回错误 (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
		writeError(c, http.StatusBadGateway, fmt.Sprintf("远程实例返回错误 (%d)", resp.StatusCode))
		return
	}

	var remoteAccounts []cpaExportEntry
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxMigratePayloadBytes)).Decode(&remoteAccounts); err != nil {
		log.Printf("[admin] 解析远程迁移数据失败: %v", err)
		writeError(c, http.StatusBadGateway, "解析远程数据失败")
		return
	}

	if len(remoteAccounts) == 0 {
		c.JSON(http.StatusOK, gin.H{"message": "远程实例没有可迁移的健康账号", "total": 0, "imported": 0, "duplicate": 0, "failed": 0})
		return
	}

	// 转换为 importToken 格式，复用 importAccountsCommon
	var tokens []importToken
	for _, entry := range remoteAccounts {
		rt := strings.TrimSpace(entry.RefreshToken)
		if rt == "" {
			continue
		}
		name := entry.Email
		if name == "" {
			name = "migrate"
		}
		tokens = append(tokens, importToken{refreshToken: rt, name: name})
	}

	log.Printf("远程迁移: 从 %s 拉取到 %d 个账号，开始导入", remoteURL, len(tokens))
	h.importAccountsCommon(c, tokens, database.ProxyConfigInput{})
}

// ==================== Models ====================

// ListModels 返回支持的模型列表（供前端设置页使用）
func (h *Handler) ListModels(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"models": proxy.SupportedModels})
}

// ==================== 账号趋势 ====================

// GetAccountEventTrend 获取账号增删趋势聚合数据
func (h *Handler) GetAccountEventTrend(c *gin.Context) {
	startStr := c.Query("start")
	endStr := c.Query("end")
	if startStr == "" || endStr == "" {
		writeError(c, http.StatusBadRequest, "start 和 end 参数为必填")
		return
	}

	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		writeError(c, http.StatusBadRequest, "start 时间格式无效（需 RFC3339）")
		return
	}
	end, err := time.Parse(time.RFC3339, endStr)
	if err != nil {
		writeError(c, http.StatusBadRequest, "end 时间格式无效（需 RFC3339）")
		return
	}

	bucketMinutes := 60
	if bStr := c.Query("bucket_minutes"); bStr != "" {
		if b, err := strconv.Atoi(bStr); err == nil && b > 0 {
			bucketMinutes = b
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	trend, err := h.db.GetAccountEventTrend(ctx, start, end, bucketMinutes)
	if err != nil {
		writeLoggedInternalError(c, "获取账号趋势失败", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"trend": trend})
}

// ==================== 清理 ====================

// CleanBanned 清理封禁（unauthorized）账号
func (h *Handler) CleanBanned(c *gin.Context) {
	h.cleanByStatus(c, "unauthorized")
}

// CleanRateLimited 清理限流（rate_limited）账号
func (h *Handler) CleanRateLimited(c *gin.Context) {
	h.cleanByStatus(c, "rate_limited")
}

// CleanError 清理错误（error）账号
func (h *Handler) CleanError(c *gin.Context) {
	h.cleanByStatus(c, "error")
}

// cleanByStatus 按运行时状态清理账号
func (h *Handler) cleanByStatus(c *gin.Context, targetStatus string) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	cleaned := h.store.CleanByRuntimeStatus(ctx, targetStatus)

	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("已清理 %d 个账号", cleaned), "cleaned": cleaned})
}

// ==================== Proxies ====================

// ListProxies 获取代理列表
func (h *Handler) ListProxies(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	proxies, err := h.db.ListProxies(ctx)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "获取代理列表失败")
		return
	}
	if proxies == nil {
		proxies = []*database.ProxyRow{}
	}
	items := make([]gin.H, 0, len(proxies))
	for _, p := range proxies {
		items = append(items, gin.H{
			"id":                      p.ID,
			"url":                     p.URL,
			"label":                   p.Label,
			"enabled":                 p.Enabled,
			"source_type":             p.SourceType,
			"provider_url":            p.ProviderURL,
			"scheme_default":          p.SchemeDefault,
			"last_resolved_proxy_url": p.LastResolvedProxyURL,
			"last_resolved_proxy":     p.LastResolvedProxyURL,
			"last_resolved_at": func() string {
				if p.LastResolvedAt.IsZero() {
					return ""
				}
				return p.LastResolvedAt.Format(time.RFC3339)
			}(),
			"last_error":      p.LastError,
			"created_at":      p.CreatedAt.Format(time.RFC3339),
			"test_ip":         p.TestIP,
			"test_location":   p.TestLocation,
			"test_latency_ms": p.TestLatencyMs,
		})
	}
	c.JSON(http.StatusOK, gin.H{"proxies": items})
}

// AddProxies 添加代理（支持批量）
func (h *Handler) AddProxies(c *gin.Context) {
	var req struct {
		URLs          []string `json:"urls"`
		URL           string   `json:"url"`
		Label         string   `json:"label"`
		SourceType    string   `json:"source_type"`
		ProviderURL   string   `json:"provider_url"`
		SchemeDefault string   `json:"scheme_default"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求格式错误")
		return
	}

	// 合并单条和批量
	urls := req.URLs
	if req.URL != "" {
		urls = append(urls, req.URL)
	}
	if len(urls) == 0 {
		writeError(c, http.StatusBadRequest, "请提供至少一个代理 URL")
		return
	}

	// 过滤空行
	cleaned := make([]string, 0, len(urls))
	for _, u := range urls {
		u = strings.TrimSpace(u)
		if u != "" {
			cleaned = append(cleaned, u)
		}
	}
	if len(cleaned) == 0 {
		writeError(c, http.StatusBadRequest, "请提供至少一个代理 URL")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	inserted := 0
	var err error
	sourceType := strings.ToLower(strings.TrimSpace(req.SourceType))
	if sourceType == "" {
		sourceType = auth.ProxyModeStatic
	}
	if sourceType == auth.ProxyModeDynamic {
		cfg, err := buildProxyConfigInput(sourceType, req.URL, req.ProviderURL, req.SchemeDefault, false, false)
		if err != nil {
			writeError(c, http.StatusBadRequest, "代理配置无效: "+err.Error())
			return
		}
		if _, err := h.db.InsertProxyWithConfig(ctx, cfg, req.Label); err != nil {
			writeError(c, http.StatusInternalServerError, "添加代理失败")
			return
		}
		inserted = 1
	} else {
		inserted, err = h.db.InsertProxies(ctx, cleaned, req.Label)
		if err != nil {
			writeError(c, http.StatusInternalServerError, "添加代理失败")
			return
		}
	}

	// 刷新代理池
	_ = h.store.ReloadProxyPool()

	c.JSON(http.StatusOK, gin.H{
		"message":  fmt.Sprintf("成功添加 %d 个代理", inserted),
		"inserted": inserted,
		"total":    len(cleaned),
	})
}

// DeleteProxy 删除单个代理
func (h *Handler) DeleteProxy(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "无效的代理 ID")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	if err := h.db.DeleteProxy(ctx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(c, http.StatusNotFound, "代理不存在")
			return
		}
		writeError(c, http.StatusInternalServerError, "删除代理失败")
		return
	}

	_ = h.store.ReloadProxyPool()
	c.JSON(http.StatusOK, gin.H{"message": "代理已删除"})
}

// UpdateProxy 更新代理（启用/禁用/改标签）
func (h *Handler) UpdateProxy(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "无效的代理 ID")
		return
	}

	var req struct {
		Label         *string `json:"label"`
		Enabled       *bool   `json:"enabled"`
		SourceType    *string `json:"source_type"`
		ProviderURL   *string `json:"provider_url"`
		SchemeDefault *string `json:"scheme_default"`
		URL           *string `json:"url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求格式错误")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	if req.ProviderURL != nil {
		trimmed := strings.TrimSpace(*req.ProviderURL)
		sanitized, err := sanitizeProxyProviderURL(trimmed)
		if err != nil {
			writeError(c, http.StatusBadRequest, "动态代理 URL 无效")
			return
		}
		req.ProviderURL = &sanitized
	}
	if req.SchemeDefault != nil {
		normalized := auth.NormalizeProxyScheme(*req.SchemeDefault)
		req.SchemeDefault = &normalized
	}
	if req.URL != nil {
		trimmed := strings.TrimSpace(*req.URL)
		req.URL = &trimmed
		if trimmed != "" {
			if err := security.ValidateProxyURL(trimmed); err != nil {
				writeError(c, http.StatusBadRequest, "代理 URL 无效")
				return
			}
		}
	}

	if err := h.db.UpdateProxyWithConfig(ctx, id, req.Label, req.Enabled, req.SourceType, req.ProviderURL, req.SchemeDefault, req.URL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(c, http.StatusNotFound, "代理不存在")
			return
		}
		writeError(c, http.StatusInternalServerError, "更新代理失败")
		return
	}

	_ = h.store.ReloadProxyPool()
	c.JSON(http.StatusOK, gin.H{"message": "代理已更新"})
}

// BatchDeleteProxies 批量删除代理
func (h *Handler) BatchDeleteProxies(c *gin.Context) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		writeError(c, http.StatusBadRequest, "请提供要删除的代理 ID 列表")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	deleted, err := h.db.DeleteProxies(ctx, req.IDs)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "批量删除失败")
		return
	}

	_ = h.store.ReloadProxyPool()
	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("已删除 %d 个代理", deleted), "deleted": deleted})
}

// TestProxy 测试代理连通性与出口 IP 位置
func (h *Handler) TestProxy(c *gin.Context) {
	var req struct {
		URL  string `json:"url"`
		ID   int64  `json:"id"`
		Lang string `json:"lang"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.URL == "" {
		writeError(c, http.StatusBadRequest, "请提供代理 URL")
		return
	}

	// 创建使用指定代理的 HTTP client
	transport := &http.Transport{}
	baseDialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = baseDialer.DialContext
	if err := auth.ConfigureTransportProxy(transport, req.URL, baseDialer); err != nil {
		log.Printf("[admin] test proxy invalid url=%q: %v", req.URL, err)
		c.JSON(http.StatusOK, gin.H{"success": false, "error": "代理 URL 格式错误"})
		return
	}
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}

	apiLang := req.Lang
	if apiLang == "" {
		apiLang = "en"
	}
	start := time.Now()
	resp, err := client.Get(fmt.Sprintf("http://ip-api.com/json/?lang=%s&fields=status,message,country,regionName,city,isp,query", neturl.QueryEscape(apiLang)))
	latencyMs := int(time.Since(start).Milliseconds())

	if err != nil {
		log.Printf("[admin] test proxy connection failed url=%q: %v", req.URL, err)
		c.JSON(http.StatusOK, gin.H{"success": false, "error": "连接失败", "latency_ms": latencyMs})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	result := gjson.ParseBytes(body)

	if result.Get("status").String() != "success" {
		log.Printf("[admin] test proxy upstream failed url=%q message=%q", req.URL, truncate(result.Get("message").String(), 200))
		c.JSON(http.StatusOK, gin.H{"success": false, "error": "查询出口信息失败", "latency_ms": latencyMs})
		return
	}

	ip := result.Get("query").String()
	country := result.Get("country").String()
	region := result.Get("regionName").String()
	city := result.Get("city").String()
	isp := result.Get("isp").String()
	location := country + "·" + region + "·" + city

	// 持久化测试结果
	if req.ID > 0 {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		_ = h.db.UpdateProxyTestResult(ctx, req.ID, ip, location, latencyMs)
	}

	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"ip":         ip,
		"country":    country,
		"region":     region,
		"city":       city,
		"isp":        isp,
		"latency_ms": latencyMs,
		"location":   location,
	})
}
