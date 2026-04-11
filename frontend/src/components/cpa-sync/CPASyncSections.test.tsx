import { fireEvent, render, screen } from '@testing-library/react'
import { Loader2 } from 'lucide-react'
import { describe, expect, it, vi } from 'vitest'
import {
  CPASyncLogsSection,
  CPASyncOverviewCardsSection,
  CPASyncTabSwitcherSection,
} from './CPASyncSections'
import type { ConnectionPresentation } from './CPASyncUtils'
import type { ConnectionTestStatus, SystemSettings } from '@/types'

const t = (key: string, options?: Record<string, unknown>) => {
  if (key === 'cpaSync.syncEverySeconds') return `每${options?.count}秒`
  if (key === 'cpaSync.actionCountSuffix') return '条'
  return key
}

const form = {
  cpa_sync_enabled: true,
  cpa_base_url: 'https://cpa.example.com',
  cpa_admin_key: 'secret',
  cpa_min_accounts: 1,
  cpa_max_accounts: 10,
  cpa_max_uploads_per_hour: 3,
  cpa_switch_after_uploads: 15,
  cpa_sync_interval_seconds: 120,
  mihomo_base_url: 'http://mihomo.local',
  mihomo_secret: 'token',
  mihomo_strategy_group: 'AUTO',
  mihomo_delay_test_url: 'https://example.com',
  mihomo_delay_timeout_ms: 1000,
} as SystemSettings

const okPresentation: ConnectionPresentation = {
  label: '连接正常',
  className: 'ok',
  dotClassName: 'dot',
  icon: Loader2,
}

const okStatus: ConnectionTestStatus = {
  ok: true,
  message: '连接可用',
  tested_at: new Date(Date.now() - 60_000).toISOString(),
  details: {},
}

describe('CPASync sections', () => {
  it('switches tabs through the tab section', () => {
    const setActiveView = vi.fn()
    render(
      <CPASyncTabSwitcherSection
        t={t}
        activeView="overview"
        recentActionsCount={3}
        lastRunStatus="success"
        setActiveView={setActiveView}
      />,
    )

    fireEvent.click(screen.getByText('cpaSync.logsTab'))
    expect(setActiveView).toHaveBeenCalledWith('logs')
  })

  it('renders overview cards with key summaries', () => {
    render(
      <CPASyncOverviewCardsSection
        t={t}
        form={form}
        runtimeStatusLabel="空闲"
        runtimeStatusDescription="当前没有执行任务"
        runtimeBusy={false}
        cpaPresentation={okPresentation}
        cpaDisplayStatus={okStatus}
        mihomoServicePresentation={okPresentation}
        mihomoServiceDisplayStatus={okStatus}
        nextRunCountdown="1分5秒"
        nextRunDetail="今天 12:00"
        syncIntervalSeconds={120}
        status={{ state: { last_run_status: 'success', last_run_summary: '完成', last_run_at: new Date().toISOString() } }}
        displayedCurrentMihomoNode="节点A"
      />,
    )

    expect(screen.getByText('cpaSync.autoSyncStatus')).toBeTruthy()
    expect(screen.getByText('1分5秒')).toBeTruthy()
    expect(screen.getByText('节点A')).toBeTruthy()
  })

  it('shows empty logs placeholder when no actions exist', () => {
    render(
      <CPASyncLogsSection
        t={t}
        active
        recentActions={[]}
        status={{ state: { last_run_status: 'success', last_run_at: new Date().toISOString(), last_error_summary: '' } }}
      />,
    )

    expect(screen.getByText('cpaSync.noActions')).toBeTruthy()
  })
})
