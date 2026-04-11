import type { ReactNode } from 'react'
import { useCallback, useEffect } from 'react'
import { Cpu, Database, HardDrive, Server, Users } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { api } from '../api'
import PageHeader from '../components/PageHeader'
import StateShell from '../components/StateShell'
import AccountTrendChart from '../components/AccountTrendChart'
import { useDataLoader } from '../hooks/useDataLoader'
import type { OpsOverviewResponse } from '../types'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'

export default function Operations() {
  const { t } = useTranslation()
  const loadOperationsData = useCallback(() => api.getOpsOverview(), [])

  const { data: overview, loading, error, reload, reloadSilently } = useDataLoader<OpsOverviewResponse | null>({
    initialData: null,
    load: loadOperationsData,
  })

  useEffect(() => {
    const timer = window.setInterval(() => {
      void reloadSilently()
    }, 15000)
    return () => window.clearInterval(timer)
  }, [reloadSilently])

  const updatedLabel = overview?.updated_at ? formatTimeLabel(overview.updated_at) : '--:--:--'

  return (
    <StateShell
      variant="page"
      loading={loading}
      error={error}
      onRetry={() => void reload()}
      loadingTitle={t('ops.loadingTitle')}
      loadingDescription={t('ops.loadingDesc')}
      errorTitle={t('ops.errorTitle')}
    >
      <>
        <PageHeader
          title={t('ops.title')}
          description={t('ops.description')}
          actions={
            <div className="flex items-center gap-3 max-sm:w-full max-sm:flex-col max-sm:items-stretch">
              <span className="text-sm text-muted-foreground max-sm:text-center">{t('ops.lastUpdated', { time: updatedLabel })}</span>
              <Button variant="outline" onClick={() => void reload()}>
                {t('common.refresh')}
              </Button>
            </div>
          }
        />

        {overview ? (
          <div className="space-y-6">
            <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4">
              <OpsMetricCard label={t('ops.cpu')} value={`${overview.cpu.percent.toFixed(1)}%`} sub={t('ops.cpuCores', { count: overview.cpu.cores })} icon={<Cpu className="size-5" />} />
              <OpsMetricCard label={t('ops.memory')} value={`${overview.memory.percent.toFixed(1)}%`} sub={`${t('ops.memoryUsage', { used: formatBytes(overview.memory.used_bytes), total: formatBytes(overview.memory.total_bytes) })} · ${t('ops.processMemory', { size: formatBytes(overview.memory.process_bytes) })}`} icon={<HardDrive className="size-5" />} />
              <OpsMetricCard label={overview.database_label || t('ops.postgres')} value={`${overview.postgres.usage_percent.toFixed(1)}%`} sub={t('ops.pgConn', { open: overview.postgres.open, max: overview.postgres.max_open || '-' })} icon={<Database className="size-5" />} />
              <OpsMetricCard label={overview.cache_label || t('ops.redis')} value={`${overview.redis.usage_percent.toFixed(1)}%`} sub={t('ops.redisConn', { open: overview.redis.total_conns, max: overview.redis.pool_size || '-' })} icon={<Server className="size-5" />} />
              <OpsMetricCard label={t('ops.accountPool')} value={`${overview.runtime.available_accounts} / ${overview.runtime.total_accounts}`} sub={t('ops.accountPoolDesc')} icon={<Users className="size-5" />} />
            </div>
            <Card>
              <CardContent className="p-6">
                <h3 className="text-base font-semibold text-foreground mb-2">{t('ops.accountTrend')}</h3>
                <AccountTrendChart />
              </CardContent>
            </Card>
          </div>
        ) : null}
      </>
    </StateShell>
  )
}

function OpsMetricCard({ label, value, sub, icon }: { label: string; value: string; sub: string; icon: React.ReactNode }) {
  return (
    <Card className="py-0">
      <CardContent className="p-4">
        <div className="flex items-center justify-between gap-3">
          <span className="text-[13px] font-semibold text-muted-foreground">{label}</span>
          <div className="flex size-10 items-center justify-center rounded-2xl bg-muted/40">
            {icon}
          </div>
        </div>
        <div className="text-[28px] font-bold leading-none tracking-tight text-foreground mt-3">{value}</div>
        <div className="text-[13px] text-muted-foreground mt-1">{sub}</div>
      </CardContent>
    </Card>
  )
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`
}

function formatTimeLabel(iso: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return '--:--:--'
  return date.toLocaleTimeString([], { hour12: false })
}
