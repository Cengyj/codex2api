import { type ReactNode, useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Download,
  AlertCircle,
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  Clock3,
  Database,
  Loader2,
  PlayCircle,
  RefreshCw,
  Repeat,
  Search,
  Save,
  Settings2,
  ShieldCheck,
  Trash2,
  Upload,
  Wrench,
  Wifi,
  WifiOff,
} from 'lucide-react'
import { api } from '../api'
import PageHeader from '../components/PageHeader'
import StateShell from '../components/StateShell'
import ToastNotice from '../components/ToastNotice'
import { useDataLoader } from '../hooks/useDataLoader'
import { useToast } from '../hooks/useToast'
import { getErrorMessage } from '../utils/error'
import { formatRelativeTime } from '../utils/time'
import type {
  ConnectionTestStatus,
  CPASyncAction,
  CPASyncStatusResponse,
  MihomoStrategyGroupOption,
  SystemSettings,
} from '../types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'

type CPASyncView = 'overview' | 'logs'

type CPASyncPageData = {
  settings: SystemSettings | null
  status: CPASyncStatusResponse | null
}

type TestSignatureState = {
  cpa: string
  mihomo: string
  mihomoService: string
}

type CPASyncPageMemory = {
  data: CPASyncPageData
  settingsForm: SystemSettings | null
  localCPATestStatus: ConnectionTestStatus | null
  localMihomoTestStatus: ConnectionTestStatus | null
  testSignatures: TestSignatureState
  activeView: CPASyncView
  configOpen: boolean
  mihomoGroups: MihomoStrategyGroupOption[]
  mihomoServiceStatus: ConnectionTestStatus
  mihomoGroupsError: string
  lastMihomoFetchKey: string
}

type MihomoServiceCacheSnapshot = {
  signature: string
  status: ConnectionTestStatus
  groups: MihomoStrategyGroupOption[]
  error: string
}

function pickCPASyncSettings(settings: SystemSettings): Partial<SystemSettings> {
  return {
    cpa_sync_enabled: settings.cpa_sync_enabled,
    cpa_base_url: settings.cpa_base_url,
    cpa_admin_key: settings.cpa_admin_key,
    cpa_min_accounts: settings.cpa_min_accounts,
    cpa_max_accounts: settings.cpa_max_accounts,
    cpa_max_uploads_per_hour: settings.cpa_max_uploads_per_hour,
    cpa_switch_after_uploads: settings.cpa_switch_after_uploads,
    cpa_sync_interval_seconds: settings.cpa_sync_interval_seconds,
    mihomo_base_url: settings.mihomo_base_url,
    mihomo_secret: settings.mihomo_secret,
    mihomo_strategy_group: settings.mihomo_strategy_group,
    mihomo_delay_test_url: settings.mihomo_delay_test_url,
    mihomo_delay_timeout_ms: settings.mihomo_delay_timeout_ms,
  }
}

function createEmptyTestStatus(): ConnectionTestStatus {
  return {
    ok: null,
    message: '',
    http_status: undefined,
    tested_at: '',
    details: {},
  }
}

function createEmptyPageData(): CPASyncPageData {
  return {
    settings: null,
    status: null,
  }
}

function createEmptyTestSignatures(): TestSignatureState {
  return {
    cpa: '',
    mihomo: '',
    mihomoService: '',
  }
}

const cpaSyncPageMemory: CPASyncPageMemory = {
  data: createEmptyPageData(),
  settingsForm: null,
  localCPATestStatus: null,
  localMihomoTestStatus: null,
  testSignatures: createEmptyTestSignatures(),
  activeView: 'overview',
  configOpen: false,
  mihomoGroups: [],
  mihomoServiceStatus: createEmptyTestStatus(),
  mihomoGroupsError: '',
  lastMihomoFetchKey: '',
}

const MIHOMO_SERVICE_CACHE_KEY = 'codex2api:cpa-sync:mihomo-service'

function readMihomoServiceCache(): MihomoServiceCacheSnapshot | null {
  if (typeof window === 'undefined') return null
  try {
    const raw = window.sessionStorage.getItem(MIHOMO_SERVICE_CACHE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<MihomoServiceCacheSnapshot>
    if (!parsed || typeof parsed.signature !== 'string') return null
    return {
      signature: parsed.signature,
      status: normalizeTestStatus(parsed.status),
      groups: Array.isArray(parsed.groups) ? parsed.groups : [],
      error: typeof parsed.error === 'string' ? parsed.error : '',
    }
  } catch {
    return null
  }
}

function writeMihomoServiceCache(snapshot: MihomoServiceCacheSnapshot | null) {
  if (typeof window === 'undefined') return
  try {
    if (!snapshot) {
      window.sessionStorage.removeItem(MIHOMO_SERVICE_CACHE_KEY)
      return
    }
    window.sessionStorage.setItem(MIHOMO_SERVICE_CACHE_KEY, JSON.stringify(snapshot))
  } catch {
    // ignore storage failures
  }
}

function buildSettingsSignature(fields: Record<string, string>): string {
  return JSON.stringify(fields)
}

function getCPASettingsSignature(settings: Partial<SystemSettings> | null | undefined): string {
  return buildSettingsSignature({
    cpa_base_url: settings?.cpa_base_url?.trim() ?? '',
    cpa_admin_key: settings?.cpa_admin_key?.trim() ?? '',
  })
}

function getMihomoServiceSettingsSignature(settings: Partial<SystemSettings> | null | undefined): string {
  return buildSettingsSignature({
    mihomo_base_url: settings?.mihomo_base_url?.trim() ?? '',
    mihomo_secret: settings?.mihomo_secret?.trim() ?? '',
  })
}

function getMihomoSettingsSignature(settings: Partial<SystemSettings> | null | undefined): string {
  return buildSettingsSignature({
    mihomo_base_url: settings?.mihomo_base_url?.trim() ?? '',
    mihomo_secret: settings?.mihomo_secret?.trim() ?? '',
    mihomo_strategy_group: settings?.mihomo_strategy_group?.trim() ?? '',
  })
}

function normalizeTestStatus(status?: ConnectionTestStatus | null): ConnectionTestStatus {
  return {
    ok: status?.ok ?? null,
    message: status?.message ?? '',
    http_status: status?.http_status,
    tested_at: status?.tested_at ?? '',
    details: status?.details ?? {},
  }
}

function getStringDetail(status: ConnectionTestStatus | null | undefined, key: string): string {
  const value = status?.details?.[key]
  if (typeof value === 'string') return value.trim()
  if (typeof value === 'number') return String(value)
  return ''
}

function getNumberDetail(status: ConnectionTestStatus | null | undefined, key: string): number | null {
  const value = status?.details?.[key]
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string') {
    const parsed = Number(value)
    if (Number.isFinite(parsed)) return parsed
  }
  return null
}

function getCPAConfigMissing(settings: Partial<SystemSettings> | null | undefined): string[] {
  if (!settings) return ['cpa_base_url', 'cpa_admin_key']
  const missing: string[] = []
  if (!settings.cpa_base_url?.trim()) missing.push('cpa_base_url')
  if (!settings.cpa_admin_key?.trim()) missing.push('cpa_admin_key')
  return missing
}

function getMihomoConfigMissing(settings: Partial<SystemSettings> | null | undefined): string[] {
  if (!settings) return ['mihomo_base_url', 'mihomo_secret', 'mihomo_strategy_group']
  const missing: string[] = []
  if (!settings.mihomo_base_url?.trim()) missing.push('mihomo_base_url')
  if (!settings.mihomo_secret?.trim()) missing.push('mihomo_secret')
  if (!settings.mihomo_strategy_group?.trim()) missing.push('mihomo_strategy_group')
  return missing
}

function getMihomoServiceConfigMissing(settings: Partial<SystemSettings> | null | undefined): string[] {
  if (!settings) return ['mihomo_base_url', 'mihomo_secret']
  const missing: string[] = []
  if (!settings.mihomo_base_url?.trim()) missing.push('mihomo_base_url')
  if (!settings.mihomo_secret?.trim()) missing.push('mihomo_secret')
  return missing
}

function stringifyPickedSettings(settings: Partial<SystemSettings> | null | undefined): string {
  return JSON.stringify(settings ? pickCPASyncSettings(settings as SystemSettings) : {})
}

function haveFieldsChanged(
  current: Partial<SystemSettings> | null | undefined,
  persisted: Partial<SystemSettings> | null | undefined,
  fields: Array<keyof SystemSettings>
): boolean {
  return fields.some((field) => {
    const left = current?.[field]
    const right = persisted?.[field]
    return JSON.stringify(left ?? '') !== JSON.stringify(right ?? '')
  })
}

function summarizeText(text: string | null | undefined, fallback = '--'): string {
  const normalized = (text ?? '').replace(/\s+/g, ' ').trim()
  if (!normalized) return fallback
  return normalized.length > 64 ? `${normalized.slice(0, 64)}...` : normalized
}

function formatAbsoluteTime(value: string | null | undefined, fallback = '--'): string {
  if (!value) return fallback
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return fallback
  return parsed.toLocaleString('zh-CN', {
    hour12: false,
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

function formatCountdownParts(totalSeconds: number): string {
  const seconds = Math.max(0, totalSeconds)
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const remainSeconds = seconds % 60
  const parts: string[] = []

  if (hours > 0) parts.push(`${hours}小时`)
  if (minutes > 0) parts.push(`${minutes}分`)
  if (parts.length < 2 || remainSeconds > 0) parts.push(`${remainSeconds}秒`)

  return parts.slice(0, 3).join(' ')
}

function formatNextRunCountdown(
  nextRunAt: string | null | undefined,
  nowMs: number,
  enabled: boolean,
  running: boolean,
  t: (key: string, options?: Record<string, unknown>) => string
): string {
  if (!enabled) return t('cpaSync.nextRunDisabled')
  if (running) return t('cpaSync.runningNow')
  if (!nextRunAt) return t('cpaSync.nextRunPending')

  const diffMs = new Date(nextRunAt).getTime() - nowMs
  if (!Number.isFinite(diffMs)) return t('cpaSync.nextRunPending')
  if (diffMs <= 0) return t('cpaSync.nextRunSoon')

  return `${formatCountdownParts(Math.ceil(diffMs / 1000))}后`
}

function buildMissingFieldsHint(fields: string[], t: (key: string, options?: Record<string, unknown>) => string): string {
  return t('cpaSync.missingFieldsHint', {
    fields: fields.map((field) => formatMissingFieldLabel(field, t)).join('、'),
  })
}

function hasMissingConfigMessage(message: string | null | undefined): boolean {
  const normalized = (message ?? '').trim()
  if (!normalized) return false
  return /missing config:/i.test(normalized) || /请先补全[:：]/.test(normalized)
}

const successBadgeClassName = 'border-transparent bg-emerald-500/12 text-emerald-700 dark:bg-emerald-500/18 dark:text-emerald-300'
const warningBadgeClassName = 'border-transparent bg-amber-500/12 text-amber-700 dark:bg-amber-500/18 dark:text-amber-300'
const errorBadgeClassName = 'border-transparent bg-destructive/10 text-destructive dark:bg-destructive/20'
const infoBadgeClassName = 'border-transparent bg-blue-500/10 text-blue-600 dark:bg-blue-500/20 dark:text-blue-300'
const mutedBadgeClassName = 'border-border bg-white/50 text-muted-foreground dark:bg-white/5'

function getConnectionPresentation(
  testStatus: ConnectionTestStatus,
  missingFields: string[],
  isLoading: boolean,
  isStale: boolean,
  t: (key: string, options?: Record<string, unknown>) => string
) {
  if (isLoading) {
    return { label: t('cpaSync.testing'), className: infoBadgeClassName, dotClassName: 'bg-blue-500', icon: Loader2 }
  }
  if (missingFields.length > 0) {
    return { label: t('cpaSync.incomplete'), className: warningBadgeClassName, dotClassName: 'bg-amber-500', icon: AlertCircle }
  }
  if (isStale) {
    return { label: t('cpaSync.pendingRetest'), className: warningBadgeClassName, dotClassName: 'bg-amber-500', icon: AlertCircle }
  }
  if (testStatus.ok === true) {
    return { label: t('cpaSync.connectionOk'), className: successBadgeClassName, dotClassName: 'bg-emerald-500', icon: CheckCircle2 }
  }
  if (testStatus.ok === false) {
    return { label: t('cpaSync.connectionFailed'), className: errorBadgeClassName, dotClassName: 'bg-destructive', icon: WifiOff }
  }
  return { label: t('cpaSync.unknown'), className: mutedBadgeClassName, dotClassName: 'bg-gray-400', icon: Wifi }
}

function getRuntimeTone(status: string) {
  switch (status) {
    case 'success':
      return successBadgeClassName
    case 'partial_success':
      return warningBadgeClassName
    case 'error':
    case 'switch_error':
      return errorBadgeClassName
    case 'switch_success':
      return infoBadgeClassName
    case 'skipped':
    case 'unknown':
    default:
      return mutedBadgeClassName
  }
}

function formatRuntimeStatus(status: string, t: (key: string, options?: Record<string, unknown>) => string): string {
  const map: Record<string, string> = {
    success: t('cpaSync.statusSuccess'),
    partial_success: t('cpaSync.statusPartial'),
    skipped: t('cpaSync.statusSkipped'),
    error: t('cpaSync.statusError'),
    switch_success: t('cpaSync.statusSwitchSuccess'),
    switch_error: t('cpaSync.statusSwitchError'),
    unknown: t('cpaSync.unknown'),
  }
  return map[status] ?? status
}

function formatActionKind(kind: string, t: (key: string, options?: Record<string, unknown>) => string): string {
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

function formatActionStatus(status: string, t: (key: string, options?: Record<string, unknown>) => string): string {
  const map: Record<string, string> = {
    success: t('cpaSync.actionSuccess'),
    error: t('cpaSync.actionError'),
    warning: t('cpaSync.actionWarning'),
    info: t('cpaSync.actionInfo'),
  }
  return map[status] ?? status
}

function formatMissingFieldLabel(field: string, t: (key: string, options?: Record<string, unknown>) => string): string {
  const map: Record<string, string> = {
    cpa_base_url: t('cpaSync.cpaBaseUrl'),
    cpa_admin_key: t('cpaSync.cpaAdminKey'),
    mihomo_base_url: t('cpaSync.mihomoBaseUrl'),
    mihomo_secret: t('cpaSync.mihomoSecret'),
    mihomo_strategy_group: t('cpaSync.mihomoStrategyGroup'),
  }
  return map[field] ?? field
}

function localizeConnectionMessage(raw: string | null | undefined, t: (key: string, options?: Record<string, unknown>) => string): string {
  const message = (raw ?? '').trim()
  if (!message) return ''

  let localized = message

  localized = localized.replace(/CPA connection OK, found (\d+) auth files/gi, (_, count: string) =>
    t('cpaSync.cpaAuthFilesFound', { count: Number(count) })
  )

  localized = localized.replace(/Mihomo connection OK, strategy group has (\d+) candidates/gi, (_, count: string) =>
    t('cpaSync.mihomoCandidatesFound', { count: Number(count) })
  )

  localized = localized.replace(/delay test failed/gi, t('cpaSync.delayTestFailed'))

  localized = localized.replace(/parse Mihomo response failed:\s*(.+?)(?=,?\s*missing config:|$)/gi, (_, reason: string) =>
    t('cpaSync.parseMihomoFailed', { reason: reason.trim() })
  )

  localized = localized.replace(/missing config:\s*(.+)$/i, (_, fieldsRaw: string) => {
    const fields = fieldsRaw
      .split(',')
      .map((field) => formatMissingFieldLabel(field.trim(), t))
      .join('、')
    return t('cpaSync.missingFieldsHint', { fields })
  })

  return localized
}

function formatTestDetailLabel(key: string, t: (key: string, options?: Record<string, unknown>) => string): string {
  const map: Record<string, string> = {
    account_count: t('cpaSync.detailAccountCount'),
    current_node: t('cpaSync.detailCurrentNode'),
    candidate_count: t('cpaSync.detailCandidateCount'),
    delay_summary: t('cpaSync.detailDelaySummary'),
    strategy_group: t('cpaSync.detailStrategyGroup'),
    selected_proxy: t('cpaSync.detailSelectedProxy'),
    tested_url: t('cpaSync.detailTestedUrl'),
    tested_group: t('cpaSync.detailTestedGroup'),
  }
  return map[key] ?? key.replace(/_/g, ' ')
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

function getConfigSummaryLabel(
  cpaMissing: string[],
  mihomoMissing: string[],
  t: (key: string, options?: Record<string, unknown>) => string
) {
  if (cpaMissing.length === 0 && mihomoMissing.length === 0) {
    return { label: t('cpaSync.configComplete'), className: successBadgeClassName }
  }
  if (cpaMissing.length > 0 && mihomoMissing.length > 0) {
    return { label: t('cpaSync.configMissingBoth'), className: warningBadgeClassName }
  }
  if (cpaMissing.length > 0) {
    return { label: t('cpaSync.configMissingCPA'), className: warningBadgeClassName }
  }
  return { label: t('cpaSync.configMissingMihomo'), className: warningBadgeClassName }
}

function getDisabledReason(
  type: 'run' | 'switch' | 'test-cpa' | 'test-mihomo',
  options: { running: boolean; cpaMissing: string[]; mihomoMissing: string[] },
  t: (key: string, options?: Record<string, unknown>) => string
): string | null {
  if (options.running) return t('cpaSync.busyHint')
  if ((type === 'run' || type === 'test-cpa') && options.cpaMissing.length > 0) {
    return t('cpaSync.missingFieldsHint', { fields: options.cpaMissing.map((field) => formatMissingFieldLabel(field, t)).join('、') })
  }
  if ((type === 'switch' || type === 'test-mihomo') && options.mihomoMissing.length > 0) {
    return t('cpaSync.missingFieldsHint', { fields: options.mihomoMissing.map((field) => formatMissingFieldLabel(field, t)).join('、') })
  }
  return null
}

export default function CPASync() {
  const { t } = useTranslation()
  const { toast, showToast } = useToast()
  const hasPageSnapshot = Boolean(cpaSyncPageMemory.data.settings || cpaSyncPageMemory.data.status)
  const initialMihomoServiceCache = readMihomoServiceCache()
  const [settingsForm, setSettingsForm] = useState<SystemSettings | null>(() => cpaSyncPageMemory.settingsForm)
  const [localCPATestStatus, setLocalCPATestStatus] = useState<ConnectionTestStatus | null>(() => cpaSyncPageMemory.localCPATestStatus)
  const [localMihomoTestStatus, setLocalMihomoTestStatus] = useState<ConnectionTestStatus | null>(() => cpaSyncPageMemory.localMihomoTestStatus)
  const [testSignatures, setTestSignatures] = useState<TestSignatureState>(() => cpaSyncPageMemory.testSignatures)
  const [saving, setSaving] = useState(false)
  const [running, setRunning] = useState(false)
  const [switching, setSwitching] = useState(false)
  const [testingCPA, setTestingCPA] = useState(false)
  const [testingMihomo, setTestingMihomo] = useState(false)
  const [configOpen, setConfigOpen] = useState<boolean>(() => cpaSyncPageMemory.configOpen)
  const [activeView, setActiveView] = useState<CPASyncView>(() => cpaSyncPageMemory.activeView)
  const [nowMs, setNowMs] = useState(() => Date.now())
  const [mihomoGroups, setMihomoGroups] = useState<MihomoStrategyGroupOption[]>(() => cpaSyncPageMemory.mihomoGroups.length > 0 ? cpaSyncPageMemory.mihomoGroups : (initialMihomoServiceCache?.groups ?? []))
  const [mihomoServiceStatus, setMihomoServiceStatus] = useState<ConnectionTestStatus>(() => {
    if (cpaSyncPageMemory.mihomoServiceStatus.tested_at || cpaSyncPageMemory.mihomoServiceStatus.message) {
      return cpaSyncPageMemory.mihomoServiceStatus
    }
    return initialMihomoServiceCache?.status ?? createEmptyTestStatus()
  })
  const [loadingMihomoGroups, setLoadingMihomoGroups] = useState(false)
  const [mihomoGroupsError, setMihomoGroupsError] = useState(() => cpaSyncPageMemory.mihomoGroupsError || initialMihomoServiceCache?.error || '')
  const [lastMihomoFetchKey, setLastMihomoFetchKey] = useState(() => cpaSyncPageMemory.lastMihomoFetchKey || initialMihomoServiceCache?.signature || '')

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
  const cpaTestStatus = normalizeTestStatus(testSignatures.cpa === cpaSettingsSignature && localCPATestStatus ? localCPATestStatus : status?.cpa_test_status)
  const mihomoTestStatus = normalizeTestStatus(testSignatures.mihomo === mihomoSettingsSignature && localMihomoTestStatus ? localMihomoTestStatus : status?.mihomo_test_status)
  const localizedCPATestMessage = localizeConnectionMessage(cpaTestStatus.message, t)
  const localizedMihomoTestMessage = localizeConnectionMessage(mihomoTestStatus.message, t)
  const cpaConnectionNeedsRetest = (cpaConnectionDirty && testSignatures.cpa !== cpaSettingsSignature) || (formCPAMissing.length === 0 && hasMissingConfigMessage(localizedCPATestMessage))
  const mihomoConnectionNeedsRetest = (mihomoConnectionDirty && testSignatures.mihomo !== mihomoSettingsSignature) || (formMihomoMissing.length === 0 && hasMissingConfigMessage(localizedMihomoTestMessage))
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
    label: `${group.name} · ${group.candidate_count}${t('cpaSync.groupCountSuffix')}`,
  }))
  const resolvedMihomoGroupOptions = form?.mihomo_strategy_group && !mihomoGroupOptions.some((option) => option.value === form.mihomo_strategy_group)
    ? [{ value: form.mihomo_strategy_group, label: `${form.mihomo_strategy_group} · ${t('cpaSync.currentValue')}` }, ...mihomoGroupOptions]
    : mihomoGroupOptions
  const hasMihomoGroupOptions = resolvedMihomoGroupOptions.length > 0
  const mihomoBaseValue = form?.mihomo_base_url?.trim() ?? ''
  const mihomoSecretValue = form?.mihomo_secret?.trim() ?? ''
  const recentActions = [...(status?.state.recent_actions ?? [])].reverse()
  const runtimeBusy = Boolean(status?.running || running || switching || testingCPA || testingMihomo)
  const runtimeStatusLabel = testingCPA || testingMihomo
    ? t('cpaSync.testing')
    : runtimeBusy
      ? t('cpaSync.runningTask')
      : t('cpaSync.idleTask')
  const runtimeStatusDescription = testingCPA || testingMihomo
    ? t('cpaSync.connectionActionsDesc')
    : runtimeBusy
      ? t('cpaSync.workerBusyDesc')
      : t('cpaSync.workerIdleDesc')
  const selectedMihomoGroup = mihomoGroups.find((group) => group.name === form?.mihomo_strategy_group)
  const currentNodeFromService = selectedMihomoGroup?.current?.trim() ?? ''
  const currentNodeFromTest = getStringDetail(localMihomoTestStatus, 'current_node') || getStringDetail(mihomoTestStatus, 'current_node')
  const displayedCurrentMihomoNode = status?.state.current_mihomo_node?.trim() || currentNodeFromService || currentNodeFromTest || '--'
  const shouldAutoProbeMihomoService = Boolean(mihomoBaseValue && mihomoSecretValue) && lastMihomoFetchKey !== mihomoServiceSettingsSignature
  const syncIntervalSeconds = status?.config.interval_seconds ?? form?.cpa_sync_interval_seconds ?? 300
  const nextRunCountdown = formatNextRunCountdown(status?.next_run_at, nowMs, Boolean(form?.cpa_sync_enabled), runtimeBusy, t)
  const nextRunDetail = !form?.cpa_sync_enabled
    ? t('cpaSync.nextRunDisabledDesc')
    : runtimeBusy
      ? t('cpaSync.nextRunRunningDesc')
      : status?.next_run_at
        ? `${formatAbsoluteTime(status.next_run_at)} · ${t('cpaSync.syncEverySeconds', { count: syncIntervalSeconds })}`
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
    cpaSyncPageMemory.localCPATestStatus = localCPATestStatus
    cpaSyncPageMemory.localMihomoTestStatus = localMihomoTestStatus
    cpaSyncPageMemory.testSignatures = testSignatures
    cpaSyncPageMemory.activeView = activeView
    cpaSyncPageMemory.configOpen = configOpen
    cpaSyncPageMemory.mihomoGroups = mihomoGroups
    cpaSyncPageMemory.mihomoServiceStatus = mihomoServiceStatus
    cpaSyncPageMemory.mihomoGroupsError = mihomoGroupsError
    cpaSyncPageMemory.lastMihomoFetchKey = lastMihomoFetchKey
  }, [
    activeView,
    configOpen,
    data,
    lastMihomoFetchKey,
    localCPATestStatus,
    localMihomoTestStatus,
    mihomoGroups,
    mihomoGroupsError,
    mihomoServiceStatus,
    settingsForm,
    testSignatures,
  ])

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
    options?: { showErrorToast?: boolean; silent?: boolean }
  ) => {
    const showErrorToast = options?.showErrorToast ?? false
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
      const selectedGroup = (result.groups ?? []).find((group) => group.name === sourceForm.mihomo_strategy_group)
      setMihomoGroups(result.groups ?? [])
      setMihomoServiceStatus({
        ok: true,
        message: (result.groups ?? []).length > 0 ? t('cpaSync.mihomoServiceConnected', { count: (result.groups ?? []).length }) : t('cpaSync.mihomoServiceConnectedNoGroups'),
        http_status: 200,
        tested_at: new Date().toISOString(),
        details: {
          group_count: (result.groups ?? []).length,
        },
      })
      patchStatus((current) => current ? ({
        ...current,
        state: {
          ...current.state,
          current_mihomo_node: selectedGroup?.current?.trim() || current.state.current_mihomo_node,
        },
      }) : current)
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
      if (showErrorToast) {
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
      if (shouldClearCPATest) {
        setLocalCPATestStatus(createEmptyTestStatus())
      }
      if (shouldClearMihomoTest) {
        setLocalMihomoTestStatus(createEmptyTestStatus())
      }
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
      setLocalCPATestStatus(result)
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
      setLocalMihomoTestStatus(result)
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
          actions={
            <Button variant="outline" onClick={() => void reload()} className="max-sm:w-full">
              <RefreshCw className="size-3.5" />
              {t('common.refresh')}
            </Button>
          }
        />

        {form ? (
          <div className="space-y-6">
            <section className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4">
              <OverviewCard
                icon={<ShieldCheck className="size-5" />}
                label={t('cpaSync.autoSyncStatus')}
                value={form.cpa_sync_enabled ? t('common.enabled') : t('common.disabled')}
                sub={form.cpa_sync_enabled ? t('cpaSync.autoSyncOnDesc') : t('cpaSync.autoSyncOffDesc')}
                badge={
                  <Badge className={form.cpa_sync_enabled ? successBadgeClassName : mutedBadgeClassName}>
                    <span className={`size-1.5 rounded-full ${form.cpa_sync_enabled ? 'bg-emerald-500' : 'bg-gray-400'}`} />
                    {form.cpa_sync_enabled ? t('common.enabled') : t('common.disabled')}
                  </Badge>
                }
              />
              <OverviewCard
                icon={<Wrench className="size-5" />}
                label={t('cpaSync.workerStatus')}
                value={runtimeStatusLabel}
                sub={runtimeStatusDescription}
                badge={
                  <Badge className={runtimeBusy ? infoBadgeClassName : mutedBadgeClassName}>
                    <span className={`size-1.5 rounded-full ${runtimeBusy ? 'bg-blue-500' : 'bg-gray-400'}`} />
                    {runtimeBusy ? t('common.running') : t('cpaSync.idle')}
                  </Badge>
                }
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
                badge={
                  <Badge className={runtimeBusy ? infoBadgeClassName : (form.cpa_sync_enabled ? mutedBadgeClassName : warningBadgeClassName)}>
                    <span className={`size-1.5 rounded-full ${runtimeBusy ? 'bg-blue-500' : (form.cpa_sync_enabled ? 'bg-gray-400' : 'bg-amber-500')}`} />
                    {t('cpaSync.syncEverySeconds', { count: syncIntervalSeconds })}
                  </Badge>
                }
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
                badge={
                  <Badge className={mutedBadgeClassName}>
                    <ShieldCheck className="size-3" />
                    {t('cpaSync.strategyNode')}
                  </Badge>
                }
              />
            </section>

            <section className="rounded-[32px] border border-border/80 bg-gradient-to-r from-white via-slate-50/90 to-white p-2 shadow-sm dark:from-slate-950 dark:via-slate-950 dark:to-slate-900/80">
              <div className="grid gap-2 lg:grid-cols-2">
                <ConsoleTabButton
                  active={activeView === 'overview'}
                  icon={<ShieldCheck className="size-4.5" />}
                  title={t('cpaSync.overviewTab')}
                  description={t('cpaSync.overviewTabDesc')}
                  meta={<RuntimeStatusBadge status={status?.state.last_run_status ?? 'unknown'} t={t} />}
                  onClick={() => setActiveView('overview')}
                />
                <ConsoleTabButton
                  active={activeView === 'logs'}
                  icon={<Clock3 className="size-4.5" />}
                  title={t('cpaSync.logsTab')}
                  description={t('cpaSync.logsTabDesc')}
                  meta={
                    <Badge className={mutedBadgeClassName}>
                      {recentActions.length}
                      {t('cpaSync.actionCountSuffix')}
                    </Badge>
                  }
                  onClick={() => setActiveView('logs')}
                />
              </div>
            </section>

            <div
              className="space-y-6"
              hidden={activeView !== 'overview'}
              aria-hidden={activeView !== 'overview'}
            >
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
                    <StatusTile label={t('cpaSync.cpaCount')} value={String(status?.state.last_cpa_account_count ?? 0)} tone="neutral" />
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
                        {t('cpaSync.missingConfig')}：
                        {[...formCPAMissing, ...formMihomoMissing].map((field) => formatMissingFieldLabel(field, t)).join('、')}
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
                        <Field label={t('cpaSync.maxAccounts')}>
                          <Input type="number" min={0} value={form.cpa_max_accounts} onChange={(event) => updateForm({ cpa_max_accounts: Number(event.target.value) || 0 })} />
                        </Field>
                        <Field label={t('cpaSync.maxUploadsPerHour')}>
                          <Input type="number" min={0} value={form.cpa_max_uploads_per_hour} onChange={(event) => updateForm({ cpa_max_uploads_per_hour: Number(event.target.value) || 0 })} />
                        </Field>
                        <Field label={t('cpaSync.switchAfterUploads')}>
                          <Input type="number" min={0} value={form.cpa_switch_after_uploads} onChange={(event) => updateForm({ cpa_switch_after_uploads: Number(event.target.value) || 0 })} />
                        </Field>
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
            </div>

            <section hidden={activeView !== 'logs'} aria-hidden={activeView !== 'logs'}>
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
          </div>
        ) : null}

        <ToastNotice toast={toast} />
      </>
    </StateShell>
  )
}

function ConnectionBadge({ presentation }: { presentation: ReturnType<typeof getConnectionPresentation> }) {
  const Icon = presentation.icon
  return (
    <Badge className={presentation.className}>
      <span className={`size-1.5 rounded-full ${presentation.dotClassName}`} />
      <Icon className={presentation.icon === Loader2 ? 'size-3 animate-spin' : 'size-3'} />
      {presentation.label}
    </Badge>
  )
}

function RuntimeStatusBadge({
  status,
  t,
}: {
  status: string
  t: (key: string, options?: Record<string, unknown>) => string
}) {
  return <Badge className={getRuntimeTone(status)}>{formatRuntimeStatus(status, t)}</Badge>
}

function OverviewCard({
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

function ConsoleTabButton({
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

function StatusTile({
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

function ActionGroup({
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

function TestStatusCard({
  title,
  description,
  status,
  presentation,
  t,
}: {
  title: string
  description: string
  status: ConnectionTestStatus
  presentation: ReturnType<typeof getConnectionPresentation>
  t: (key: string, options?: Record<string, unknown>) => string
}) {
  const detailsEntries = Object.entries(status.details ?? {}).filter(([, value]) => value !== null && value !== undefined && value !== '').slice(0, 6)

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

function ConfigSection({
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

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <label className="mb-2 block text-sm font-semibold text-slate-700 dark:text-slate-300">{label}</label>
      {children}
    </div>
  )
}

function FieldBadge({ label, missing }: { label: string; missing: boolean }) {
  return (
    <Badge className={missing ? warningBadgeClassName : successBadgeClassName}>
      <span className={`size-1.5 rounded-full ${missing ? 'bg-amber-500' : 'bg-emerald-500'}`} />
      {label}
    </Badge>
  )
}

function LogMetaCard({
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

function MiniConfigSummaryCard({
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

function ActionLogRow({
  action,
  t,
}: {
  action: CPASyncAction
  t: (key: string, options?: Record<string, unknown>) => string
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
