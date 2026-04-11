import type { ReactNode } from 'react'
import {
  Clock3,
  Download,
  Loader2,
  PlayCircle,
  Repeat,
  Search,
  Trash2,
  Upload,
} from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import type { ConnectionTestStatus, CPASyncAction } from '@/types'
import { formatRelativeTime } from '@/utils/time'
import {
  type ConnectionPresentation,
  type TranslateFn,
  errorBadgeClassName,
  formatRuntimeStatus,
  formatTestDetailLabel,
  getRuntimeTone,
  infoBadgeClassName,
  mutedBadgeClassName,
  successBadgeClassName,
  warningBadgeClassName,
} from './CPASyncUtils'

export function OverviewCard({
  icon,
  label,
  value,
  sub,
  badge,
}: {
  icon: ReactNode
  label: string
  value: string
  sub: string
  badge?: ReactNode
}) {
  return (
    <Card className="overflow-hidden border-border/80 bg-gradient-to-br from-white via-white to-slate-50/80 shadow-sm dark:from-slate-950 dark:via-slate-950 dark:to-slate-900/80">
      <CardContent className="flex min-h-[160px] flex-col justify-between gap-4 p-5">
        <div className="flex items-start justify-between gap-3">
          <div>
            <div className="text-xs font-semibold tracking-[0.08em] text-muted-foreground">{label}</div>
            <div className="mt-3 text-[22px] font-semibold leading-tight text-foreground break-words">{value}</div>
          </div>
          <div className="flex size-11 shrink-0 items-center justify-center rounded-2xl bg-primary/10 text-primary">
            {icon}
          </div>
        </div>
        <div className="space-y-3">
          <div className="text-sm text-muted-foreground break-words">{sub}</div>
          {badge ? <div>{badge}</div> : null}
        </div>
      </CardContent>
    </Card>
  )
}

export function ConsoleTabButton({
  active,
  icon,
  title,
  description,
  meta,
  onClick,
}: {
  active: boolean
  icon: ReactNode
  title: string
  description: string
  meta?: ReactNode
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`rounded-[28px] border px-4 py-4 text-left transition-all ${
        active
          ? 'border-primary/30 bg-primary/10 shadow-[0_10px_30px_rgba(15,23,42,0.06)] dark:border-primary/25 dark:bg-primary/15'
          : 'border-transparent bg-white/70 hover:border-border hover:bg-white dark:bg-white/5 dark:hover:border-white/10'
      }`}
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 items-start gap-3">
          <div className={`flex size-11 shrink-0 items-center justify-center rounded-2xl ${active ? 'bg-primary text-primary-foreground' : 'bg-primary/10 text-primary'}`}>
            {icon}
          </div>
          <div className="min-w-0">
            <div className="text-sm font-semibold text-foreground">{title}</div>
            <div className="mt-1 text-xs leading-5 text-muted-foreground">{description}</div>
          </div>
        </div>
        {meta ? <div className="shrink-0">{meta}</div> : null}
      </div>
    </button>
  )
}

export function StatusTile({
  label,
  value,
  tone,
}: {
  label: string
  value: string
  tone: 'neutral' | 'success' | 'info' | 'warning'
}) {
  const dotClass = {
    neutral: 'bg-slate-400',
    success: 'bg-emerald-500',
    info: 'bg-blue-500',
    warning: 'bg-amber-500',
  }[tone]

  return (
    <div className="rounded-3xl border border-border bg-white/60 px-4 py-4 dark:bg-white/5">
      <div className="text-xs font-semibold text-muted-foreground">{label}</div>
      <div className="mt-3 flex items-center justify-between gap-3">
        <div className="text-lg font-semibold text-foreground break-all">{value}</div>
        <span className={`size-2.5 rounded-full ${dotClass}`} />
      </div>
    </div>
  )
}

export function ActionGroup({
  title,
  description,
  footer,
  children,
}: {
  title: string
  description: string
  footer: string
  children: ReactNode
}) {
  return (
    <div className="rounded-3xl border border-border bg-white/70 p-5 dark:bg-white/5">
      <div>
        <div className="text-sm font-semibold text-foreground">{title}</div>
        <div className="mt-1 text-xs text-muted-foreground">{description}</div>
      </div>
      <div className="mt-4 grid gap-3 sm:grid-cols-2">{children}</div>
      <div className="mt-3 text-xs text-muted-foreground">{footer}</div>
    </div>
  )
}

export function ConfigSection({
  title,
  description,
  children,
}: {
  title: string
  description?: string
  children: ReactNode
}) {
  return (
    <div className="rounded-[30px] border border-border/80 bg-white/88 p-5 shadow-sm dark:bg-white/5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="text-sm font-semibold text-foreground">{title}</div>
          {description ? <div className="mt-1 text-xs leading-relaxed text-muted-foreground">{description}</div> : null}
        </div>
      </div>
      <div className="mt-5 grid gap-4 grid-cols-[repeat(auto-fit,minmax(220px,1fr))]">{children}</div>
    </div>
  )
}

export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <label className="mb-2 block text-sm font-semibold text-slate-700 dark:text-slate-300">{label}</label>
      {children}
    </div>
  )
}

export function LogMetaCard({
  label,
  value,
  badgeClassName,
}: {
  label: string
  value: string
  badgeClassName: string
}) {
  return (
    <div className="rounded-[28px] border border-border/80 bg-white/70 p-4 shadow-sm dark:bg-white/5">
      <div className="text-xs font-semibold tracking-[0.08em] text-muted-foreground">{label}</div>
      <div className="mt-3">
        <Badge className={badgeClassName}>{value}</Badge>
      </div>
    </div>
  )
}

export function MiniConfigSummaryCard({
  title,
  value,
  note,
  tone,
}: {
  title: string
  value: string
  note: string
  tone: 'success' | 'warning' | 'info'
}) {
  const toneMap = {
    success: {
      badge: successBadgeClassName,
      icon: 'bg-emerald-500',
    },
    warning: {
      badge: warningBadgeClassName,
      icon: 'bg-amber-500',
    },
    info: {
      badge: infoBadgeClassName,
      icon: 'bg-blue-500',
    },
  }[tone]

  return (
    <div className="rounded-[28px] border border-border/80 bg-white/85 p-5 shadow-sm dark:bg-white/5">
      <div className="flex items-center justify-between gap-3">
        <div className="text-xs font-semibold tracking-[0.08em] text-muted-foreground">{title}</div>
        <span className={`size-2.5 rounded-full ${toneMap.icon}`} />
      </div>
      <div className="mt-3">
        <Badge className={toneMap.badge}>{value}</Badge>
      </div>
      <div className="mt-3 text-sm leading-relaxed text-muted-foreground break-words">{note}</div>
    </div>
  )
}

function formatActionKind(kind: string, t: TranslateFn): string {
  const map: Record<string, string> = {
    upload: t('cpaSync.kindUpload'),
    delete: t('cpaSync.kindDelete'),
    download: t('cpaSync.kindDownload'),
    reconcile: t('cpaSync.kindReconcile'),
    switch: t('cpaSync.kindSwitch'),
    run: t('cpaSync.kindRun'),
  }
  return map[kind] ?? kind
}

function formatActionStatus(status: string, t: TranslateFn): string {
  const map: Record<string, string> = {
    success: t('cpaSync.actionSuccess'),
    error: t('cpaSync.actionError'),
    warning: t('cpaSync.actionWarning'),
    info: t('cpaSync.actionInfo'),
  }
  return map[status] ?? status
}

function getActionKindClassName(kind: string): string {
  const map: Record<string, string> = {
    upload: successBadgeClassName,
    switch: infoBadgeClassName,
    delete: errorBadgeClassName,
    download: mutedBadgeClassName,
    reconcile: warningBadgeClassName,
    run: mutedBadgeClassName,
  }
  return map[kind] ?? mutedBadgeClassName
}

function getActionStatusClassName(status: string): string {
  const map: Record<string, string> = {
    success: successBadgeClassName,
    error: errorBadgeClassName,
    warning: warningBadgeClassName,
    info: mutedBadgeClassName,
  }
  return map[status] ?? mutedBadgeClassName
}

export function ConnectionBadge({ presentation }: { presentation: ConnectionPresentation }) {
  const Icon = presentation.icon
  return (
    <Badge className={presentation.className}>
      <span className={`size-1.5 rounded-full ${presentation.dotClassName}`} />
      <Icon className={presentation.icon === Loader2 ? 'size-3 animate-spin' : 'size-3'} />
      {presentation.label}
    </Badge>
  )
}

export function RuntimeStatusBadge({
  status,
  t,
}: {
  status: string
  t: TranslateFn
}) {
  return <Badge className={getRuntimeTone(status)}>{formatRuntimeStatus(status, t)}</Badge>
}

export function TestStatusCard({
  title,
  description,
  status,
  presentation,
  t,
}: {
  title: string
  description: string
  status: ConnectionTestStatus
  presentation: ConnectionPresentation
  t: TranslateFn
}) {
  const detailsEntries = Object.entries(status.details ?? {})
    .filter(([, value]) => value !== null && value !== undefined && value !== '')
    .slice(0, 6)

  return (
    <Card className="overflow-hidden border-border/80 bg-gradient-to-br from-white via-white to-slate-50/80 shadow-sm dark:from-slate-950 dark:via-slate-950 dark:to-slate-900/80">
      <CardContent className="p-6 space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold text-foreground">{title}</h3>
            <p className="mt-1 text-sm text-muted-foreground">{description}</p>
          </div>
          <ConnectionBadge presentation={presentation} />
        </div>

        <div className="flex flex-wrap items-center gap-2">
          {status.http_status ? <Badge className={mutedBadgeClassName}>HTTP {status.http_status}</Badge> : null}
          {status.tested_at ? (
            <Badge className={mutedBadgeClassName}>
              <Clock3 className="size-3" />
              {formatRelativeTime(status.tested_at, { variant: 'compact' })}
            </Badge>
          ) : (
            <Badge className={mutedBadgeClassName}>{t('cpaSync.awaitingTest')}</Badge>
          )}
        </div>

        <div className="rounded-3xl border border-border bg-white/70 p-4 dark:bg-white/5">
          <div className="text-sm font-semibold text-foreground break-words">{status.message || t('cpaSync.awaitingTest')}</div>
          {detailsEntries.length > 0 ? (
            <div className="mt-4 grid gap-2 sm:grid-cols-2">
              {detailsEntries.map(([key, value]) => (
                <div key={key} className="rounded-2xl border border-border/70 bg-background/80 px-3 py-2">
                  <div className="text-[11px] font-semibold tracking-[0.08em] text-muted-foreground">{formatTestDetailLabel(key, t)}</div>
                  <div className="mt-1 text-sm font-medium text-foreground break-all">{String(value)}</div>
                </div>
              ))}
            </div>
          ) : null}
        </div>
      </CardContent>
    </Card>
  )
}

export function FieldBadge({ label, missing }: { label: string; missing: boolean }) {
  return (
    <Badge className={missing ? warningBadgeClassName : successBadgeClassName}>
      <span className={`size-1.5 rounded-full ${missing ? 'bg-amber-500' : 'bg-emerald-500'}`} />
      {label}
    </Badge>
  )
}

export function ActionLogRow({
  action,
  t,
}: {
  action: CPASyncAction
  t: TranslateFn
}) {
  const Icon = getActionIcon(action.kind)
  const accent = getActionAccent(action.kind, action.status)

  return (
    <div className="grid gap-3 px-4 py-4 lg:grid-cols-[minmax(0,1fr)_240px] lg:items-center lg:px-5">
      <div className="flex min-w-0 items-start gap-3">
        <div className={`flex size-10 shrink-0 items-center justify-center rounded-2xl border ${accent.iconWrap}`}>
          <Icon className="size-4" />
        </div>
        <div className="min-w-0 space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <Badge className={getActionKindClassName(action.kind)}>{formatActionKind(action.kind, t)}</Badge>
            <Badge className={getActionStatusClassName(action.status)}>{formatActionStatus(action.status, t)}</Badge>
          </div>
          <div className="text-sm font-semibold text-foreground break-words">{action.message}</div>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-2 lg:justify-end">
        <Badge className={mutedBadgeClassName}>
          <Clock3 className="size-3" />
          {action.timestamp ? formatRelativeTime(action.timestamp, { variant: 'compact' }) : '--'}
        </Badge>
        {action.target ? (
          <span className="inline-flex max-w-full items-center rounded-full border border-border/80 bg-background/80 px-3 py-1 text-[12px] text-muted-foreground break-all">
            {action.target}
          </span>
        ) : (
          <span className="inline-flex items-center rounded-full border border-dashed border-border/80 px-3 py-1 text-[12px] text-muted-foreground">
            {t('cpaSync.noActionTarget')}
          </span>
        )}
      </div>
    </div>
  )
}

function getActionIcon(kind: string) {
  switch (kind) {
    case 'upload':
      return Upload
    case 'delete':
      return Trash2
    case 'download':
      return Download
    case 'reconcile':
      return Search
    case 'switch':
      return Repeat
    case 'run':
    default:
      return PlayCircle
  }
}

function getActionAccent(kind: string, status: string) {
  const base = status === 'error'
    ? {
        card: 'border-red-200/80 dark:border-red-500/20',
        iconWrap: 'border-red-200 bg-red-50 text-red-600 dark:border-red-500/20 dark:bg-red-500/10 dark:text-red-300',
      }
    : status === 'warning'
      ? {
          card: 'border-amber-200/80 dark:border-amber-500/20',
          iconWrap: 'border-amber-200 bg-amber-50 text-amber-600 dark:border-amber-500/20 dark:bg-amber-500/10 dark:text-amber-300',
        }
      : {
          card: 'border-border/80 dark:border-white/10',
          iconWrap: 'border-border bg-white text-slate-700 dark:border-white/10 dark:bg-slate-900 dark:text-slate-200',
        }

  if (kind === 'switch') {
    return {
      card: status === 'error' ? base.card : 'border-blue-200/80 dark:border-blue-500/20',
      iconWrap: status === 'error' ? base.iconWrap : 'border-blue-200 bg-blue-50 text-blue-600 dark:border-blue-500/20 dark:bg-blue-500/10 dark:text-blue-300',
    }
  }
  if (kind === 'upload') {
    return {
      card: status === 'error' ? base.card : 'border-emerald-200/80 dark:border-emerald-500/20',
      iconWrap: status === 'error' ? base.iconWrap : 'border-emerald-200 bg-emerald-50 text-emerald-600 dark:border-emerald-500/20 dark:bg-emerald-500/10 dark:text-emerald-300',
    }
  }
  return base
}
