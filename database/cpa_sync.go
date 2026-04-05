package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

type CPASyncAction struct {
	Timestamp string `json:"timestamp"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	Target    string `json:"target,omitempty"`
}

type ConnectionTestStatus struct {
	Ok         *bool          `json:"ok"`
	Message    string         `json:"message"`
	HTTPStatus *int           `json:"http_status,omitempty"`
	TestedAt   string         `json:"tested_at"`
	Details    map[string]any `json:"details"`
}

type CPASyncState struct {
	HourBucketStart        string               `json:"hour_bucket_start"`
	HourlyUploadCount      int                  `json:"hourly_upload_count"`
	ConsecutiveUploadCount int                  `json:"consecutive_upload_count"`
	LastRunAt              string               `json:"last_run_at"`
	LastRunStatus          string               `json:"last_run_status"`
	LastRunSummary         string               `json:"last_run_summary"`
	LastErrorSummary       string               `json:"last_error_summary"`
	CurrentMihomoNode      string               `json:"current_mihomo_node"`
	LastCPAAccountCount    int                  `json:"last_cpa_account_count"`
	RecentActions          []CPASyncAction      `json:"recent_actions"`
	CPATestStatus          ConnectionTestStatus `json:"cpa_test_status"`
	MihomoTestStatus       ConnectionTestStatus `json:"mihomo_test_status"`
}

func (db *DB) GetCPASyncState(ctx context.Context) (*CPASyncState, error) {
	state := &CPASyncState{
		RecentActions:    []CPASyncAction{},
		CPATestStatus:    ConnectionTestStatus{Details: map[string]any{}},
		MihomoTestStatus: ConnectionTestStatus{Details: map[string]any{}},
	}

	var (
		hourBucketRaw        interface{}
		lastRunRaw           interface{}
		recentActions        string
		cpaTestOK            sql.NullBool
		cpaTestHTTPStatus    sql.NullInt64
		cpaTestedAtRaw       interface{}
		cpaTestDetails       sql.NullString
		mihomoTestOK         sql.NullBool
		mihomoTestHTTPStatus sql.NullInt64
		mihomoTestedAtRaw    interface{}
		mihomoTestDetails    sql.NullString
	)
	err := db.conn.QueryRowContext(ctx, `
		SELECT hour_bucket_start, COALESCE(hourly_upload_count, 0), COALESCE(consecutive_upload_count, 0),
		       last_run_at, COALESCE(last_run_status, ''), COALESCE(last_run_summary, ''),
		       COALESCE(last_error_summary, ''), COALESCE(current_mihomo_node, ''),
		       COALESCE(last_cpa_account_count, 0), COALESCE(recent_actions, '[]'),
		       cpa_test_ok, COALESCE(cpa_test_message, ''), cpa_test_http_status, cpa_tested_at, COALESCE(cpa_test_details, '{}'),
		       mihomo_test_ok, COALESCE(mihomo_test_message, ''), mihomo_test_http_status, mihomo_tested_at, COALESCE(mihomo_test_details, '{}')
		FROM cpa_sync_state
		WHERE id = 1
	`).Scan(
		&hourBucketRaw,
		&state.HourlyUploadCount,
		&state.ConsecutiveUploadCount,
		&lastRunRaw,
		&state.LastRunStatus,
		&state.LastRunSummary,
		&state.LastErrorSummary,
		&state.CurrentMihomoNode,
		&state.LastCPAAccountCount,
		&recentActions,
		&cpaTestOK,
		&state.CPATestStatus.Message,
		&cpaTestHTTPStatus,
		&cpaTestedAtRaw,
		&cpaTestDetails,
		&mihomoTestOK,
		&state.MihomoTestStatus.Message,
		&mihomoTestHTTPStatus,
		&mihomoTestedAtRaw,
		&mihomoTestDetails,
	)
	if err == sql.ErrNoRows {
		return state, nil
	}
	if err != nil {
		return nil, err
	}

	if parsed, err := parseNullableDBTime(hourBucketRaw); err == nil {
		state.HourBucketStart = parsed
	}
	if parsed, err := parseNullableDBTime(lastRunRaw); err == nil {
		state.LastRunAt = parsed
	}
	if recentActions != "" {
		if err := json.Unmarshal([]byte(recentActions), &state.RecentActions); err != nil {
			state.RecentActions = []CPASyncAction{}
		}
	}
	state.CPATestStatus.Ok = nullableBool(cpaTestOK)
	state.CPATestStatus.HTTPStatus = nullableInt(cpaTestHTTPStatus)
	if parsed, err := parseNullableDBTime(cpaTestedAtRaw); err == nil {
		state.CPATestStatus.TestedAt = parsed
	}
	if cpaTestDetails.Valid && strings.TrimSpace(cpaTestDetails.String) != "" {
		if err := json.Unmarshal([]byte(cpaTestDetails.String), &state.CPATestStatus.Details); err != nil {
			state.CPATestStatus.Details = map[string]any{}
		}
	}
	if state.CPATestStatus.Details == nil {
		state.CPATestStatus.Details = map[string]any{}
	}
	state.MihomoTestStatus.Ok = nullableBool(mihomoTestOK)
	state.MihomoTestStatus.HTTPStatus = nullableInt(mihomoTestHTTPStatus)
	if parsed, err := parseNullableDBTime(mihomoTestedAtRaw); err == nil {
		state.MihomoTestStatus.TestedAt = parsed
	}
	if mihomoTestDetails.Valid && strings.TrimSpace(mihomoTestDetails.String) != "" {
		if err := json.Unmarshal([]byte(mihomoTestDetails.String), &state.MihomoTestStatus.Details); err != nil {
			state.MihomoTestStatus.Details = map[string]any{}
		}
	}
	if state.MihomoTestStatus.Details == nil {
		state.MihomoTestStatus.Details = map[string]any{}
	}
	return state, nil
}

func (db *DB) UpdateCPASyncState(ctx context.Context, state *CPASyncState) error {
	if state == nil {
		return nil
	}
	actionsJSON, err := json.Marshal(state.RecentActions)
	if err != nil {
		return err
	}
	cpaTestDetailsJSON, err := json.Marshal(normalizeConnectionTestDetails(state.CPATestStatus.Details))
	if err != nil {
		return err
	}
	mihomoTestDetailsJSON, err := json.Marshal(normalizeConnectionTestDetails(state.MihomoTestStatus.Details))
	if err != nil {
		return err
	}

	_, err = db.conn.ExecContext(ctx, `
		INSERT INTO cpa_sync_state (
			id, hour_bucket_start, hourly_upload_count, consecutive_upload_count,
			last_run_at, last_run_status, last_run_summary, last_error_summary,
			current_mihomo_node, last_cpa_account_count, recent_actions,
			cpa_test_ok, cpa_test_message, cpa_test_http_status, cpa_tested_at, cpa_test_details,
			mihomo_test_ok, mihomo_test_message, mihomo_test_http_status, mihomo_tested_at, mihomo_test_details
		)
		VALUES (1, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		ON CONFLICT (id) DO UPDATE SET
			hour_bucket_start = EXCLUDED.hour_bucket_start,
			hourly_upload_count = EXCLUDED.hourly_upload_count,
			consecutive_upload_count = EXCLUDED.consecutive_upload_count,
			last_run_at = EXCLUDED.last_run_at,
			last_run_status = EXCLUDED.last_run_status,
			last_run_summary = EXCLUDED.last_run_summary,
			last_error_summary = EXCLUDED.last_error_summary,
			current_mihomo_node = EXCLUDED.current_mihomo_node,
			last_cpa_account_count = EXCLUDED.last_cpa_account_count,
			recent_actions = EXCLUDED.recent_actions,
			cpa_test_ok = EXCLUDED.cpa_test_ok,
			cpa_test_message = EXCLUDED.cpa_test_message,
			cpa_test_http_status = EXCLUDED.cpa_test_http_status,
			cpa_tested_at = EXCLUDED.cpa_tested_at,
			cpa_test_details = EXCLUDED.cpa_test_details,
			mihomo_test_ok = EXCLUDED.mihomo_test_ok,
			mihomo_test_message = EXCLUDED.mihomo_test_message,
			mihomo_test_http_status = EXCLUDED.mihomo_test_http_status,
			mihomo_tested_at = EXCLUDED.mihomo_tested_at,
			mihomo_test_details = EXCLUDED.mihomo_test_details
	`, nullableRFC3339(state.HourBucketStart), state.HourlyUploadCount, state.ConsecutiveUploadCount,
		nullableRFC3339(state.LastRunAt), state.LastRunStatus, state.LastRunSummary, state.LastErrorSummary,
		state.CurrentMihomoNode, state.LastCPAAccountCount, string(actionsJSON),
		nullableBoolValue(state.CPATestStatus.Ok), state.CPATestStatus.Message, nullableIntValue(state.CPATestStatus.HTTPStatus), nullableRFC3339(state.CPATestStatus.TestedAt), string(cpaTestDetailsJSON),
		nullableBoolValue(state.MihomoTestStatus.Ok), state.MihomoTestStatus.Message, nullableIntValue(state.MihomoTestStatus.HTTPStatus), nullableRFC3339(state.MihomoTestStatus.TestedAt), string(mihomoTestDetailsJSON))
	return err
}

func parseNullableDBTime(raw interface{}) (string, error) {
	if raw == nil {
		return "", nil
	}
	t, err := parseDBTimeValue(raw)
	if err != nil || t.IsZero() {
		return "", err
	}
	return t.UTC().Format(time.RFC3339), nil
}

func nullableRFC3339(value string) interface{} {
	if value == "" {
		return nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed
	}
	return value
}

func nullableBool(value sql.NullBool) *bool {
	if !value.Valid {
		return nil
	}
	result := value.Bool
	return &result
}

func nullableInt(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int64)
	return &result
}

func nullableBoolValue(value *bool) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func nullableIntValue(value *int) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func normalizeConnectionTestDetails(details map[string]any) map[string]any {
	if details == nil {
		return map[string]any{}
	}
	return details
}
