package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/proxy"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var executeRequest = proxy.ExecuteRequest

const (
	maxTestErrorBodyBytes = 256 * 1024
	maxBatchDrainBytes    = 512 * 1024
	batchTestTimeout      = 45 * time.Second
)

// testEvent SSE 测试事件
type testEvent struct {
	Type    string `json:"type"`              // test_start | content | test_complete | error
	Text    string `json:"text,omitempty"`    // 内容文本
	Model   string `json:"model,omitempty"`   // 测试模型
	Success bool   `json:"success,omitempty"` // 是否成功
	Error   string `json:"error,omitempty"`   // 错误信息
}

func canStreamTestEvent(c *gin.Context) bool {
	return c != nil && c.Request != nil && c.Request.Context().Err() == nil
}

// TestConnection 测试账号连接（SSE 流式返回）
// GET /api/admin/accounts/:id/test
func (h *Handler) TestConnection(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的账号 ID"})
		return
	}

	// 查找运行时账号
	account := h.store.FindByID(id)
	if account == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "账号不在运行时池中"})
		return
	}

	// 检查 access_token 是否可用
	account.Mu().RLock()
	hasToken := account.AccessToken != ""
	account.Mu().RUnlock()

	if !hasToken {
		c.JSON(http.StatusBadRequest, gin.H{"error": "账号没有可用的 Access Token，请先刷新"})
		return
	}

	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	if canStreamTestEvent(c) {
		c.Writer.Flush()
	}

	testModel := h.store.GetTestModel()

	// 发送 test_start
	if !sendTestEvent(c, testEvent{Type: "test_start", Model: testModel}) {
		return
	}

	// 构建最小测试请求体（参考 sub2api createOpenAITestPayload）
	payload := buildTestPayload(testModel)

	// 发送请求
	start := time.Now()
	proxyOverride := h.store.ResolveMaintenanceProxy(c.Request.Context(), account)
	resp, reqErr := executeRequest(c.Request.Context(), account, payload, "", proxyOverride, "", nil, nil)
	if reqErr != nil {
		log.Printf("[admin] test connection request failed account=%d: %v", id, reqErr)
		_ = sendTestEvent(c, testEvent{Type: "error", Error: "请求失败"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if usagePct, ok := proxy.ParseCodexUsageHeaders(resp, account); ok {
			h.store.PersistUsageSnapshot(account, usagePct)
		}
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			h.store.MarkCooldown(account, 24*time.Hour, "unauthorized")
		case http.StatusTooManyRequests:
			h.store.MarkCooldown(account, 5*time.Minute, "rate_limited")
		}
		errBody, _ := readLimitedBytes(resp.Body, maxTestErrorBodyBytes)
		log.Printf("[admin] test connection upstream error account=%d status=%d body=%s", id, resp.StatusCode, truncate(string(errBody), 500))
		_ = sendTestEvent(c, testEvent{Type: "error", Error: fmt.Sprintf("上游返回 %d", resp.StatusCode)})
		return
	}

	usagePct, hasUsage := proxy.ParseCodexUsageHeaders(resp, account)
	if hasUsage {
		h.store.PersistUsageSnapshot(account, usagePct)
	}

	// 解析 SSE 流
	hasContent := false
	_ = proxy.ReadSSEStream(resp.Body, func(data []byte) bool {
		if !canStreamTestEvent(c) {
			return false
		}

		eventType := gjson.GetBytes(data, "type").String()

		switch eventType {
		case "response.output_text.delta":
			delta := gjson.GetBytes(data, "delta").String()
			if delta != "" {
				hasContent = true
				if !sendTestEvent(c, testEvent{Type: "content", Text: delta}) {
					return false
				}
			}
		case "response.completed":
			// 只有用量未耗尽时才重置状态
			if !hasUsage || usagePct < 100 {
				h.store.ClearCooldown(account)
			}
			duration := time.Since(start).Milliseconds()
			if !sendTestEvent(c, testEvent{
				Type: "content",
				Text: fmt.Sprintf("\n\n--- 耗时 %dms ---", duration),
			}) {
				return false
			}
			_ = sendTestEvent(c, testEvent{Type: "test_complete", Success: true})
			return false
		case "response.failed":
			errMsg := gjson.GetBytes(data, "response.status_details.error.message").String()
			if errMsg == "" {
				errMsg = string(data)
			}
			log.Printf("[admin] test connection response.failed account=%d detail=%s", id, truncate(errMsg, 500))
			_ = sendTestEvent(c, testEvent{Type: "error", Error: "上游返回 response.failed"})
			return false
		}
		return true
	})

	if !hasContent {
		_ = sendTestEvent(c, testEvent{Type: "error", Error: "未收到模型输出"})
	}
}

// buildTestPayload 构建最小测试请求体
func buildTestPayload(model string) []byte {
	payload := []byte(`{}`)
	payload, _ = sjson.SetBytes(payload, "model", model)
	payload, _ = sjson.SetBytes(payload, "input", []map[string]any{
		{
			"role": "user",
			"content": []map[string]any{
				{
					"type": "input_text",
					"text": "Say hello in one sentence.",
				},
			},
		},
	})
	payload, _ = sjson.SetBytes(payload, "stream", true)
	payload, _ = sjson.SetBytes(payload, "store", false)
	payload, _ = sjson.SetBytes(payload, "instructions", "You are a helpful assistant. Reply briefly.")
	return payload
}

// sendTestEvent 发送 SSE 事件
func sendTestEvent(c *gin.Context, event testEvent) bool {
	if !canStreamTestEvent(c) {
		return false
	}

	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("序列化测试事件失败: %v", err)
		return false
	}
	if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", data); err != nil {
		log.Printf("写入 SSE 事件失败: %v", err)
		return false
	}
	if canStreamTestEvent(c) {
		c.Writer.Flush()
	}
	return true
}

func readLimitedBytes(r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = maxTestErrorBodyBytes
	}
	limited := io.LimitReader(r, maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return data[:maxBytes], nil
	}
	return data, nil
}

// truncate 截断字符串
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// BatchTest 批量测试所有账号连接
// POST /api/admin/accounts/batch-test
func (h *Handler) BatchTest(c *gin.Context) {
	accounts := h.store.Accounts()
	if len(accounts) == 0 {
		c.JSON(http.StatusOK, gin.H{"total": 0, "success": 0, "failed": 0, "banned": 0, "rate_limited": 0})
		return
	}

	testModel := h.store.GetTestModel()
	payload := buildTestPayload(testModel)
	concurrency := h.store.GetTestConcurrency()
	if concurrency <= 0 {
		concurrency = 1
	}

	var (
		successCount   int64
		failedCount    int64
		bannedCount    int64
		rateLimitCount int64
		wg             sync.WaitGroup
	)

	jobs := make(chan *auth.Account)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for acc := range jobs {
				testCtx, cancel := context.WithTimeout(context.Background(), batchTestTimeout)
				proxyOverride := h.store.ResolveMaintenanceProxy(testCtx, acc)
				resp, err := executeRequest(testCtx, acc, payload, "", proxyOverride, "", nil, nil)
				cancel()
				if err != nil {
					atomic.AddInt64(&failedCount, 1)
					continue
				}
				func() {
					defer resp.Body.Close()
					_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBatchDrainBytes))

					switch resp.StatusCode {
					case http.StatusOK:
						usagePct, hasUsage := proxy.ParseCodexUsageHeaders(resp, acc)
						if hasUsage {
							h.store.PersistUsageSnapshot(acc, usagePct)
						}
						if !hasUsage || usagePct < 100 {
							h.store.ClearCooldown(acc)
						}
						atomic.AddInt64(&successCount, 1)
					case http.StatusUnauthorized:
						if usagePct, ok := proxy.ParseCodexUsageHeaders(resp, acc); ok {
							h.store.PersistUsageSnapshot(acc, usagePct)
						}
						h.store.MarkCooldown(acc, 24*time.Hour, "unauthorized")
						atomic.AddInt64(&bannedCount, 1)
					case http.StatusTooManyRequests:
						if usagePct, ok := proxy.ParseCodexUsageHeaders(resp, acc); ok {
							h.store.PersistUsageSnapshot(acc, usagePct)
						}
						h.store.MarkCooldown(acc, 5*time.Minute, "rate_limited")
						atomic.AddInt64(&rateLimitCount, 1)
					default:
						atomic.AddInt64(&failedCount, 1)
					}
				}()
			}
		}()
	}

	for _, account := range accounts {
		account.Mu().RLock()
		hasToken := account.AccessToken != ""
		account.Mu().RUnlock()
		if !hasToken {
			atomic.AddInt64(&failedCount, 1)
			continue
		}
		jobs <- account
	}

	close(jobs)
	wg.Wait()

	c.JSON(http.StatusOK, gin.H{
		"total":        len(accounts),
		"success":      successCount,
		"failed":       failedCount,
		"banned":       bannedCount,
		"rate_limited": rateLimitCount,
	})
}
