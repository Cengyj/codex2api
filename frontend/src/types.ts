export type ToastType = 'success' | 'error'

export type ISODateString = string



export type ProxyMode = 'static' | 'dynamic' | 'auto' | 'none'

export type ProxyProtocol = 'http' | 'https' | 'socks5' | 'socks4' | 'auto'



export interface ToastState {

  msg: string

  type: ToastType

}



export type AccountStatus = 'active' | 'ready' | 'cooldown' | 'error' | 'paused' | string



export interface StatsResponse {

  total: number

  available: number

  error: number

  refresh_scheduler?: RefreshSchedulerStatus

  refresh_config?: RefreshSchedulerConfig

}



export interface AccountRow {

  id: number

  name: string

  email: string

  plan_type: string

  status: AccountStatus

  at_only?: boolean

  health_tier?: string

  scheduler_score?: number

  dynamic_concurrency_limit?: number

  scheduler_breakdown?: {

    unauthorized_penalty: number

    rate_limit_penalty: number

    timeout_penalty: number

    server_penalty: number

    failure_penalty: number

    success_bonus: number

    latency_penalty: number

  }

  last_unauthorized_at?: ISODateString

  last_rate_limited_at?: ISODateString

  last_timeout_at?: ISODateString

  last_server_error_at?: ISODateString

  proxy_url: string

  proxy_mode?: ProxyMode

  proxy_provider_url?: string

  proxy_protocol?: ProxyProtocol

  proxy_assigned_url?: string

  proxy_assigned_at?: ISODateString

  proxy_last_switched_at?: ISODateString

  created_at: ISODateString

  updated_at: ISODateString

  cooldown_until?: ISODateString

  locked?: boolean

}



export interface RefreshSchedulerStatus {

  running: boolean

  total_accounts: number

  target_accounts: number

  processed: number

  success: number

  failure: number

  next_scan_at?: ISODateString

  started_at?: ISODateString

  finished_at?: ISODateString

}



export interface RefreshSchedulerConfig {

  scan_enabled: boolean

  scan_interval_seconds: number

  pre_expire_seconds: number

}



export interface AccountsResponse {

  accounts: AccountRow[]

  refresh_scheduler?: RefreshSchedulerStatus

}



export interface AddAccountRequest {

  name?: string

  refresh_token: string

  proxy_url: string

  proxy_mode?: ProxyMode

  proxy_provider_url?: string

  proxy_protocol?: ProxyProtocol

}



export interface AddATAccountRequest {

  name?: string

  access_token: string

  proxy_url: string

  proxy_mode?: ProxyMode

  proxy_provider_url?: string

  proxy_protocol?: ProxyProtocol

}



export interface AccountModelStat {

  model: string

  requests: number

  tokens: number

}






export interface MessageResponse {

  message: string

}



export interface CreateAccountResponse extends MessageResponse {

  id: number

}



export interface AdminErrorResponse {

  error: string

}



export interface HealthResponse {

  status: 'ok' | string

  available: number

  total: number

  refresh_scheduler?: RefreshSchedulerStatus

}



export interface AccountEventTrendPoint {

  bucket: string

  added: number

  deleted: number

}



export interface OpsOverviewResponse {

  updated_at: ISODateString

  uptime_seconds: number

  database_driver: string

  database_label: string

  cache_driver: string

  cache_label: string

  cpu: {

    percent: number

    cores: number

  }

  memory: {

    percent: number

    used_bytes: number

    total_bytes: number

    process_bytes: number

  }

  runtime: {

    goroutines: number

    available_accounts: number

    total_accounts: number

  }

  postgres: {

    healthy: boolean

    open: number

    in_use: number

    idle: number

    max_open: number

    wait_count: number

    usage_percent: number

  }

  redis: {

    healthy: boolean

    total_conns: number

    idle_conns: number

    stale_conns: number

    pool_size: number

    usage_percent: number

  }

}



export interface SystemSettings {

  max_concurrency: number

  global_rpm: number

  test_model: string

  test_concurrency: number

  refresh_scan_enabled?: boolean

  refresh_scan_interval_seconds?: number

  refresh_pre_expire_seconds?: number

  refresh_max_concurrency?: number

  refresh_on_import_enabled?: boolean

  refresh_on_import_concurrency?: number

  usage_probe_enabled?: boolean

  usage_probe_stale_after_seconds?: number

  usage_probe_max_concurrency?: number

  recovery_probe_enabled?: boolean

  recovery_probe_min_interval_seconds?: number

  recovery_probe_max_concurrency?: number

  proxy_url?: string

  pg_max_conns: number

  redis_pool_size: number

  auto_clean_unauthorized: boolean

  auto_clean_rate_limited: boolean

  admin_secret: string

  admin_auth_source: 'env' | 'database' | 'disabled' | string

  auto_clean_full_usage: boolean

  auto_clean_error: boolean

  auto_clean_expired: boolean

  proxy_pool_enabled: boolean

  max_retries: number

  allow_remote_migration: boolean

  database_driver: string

  database_label: string

  cache_driver: string

  cache_label: string

  expired_cleaned?: number

  cpa_sync_enabled: boolean

  cpa_base_url: string

  cpa_admin_key: string

  cpa_min_accounts: number

  cpa_max_accounts: number

  cpa_max_uploads_per_hour: number

  cpa_switch_after_uploads: number

  cpa_sync_interval_seconds: number

  mihomo_base_url: string

  mihomo_secret: string

  mihomo_strategy_group: string

  mihomo_delay_test_url: string

  mihomo_delay_timeout_ms: number

  proxy_default_mode?: ProxyMode

  proxy_dynamic_provider_url?: string

  proxy_default_protocol?: ProxyProtocol

  proxy_rotation_hours?: number

}



export interface CPASyncAction {

  timestamp: ISODateString

  kind: string

  status: string

  message: string

  target?: string

}



export interface CPASyncState {

  hour_bucket_start: ISODateString

  hourly_upload_count: number

  consecutive_upload_count: number

  last_switch_at: ISODateString

  last_run_at: ISODateString

  last_run_status: string

  last_run_summary: string

  last_error_summary: string

  current_mihomo_node: string

  last_cpa_account_count: number

  recent_actions: CPASyncAction[]

}



export interface ConnectionTestStatus {

  ok: boolean | null

  message: string

  http_status?: number

  tested_at: ISODateString

  details: Record<string, unknown>

}



export interface MihomoStrategyGroupOption {

  name: string

  type: string

  current: string

  candidate_count: number

}



export interface CPASyncStatusResponse {

  config: {

    enabled: boolean

    interval_seconds: number

    cpa_base_url: string

    cpa_min_accounts: number

    cpa_max_accounts: number

    cpa_max_uploads_per_hour: number

    cpa_switch_after_uploads: number

    mihomo_base_url: string

    mihomo_strategy_group: string

    mihomo_delay_test_url: string

    mihomo_delay_timeout_ms: number

    missing_config: string[]

  }

  state: CPASyncState

  cpa_test_status: ConnectionTestStatus

  mihomo_test_status: ConnectionTestStatus

  running: boolean

  next_run_at?: ISODateString

}



export interface CPAExportEntry {

  type: string

  email: string

  expired: string

  id_token: string

  account_id: string

  access_token: string

  last_refresh: string

  refresh_token: string

}
























export type ApiListResponse<K extends string, T> = {

  [P in K]: T[]

}



export interface OAuthURLResponse {

  auth_url: string

  session_id: string

}



export interface OAuthExchangeResponse {

  message: string

  id: number

  email: string

  plan_type: string

}

