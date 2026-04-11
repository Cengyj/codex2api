import { renderHook, act, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { CPASyncStatusResponse, SystemSettings } from '../types'
import { getMihomoServiceSettingsSignature } from '../components/cpa-sync/CPASyncUtils'

const mockApi = {
  getSettings: vi.fn(),
  getCPASyncStatus: vi.fn(),
  updateSettings: vi.fn(),
  runCPASync: vi.fn(),
  switchCPASync: vi.fn(),
  testCPASyncCPA: vi.fn(),
  testCPASyncMihomo: vi.fn(),
  listCPASyncMihomoGroups: vi.fn(),
}

let loaderSnapshot: { settings: SystemSettings | null; status: CPASyncStatusResponse | null }
const mockReload = vi.fn()

vi.mock('../api', () => ({ api: mockApi }))
vi.mock('../hooks/useDataLoader', async () => {
  const React = await import('react')
  return {
    useDataLoader: () => {
      const [data, setData] = React.useState(loaderSnapshot)
      return {
        data,
        setData,
        loading: false,
        error: '',
        reload: mockReload,
      }
    },
  }
})

function createSettings(overrides: Partial<SystemSettings> = {}): SystemSettings {
  return {
    max_concurrency: 10,
    global_rpm: 1000,
    test_model: 'gpt-4.1-mini',
    test_concurrency: 1,
    pg_max_conns: 10,
    redis_pool_size: 10,
    auto_clean_unauthorized: false,
    auto_clean_rate_limited: false,
    admin_secret: 'secret',
    admin_auth_source: 'disabled',
    auto_clean_full_usage: false,
    auto_clean_error: false,
    auto_clean_expired: false,
    proxy_pool_enabled: false,
    max_retries: 1,
    allow_remote_migration: false,
    database_driver: 'sqlite',
    database_label: 'sqlite',
    cache_driver: 'memory',
    cache_label: 'memory',
    cpa_sync_enabled: true,
    cpa_base_url: 'https://cpa.example.com',
    cpa_admin_key: 'admin-key',
    cpa_min_accounts: 1,
    cpa_max_accounts: 10,
    cpa_max_uploads_per_hour: 3,
    cpa_switch_after_uploads: 15,
    cpa_sync_interval_seconds: 120,
    mihomo_base_url: '',
    mihomo_secret: '',
    mihomo_strategy_group: '',
    mihomo_delay_test_url: 'https://example.com',
    mihomo_delay_timeout_ms: 1000,
    ...overrides,
  }
}

function createStatus(overrides: Partial<CPASyncStatusResponse> = {}): CPASyncStatusResponse {
  return {
    config: {
      enabled: true,
      interval_seconds: 120,
      cpa_base_url: 'https://cpa.example.com',
      cpa_min_accounts: 1,
      cpa_max_accounts: 10,
      cpa_max_uploads_per_hour: 3,
      cpa_switch_after_uploads: 15,
      mihomo_base_url: '',
      mihomo_strategy_group: '',
      mihomo_delay_test_url: 'https://example.com',
      mihomo_delay_timeout_ms: 1000,
      missing_config: [],
    },
    state: {
      hour_bucket_start: new Date().toISOString(),
      hourly_upload_count: 0,
      consecutive_upload_count: 0,
      last_switch_at: '',
      last_run_at: '',
      last_run_status: 'success',
      last_run_summary: '完成',
      last_error_summary: '',
      current_mihomo_node: '',
      last_cpa_account_count: 1,
      recent_actions: [],
    },
    cpa_test_status: {
      ok: null,
      message: '',
      tested_at: '',
      details: {},
    },
    mihomo_test_status: {
      ok: null,
      message: '',
      tested_at: '',
      details: {},
    },
    running: false,
    next_run_at: new Date(Date.now() + 60_000).toISOString(),
    ...overrides,
  }
}

const t = (key: string, options?: Record<string, unknown>) => {
  switch (key) {
    case 'cpaSync.pendingRetestHint':
      return '待重新测试'
    case 'cpaSync.saveSuccess':
      return '保存成功'
    case 'cpaSync.saveFailed':
      return '保存失败'
    case 'cpaSync.runSuccess':
      return '执行成功'
    case 'cpaSync.runFailed':
      return '执行失败'
    case 'cpaSync.switchSuccess':
      return '切换成功'
    case 'cpaSync.switchFailed':
      return '切换失败'
    case 'cpaSync.cpaTestSuccess':
      return 'CPA 测试成功'
    case 'cpaSync.cpaTestFailed':
      return 'CPA 测试失败'
    case 'cpaSync.mihomoTestSuccess':
      return 'Mihomo 测试成功'
    case 'cpaSync.mihomoTestFailed':
      return 'Mihomo 测试失败'
    case 'cpaSync.loadMihomoGroupsFailed':
      return '加载策略组失败'
    case 'cpaSync.noStrategyGroups':
      return '没有策略组'
    case 'cpaSync.mihomoServiceConnected':
      return `已连接 ${options?.count}`
    case 'cpaSync.mihomoServiceConnectedNoGroups':
      return '已连接但无策略组'
    case 'cpaSync.groupCountSuffix':
      return '个候选'
    case 'cpaSync.currentValue':
      return '当前值'
    case 'cpaSync.testing':
      return '测试中'
    case 'cpaSync.runningTask':
      return '运行中'
    case 'cpaSync.idleTask':
      return '空闲'
    case 'cpaSync.connectionActionsDesc':
      return '连接动作'
    case 'cpaSync.workerBusyDesc':
      return '忙碌'
    case 'cpaSync.workerIdleDesc':
      return '空闲'
    case 'cpaSync.nextRunDisabledDesc':
      return '已关闭'
    case 'cpaSync.nextRunRunningDesc':
      return '执行中'
    case 'cpaSync.nextRunPendingDesc':
      return '等待中'
    case 'cpaSync.syncEverySeconds':
      return `每${options?.count}秒`
    case 'cpaSync.nextRunDisabled':
      return '已停用'
    case 'cpaSync.runningNow':
      return '执行中'
    case 'cpaSync.nextRunPending':
      return '等待中'
    case 'cpaSync.nextRunSoon':
      return '即将执行'
    case 'common.countdownHoursMinutes':
      return `${options?.hours}小时${options?.minutes}分`
    case 'common.countdownMinutesSeconds':
      return `${options?.minutes}分${options?.seconds}秒`
    case 'common.inSecondsLong':
      return `${options?.count}秒`
    case 'cpaSync.cpaAuthFilesFound':
      return `找到${options?.count}个授权文件`
    case 'cpaSync.parseMihomoFailed':
      return `解析失败:${options?.reason}`
    case 'cpaSync.missingFieldsHint':
      return `缺少:${options?.fields}`
    case 'cpaSync.cpaBaseUrl':
      return 'CPA 地址'
    case 'cpaSync.cpaAdminKey':
      return 'CPA 密钥'
    case 'cpaSync.mihomoBaseUrl':
      return 'Mihomo 地址'
    case 'cpaSync.mihomoSecret':
      return 'Mihomo 密钥'
    case 'cpaSync.mihomoStrategyGroup':
      return '策略组'
    case 'cpaSync.configComplete':
      return '配置完整'
    case 'cpaSync.configMissingBoth':
      return '缺少两侧配置'
    case 'cpaSync.configMissingCPA':
      return '缺少 CPA 配置'
    case 'cpaSync.configMissingMihomo':
      return '缺少 Mihomo 配置'
    case 'cpaSync.connectionOk':
      return '连接正常'
    case 'cpaSync.connectionFailed':
      return '连接失败'
    case 'cpaSync.incomplete':
      return '未完成'
    case 'cpaSync.pendingRetest':
      return '待重测'
    case 'cpaSync.unknown':
      return '未知'
    case 'cpaSync.busyHint':
      return '任务忙碌'
    default:
      return key
  }
}

describe('useCPASyncPageState', () => {
  beforeEach(() => {
    vi.resetModules()
    vi.clearAllMocks()
    loaderSnapshot = {
      settings: createSettings(),
      status: createStatus(),
    }
    sessionStorage.clear()
  })

  it('marks form dirty and saves updated settings successfully', async () => {
    const updatedSettings = createSettings({ cpa_base_url: 'https://changed.example.com' })
    mockApi.updateSettings.mockResolvedValue(updatedSettings)
    mockApi.getCPASyncStatus.mockResolvedValue(createStatus({ state: { ...createStatus().state, last_cpa_account_count: 9 } }))

    const { useCPASyncPageState } = await import('./useCPASyncPageState')
    const showToast = vi.fn()
    const { result } = renderHook(() => useCPASyncPageState({ t, showToast }))

    act(() => {
      result.current.updateForm({ cpa_base_url: 'https://changed.example.com' })
    })
    expect(result.current.dirty).toBe(true)

    await act(async () => {
      await result.current.handleSave()
    })

    expect(mockApi.updateSettings).toHaveBeenCalledWith(expect.objectContaining({ cpa_base_url: 'https://changed.example.com' }))
    await waitFor(() => expect(result.current.dirty).toBe(false))
    expect(showToast).toHaveBeenCalledWith('保存成功')
  })

  it('updates CPA test result and displayed account count', async () => {
    mockApi.testCPASyncCPA.mockResolvedValue({
      ok: true,
      message: 'CPA connection OK, found 2 auth files',
      tested_at: new Date().toISOString(),
      details: { account_count: 9 },
    })
    mockApi.getCPASyncStatus.mockResolvedValue(createStatus({ state: { ...createStatus().state, last_cpa_account_count: 9 } }))

    const { useCPASyncPageState } = await import('./useCPASyncPageState')
    const showToast = vi.fn()
    const { result } = renderHook(() => useCPASyncPageState({ t, showToast }))

    await act(async () => {
      await result.current.handleTestCPA()
    })

    expect(mockApi.testCPASyncCPA).toHaveBeenCalledTimes(1)
    await waitFor(() => expect(result.current.displayedCPAAccountCount).toBe(9))
    expect(showToast).toHaveBeenCalledWith(expect.stringContaining('找到2个授权文件'), 'success')
  })

  it('loads mihomo strategy groups and updates derived options', async () => {
    const settings = createSettings({
      mihomo_base_url: 'http://mihomo.local',
      mihomo_secret: 'token',
      mihomo_strategy_group: 'AUTO',
    })
    loaderSnapshot = {
      settings,
      status: createStatus({
        config: {
          ...createStatus().config,
          mihomo_base_url: settings.mihomo_base_url,
          mihomo_strategy_group: settings.mihomo_strategy_group,
        },
      }),
    }
    sessionStorage.setItem('codex2api:cpa-sync:mihomo-service', JSON.stringify({
      signature: getMihomoServiceSettingsSignature(settings),
      status: { ok: null, message: '', tested_at: '', details: {} },
      groups: [],
      error: '',
    }))
    mockApi.listCPASyncMihomoGroups.mockResolvedValue({
      groups: [{ name: 'AUTO', type: 'selector', current: 'node-a', candidate_count: 2 }],
    })

    const { useCPASyncPageState } = await import('./useCPASyncPageState')
    const showToast = vi.fn()
    const { result } = renderHook(() => useCPASyncPageState({ t, showToast }))

    await act(async () => {
      await result.current.fetchMihomoGroups(result.current.form, { showErrorToast: true })
    })

    await waitFor(() => expect(result.current.hasMihomoGroupOptions).toBe(true))
    expect(result.current.resolvedMihomoGroupOptions[0]?.value).toBe('AUTO')
    expect(result.current.mihomoServiceDisplayStatus.ok).toBe(true)
    expect(sessionStorage.getItem('codex2api:cpa-sync:mihomo-service')).not.toContain(settings.mihomo_secret)
    expect(showToast).not.toHaveBeenCalledWith(expect.stringContaining('加载策略组失败'), 'error')
  })
})
