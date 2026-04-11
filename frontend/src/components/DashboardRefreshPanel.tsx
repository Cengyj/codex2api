import { Clock3 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { RefreshSchedulerConfig, RefreshSchedulerStatus } from '../types'
import { formatBeijingTime, formatRelativeTime } from '../utils/time'
import { Card, CardContent } from '@/components/ui/card'

type Tone = 'neutral' | 'success' | 'warning' | 'info'

export default function DashboardRefreshPanel({
  scheduler,
  refreshConfig,
  totalAccounts,
}: {
  scheduler?: RefreshSchedulerStatus
  refreshConfig?: RefreshSchedulerConfig
  totalAccounts: number
}) {
  const { t } = useTranslation()

  const enabled = refreshConfig?.scan_enabled ?? true
  const intervalMinutes = Math.max(1, Math.round((refreshConfig?.scan_interval_seconds ?? 120) / 60))
  const preExpireMinutes = Math.max(0, Math.round((refreshConfig?.pre_expire_seconds ?? 300) / 60))
  const targetAccounts = Math.max(0, scheduler?.target_accounts ?? 0)
  const processed = Math.max(0, scheduler?.processed ?? 0)
  const success = Math.max(0, scheduler?.success ?? 0)
  const failure = Math.max(0, scheduler?.failure ?? 0)
  const progressTotal = Math.max(targetAccounts, processed)
  const finishedAt = scheduler?.finished_at ?? null
  const nextScanAt = enabled ? scheduler?.next_scan_at ?? null : null
  const hasStarted = Boolean(
    scheduler?.started_at || scheduler?.finished_at || targetAccounts > 0 || processed > 0,
  )

  const schedulerStatusTone: Tone = scheduler?.running
    ? 'info'
    : enabled
      ? (hasStarted ? 'success' : 'warning')
      : 'neutral'

  const schedulerBadge = schedulerStatusTone === 'info'
    ? {
        label: t('dashboard.schedulerTaskRunning'),
        className: 'bg-blue-500/10 text-blue-600 dark:bg-blue-500/20 dark:text-blue-300',
      }
    : schedulerStatusTone === 'success'
      ? {
          label: t('dashboard.schedulerTaskIdle'),
          className: 'bg-emerald-500/10 text-emerald-600 dark:bg-emerald-500/20 dark:text-emerald-300',
        }
      : schedulerStatusTone === 'warning'
        ? {
            label: t('dashboard.schedulerTaskNotStarted'),
            className: 'bg-amber-500/10 text-amber-600 dark:bg-amber-500/20 dark:text-amber-300',
          }
        : {
            label: t('dashboard.schedulerTaskDisabled'),
            className: 'bg-slate-500/10 text-slate-600 dark:bg-slate-500/20 dark:text-slate-300',
          }

  const schedulerStatusLabel = scheduler?.running
    ? t('dashboard.schedulerTaskRunning')
    : enabled && hasStarted
      ? t('dashboard.schedulerTaskIdle')
      : enabled
        ? t('dashboard.schedulerTaskNotStarted')
        : t('dashboard.schedulerTaskDisabled')

  const schedulerStatusHint = scheduler?.running
    ? t('dashboard.schedulerTaskRunningHint', { processed, total: progressTotal })
    : enabled && hasStarted
      ? (finishedAt
        ? t('dashboard.lastFinishedAt', { time: formatBeijingTime(finishedAt) })
        : t('dashboard.schedulerTaskIdleHint'))
      : enabled
        ? t('dashboard.schedulerTaskNotStartedHint')
        : t('dashboard.schedulerTaskDisabledHint')

  const nextScanValue = enabled
    ? (nextScanAt ? formatBeijingTime(nextScanAt) : t('dashboard.schedulerTaskNotStarted'))
    : t('dashboard.schedulerTaskDisabled')

  const nextScanHint = enabled
    ? (nextScanAt ? formatCountdown(nextScanAt, t) : t('dashboard.schedulerTaskNotStartedHint'))
    : t('dashboard.schedulerScanStopped')

  const lastCycleValue = finishedAt
    ? formatRelativeTime(finishedAt, { includeSeconds: true })
    : (enabled ? t('dashboard.schedulerNeverRun') : t('dashboard.schedulerTaskDisabled'))

  const lastCycleHint = finishedAt
    ? formatBeijingTime(finishedAt)
    : (enabled ? t('dashboard.schedulerTaskNotStartedHint') : t('dashboard.schedulerScanStopped'))

  const progressValue = progressTotal > 0
    ? `${processed} / ${progressTotal}`
    : (scheduler?.running ? '0 / 0' : t('dashboard.schedulerNeverRun'))

  const progressPercent = Math.max(
    0,
    Math.min(100, progressTotal > 0 ? Math.round((processed / progressTotal) * 100) : 0),
  )

  return (
    <Card className="border-border/80 shadow-sm">
      <CardContent className="p-5">
        <div className="flex items-start justify-between gap-3 max-md:flex-col">
          <div>
            <div className="text-sm font-semibold text-foreground">{t('dashboard.schedulerOverviewTitle')}</div>
            <p className="mt-1 text-xs text-muted-foreground">{t('dashboard.schedulerOverviewDesc')}</p>
          </div>
          <span className={`inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-[11px] font-semibold ${schedulerBadge.className}`}>
            <Clock3 className="size-3.5" />
            {schedulerBadge.label}
          </span>
        </div>

        <div className="mt-4 rounded-2xl border border-border/80 bg-muted/15 p-4">
          <div className="flex flex-wrap items-center gap-2">
            <SchedulerInlineChip
              label={t('dashboard.currentTaskStatus')}
              value={schedulerStatusLabel}
              tone={schedulerStatusTone}
            />
            <SchedulerInlineChip
              label={t('dashboard.estimatedNextScan')}
              value={nextScanValue}
              tone={enabled ? 'info' : 'neutral'}
            />
            <SchedulerInlineChip
              label={t('dashboard.lastCycleStatus')}
              value={lastCycleValue}
              tone="neutral"
            />
            <SchedulerInlineChip
              label={t('dashboard.processedThisCycle')}
              value={progressValue}
              tone={scheduler?.running ? 'warning' : 'neutral'}
            />
          </div>

          <div className="mt-4 grid gap-3 lg:grid-cols-[minmax(0,1.4fr)_minmax(320px,1fr)]">
            <div className="rounded-2xl border border-border bg-background/80 px-4 py-3">
              <div className="flex items-center justify-between gap-3">
                <div className="text-[13px] font-semibold text-foreground">{t('dashboard.processedThisCycle')}</div>
                <div className="text-[12px] text-muted-foreground">{progressValue}</div>
              </div>
              <div className="mt-3 h-2.5 overflow-hidden rounded-full bg-muted">
                <div
                  className="h-full rounded-full bg-primary transition-all"
                  style={{ width: `${progressPercent}%` }}
                />
              </div>
              <div className="mt-2 text-[12px] text-muted-foreground">
                {t('dashboard.processedThisCycleHint', {
                  total: targetAccounts,
                  success,
                  failure,
                })}
              </div>
            </div>

            <div className="rounded-2xl border border-border bg-background/80 px-4 py-3">
              <div className="text-[13px] font-semibold text-foreground">{t('dashboard.schedulerOverviewSummary')}</div>
              <div className="mt-2 space-y-1.5 text-[12px] text-muted-foreground">
                <div>{schedulerStatusHint}</div>
                <div>{t('dashboard.currentBatchHint', { interval: intervalMinutes, preExpire: preExpireMinutes })}</div>
                <div>{nextScanHint}</div>
                <div>{lastCycleHint}</div>
                <div>{t('dashboard.schedulerScopeHint', { total: scheduler?.total_accounts ?? totalAccounts })}</div>
              </div>
            </div>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

function SchedulerInlineChip({
  label,
  value,
  tone,
}: {
  label: string
  value: string | number
  tone: Tone
}) {
  const toneClass = {
    neutral: 'border-border bg-background text-foreground',
    success: 'border-emerald-500/20 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
    warning: 'border-amber-500/20 bg-amber-500/10 text-amber-700 dark:text-amber-300',
    info: 'border-blue-500/20 bg-blue-500/10 text-blue-700 dark:text-blue-300',
  }[tone]

  return (
    <div className={`inline-flex items-center gap-2 rounded-full border px-3 py-1.5 text-[12px] ${toneClass}`}>
      <span className="font-medium opacity-75">{label}</span>
      <span className="font-semibold">{value}</span>
    </div>
  )
}

function formatCountdown(
  dateStr: string | null,
  t: (key: string, options?: Record<string, unknown>) => string,
) {
  if (!dateStr) return t('dashboard.schedulerNextScanUnavailable')

  const diffMs = new Date(dateStr).getTime() - Date.now()
  if (!Number.isFinite(diffMs) || diffMs <= 0) {
    return t('common.justNow')
  }

  const seconds = Math.floor(diffMs / 1000)
  if (seconds < 3600) {
    const minutes = Math.floor(seconds / 60)
    const remainSeconds = seconds % 60
    if (minutes <= 0) {
      return t('common.inSecondsLong', { count: remainSeconds })
    }
    return t('common.countdownMinutesSeconds', { minutes, seconds: remainSeconds })
  }

  const minutes = Math.floor(seconds / 60)
  if (minutes < 1440) {
    const hours = Math.floor(minutes / 60)
    const remainMinutes = minutes % 60
    return remainMinutes > 0
      ? t('common.countdownHoursMinutes', { hours, minutes: remainMinutes })
      : t('common.inHoursLong', { count: hours })
  }

  const hours = Math.floor(minutes / 60)
  const days = Math.floor(hours / 24)
  return t('common.inDaysLong', { count: days })
}
