import {
  AlertCircle,
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  Clock3,
  Database,
  Loader2,
  RefreshCw,
  Repeat,
  Save,
  Settings2,
  ShieldCheck,
  Wrench,
  Wifi,
} from 'lucide-react'
import {
  ActionLogRow,
  ActionGroup,
  ConnectionBadge,
  ConfigSection,
  ConsoleTabButton,
  Field,
  FieldBadge,
  LogMetaCard,
  MiniConfigSummaryCard,
  OverviewCard,
  RuntimeStatusBadge,
  StatusTile,
  TestStatusCard,
} from './CPASyncPrimitives'
import {
  type ConnectionPresentation,
  type CPASyncView,
  type TranslateFn,
  formatMissingFieldLabel,
  formatRuntimeStatus,
  getRuntimeTone,
  infoBadgeClassName,
  joinLocalizedItems,
  mutedBadgeClassName,
  successBadgeClassName,
  summarizeText,
  warningBadgeClassName,
} from './CPASyncUtils'
import { formatRelativeTime } from '@/utils/time'
import type { ConnectionTestStatus, CPASyncAction, CPASyncStatusResponse, SystemSettings } from '@/types'
import type { SelectOption } from '@/components/ui/select'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'

export type CPASyncStatusLike = {
  state: Partial<CPASyncStatusResponse['state']>
} | null

export type CPASyncConfigSummary = {
  label: string
  className: string
}

type AsyncActionHandler = () => Promise<void>

export type CPASyncOverviewCardsSectionProps = {
  t: TranslateFn
  form: SystemSettings
  runtimeStatusLabel: string
  runtimeStatusDescription: string
  runtimeBusy: boolean
  cpaPresentation: ConnectionPresentation
  cpaDisplayStatus: ConnectionTestStatus
  mihomoServicePresentation: ConnectionPresentation
  mihomoServiceDisplayStatus: ConnectionTestStatus
  nextRunCountdown: string
  nextRunDetail: string
  syncIntervalSeconds: number
  status: CPASyncStatusLike
  displayedCurrentMihomoNode: string
}

export function CPASyncOverviewCardsSection({
  t,
  form,
  runtimeStatusLabel,
  runtimeStatusDescription,
  runtimeBusy,
  cpaPresentation,
  cpaDisplayStatus,
  mihomoServicePresentation,
  mihomoServiceDisplayStatus,
  nextRunCountdown,
  nextRunDetail,
  syncIntervalSeconds,
  status,
  displayedCurrentMihomoNode,
}: CPASyncOverviewCardsSectionProps) {
  return (
    <section className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4">
      <OverviewCard
        icon={<ShieldCheck className="size-5" />}
        label={t('cpaSync.autoSyncStatus')}
        value={form.cpa_sync_enabled ? t('common.enabled') : t('common.disabled')}
        sub={form.cpa_sync_enabled ? t('cpaSync.autoSyncOnDesc') : t('cpaSync.autoSyncOffDesc')}
        badge={(
          <Badge className={form.cpa_sync_enabled ? successBadgeClassName : mutedBadgeClassName}>
            <span className={`size-1.5 rounded-full ${form.cpa_sync_enabled ? 'bg-emerald-500' : 'bg-gray-400'}`} />
            {form.cpa_sync_enabled ? t('common.enabled') : t('common.disabled')}
          </Badge>
        )}
      />
      <OverviewCard
        icon={<Wrench className="size-5" />}
        label={t('cpaSync.workerStatus')}
        value={runtimeStatusLabel}
        sub={runtimeStatusDescription}
        badge={(
          <Badge className={runtimeBusy ? infoBadgeClassName : mutedBadgeClassName}>
            <span className={`size-1.5 rounded-full ${runtimeBusy ? 'bg-blue-500' : 'bg-gray-400'}`} />
            {runtimeBusy ? t('common.running') : t('cpaSync.idle')}
          </Badge>
        )}
      />
      <OverviewCard
        icon={<Wifi className="size-5" />}
        label={t('cpaSync.cpaConnection')}
        value={cpaPresentation.label}
        sub={summarizeText(cpaDisplayStatus.message, t('cpaSync.awaitingTest'))}
        badge={<ConnectionBadge presentation={cpaPresentation} />}
      />
      <OverviewCard
        icon={<Wifi className="size-5" />}
        label={t('cpaSync.mihomoConnection')}
        value={mihomoServicePresentation.label}
        sub={summarizeText(mihomoServiceDisplayStatus.message, t('cpaSync.awaitingTest'))}
        badge={<ConnectionBadge presentation={mihomoServicePresentation} />}
      />
      <OverviewCard
        icon={<Clock3 className="size-5" />}
        label={t('cpaSync.nextRunAt')}
        value={nextRunCountdown}
        sub={nextRunDetail}
        badge={(
          <Badge className={runtimeBusy ? infoBadgeClassName : (form.cpa_sync_enabled ? mutedBadgeClassName : warningBadgeClassName)}>
            <span className={`size-1.5 rounded-full ${runtimeBusy ? 'bg-blue-500' : (form.cpa_sync_enabled ? 'bg-gray-400' : 'bg-amber-500')}`} />
            {t('cpaSync.syncEverySeconds', { count: syncIntervalSeconds })}
          </Badge>
        )}
      />
      <OverviewCard
        icon={<Clock3 className="size-5" />}
        label={t('cpaSync.lastRunAt')}
        value={status?.state.last_run_at ? formatRelativeTime(status.state.last_run_at, { variant: 'compact' }) : '--'}
        sub={summarizeText(status?.state.last_run_summary, t('cpaSync.awaitingRun'))}
        badge={status?.state.last_run_status ? <RuntimeStatusBadge status={status.state.last_run_status} t={t} /> : undefined}
      />
      <OverviewCard
        icon={<Database className="size-5" />}
        label={t('cpaSync.currentNode')}
        value={displayedCurrentMihomoNode}
        sub={t('cpaSync.currentNodeDesc')}
        badge={(
          <Badge className={mutedBadgeClassName}>
            <ShieldCheck className="size-3" />
            {t('cpaSync.strategyNode')}
          </Badge>
        )}
      />
    </section>
  )
}

export type CPASyncTabSwitcherSectionProps = {
  t: TranslateFn
  activeView: CPASyncView
  recentActionsCount: number
  lastRunStatus: string
  setActiveView: (view: CPASyncView) => void
}

export function CPASyncTabSwitcherSection({
  t,
  activeView,
  recentActionsCount,
  lastRunStatus,
  setActiveView,
}: CPASyncTabSwitcherSectionProps) {
  return (
    <section className="rounded-[32px] border border-border/80 bg-gradient-to-r from-white via-slate-50/90 to-white p-2 shadow-sm dark:from-slate-950 dark:via-slate-950 dark:to-slate-900/80">
      <div className="grid gap-2 lg:grid-cols-2">
        <ConsoleTabButton
          active={activeView === 'overview'}
          icon={<ShieldCheck className="size-4.5" />}
          title={t('cpaSync.overviewTab')}
          description={t('cpaSync.overviewTabDesc')}
          meta={<RuntimeStatusBadge status={lastRunStatus} t={t} />}
          onClick={() => setActiveView('overview')}
        />
        <ConsoleTabButton
          active={activeView === 'logs'}
          icon={<Clock3 className="size-4.5" />}
          title={t('cpaSync.logsTab')}
          description={t('cpaSync.logsTabDesc')}
          meta={(
            <Badge className={mutedBadgeClassName}>
              {recentActionsCount}
              {t('cpaSync.actionCountSuffix')}
            </Badge>
          )}
          onClick={() => setActiveView('logs')}
        />
      </div>
    </section>
  )
}

export type CPASyncOverviewMainSectionProps = {
  t: TranslateFn
  active: boolean
  status: CPASyncStatusLike
  displayedCPAAccountCount: number
  displayedCurrentMihomoNode: string
  nextRunCountdown: string
  runtimeBusy: boolean
  dirty: boolean
  runDisabledReason: string | null
  switchDisabledReason: string | null
  testCPADisabledReason: string | null
  testMihomoDisabledReason: string | null
  configSummary: CPASyncConfigSummary
  formCPAMissing: string[]
  formMihomoMissing: string[]
  cpaDisplayStatus: ConnectionTestStatus
  cpaPresentation: ConnectionPresentation
  mihomoDisplayStatus: ConnectionTestStatus
  mihomoPresentation: ConnectionPresentation
  running: boolean
  switching: boolean
  testingCPA: boolean
  testingMihomo: boolean
  handleRun: AsyncActionHandler
  handleSwitch: AsyncActionHandler
  handleTestCPA: AsyncActionHandler
  handleTestMihomo: AsyncActionHandler
}

export function CPASyncOverviewMainSection({
  t,
  active,
  status,
  displayedCPAAccountCount,
  displayedCurrentMihomoNode,
  nextRunCountdown,
  runtimeBusy,
  dirty,
  runDisabledReason,
  switchDisabledReason,
  testCPADisabledReason,
  testMihomoDisabledReason,
  configSummary,
  formCPAMissing,
  formMihomoMissing,
  cpaDisplayStatus,
  cpaPresentation,
  mihomoDisplayStatus,
  mihomoPresentation,
  running,
  switching,
  testingCPA,
  testingMihomo,
  handleRun,
  handleSwitch,
  handleTestCPA,
  handleTestMihomo,
}: CPASyncOverviewMainSectionProps) {
  return (
    <div className="space-y-6" hidden={!active} aria-hidden={!active}>
      <section className="grid gap-4 xl:grid-cols-[minmax(0,1.15fr)_minmax(340px,0.85fr)]">
        <Card className="overflow-hidden border-border/80 bg-gradient-to-br from-white via-white to-slate-50/90 shadow-sm dark:from-slate-950 dark:via-slate-950 dark:to-slate-900/80">
          <CardContent className="p-6 space-y-5">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <h3 className="text-lg font-semibold text-foreground">{t('cpaSync.statusTitle')}</h3>
                <p className="mt-1 text-sm text-muted-foreground">{t('cpaSync.runtimeDesc')}</p>
              </div>
              <RuntimeStatusBadge status={status?.state.last_run_status ?? 'unknown'} t={t} />
            </div>

            <div className="grid grid-cols-[repeat(auto-fit,minmax(160px,1fr))] gap-3">
              <StatusTile label={t('cpaSync.cpaCount')} value={String(displayedCPAAccountCount)} tone="neutral" />
              <StatusTile label={t('cpaSync.hourlyUploads')} value={String(status?.state.hourly_upload_count ?? 0)} tone="success" />
              <StatusTile label={t('cpaSync.lastSwitchAt')} value={status?.state.last_switch_at ? formatRelativeTime(status.state.last_switch_at, { variant: 'compact' }) : '--'} tone="info" />
              <StatusTile label={t('cpaSync.currentNode')} value={displayedCurrentMihomoNode} tone="warning" />
              <StatusTile label={t('cpaSync.nextRunAt')} value={nextRunCountdown} tone={runtimeBusy ? 'info' : 'neutral'} />
            </div>

            <div className="rounded-3xl border border-border bg-white/70 p-5 shadow-[inset_0_1px_0_rgba(255,255,255,0.75)] dark:bg-white/5 dark:shadow-none">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="text-sm font-semibold text-foreground">{t('cpaSync.lastRunResult')}</div>
                <div className="text-xs text-muted-foreground">
                  {status?.state.last_run_at ? formatRelativeTime(status.state.last_run_at, { variant: 'compact' }) : '--'}
                </div>
              </div>
              <div className="mt-3 text-[15px] font-semibold text-foreground break-words">
                {status?.state.last_run_summary || t('cpaSync.awaitingRun')}
              </div>
              <div className="mt-2 text-sm text-muted-foreground break-words">
                {formatRuntimeStatus(status?.state.last_run_status ?? 'unknown', t)}
              </div>
            </div>

            <div className="rounded-3xl border border-border bg-white/70 p-5 dark:bg-white/5">
              <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                <AlertTriangle className="size-4 text-amber-500" />
                {t('cpaSync.lastErrorTitle')}
              </div>
              <div className={`mt-3 text-sm break-words ${status?.state.last_error_summary ? 'text-foreground' : 'text-muted-foreground'}`}>
                {status?.state.last_error_summary || t('cpaSync.noErrorSummary')}
              </div>
            </div>
          </CardContent>
        </Card>

        <Card className="overflow-hidden border-border/80 bg-gradient-to-br from-slate-50 via-white to-white shadow-sm dark:from-slate-950 dark:via-slate-950 dark:to-slate-900/80">
          <CardContent className="p-6 space-y-5">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <h3 className="text-lg font-semibold text-foreground">{t('cpaSync.controlTitle')}</h3>
                <p className="mt-1 text-sm text-muted-foreground">{t('cpaSync.controlDesc')}</p>
              </div>
              <Badge className={dirty ? warningBadgeClassName : mutedBadgeClassName}>
                {dirty ? <AlertCircle className="size-3" /> : <CheckCircle2 className="size-3" />}
                {dirty ? t('cpaSync.unsavedChanges') : t('cpaSync.savedState')}
              </Badge>
            </div>

            <ActionGroup
              title={t('cpaSync.taskActionsTitle')}
              description={t('cpaSync.taskActionsDesc')}
              footer={runDisabledReason || switchDisabledReason || t('cpaSync.taskActionsReady')}
            >
              <Button className="w-full" onClick={() => void handleRun()} disabled={Boolean(runDisabledReason) || running}>
                {running ? <Loader2 className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />}
                {t('cpaSync.runNow')}
              </Button>
              <Button variant="outline" className="w-full" onClick={() => void handleSwitch()} disabled={Boolean(switchDisabledReason) || switching}>
                {switching ? <Loader2 className="size-3.5 animate-spin" /> : <Repeat className="size-3.5" />}
                {t('cpaSync.switchNow')}
              </Button>
            </ActionGroup>

            <ActionGroup
              title={t('cpaSync.connectionActionsTitle')}
              description={t('cpaSync.connectionActionsDesc')}
              footer={testCPADisabledReason || testMihomoDisabledReason || (dirty ? t('cpaSync.testUsesCurrentForm') : t('cpaSync.connectionActionsReady'))}
            >
              <Button variant="outline" className="w-full" onClick={() => void handleTestCPA()} disabled={Boolean(testCPADisabledReason) || testingCPA}>
                {testingCPA ? <Loader2 className="size-3.5 animate-spin" /> : <Wifi className="size-3.5" />}
                {t('cpaSync.testCPA')}
              </Button>
              <Button variant="outline" className="w-full" onClick={() => void handleTestMihomo()} disabled={Boolean(testMihomoDisabledReason) || testingMihomo}>
                {testingMihomo ? <Loader2 className="size-3.5 animate-spin" /> : <Wifi className="size-3.5" />}
                {t('cpaSync.testMihomo')}
              </Button>
            </ActionGroup>

            <div className="rounded-3xl border border-border bg-white/70 p-5 dark:bg-white/5">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <div className="text-sm font-semibold text-foreground">{t('cpaSync.configHealthTitle')}</div>
                  <div className="mt-1 text-xs text-muted-foreground">{t('cpaSync.configHealthDesc')}</div>
                </div>
                <Badge className={configSummary.className}>{configSummary.label}</Badge>
              </div>
              <div className="mt-4 flex flex-wrap gap-2">
                <FieldBadge label={t('cpaSync.cpaSection')} missing={formCPAMissing.length > 0} />
                <FieldBadge label={t('cpaSync.mihomoSection')} missing={formMihomoMissing.length > 0} />
                <Badge className={dirty ? warningBadgeClassName : mutedBadgeClassName}>
                  {dirty ? t('cpaSync.unsavedChanges') : t('cpaSync.savedState')}
                </Badge>
              </div>
              {(formCPAMissing.length > 0 || formMihomoMissing.length > 0) ? (
                <div className="mt-3 text-xs text-amber-700 dark:text-amber-300">
                  {t('cpaSync.missingConfig')}: {' '}
                  {joinLocalizedItems([...formCPAMissing, ...formMihomoMissing].map((field) => formatMissingFieldLabel(field, t)))}
                </div>
              ) : null}
            </div>
          </CardContent>
        </Card>
      </section>

      <section className="grid gap-4 xl:grid-cols-2">
        <TestStatusCard title={t('cpaSync.cpaConnectionTest')} description={t('cpaSync.cpaTestDesc')} status={cpaDisplayStatus} presentation={cpaPresentation} t={t} />
        <TestStatusCard title={t('cpaSync.mihomoConnectionTest')} description={t('cpaSync.mihomoTestDesc')} status={mihomoDisplayStatus} presentation={mihomoPresentation} t={t} />
      </section>
    </div>
  )
}

export type CPASyncConfigPanelSectionProps = {
  t: TranslateFn
  form: SystemSettings
  configOpen: boolean
  configSummary: CPASyncConfigSummary
  dirty: boolean
  formCPAMissing: string[]
  formMihomoMissing: string[]
  hasMihomoGroupOptions: boolean
  resolvedMihomoGroupOptions: SelectOption[]
  loadingMihomoGroups: boolean
  mihomoFetchable: boolean
  mihomoGroupsError: string
  saving: boolean
  isBusy: boolean
  setConfigOpen: (value: boolean | ((prev: boolean) => boolean)) => void
  updateForm: (patch: Partial<SystemSettings>) => void
  handleSave: AsyncActionHandler
  fetchMihomoGroups: (sourceForm: Partial<SystemSettings> | null | undefined, options?: { showErrorToast?: boolean; silent?: boolean }) => Promise<void>
}

export function CPASyncConfigPanelSection({
  t,
  form,
  configOpen,
  configSummary,
  dirty,
  formCPAMissing,
  formMihomoMissing,
  hasMihomoGroupOptions,
  resolvedMihomoGroupOptions,
  loadingMihomoGroups,
  mihomoFetchable,
  mihomoGroupsError,
  saving,
  isBusy,
  setConfigOpen,
  updateForm,
  handleSave,
  fetchMihomoGroups,
}: CPASyncConfigPanelSectionProps) {
  return (
    <section>
      <Card className="overflow-hidden">
        <button
          type="button"
          onClick={() => setConfigOpen((prev) => !prev)}
          className="flex w-full flex-wrap items-center justify-between gap-4 border-b border-border bg-gradient-to-r from-slate-50 via-white to-white px-5 py-4 text-left transition-colors hover:bg-slate-50/90 dark:from-slate-950 dark:via-slate-950 dark:to-slate-900/80"
          aria-expanded={configOpen}
        >
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <div className="flex size-10 items-center justify-center rounded-2xl bg-primary/10 text-primary">
                <Settings2 className="size-5" />
              </div>
              <div>
                <div className="text-base font-semibold text-foreground">{t('cpaSync.lowFrequencyConfig')}</div>
                <div className="mt-1 text-sm text-muted-foreground">{t('cpaSync.lowFrequencyConfigDesc')}</div>
              </div>
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <Badge className={configSummary.className}>{configSummary.label}</Badge>
            {dirty ? <Badge className={warningBadgeClassName}>{t('cpaSync.unsavedChanges')}</Badge> : null}
            <Badge className={mutedBadgeClassName}>{configOpen ? t('cpaSync.collapseConfig') : t('cpaSync.expandConfig')}</Badge>
            {configOpen ? <ChevronUp className="size-4 text-muted-foreground" /> : <ChevronDown className="size-4 text-muted-foreground" />}
          </div>
        </button>

        {configOpen ? (
          <CardContent className="space-y-6 bg-[radial-gradient(circle_at_top_left,rgba(59,130,246,0.06),transparent_32%),radial-gradient(circle_at_top_right,rgba(16,185,129,0.05),transparent_26%)] p-6">
            <div className="grid gap-3 lg:grid-cols-[1.2fr_1fr_1fr]">
              <div className="rounded-[28px] border border-border/80 bg-white/85 p-5 shadow-sm dark:bg-white/5">
                <div className="flex items-start gap-3">
                  <div className="flex size-11 shrink-0 items-center justify-center rounded-2xl bg-primary/10 text-primary">
                    <Settings2 className="size-5" />
                  </div>
                  <div>
                    <div className="text-sm font-semibold text-foreground">{t('cpaSync.lowFrequencyConfig')}</div>
                    <div className="mt-1 text-sm leading-relaxed text-muted-foreground">
                      {dirty ? t('cpaSync.testUsesCurrentForm') : t('cpaSync.lowFrequencyConfigHint')}
                    </div>
                  </div>
                </div>
              </div>

              <MiniConfigSummaryCard
                title={t('cpaSync.cpaSection')}
                value={formCPAMissing.length > 0 ? t('cpaSync.configMissingCPA') : t('cpaSync.configReady')}
                note={form.cpa_base_url?.trim() ? summarizeText(form.cpa_base_url, '--') : t('cpaSync.configEmpty')}
                tone={formCPAMissing.length > 0 ? 'warning' : 'success'}
              />
              <MiniConfigSummaryCard
                title={t('cpaSync.mihomoSection')}
                value={formMihomoMissing.length > 0 ? t('cpaSync.configMissingMihomo') : t('cpaSync.configReady')}
                note={form.mihomo_base_url?.trim() ? summarizeText(form.mihomo_base_url, '--') : t('cpaSync.configEmpty')}
                tone={formMihomoMissing.length > 0 ? 'warning' : 'info'}
              />
            </div>

            <div className="grid gap-6 xl:grid-cols-2">
              <ConfigSection title={t('cpaSync.cpaSection')} description={t('cpaSync.cpaSectionDesc')}>
                <Field label={t('cpaSync.cpaBaseUrl')}>
                  <Input value={form.cpa_base_url} onChange={(event) => updateForm({ cpa_base_url: event.target.value })} />
                </Field>
                <Field label={t('cpaSync.cpaAdminKey')}>
                  <Input type="password" value={form.cpa_admin_key} onChange={(event) => updateForm({ cpa_admin_key: event.target.value })} />
                </Field>
                <Field label={t('cpaSync.minAccounts')}>
                  <Input type="number" min={0} value={form.cpa_min_accounts} onChange={(event) => updateForm({ cpa_min_accounts: Number(event.target.value) || 0 })} />
                </Field>
                <Field label={t('cpaSync.maxUploadsPerHour')}>
                  <Input type="number" min={0} value={form.cpa_max_uploads_per_hour} onChange={(event) => updateForm({ cpa_max_uploads_per_hour: Number(event.target.value) || 0 })} />
                </Field>
                <Field label={t('cpaSync.switchAfterUploads')}>
                  <Input type="number" min={0} value={form.cpa_switch_after_uploads} onChange={(event) => updateForm({ cpa_switch_after_uploads: Number(event.target.value) || 0 })} />
                </Field>
                <div className="rounded-3xl border border-border/80 bg-slate-50/90 px-4 py-4 text-sm leading-6 text-muted-foreground shadow-sm dark:bg-white/5 sm:col-span-2 xl:col-span-2">
                  {t('cpaSync.switchRuleHint', {
                    uploads: form.cpa_max_uploads_per_hour || 0,
                    minutes: form.cpa_switch_after_uploads || 0,
                  })}
                </div>
              </ConfigSection>

              <ConfigSection title={t('cpaSync.mihomoSection')} description={t('cpaSync.mihomoSectionDesc')}>
                <Field label={t('cpaSync.mihomoBaseUrl')}>
                  <Input value={form.mihomo_base_url} onChange={(event) => updateForm({ mihomo_base_url: event.target.value })} />
                </Field>
                <Field label={t('cpaSync.mihomoSecret')}>
                  <Input type="password" value={form.mihomo_secret} onChange={(event) => updateForm({ mihomo_secret: event.target.value })} />
                </Field>
                <div className="space-y-2">
                  <label className="block text-sm font-semibold text-slate-700 dark:text-slate-300">
                    {t('cpaSync.mihomoStrategyGroup')}
                  </label>

                  {hasMihomoGroupOptions ? (
                    <Select
                      value={form.mihomo_strategy_group}
                      onValueChange={(value) => updateForm({ mihomo_strategy_group: value })}
                      options={resolvedMihomoGroupOptions}
                      placeholder={loadingMihomoGroups ? t('cpaSync.loadingStrategyGroups') : t('cpaSync.selectStrategyGroup')}
                      disabled={loadingMihomoGroups}
                    />
                  ) : (
                    <Input
                      value={form.mihomo_strategy_group}
                      placeholder={mihomoFetchable ? t('cpaSync.strategyGroupFallback') : t('cpaSync.fillMihomoFirst')}
                      onChange={(event) => updateForm({ mihomo_strategy_group: event.target.value })}
                    />
                  )}

                  {mihomoGroupsError ? (
                    <div className="text-xs text-amber-700 dark:text-amber-300">
                      {t('cpaSync.loadMihomoGroupsFailed')}: {mihomoGroupsError}
                    </div>
                  ) : null}
                </div>
                <Field label={t('cpaSync.delayTestUrl')}>
                  <Input value={form.mihomo_delay_test_url} onChange={(event) => updateForm({ mihomo_delay_test_url: event.target.value })} />
                </Field>
                <Field label={t('cpaSync.delayTimeout')}>
                  <Input type="number" min={100} value={form.mihomo_delay_timeout_ms} onChange={(event) => updateForm({ mihomo_delay_timeout_ms: Number(event.target.value) || 100 })} />
                </Field>
                <div className="flex items-end">
                  <Button
                    type="button"
                    variant="outline"
                    onClick={() => void fetchMihomoGroups(form, { showErrorToast: true })}
                    disabled={!mihomoFetchable || loadingMihomoGroups}
                    className="h-11 w-full rounded-2xl border-primary/20 bg-gradient-to-r from-primary/6 via-white to-primary/10 text-primary shadow-sm transition-all hover:border-primary/30 hover:bg-primary/8 dark:from-primary/10 dark:via-slate-950 dark:to-primary/15"
                  >
                    {loadingMihomoGroups ? <Loader2 className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />}
                    {t('cpaSync.refreshStrategyGroups')}
                  </Button>
                </div>
              </ConfigSection>
            </div>

            <ConfigSection title={t('cpaSync.taskSection')} description={t('cpaSync.taskSectionDesc')}>
              <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_220px]">
                <div className="rounded-3xl border border-border bg-white/70 px-4 py-4 shadow-sm dark:bg-white/5">
                  <div className="flex flex-wrap items-center justify-between gap-4">
                    <div>
                      <div className="text-sm font-semibold text-foreground">{t('cpaSync.enabled')}</div>
                      <div className="mt-1 text-xs text-muted-foreground">{t('cpaSync.taskToggleDesc')}</div>
                    </div>
                    <button
                      type="button"
                      onClick={() => updateForm({ cpa_sync_enabled: !form.cpa_sync_enabled })}
                      className={`inline-flex h-11 items-center rounded-2xl px-4 text-sm font-semibold transition-colors ${form.cpa_sync_enabled ? 'bg-emerald-500 text-white' : 'bg-muted text-muted-foreground'}`}
                    >
                      {form.cpa_sync_enabled ? t('common.enabled') : t('common.disabled')}
                    </button>
                  </div>
                </div>

                <div className="rounded-3xl border border-border bg-white/70 px-4 py-4 shadow-sm dark:bg-white/5">
                  <Field label={t('cpaSync.syncIntervalSeconds')}>
                    <Input
                      type="number"
                      min={30}
                      max={86400}
                      value={form.cpa_sync_interval_seconds}
                      onChange={(event) => updateForm({ cpa_sync_interval_seconds: Number(event.target.value) || 30 })}
                    />
                  </Field>
                  <div className="mt-2 text-xs text-muted-foreground">{t('cpaSync.syncIntervalHint')}</div>
                </div>
              </div>
            </ConfigSection>

            <div className="flex flex-wrap items-center justify-between gap-3 border-t border-border pt-4">
              <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                <Badge className={configSummary.className}>{configSummary.label}</Badge>
                {dirty ? <Badge className={warningBadgeClassName}>{t('cpaSync.unsavedChanges')}</Badge> : <Badge className={mutedBadgeClassName}>{t('cpaSync.savedState')}</Badge>}
              </div>
              <Button onClick={() => void handleSave()} disabled={saving || isBusy}>
                {saving ? <Loader2 className="size-3.5 animate-spin" /> : <Save className="size-3.5" />}
                {saving ? t('common.saving') : t('common.save')}
              </Button>
            </div>
          </CardContent>
        ) : null}
      </Card>
    </section>
  )
}

export type CPASyncLogsSectionProps = {
  t: TranslateFn
  active: boolean
  recentActions: CPASyncAction[]
  status: CPASyncStatusLike
}

export function CPASyncLogsSection({
  t,
  active,
  recentActions,
  status,
}: CPASyncLogsSectionProps) {
  return (
    <section hidden={!active} aria-hidden={!active}>
      <Card className="overflow-hidden border-border/80 bg-gradient-to-br from-white via-white to-slate-50/90 shadow-sm dark:from-slate-950 dark:via-slate-950 dark:to-slate-900/80">
        <CardContent className="space-y-5 p-6">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 className="text-lg font-semibold text-foreground">{t('cpaSync.actionsTitle')}</h3>
              <p className="mt-1 text-sm text-muted-foreground">{t('cpaSync.actionsDesc')}</p>
            </div>
            <Badge className={mutedBadgeClassName}>
              {recentActions.length}
              {t('cpaSync.actionCountSuffix')}
            </Badge>
          </div>

          <div className="grid gap-3 xl:grid-cols-[220px_220px_minmax(0,1fr)]">
            <LogMetaCard
              label={t('cpaSync.lastRunStatus')}
              value={formatRuntimeStatus(status?.state.last_run_status ?? 'unknown', t)}
              badgeClassName={getRuntimeTone(status?.state.last_run_status ?? 'unknown')}
            />
            <LogMetaCard
              label={t('cpaSync.lastRunAt')}
              value={status?.state.last_run_at ? formatRelativeTime(status.state.last_run_at, { variant: 'compact' }) : '--'}
              badgeClassName={mutedBadgeClassName}
            />
            <div className="rounded-[28px] border border-border/80 bg-white/70 p-4 shadow-sm dark:bg-white/5">
              <div className="text-xs font-semibold tracking-[0.08em] text-muted-foreground">{t('cpaSync.lastErrorTitle')}</div>
              <div className={`mt-3 text-sm leading-6 break-words ${status?.state.last_error_summary ? 'text-foreground' : 'text-muted-foreground'}`}>
                {status?.state.last_error_summary || t('cpaSync.noErrorSummary')}
              </div>
            </div>
          </div>

          <div className="overflow-hidden rounded-[28px] border border-border/80 bg-white/80 shadow-sm dark:bg-white/5">
            {recentActions.length === 0 ? (
              <div className="px-4 py-10 text-center text-sm text-muted-foreground">{t('cpaSync.noActions')}</div>
            ) : (
              <div className="divide-y divide-border/70">
                {recentActions.map((action, index) => (
                  <ActionLogRow key={`${action.timestamp}-${index}`} action={action} t={t} />
                ))}
              </div>
            )}
          </div>
        </CardContent>
      </Card>
    </section>
  )
}
