import type { ReactNode } from 'react'
import { useMemo } from 'react'
import { Link } from 'react-router-dom'
import { AlertTriangle, ArrowRight, ShieldCheck, Siren, TimerReset } from 'lucide-react'
import StatusBadge from './StatusBadge'
import type { AccountRow } from '../types'
import { formatRelativeTime } from '../utils/time'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'

export default function AccountHealthPanel({
  accounts,
}: {
  accounts: AccountRow[]
}) {
  const updatedLabel = useMemo(() => {
    const latest = [...accounts]
      .map((account) => account.updated_at)
      .filter(Boolean)
      .sort((left, right) => new Date(right).getTime() - new Date(left).getTime())[0]

    return latest ? formatTimeLabel(latest) : '--:--:--'
  }, [accounts])

  const healthRows = useMemo(
    () => [
      {
        key: 'healthy',
        label: '健康',
        description: '可直接参与调度',
        tone: 'success' as const,
        count: accounts.filter((account) => account.health_tier === 'healthy').length,
      },
      {
        key: 'warm',
        label: '观察中',
        description: '近期有波动，建议关注',
        tone: 'warning' as const,
        count: accounts.filter((account) => account.health_tier === 'warm').length,
      },
      {
        key: 'risky',
        label: '待处理',
        description: '风险较高，建议尽快处理',
        tone: 'danger' as const,
        count: accounts.filter((account) => account.health_tier === 'risky').length,
      },
      {
        key: 'isolated',
        label: '已隔离',
        description: '401 或不可用，暂不参与调度',
        tone: 'neutral' as const,
        count: accounts.filter((account) => account.health_tier === 'banned' || account.status === 'unauthorized').length,
      },
    ],
    [accounts],
  )

  const signalRows = useMemo(() => {
    const now = Date.now()
    const withinWindow = (iso?: string, minutes = 60) => {
      if (!iso) return false
      const ts = new Date(iso).getTime()
      if (Number.isNaN(ts)) return false
      return now - ts <= minutes * 60 * 1000
    }

    return [
      {
        label: '最近 401',
        description: '24 小时内出现 unauthorized',
        value: accounts.filter((account) => withinWindow(account.last_unauthorized_at, 24 * 60)).length,
        tone: 'danger' as const,
        icon: <Siren className="size-4" />,
      },
      {
        label: '最近限流',
        description: '1 小时内触发 429',
        value: accounts.filter((account) => withinWindow(account.last_rate_limited_at, 60)).length,
        tone: 'warning' as const,
        icon: <AlertTriangle className="size-4" />,
      },
      {
        label: '最近超时',
        description: '15 分钟内发生请求超时',
        value: accounts.filter((account) => withinWindow(account.last_timeout_at, 15)).length,
        tone: 'neutral' as const,
        icon: <TimerReset className="size-4" />,
      },
    ]
  }, [accounts])

  const focusAccounts = useMemo(() => {
    const priority = (account: AccountRow) => {
      if (account.health_tier === 'banned' || account.status === 'unauthorized') return 4
      if (account.health_tier === 'risky') return 3
      if (account.health_tier === 'warm') return 2
      return 1
    }

    const latestSignalTime = (account: AccountRow) =>
      Math.max(
        account.last_unauthorized_at ? new Date(account.last_unauthorized_at).getTime() : 0,
        account.last_rate_limited_at ? new Date(account.last_rate_limited_at).getTime() : 0,
        account.last_timeout_at ? new Date(account.last_timeout_at).getTime() : 0,
        account.updated_at ? new Date(account.updated_at).getTime() : 0,
      )

    return [...accounts]
      .filter((account) => priority(account) >= 2)
      .sort((left, right) => {
        const priorityDiff = priority(right) - priority(left)
        if (priorityDiff !== 0) return priorityDiff

        const signalDiff = latestSignalTime(right) - latestSignalTime(left)
        if (signalDiff !== 0) return signalDiff

        return (left.scheduler_score ?? 0) - (right.scheduler_score ?? 0)
      })
      .slice(0, 4)
  }, [accounts])

  return (
    <Card className="overflow-hidden border-border/80 py-0 shadow-sm">
      <CardContent className="p-0">
        <div className="flex items-center justify-between gap-4 border-b border-border px-4 py-3 max-lg:flex-col max-lg:items-start">
          <div>
            <h3 className="text-lg font-semibold tracking-tight text-foreground">账号健康与待处理事项</h3>
            <p className="mt-1 text-sm text-muted-foreground">首屏查看账号结构、异常摘要和优先处理账号。</p>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="outline" className="border-border bg-background/70 text-muted-foreground">
              最后更新：{updatedLabel}
            </Badge>
            <Button variant="outline" size="sm" asChild>
              <Link to="/accounts">
                查看全部账号
                <ArrowRight className="size-4" />
              </Link>
            </Button>
          </div>
        </div>

        <div className="grid gap-4 p-4 lg:grid-cols-[360px_minmax(0,1fr)]">
          <div className="space-y-4">
            <section className="rounded-2xl border border-border bg-muted/15 p-4">
              <div className="text-sm font-semibold text-foreground">账号分层</div>
              <div className="mt-3 space-y-2.5">
                {healthRows.map((item) => (
                  <CompactRow
                    key={item.key}
                    label={item.label}
                    description={item.description}
                    value={item.count}
                    tone={item.tone}
                  />
                ))}
              </div>
            </section>

            <section className="rounded-2xl border border-border bg-muted/15 p-4">
              <div className="text-sm font-semibold text-foreground">异常摘要</div>
              <div className="mt-3 grid grid-cols-3 gap-2">
                {signalRows.map((item) => (
                  <SignalStat
                    key={item.label}
                    icon={item.icon}
                    label={item.label}
                    value={item.value}
                    tone={item.tone}
                  />
                ))}
              </div>
            </section>
          </div>

          <section className="rounded-2xl border border-border bg-muted/15 p-4">
            <div className="flex items-start justify-between gap-3 max-sm:flex-col max-sm:items-start">
              <div>
                <div className="text-sm font-semibold text-foreground">优先处理账号</div>
              </div>
              <Badge variant="outline" className="border-border bg-background/70 text-muted-foreground">
                {focusAccounts.length} / 4
              </Badge>
            </div>

            <div className="mt-3 grid gap-3 xl:grid-cols-2">
              {focusAccounts.length > 0 ? (
                focusAccounts.map((account) => <FocusAccountCard key={account.id} account={account} />)
              ) : (
                <div className="rounded-2xl border border-dashed border-border bg-background/80 px-4 py-8 text-sm text-muted-foreground xl:col-span-2">
                  当前没有需要优先处理的账号，账号池整体较为稳定。
                </div>
              )}
            </div>
          </section>
        </div>
      </CardContent>
    </Card>
  )
}

function CompactRow({
  label,
  description,
  value,
  tone,
}: {
  label: string
  description: string
  value: number
  tone: 'neutral' | 'success' | 'warning' | 'danger'
}) {
  const toneClass = {
    neutral: 'bg-slate-500/10 text-slate-600 dark:bg-slate-500/20 dark:text-slate-300',
    success: 'bg-emerald-500/10 text-emerald-600 dark:bg-emerald-500/20 dark:text-emerald-300',
    warning: 'bg-amber-500/10 text-amber-600 dark:bg-amber-500/20 dark:text-amber-300',
    danger: 'bg-red-500/10 text-red-600 dark:bg-red-500/20 dark:text-red-300',
  }[tone]

  return (
    <div className="flex items-center justify-between gap-3 rounded-2xl border border-border bg-background/80 px-3 py-2.5">
      <div className="min-w-0">
        <span className={`inline-flex rounded-full px-2 py-0.5 text-[11px] font-semibold ${toneClass}`}>{label}</span>
        <div className="mt-1 text-[11px] text-muted-foreground">{description}</div>
      </div>
      <span className="text-lg font-bold tracking-tight text-foreground">{value}</span>
    </div>
  )
}

function SignalStat({
  icon,
  label,
  value,
  tone,
}: {
  icon: ReactNode
  label: string
  value: number
  tone: 'neutral' | 'warning' | 'danger'
}) {
  const toneClass = {
    neutral: 'bg-slate-500/10 text-slate-600 dark:bg-slate-500/20 dark:text-slate-300',
    warning: 'bg-amber-500/10 text-amber-600 dark:bg-amber-500/20 dark:text-amber-300',
    danger: 'bg-red-500/10 text-red-600 dark:bg-red-500/20 dark:text-red-300',
  }[tone]

  return (
    <div className="rounded-2xl border border-border bg-background/80 px-3 py-3">
      <div className="flex items-center justify-between gap-2">
        <span className="text-[12px] font-medium text-muted-foreground">{label}</span>
        <span className={`inline-flex size-7 items-center justify-center rounded-xl ${toneClass}`}>{icon}</span>
      </div>
      <div className="mt-2 text-2xl font-bold leading-none tracking-tight text-foreground">{value}</div>
    </div>
  )
}

function FocusAccountCard({ account }: { account: AccountRow }) {
  const tags = buildReasonTags(account)
  const healthMeta = getHealthTierMeta(account)

  return (
    <div className="rounded-2xl border border-border bg-background/80 px-4 py-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="truncate text-[14px] font-semibold text-foreground">
            {account.email || account.name || `ID ${account.id}`}
          </div>
          <div className="mt-1 text-[11px] text-muted-foreground">
            最近更新 {formatRelativeTime(account.updated_at, { variant: 'compact' })}
          </div>
        </div>
        <StatusBadge status={account.status} />
      </div>

      <div className="mt-2 flex flex-wrap items-center gap-2">
        <Badge variant="outline" className={healthMeta.className}>
          {healthMeta.label}
        </Badge>
        {tags.length > 0 ? (
          tags.map((tag) => (
            <Badge key={tag.label} variant="outline" className={tag.className}>
              {tag.label}
            </Badge>
          ))
        ) : (
          <Badge variant="outline" className="border-transparent bg-emerald-500/10 text-emerald-600 dark:bg-emerald-500/20 dark:text-emerald-300">
            <ShieldCheck className="mr-1 size-3.5" />
            状态稳定
          </Badge>
        )}
      </div>

      <div className="mt-2 text-[12px] text-muted-foreground">
        评分 {Math.round(account.scheduler_score ?? 0)} · 并发 {account.dynamic_concurrency_limit ?? '-'} · 套餐 {account.plan_type || '-'}
      </div>
    </div>
  )
}

function getHealthTierMeta(account: AccountRow) {
  const isolated = account.health_tier === 'banned' || account.status === 'unauthorized'
  if (isolated) {
    return {
      label: '已隔离',
      className: 'border-transparent bg-slate-500/10 text-slate-600 dark:bg-slate-500/20 dark:text-slate-300',
    }
  }

  switch (account.health_tier) {
    case 'healthy':
      return {
        label: '健康',
        className: 'border-transparent bg-emerald-500/10 text-emerald-600 dark:bg-emerald-500/20 dark:text-emerald-300',
      }
    case 'warm':
      return {
        label: '观察中',
        className: 'border-transparent bg-amber-500/10 text-amber-600 dark:bg-amber-500/20 dark:text-amber-300',
      }
    case 'risky':
      return {
        label: '待处理',
        className: 'border-transparent bg-red-500/10 text-red-600 dark:bg-red-500/20 dark:text-red-300',
      }
    default:
      return {
        label: '未知',
        className: 'border-border text-muted-foreground',
      }
  }
}

function buildReasonTags(account: AccountRow) {
  const breakdown = account.scheduler_breakdown
  if (!breakdown) return []

  const tags: Array<{ label: string; className: string }> = []

  if (breakdown.unauthorized_penalty > 0) {
    tags.push({
      label: '401 风险',
      className: 'border-transparent bg-red-500/10 text-red-600 dark:bg-red-500/20 dark:text-red-300',
    })
  }
  if (breakdown.rate_limit_penalty > 0) {
    tags.push({
      label: '429 限流',
      className: 'border-transparent bg-amber-500/10 text-amber-600 dark:bg-amber-500/20 dark:text-amber-300',
    })
  }
  if (breakdown.timeout_penalty > 0) {
    tags.push({
      label: '超时',
      className: 'border-transparent bg-orange-500/10 text-orange-600 dark:bg-orange-500/20 dark:text-orange-300',
    })
  }
  if (breakdown.server_penalty > 0) {
    tags.push({
      label: '5xx',
      className: 'border-transparent bg-rose-500/10 text-rose-600 dark:bg-rose-500/20 dark:text-rose-300',
    })
  }
  if (breakdown.latency_penalty > 0) {
    tags.push({
      label: '高延迟',
      className: 'border-transparent bg-cyan-500/10 text-cyan-700 dark:bg-cyan-500/20 dark:text-cyan-300',
    })
  }

  return tags.slice(0, 2)
}

function formatTimeLabel(iso: string) {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return '--:--:--'
  return date.toLocaleTimeString('zh-CN', { hour12: false })
}
