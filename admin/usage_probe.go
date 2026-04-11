package admin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/proxy"
)

// ProbeUsageSnapshot 涓诲姩鍙戦€佹渶灏忔帰閽堣姹傚埛鏂拌处鍙风敤閲?
func (h *Handler) ProbeUsageSnapshot(ctx context.Context, account *auth.Account) error {
	if account == nil {
		return nil
	}

	account.Mu().RLock()
	hasToken := account.AccessToken != ""
	account.Mu().RUnlock()
	if !hasToken {
		return nil
	}

	payload := buildTestPayload(h.store.GetTestModel())
	proxyOverride := h.store.ResolveMaintenanceProxy(ctx, account)
	resp, err := executeRequest(ctx, account, payload, "", proxyOverride, "", nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	usagePct, hasUsage := proxy.ParseCodexUsageHeaders(resp, account)
	if hasUsage {
		h.store.PersistUsageSnapshot(account, usagePct)
	}

	_, _ = io.Copy(io.Discard, resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		h.store.ReportRequestSuccess(account, 0)
		// 鍙湁鐢ㄩ噺鏈€楀敖鏃舵墠閲嶇疆鐘舵€?
		if !hasUsage || usagePct < 100 {
			h.store.ClearCooldown(account)
		}
		return nil
	case http.StatusUnauthorized:
		h.store.ReportRequestFailure(account, "client", 0)
		h.store.MarkCooldown(account, 24*time.Hour, "unauthorized")
		return nil
	case http.StatusTooManyRequests:
		h.store.ReportRequestFailure(account, "client", 0)
		h.store.MarkCooldown(account, 5*time.Minute, "rate_limited")
		return nil
	default:
		if resp.StatusCode >= 500 {
			h.store.ReportRequestFailure(account, "server", 0)
		} else if resp.StatusCode >= 400 {
			h.store.ReportRequestFailure(account, "client", 0)
		}
		return fmt.Errorf("鎺㈤拡杩斿洖鐘舵€?%d", resp.StatusCode)
	}
}
