import { useTranslation } from 'react-i18next'
import { RefreshCw } from 'lucide-react'
import PageHeader from '../components/PageHeader'
import StateShell from '../components/StateShell'
import ToastNotice from '../components/ToastNotice'
import {
  CPASyncConfigPanelSection,
  CPASyncLogsSection,
  CPASyncOverviewCardsSection,
  CPASyncOverviewMainSection,
  CPASyncTabSwitcherSection,
  type CPASyncConfigPanelSectionProps,
  type CPASyncLogsSectionProps,
  type CPASyncOverviewCardsSectionProps,
  type CPASyncOverviewMainSectionProps,
} from '../components/cpa-sync/CPASyncSections'
import { useCPASyncPageState } from '../hooks/useCPASyncPageState'
import { useToast } from '../hooks/useToast'
import { Button } from '@/components/ui/button'

export default function CPASync() {
  const { t } = useTranslation()
  const { toast, showToast } = useToast()
  const {
    activeView,
    configOpen,
    cpaDisplayStatus,
    cpaPresentation,
    configSummary,
    dirty,
    displayedCPAAccountCount,
    displayedCurrentMihomoNode,
    error,
    fetchMihomoGroups,
    form,
    formCPAMissing,
    formMihomoMissing,
    handleRun,
    handleSave,
    handleSwitch,
    handleTestCPA,
    handleTestMihomo,
    hasMihomoGroupOptions,
    isBusy,
    loading,
    loadingMihomoGroups,
    mihomoDisplayStatus,
    mihomoFetchable,
    mihomoGroupsError,
    mihomoPresentation,
    mihomoServiceDisplayStatus,
    mihomoServicePresentation,
    nextRunCountdown,
    nextRunDetail,
    recentActions,
    reload,
    resolvedMihomoGroupOptions,
    runDisabledReason,
    running,
    runtimeBusy,
    runtimeStatusDescription,
    runtimeStatusLabel,
    saving,
    setActiveView,
    setConfigOpen,
    status,
    switchDisabledReason,
    switching,
    syncIntervalSeconds,
    testCPADisabledReason,
    testMihomoDisabledReason,
    testingCPA,
    testingMihomo,
    updateForm,
  } = useCPASyncPageState({ t, showToast })

  const overviewCardsProps = {
    t,
    form: form!,
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
  } satisfies CPASyncOverviewCardsSectionProps

  const overviewMainProps = {
    t,
    active: activeView === 'overview',
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
  } satisfies CPASyncOverviewMainSectionProps

  const configPanelProps = {
    t,
    form: form!,
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
  } satisfies CPASyncConfigPanelSectionProps

  const logsSectionProps = {
    t,
    active: activeView === 'logs',
    recentActions,
    status,
  } satisfies CPASyncLogsSectionProps

  return (
    <StateShell
      variant="page"
      loading={loading}
      error={error}
      onRetry={() => void reload()}
      loadingTitle={t('cpaSync.loadingTitle')}
      loadingDescription={t('cpaSync.loadingDesc')}
      errorTitle={t('cpaSync.errorTitle')}
    >
      <>
        <PageHeader
          title={t('cpaSync.title')}
          description={t('cpaSync.description')}
          actions={(
            <Button variant="outline" onClick={() => void reload()} className="max-sm:w-full">
              <RefreshCw className="size-3.5" />
              {t('common.refresh')}
            </Button>
          )}
        />

        {form ? (
          <div className="space-y-6">
            <CPASyncOverviewCardsSection {...overviewCardsProps} />

            <CPASyncTabSwitcherSection
              t={t}
              activeView={activeView}
              recentActionsCount={recentActions.length}
              lastRunStatus={status?.state.last_run_status ?? 'unknown'}
              setActiveView={setActiveView}
            />

            <CPASyncOverviewMainSection {...overviewMainProps} />

            <CPASyncConfigPanelSection {...configPanelProps} />

            <CPASyncLogsSection {...logsSectionProps} />
          </div>
        ) : null}

        <ToastNotice toast={toast} />
      </>
    </StateShell>
  )
}
