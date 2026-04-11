package admin

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

type errorResponse struct {
	Error string `json:"error"`
}

type messageResponse struct {
	Message string `json:"message"`
}

type statsResponse struct {
	Total            int                   `json:"total"`
	Available        int                   `json:"available"`
	Error            int                   `json:"error"`
	RefreshScheduler refreshStatusResponse `json:"refresh_scheduler"`
	RefreshConfig    refreshConfigResponse `json:"refresh_config"`
}

type accountsResponse struct {
	Accounts         []accountResponse     `json:"accounts"`
	RefreshScheduler refreshStatusResponse `json:"refresh_scheduler"`
}

type createAccountResponse struct {
	ID      int64  `json:"id"`
	Message string `json:"message"`
}

type healthResponse struct {
	Status           string                `json:"status"`
	Available        int                   `json:"available"`
	Total            int                   `json:"total"`
	RefreshScheduler refreshStatusResponse `json:"refresh_scheduler"`
}

type refreshStatusResponse struct {
	Running        bool   `json:"running"`
	TotalAccounts  int    `json:"total_accounts"`
	TargetAccounts int    `json:"target_accounts"`
	Processed      int    `json:"processed"`
	Success        int    `json:"success"`
	Failure        int    `json:"failure"`
	NextScanAt     string `json:"next_scan_at,omitempty"`
	StartedAt      string `json:"started_at,omitempty"`
	FinishedAt     string `json:"finished_at,omitempty"`
}

type refreshConfigResponse struct {
	ScanEnabled         bool `json:"scan_enabled"`
	ScanIntervalSeconds int  `json:"scan_interval_seconds"`
	PreExpireSeconds    int  `json:"pre_expire_seconds"`
}

type opsOverviewResponse struct {
	UpdatedAt      string              `json:"updated_at"`
	UptimeSeconds  int64               `json:"uptime_seconds"`
	DatabaseDriver string              `json:"database_driver"`
	DatabaseLabel  string              `json:"database_label"`
	CacheDriver    string              `json:"cache_driver"`
	CacheLabel     string              `json:"cache_label"`
	CPU            opsCPUResponse      `json:"cpu"`
	Memory         opsMemoryResponse   `json:"memory"`
	Runtime        opsRuntimeResponse  `json:"runtime"`
	Postgres       opsDatabaseResponse `json:"postgres"`
	Redis          opsRedisResponse    `json:"redis"`
}

type opsCPUResponse struct {
	Percent float64 `json:"percent"`
	Cores   int     `json:"cores"`
}

type opsMemoryResponse struct {
	Percent      float64 `json:"percent"`
	UsedBytes    uint64  `json:"used_bytes"`
	TotalBytes   uint64  `json:"total_bytes"`
	ProcessBytes uint64  `json:"process_bytes"`
}

type opsRuntimeResponse struct {
	Goroutines        int `json:"goroutines"`
	AvailableAccounts int `json:"available_accounts"`
	TotalAccounts     int `json:"total_accounts"`
}

type opsDatabaseResponse struct {
	Healthy      bool    `json:"healthy"`
	Open         int     `json:"open"`
	InUse        int     `json:"in_use"`
	Idle         int     `json:"idle"`
	MaxOpen      int     `json:"max_open"`
	WaitCount    int64   `json:"wait_count"`
	UsagePercent float64 `json:"usage_percent"`
}

type opsRedisResponse struct {
	Healthy      bool    `json:"healthy"`
	TotalConns   uint32  `json:"total_conns"`
	IdleConns    uint32  `json:"idle_conns"`
	StaleConns   uint32  `json:"stale_conns"`
	PoolSize     int     `json:"pool_size"`
	UsagePercent float64 `json:"usage_percent"`
}

func writeError(c *gin.Context, statusCode int, message string) {
	c.JSON(statusCode, errorResponse{Error: message})
}

func writeMessage(c *gin.Context, statusCode int, message string) {
	c.JSON(statusCode, messageResponse{Message: message})
}

func writeInternalError(c *gin.Context, err error) {
	writeError(c, http.StatusInternalServerError, err.Error())
}

func writeLoggedInternalError(c *gin.Context, publicMessage string, err error) {
	if err != nil {
		log.Printf("[admin] %s: %v", publicMessage, err)
	}
	writeError(c, http.StatusInternalServerError, publicMessage)
}
