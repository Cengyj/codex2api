import type { ReactNode } from 'react'
import { useCallback, useEffect, useMemo } from 'react'
import { Link } from 'react-router-dom'
import { AlertTriangle, CheckCircle2, RefreshCw, ShieldCheck, Users } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { api } from '../api'
import AccountHealthPanel from '../components/AccountHealthPanel'
import DashboardRefreshPanel from '../components/DashboardRefreshPanel'
import StateShell from '../components/StateShell'
import type { AccountRow, StatsResponse } from '../types'
import { useDataLoader } from '../hooks/useDataLoader'
import { Button } from '@/components/ui/button'

export default function Dashboard() {
  const { t } = useTranslation()

  const loadDashboardData = useCallback(async () => {
    const [stats, accountsResponse] = await Promise.all([
      api.getStats(),
      api.getAccounts(),
    ])

    return {
      stats,
      accounts: accountsResponse.accounts ?? [],
    }
  }, [])

  const { data, loading, error, reload, reloadSilently } = useDataLoader<{
    stats: StatsResponse | null
    accounts: AccountRow[]
  }>({
    initialData: { stats: null, accounts: [] },
    load: loadDashboardData,
  })

  useEffect(() => {
    const timer = window.setInterval(() => {
      void reloadSilently()
    }, 15000)
    return () => window.clearInterval(timer)
  }, [reloadSilently])

  const total = data.stats?.total ?? 0
  const available = data.stats?.available ?? 0

  const healthStats = useMemo(() => {
    const healthy = data.accounts.filter((account) => account.health_tier === 'healthy').length
    const warm = data.accounts.filter((account) => account.health_tier === 'warm').length
    const risky = data.accounts.filter((account) => account.health_tier === 'risky').length
    const isolated = data.accounts.filter(
      (account) => account.health_tier === 'banned' || account.status === 'unauthorized',
    ).length

    return {
      healthy,
      warm,
      risky,
      isolated,
      schedulable: healthy + warm,
    }
  }, [data.accounts])

  const availableRate = total ? Math.round((available / total) * 100) : 0
  const schedulableRate = total ? Math.round((healthStats.schedulable / total) * 100) : 0
  const pendingCount = healthStats.risky + healthStats.isolated

  return (
    <StateShell
      variant="page"
      loading={loading}
      error={error}
      onRetry={() => void reload()}
      loadingTitle={t('dashboard.loadingTitle')}
      loadingDescription={t('dashboard.loadingDesc')}
      errorTitle={t('dashboard.errorTitle')}
    >
      <div className="space-y-4">
        <section className="flex items-center justify-between gap-4 rounded-2xl border border-border bg-background px-5 py-4 shadow-sm max-md:flex-col max-md:items-start">
          <div className="min-w-0">
            <h1 className="text-[28px] font-semibold tracking-tight text-foreground">{t('dashboard.title')}</h1>
            <p className="mt-1 text-sm text-muted-foreground">{t('dashboard.schedulerOverviewDesc')}</p>
          </div>

          <div className="flex items-center gap-3 max-md:w-full max-md:flex-col max-md:items-stretch">
            <Button variant="outline" onClick={() => void reload()}>
              <RefreshCw className="size-4" />
              {t('common.refresh')}
            </Button>
            <Button variant="outline" asChild>
              <Link to="/accounts">{t('accounts.title')}</Link>
            </Button>
          </div>
        </section>

        <div className="grid grid-cols-4 gap-3 max-xl:grid-cols-2 max-sm:grid-cols-1">
          <DashboardStat
            label={t('dashboard.totalAccounts')}
            value={total}
            sub={t('dashboard.totalAccountsSub')}
            tone="blue"
            icon={<Users className="size-5" />}
          />
          <DashboardStat
            label={t('dashboard.availableAccounts')}
            value={available}
            sub={t('dashboard.availableAccountsSub', { rate: availableRate })}
            tone="green"
            icon={<CheckCircle2 className="size-5" />}
          />
          <DashboardStat
            label={t('dashboard.schedulableAccounts')}
            value={healthStats.schedulable}
            sub={t('dashboard.schedulableAccountsSub', { rate: schedulableRate })}
            tone="purple"
            icon={<ShieldCheck className="size-5" />}
          />
          <DashboardStat
            label={t('dashboard.pendingAccounts')}
            value={pendingCount}
            sub={t('dashboard.pendingAccountsSub')}
            tone="red"
            icon={<AlertTriangle className="size-5" />}
          />
        </div>

        <DashboardRefreshPanel
          scheduler={data.stats?.refresh_scheduler}
          refreshConfig={data.stats?.refresh_config}
          totalAccounts={total || data.accounts.length}
        />

        <AccountHealthPanel accounts={data.accounts} />
      </div>
    </StateShell>
  )
}

function DashboardStat({
  label,
  value,
  sub,
  tone,
  icon,
}: {
  label: string
  value: number | string
  sub: string
  tone: 'blue' | 'green' | 'purple' | 'red'
  icon: ReactNode
}) {
  const toneClass = {
    blue: 'bg-[hsl(var(--info-bg))] text-[hsl(var(--info))]',
    green: 'bg-[hsl(var(--success-bg))] text-[hsl(var(--success))]',
    purple: 'bg-primary/12 text-primary',
    red: 'bg-destructive/12 text-destructive',
  }[tone]

  return (
    <div className="rounded-2xl border border-border bg-background px-4 py-4 shadow-sm">
      <div className="flex items-center justify-between gap-3">
        <div className="text-[13px] font-semibold text-muted-foreground">{label}</div>
        <div className={`inline-flex size-9 items-center justify-center rounded-2xl ${toneClass}`}>
          {icon}
        </div>
      </div>
      <div className="mt-3 text-[28px] font-bold leading-none tracking-tight text-foreground">{value}</div>
      <div className="mt-1 text-[12px] text-muted-foreground">{sub}</div>
    </div>
  )
}
