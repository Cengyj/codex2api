import { useCallback, useEffect, useState } from 'react'
import { api } from '../api'
import { useDataLoader } from '../hooks/useDataLoader'
import { getErrorMessage } from '../utils/error'
import type {
  ConnectionTestStatus,
  CPASyncStatusResponse,
  MihomoStrategyGroupOption,
  SystemSettings,
  ToastType,
} from '../types'
import {
  type CPASyncPageData,
  type CPASyncPageMemory,
  type CPASyncView,
  type MihomoServiceCacheSnapshot,
  type TestSignatureState,
  type TranslateFn,
  buildMissingFieldsHint,
  createEmptyPageData,
  createEmptyTestSignatures,
  createEmptyTestStatus,
  formatAbsoluteTime,
  formatNextRunCountdown,
  getCPAConfigMissing,
  getCPASettingsSignature,
  getConfigSummaryLabel,
  getConnectionPresentation,
  getDisabledReason,
  getMihomoConfigMissing,
  getMihomoServiceConfigMissing,
  getMihomoServiceSettingsSignature,
  getMihomoSettingsSignature,
  getNumberDetail,
  getStringDetail,
  hasMissingConfigMessage,
  haveFieldsChanged,
  localizeConnectionMessage,
  normalizeTestStatus,
  pickCPASyncSettings,
  readMihomoServiceCache,
  stringifyPickedSettings,
  writeMihomoServiceCache,
} from '../components/cpa-sync/CPASyncUtils'

const cpaSyncPageMemory: CPASyncPageMemory = {
  data: createEmptyPageData(),
  settingsForm: null,
  testSignatures: createEmptyTestSignatures(),
  activeView: 'overview',
  configOpen: false,
  mihomoGroups: [],
  mihomoServiceStatus: createEmptyTestStatus(),
  mihomoGroupsError: '',
  lastMihomoFetchKey: '',
}

type UseCPASyncPageStateOptions = {
  t: TranslateFn
  showToast: (message: string, type?: ToastType) => void
}

export function useCPASyncPageState({ t, showToast }: UseCPASyncPageStateOptions) {
  const hasPageSnapshot = Boolean(cpaSyncPageMemory.data.settings || cpaSyncPageMemory.data.status)
  const initialMihomoServiceCache = readMihomoServiceCache()
  const [settingsForm, setSettingsForm] = useState<SystemSettings | null>(() => cpaSyncPageMemory.settingsForm)
  const [testSignatures, setTestSignatures] = useState<TestSignatureState>(() => cpaSyncPageMemory.testSignatures)
  const [saving, setSaving] = useState(false)
  const [running, setRunning] = useState(false)
  const [switching, setSwitching] = useState(false)
  const [testingCPA, setTestingCPA] = useState(false)
  const [testingMihomo, setTestingMihomo] = useState(false)
  const [configOpen, setConfigOpen] = useState<boolean>(() => cpaSyncPageMemory.configOpen)
  const [activeView, setActiveView] = useState<CPASyncView>(() => cpaSyncPageMemory.activeView)
  const [nowMs, setNowMs] = useState(() => Date.now())
  const [mihomoGroups, setMihomoGroups] = useState<MihomoStrategyGroupOption[]>(() =>
    cpaSyncPageMemory.mihomoGroups.length > 0 ? cpaSyncPageMemory.mihomoGroups : (initialMihomoServiceCache?.groups ?? []),
  )
  const [mihomoServiceStatus, setMihomoServiceStatus] = useState<ConnectionTestStatus>(() => {
    if (cpaSyncPageMemory.mihomoServiceStatus.tested_at || cpaSyncPageMemory.mihomoServiceStatus.message) {
      return cpaSyncPageMemory.mihomoServiceStatus
    }
    return initialMihomoServiceCache?.status ?? createEmptyTestStatus()
  })
  const [loadingMihomoGroups, setLoadingMihomoGroups] = useState(false)
  const [mihomoGroupsError, setMihomoGroupsError] = useState(() =>
    cpaSyncPageMemory.mihomoGroupsError || initialMihomoServiceCache?.error || '',
  )
  const [lastMihomoFetchKey, setLastMihomoFetchKey] = useState(
    () => cpaSyncPageMemory.lastMihomoFetchKey || initialMihomoServiceCache?.signature || '',
  )

  const load = useCallback(async () => {
    const [settings, status] = await Promise.all([api.getSettings(), api.getCPASyncStatus()])
    return { settings, status }
  }, [])

  const { data, setData, loading, error, reload } = useDataLoader<CPASyncPageData>({
    initialData: cpaSyncPageMemory.data,
    initialLoadMode: hasPageSnapshot ? 'silent' : 'blocking',
    load,
  })

  const persistedSettings = data.settings
  const status = data.status
  const form = settingsForm ?? persistedSettings
  const cpaSettingsSignature = getCPASettingsSignature(form)
  const mihomoServiceSettingsSignature = getMihomoServiceSettingsSignature(form)
  const mihomoSettingsSignature = getMihomoSettingsSignature(form)
  const formCPAMissing = getCPAConfigMissing(form)
  const formMihomoServiceMissing = getMihomoServiceConfigMissing(form)
  const formMihomoMissing = getMihomoConfigMissing(form)
  const dirty = stringifyPickedSettings(form) !== stringifyPickedSettings(persistedSettings)
  const cpaConnectionDirty = haveFieldsChanged(form, persistedSettings, ['cpa_base_url', 'cpa_admin_key'])
  const mihomoServiceDirty = haveFieldsChanged(form, persistedSettings, ['mihomo_base_url', 'mihomo_secret'])
  const mihomoConnectionDirty = haveFieldsChanged(form, persistedSettings, ['mihomo_base_url', 'mihomo_secret', 'mihomo_strategy_group'])
  const cpaTestStatus = normalizeTestStatus(status?.cpa_test_status)
  const mihomoTestStatus = normalizeTestStatus(status?.mihomo_test_status)
  const localizedCPATestMessage = localizeConnectionMessage(cpaTestStatus.message, t)
  const localizedMihomoTestMessage = localizeConnectionMessage(mihomoTestStatus.message, t)
  const cpaConnectionNeedsRetest =
    (cpaConnectionDirty && testSignatures.cpa !== cpaSettingsSignature) ||
    (formCPAMissing.length === 0 && hasMissingConfigMessage(localizedCPATestMessage, t))
  const mihomoConnectionNeedsRetest =
    (mihomoConnectionDirty && testSignatures.mihomo !== mihomoSettingsSignature) ||
    (formMihomoMissing.length === 0 && hasMissingConfigMessage(localizedMihomoTestMessage, t))
  const mihomoServiceNeedsRetest = mihomoServiceDirty && testSignatures.mihomoService !== mihomoServiceSettingsSignature
  const cpaDisplayStatus = formCPAMissing.length > 0
    ? { ...cpaTestStatus, ok: null, message: buildMissingFieldsHint(formCPAMissing, t), http_status: undefined, tested_at: '', details: {} }
    : cpaConnectionNeedsRetest
      ? { ...cpaTestStatus, ok: null, message: t('cpaSync.pendingRetestHint'), http_status: undefined, tested_at: '', details: {} }
      : { ...cpaTestStatus, message: localizedCPATestMessage }
  const mihomoDisplayStatus = formMihomoMissing.length > 0
    ? { ...mihomoTestStatus, ok: null, message: buildMissingFieldsHint(formMihomoMissing, t), http_status: undefined, tested_at: '', details: {} }
    : mihomoConnectionNeedsRetest
      ? { ...mihomoTestStatus, ok: null, message: t('cpaSync.pendingRetestHint'), http_status: undefined, tested_at: '', details: {} }
      : { ...mihomoTestStatus, message: localizedMihomoTestMessage }
  const mihomoServiceDisplayStatus = formMihomoServiceMissing.length > 0
    ? { ...mihomoServiceStatus, ok: null, message: buildMissingFieldsHint(formMihomoServiceMissing, t), http_status: undefined, tested_at: '', details: {} }
    : mihomoServiceNeedsRetest
      ? { ...mihomoServiceStatus, ok: null, message: t('cpaSync.pendingRetestHint'), http_status: undefined, tested_at: '', details: {} }
      : { ...mihomoServiceStatus, message: localizeConnectionMessage(mihomoServiceStatus.message, t) }
  const cpaPresentation = getConnectionPresentation(cpaDisplayStatus, formCPAMissing, testingCPA, cpaConnectionNeedsRetest, t)
  const mihomoServicePresentation = getConnectionPresentation(mihomoServiceDisplayStatus, formMihomoServiceMissing, false, mihomoServiceNeedsRetest, t)
  const mihomoPresentation = getConnectionPresentation(mihomoDisplayStatus, formMihomoMissing, testingMihomo, mihomoConnectionNeedsRetest, t)
  const configSummary = getConfigSummaryLabel(formCPAMissing, formMihomoMissing, t)
  const runDisabledReason = getDisabledReason('run', { running: Boolean(status?.running), cpaMissing: formCPAMissing, mihomoMissing: formMihomoMissing }, t)
  const switchDisabledReason = getDisabledReason('switch', { running: Boolean(status?.running), cpaMissing: formCPAMissing, mihomoMissing: formMihomoMissing }, t)
  const testCPADisabledReason = getDisabledReason('test-cpa', { running: Boolean(status?.running), cpaMissing: formCPAMissing, mihomoMissing: formMihomoMissing }, t)
  const testMihomoDisabledReason = getDisabledReason('test-mihomo', { running: Boolean(status?.running), cpaMissing: formCPAMissing, mihomoMissing: formMihomoMissing }, t)
  const isBusy = Boolean(status?.running || running || switching || testingCPA || testingMihomo)
  const mihomoFetchable = Boolean(form?.mihomo_base_url?.trim() && form?.mihomo_secret?.trim())
  const mihomoGroupOptions = mihomoGroups.map((group) => ({
    value: group.name,
    label: `${group.name} / ${group.candidate_count}${t('cpaSync.groupCountSuffix')}`,
  }))
  const resolvedMihomoGroupOptions =
    form?.mihomo_strategy_group && !mihomoGroupOptions.some((option) => option.value === form.mihomo_strategy_group)
      ? [{ value: form.mihomo_strategy_group, label: `${form.mihomo_strategy_group} / ${t('cpaSync.currentValue')}` }, ...mihomoGroupOptions]
      : mihomoGroupOptions
  const hasMihomoGroupOptions = resolvedMihomoGroupOptions.length > 0
  const mihomoBaseValue = form?.mihomo_base_url?.trim() ?? ''
  const mihomoSecretValue = form?.mihomo_secret?.trim() ?? ''
  const recentActions = [...(status?.state.recent_actions ?? [])].reverse()
  const displayedCPAAccountCount = status?.state.last_cpa_account_count ?? getNumberDetail(status?.cpa_test_status, 'account_count') ?? 0
  const runtimeBusy = Boolean(status?.running || running || switching || testingCPA || testingMihomo)
  const runtimeStatusLabel = testingCPA || testingMihomo ? t('cpaSync.testing') : runtimeBusy ? t('cpaSync.runningTask') : t('cpaSync.idleTask')
  const runtimeStatusDescription = testingCPA || testingMihomo
    ? t('cpaSync.connectionActionsDesc')
    : runtimeBusy
      ? t('cpaSync.workerBusyDesc')
      : t('cpaSync.workerIdleDesc')
  const displayedCurrentMihomoNode = status?.state.current_mihomo_node?.trim() || getStringDetail(status?.mihomo_test_status, 'current_node') || '--'
  const shouldAutoProbeMihomoService = Boolean(mihomoBaseValue && mihomoSecretValue) && lastMihomoFetchKey !== mihomoServiceSettingsSignature
  const syncIntervalSeconds = status?.config.interval_seconds ?? form?.cpa_sync_interval_seconds ?? 300
  const nextRunCountdown = formatNextRunCountdown(status?.next_run_at, nowMs, Boolean(form?.cpa_sync_enabled), runtimeBusy, t)
  const nextRunDetail = !form?.cpa_sync_enabled
    ? t('cpaSync.nextRunDisabledDesc')
    : runtimeBusy
      ? t('cpaSync.nextRunRunningDesc')
      : status?.next_run_at
        ? `${formatAbsoluteTime(status.next_run_at)} / ${t('cpaSync.syncEverySeconds', { count: syncIntervalSeconds })}`
        : t('cpaSync.nextRunPendingDesc')

  const patchSettings = useCallback((nextSettings: SystemSettings) => {
    setData((prev) => ({
      ...prev,
      settings: nextSettings,
    }))
  }, [setData])

  const patchStatus = useCallback((updater: (current: CPASyncStatusResponse | null) => CPASyncStatusResponse | null) => {
    setData((prev) => ({
      ...prev,
      status: updater(prev.status),
    }))
  }, [setData])

  const refreshStatus = useCallback(async () => {
    try {
      const nextStatus = await api.getCPASyncStatus()
      patchStatus(() => nextStatus)
    } catch {
      // keep current status and retry in the next polling cycle
    }
  }, [patchStatus])

  useEffect(() => {
    if (!mihomoBaseValue || !mihomoSecretValue) return
    const cached = readMihomoServiceCache()
    if (!cached || cached.signature === mihomoServiceSettingsSignature) return
    setMihomoGroups([])
    setMihomoGroupsError('')
    setMihomoServiceStatus(createEmptyTestStatus())
    setLastMihomoFetchKey('')
  }, [mihomoBaseValue, mihomoSecretValue, mihomoServiceSettingsSignature])

  useEffect(() => {
    if (!mihomoBaseValue || !mihomoSecretValue) {
      writeMihomoServiceCache(null)
      return
    }
    const snapshot: MihomoServiceCacheSnapshot = {
      signature: mihomoServiceSettingsSignature,
      status: mihomoServiceStatus,
      groups: mihomoGroups,
      error: mihomoGroupsError,
    }
    writeMihomoServiceCache(snapshot)
  }, [mihomoBaseValue, mihomoGroups, mihomoGroupsError, mihomoSecretValue, mihomoServiceSettingsSignature, mihomoServiceStatus])

  useEffect(() => {
    cpaSyncPageMemory.data = data
    cpaSyncPageMemory.settingsForm = settingsForm
    cpaSyncPageMemory.testSignatures = testSignatures
    cpaSyncPageMemory.activeView = activeView
    cpaSyncPageMemory.configOpen = configOpen
    cpaSyncPageMemory.mihomoGroups = mihomoGroups
    cpaSyncPageMemory.mihomoServiceStatus = mihomoServiceStatus
    cpaSyncPageMemory.mihomoGroupsError = mihomoGroupsError
    cpaSyncPageMemory.lastMihomoFetchKey = lastMihomoFetchKey
  }, [activeView, configOpen, data, lastMihomoFetchKey, mihomoGroups, mihomoGroupsError, mihomoServiceStatus, settingsForm, testSignatures])

  useEffect(() => {
    const timer = window.setInterval(() => {
      setNowMs(Date.now())
    }, 1000)
    return () => window.clearInterval(timer)
  }, [])

  useEffect(() => {
    const intervalMs = status?.running ? 2000 : 10000
    const timer = window.setInterval(() => {
      void refreshStatus()
    }, intervalMs)
    return () => window.clearInterval(timer)
  }, [refreshStatus, status?.running])

  const updateForm = (patch: Partial<SystemSettings>) => {
    setSettingsForm((prev) => {
      const base = prev ?? persistedSettings
      if (!base) return prev
      return { ...base, ...patch }
    })
  }

  const fetchMihomoGroups = useCallback(async (
    sourceForm: Partial<SystemSettings> | null | undefined,
    options?: { showErrorToast?: boolean; silent?: boolean },
  ) => {
    const showErrorToastOption = options?.showErrorToast ?? false
    const silent = options?.silent ?? false
    if (!sourceForm?.mihomo_base_url?.trim() || !sourceForm?.mihomo_secret?.trim()) {
      setMihomoGroups([])
      setMihomoGroupsError('')
      setMihomoServiceStatus(createEmptyTestStatus())
      setTestSignatures((prev) => ({ ...prev, mihomoService: '' }))
      setLastMihomoFetchKey('')
      return
    }

    const fetchKey = getMihomoServiceSettingsSignature(sourceForm)
    if (!silent) {
      setLoadingMihomoGroups(true)
    }
    setMihomoGroupsError('')
    try {
      const result = await api.listCPASyncMihomoGroups({
        mihomo_base_url: sourceForm.mihomo_base_url.trim(),
        mihomo_secret: sourceForm.mihomo_secret.trim(),
      })
      setMihomoGroups(result.groups ?? [])
      setMihomoServiceStatus({
        ok: true,
        message: (result.groups ?? []).length > 0
          ? t('cpaSync.mihomoServiceConnected', { count: (result.groups ?? []).length })
          : t('cpaSync.mihomoServiceConnectedNoGroups'),
        http_status: 200,
        tested_at: new Date().toISOString(),
        details: {
          group_count: (result.groups ?? []).length,
        },
      })
      setTestSignatures((prev) => ({ ...prev, mihomoService: fetchKey }))
      setLastMihomoFetchKey(fetchKey)
      if ((result.groups ?? []).length === 0) {
        setMihomoGroupsError(t('cpaSync.noStrategyGroups'))
      }
    } catch (err) {
      const message = getErrorMessage(err)
      setMihomoGroups([])
      setMihomoGroupsError(message)
      setMihomoServiceStatus({
        ok: false,
        message,
        http_status: undefined,
        tested_at: new Date().toISOString(),
        details: {},
      })
      setTestSignatures((prev) => ({ ...prev, mihomoService: fetchKey }))
      if (showErrorToastOption) {
        showToast(`${t('cpaSync.loadMihomoGroupsFailed')}: ${message}`, 'error')
      }
    } finally {
      if (!silent) {
        setLoadingMihomoGroups(false)
      }
    }
  }, [showToast, t])

  useEffect(() => {
    if (!mihomoBaseValue || !mihomoSecretValue) {
      setMihomoGroups([])
      setMihomoGroupsError('')
      setMihomoServiceStatus(createEmptyTestStatus())
      setTestSignatures((prev) => ({ ...prev, mihomoService: '' }))
      setLastMihomoFetchKey('')
      return
    }

    if (!shouldAutoProbeMihomoService) {
      return
    }

    const timer = window.setTimeout(() => {
      void fetchMihomoGroups({
        mihomo_base_url: mihomoBaseValue,
        mihomo_secret: mihomoSecretValue,
      }, { silent: true })
    }, 200)
    return () => window.clearTimeout(timer)
  }, [fetchMihomoGroups, mihomoBaseValue, mihomoSecretValue, shouldAutoProbeMihomoService])

  const handleSave = async () => {
    if (!form) return
    setSaving(true)
    try {
      const shouldClearCPATest = cpaConnectionDirty && testSignatures.cpa !== cpaSettingsSignature
      const shouldClearMihomoTest = mihomoConnectionDirty && testSignatures.mihomo !== mihomoSettingsSignature
      const updated = await api.updateSettings(pickCPASyncSettings(form))
      setSettingsForm(updated)
      patchSettings(updated)
      if (shouldClearCPATest || shouldClearMihomoTest) {
        setTestSignatures((prev) => ({
          ...prev,
          cpa: shouldClearCPATest ? cpaSettingsSignature : prev.cpa,
          mihomo: shouldClearMihomoTest ? mihomoSettingsSignature : prev.mihomo,
        }))
      }
      void refreshStatus()
      showToast(t('cpaSync.saveSuccess'))
    } catch (err) {
      showToast(`${t('cpaSync.saveFailed')}: ${getErrorMessage(err)}`, 'error')
    } finally {
      setSaving(false)
    }
  }

  const handleRun = async () => {
    setRunning(true)
    try {
      const nextStatus = await api.runCPASync()
      patchStatus(() => nextStatus)
      showToast(t('cpaSync.runSuccess'))
    } catch (err) {
      showToast(`${t('cpaSync.runFailed')}: ${getErrorMessage(err)}`, 'error')
    } finally {
      setRunning(false)
    }
  }

  const handleSwitch = async () => {
    setSwitching(true)
    try {
      const nextStatus = await api.switchCPASync()
      patchStatus(() => nextStatus)
      showToast(t('cpaSync.switchSuccess'))
    } catch (err) {
      showToast(`${t('cpaSync.switchFailed')}: ${getErrorMessage(err)}`, 'error')
    } finally {
      setSwitching(false)
    }
  }

  const handleTestCPA = async () => {
    if (!form) return
    setTestingCPA(true)
    try {
      const result = await api.testCPASyncCPA(pickCPASyncSettings(form))
      setTestSignatures((prev) => ({ ...prev, cpa: cpaSettingsSignature }))
      patchStatus((current) => current ? ({
        ...current,
        cpa_test_status: result,
        state: {
          ...current.state,
          last_cpa_account_count: getNumberDetail(result, 'account_count') ?? current.state.last_cpa_account_count,
        },
      }) : current)
      const message = localizeConnectionMessage(result.message, t)
      showToast(result.ok ? message || t('cpaSync.cpaTestSuccess') : message || t('cpaSync.cpaTestFailed'), result.ok ? 'success' : 'error')
      void refreshStatus()
    } catch (err) {
      showToast(`${t('cpaSync.cpaTestFailed')}: ${getErrorMessage(err)}`, 'error')
    } finally {
      setTestingCPA(false)
    }
  }

  const handleTestMihomo = async () => {
    if (!form) return
    setTestingMihomo(true)
    try {
      const result = await api.testCPASyncMihomo(pickCPASyncSettings(form))
      setTestSignatures((prev) => ({ ...prev, mihomo: mihomoSettingsSignature }))
      patchStatus((current) => {
        if (!current) return current
        const nextNode = getStringDetail(result, 'current_node')
        return {
          ...current,
          mihomo_test_status: result,
          state: {
            ...current.state,
            current_mihomo_node: nextNode || current.state.current_mihomo_node,
          },
        }
      })
      const message = localizeConnectionMessage(result.message, t)
      showToast(result.ok ? message || t('cpaSync.mihomoTestSuccess') : message || t('cpaSync.mihomoTestFailed'), result.ok ? 'success' : 'error')
      void refreshStatus()
    } catch (err) {
      showToast(`${t('cpaSync.mihomoTestFailed')}: ${getErrorMessage(err)}`, 'error')
    } finally {
      setTestingMihomo(false)
    }
  }

  return {
    activeView,
    configOpen,
    cpaDisplayStatus,
    cpaPresentation,
    configSummary,
    dirty,
    displayedCPAAccountCount,
    displayedCurrentMihomoNode,
    error,
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
    runtimeBusy,
    runtimeStatusDescription,
    runtimeStatusLabel,
    setActiveView,
    setConfigOpen,
    status,
    switchDisabledReason,
    syncIntervalSeconds,
    testCPADisabledReason,
    testMihomoDisabledReason,
    testingCPA,
    testingMihomo,
    updateForm,
    fetchMihomoGroups,
    saving,
    running,
    switching,
  }
}
