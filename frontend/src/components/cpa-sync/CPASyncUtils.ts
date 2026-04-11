import {
  AlertCircle,
  CheckCircle2,
  Loader2,
  Wifi,
  WifiOff,
  type LucideIcon,
} from 'lucide-react'
import type {
  ConnectionTestStatus,
  CPASyncStatusResponse,
  MihomoStrategyGroupOption,
  SystemSettings,
} from '@/types'

export type TranslateFn = (key: string, options?: Record<string, unknown>) => string

export type CPASyncView = 'overview' | 'logs'

export type CPASyncPageData = {
  settings: SystemSettings | null
  status: CPASyncStatusResponse | null
}

export type TestSignatureState = {
  cpa: string
  mihomo: string
  mihomoService: string
}

export type CPASyncPageMemory = {
  data: CPASyncPageData
  settingsForm: SystemSettings | null
  testSignatures: TestSignatureState
  activeView: CPASyncView
  configOpen: boolean
  mihomoGroups: MihomoStrategyGroupOption[]
  mihomoServiceStatus: ConnectionTestStatus
  mihomoGroupsError: string
  lastMihomoFetchKey: string
}

export type MihomoServiceCacheSnapshot = {
  signature: string
  status: ConnectionTestStatus
  groups: MihomoStrategyGroupOption[]
  error: string
}

export type ConnectionPresentation = {
  label: string
  className: string
  dotClassName: string
  icon: LucideIcon
}

export const successBadgeClassName = 'border-transparent bg-emerald-500/12 text-emerald-700 dark:bg-emerald-500/18 dark:text-emerald-300'
export const warningBadgeClassName = 'border-transparent bg-amber-500/12 text-amber-700 dark:bg-amber-500/18 dark:text-amber-300'
export const errorBadgeClassName = 'border-transparent bg-destructive/10 text-destructive dark:bg-destructive/20'
export const infoBadgeClassName = 'border-transparent bg-blue-500/10 text-blue-600 dark:bg-blue-500/20 dark:text-blue-300'
export const mutedBadgeClassName = 'border-border bg-white/50 text-muted-foreground dark:bg-white/5'

const MIHOMO_SERVICE_CACHE_KEY = 'codex2api:cpa-sync:mihomo-service'

export function pickCPASyncSettings(settings: SystemSettings): Partial<SystemSettings> {
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

export function createEmptyTestStatus(): ConnectionTestStatus {
  return {
    ok: null,
    message: '',
    http_status: undefined,
    tested_at: '',
    details: {},
  }
}

export function createEmptyPageData(): CPASyncPageData {
  return {
    settings: null,
    status: null,
  }
}

export function createEmptyTestSignatures(): TestSignatureState {
  return {
    cpa: '',
    mihomo: '',
    mihomoService: '',
  }
}

export function readMihomoServiceCache(): MihomoServiceCacheSnapshot | null {
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

export function writeMihomoServiceCache(snapshot: MihomoServiceCacheSnapshot | null) {
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
  const normalized = JSON.stringify(fields)
  let hash = 0xcbf29ce484222325n
  const prime = 0x100000001b3n

  for (let index = 0; index < normalized.length; index += 1) {
    hash ^= BigInt(normalized.charCodeAt(index))
    hash = BigInt.asUintN(64, hash * prime)
  }

  return `fnv1a64:${hash.toString(16).padStart(16, '0')}`
}

export function getCPASettingsSignature(settings: Partial<SystemSettings> | null | undefined): string {
  return buildSettingsSignature({
    cpa_base_url: settings?.cpa_base_url?.trim() ?? '',
    cpa_admin_key: settings?.cpa_admin_key?.trim() ?? '',
  })
}

export function getMihomoServiceSettingsSignature(settings: Partial<SystemSettings> | null | undefined): string {
  return buildSettingsSignature({
    mihomo_base_url: settings?.mihomo_base_url?.trim() ?? '',
    mihomo_secret: settings?.mihomo_secret?.trim() ?? '',
  })
}

export function getMihomoSettingsSignature(settings: Partial<SystemSettings> | null | undefined): string {
  return buildSettingsSignature({
    mihomo_base_url: settings?.mihomo_base_url?.trim() ?? '',
    mihomo_secret: settings?.mihomo_secret?.trim() ?? '',
    mihomo_strategy_group: settings?.mihomo_strategy_group?.trim() ?? '',
  })
}

export function normalizeTestStatus(status?: ConnectionTestStatus | null): ConnectionTestStatus {
  return {
    ok: status?.ok ?? null,
    message: status?.message ?? '',
    http_status: status?.http_status,
    tested_at: status?.tested_at ?? '',
    details: status?.details ?? {},
  }
}

export function getStringDetail(status: ConnectionTestStatus | null | undefined, key: string): string {
  const value = status?.details?.[key]
  if (typeof value === 'string') return value.trim()
  if (typeof value === 'number') return String(value)
  return ''
}

export function getNumberDetail(status: ConnectionTestStatus | null | undefined, key: string): number | null {
  const value = status?.details?.[key]
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string') {
    const parsed = Number(value)
    if (Number.isFinite(parsed)) return parsed
  }
  return null
}

export function getCPAConfigMissing(settings: Partial<SystemSettings> | null | undefined): string[] {
  if (!settings) return ['cpa_base_url', 'cpa_admin_key']
  const missing: string[] = []
  if (!settings.cpa_base_url?.trim()) missing.push('cpa_base_url')
  if (!settings.cpa_admin_key?.trim()) missing.push('cpa_admin_key')
  return missing
}

export function getMihomoConfigMissing(settings: Partial<SystemSettings> | null | undefined): string[] {
  if (!settings) return ['mihomo_base_url', 'mihomo_secret', 'mihomo_strategy_group']
  const missing: string[] = []
  if (!settings.mihomo_base_url?.trim()) missing.push('mihomo_base_url')
  if (!settings.mihomo_secret?.trim()) missing.push('mihomo_secret')
  if (!settings.mihomo_strategy_group?.trim()) missing.push('mihomo_strategy_group')
  return missing
}

export function getMihomoServiceConfigMissing(settings: Partial<SystemSettings> | null | undefined): string[] {
  if (!settings) return ['mihomo_base_url', 'mihomo_secret']
  const missing: string[] = []
  if (!settings.mihomo_base_url?.trim()) missing.push('mihomo_base_url')
  if (!settings.mihomo_secret?.trim()) missing.push('mihomo_secret')
  return missing
}

export function stringifyPickedSettings(settings: Partial<SystemSettings> | null | undefined): string {
  return JSON.stringify(settings ? pickCPASyncSettings(settings as SystemSettings) : {})
}

export function haveFieldsChanged(
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

export function summarizeText(text: string | null | undefined, fallback = '--'): string {
  const normalized = (text ?? '').replace(/\s+/g, ' ').trim()
  if (!normalized) return fallback
  return normalized.length > 64 ? `${normalized.slice(0, 64)}...` : normalized
}

export function joinLocalizedItems(items: string[]): string {
  return items.filter(Boolean).join(' / ')
}

export function formatAbsoluteTime(value: string | null | undefined, fallback = '--'): string {
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

export function formatCountdownParts(totalSeconds: number, t: TranslateFn): string {
  const seconds = Math.max(0, totalSeconds)
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const remainSeconds = seconds % 60

  if (hours > 0) {
    return t('common.countdownHoursMinutes', { hours, minutes })
  }
  if (minutes > 0) {
    return t('common.countdownMinutesSeconds', { minutes, seconds: remainSeconds })
  }
  return t('common.inSecondsLong', { count: remainSeconds })
}

export function formatNextRunCountdown(
  nextRunAt: string | null | undefined,
  nowMs: number,
  enabled: boolean,
  running: boolean,
  t: TranslateFn
): string {
  if (!enabled) return t('cpaSync.nextRunDisabled')
  if (running) return t('cpaSync.runningNow')
  if (!nextRunAt) return t('cpaSync.nextRunPending')

  const diffMs = new Date(nextRunAt).getTime() - nowMs
  if (!Number.isFinite(diffMs)) return t('cpaSync.nextRunPending')
  if (diffMs <= 0) return t('cpaSync.nextRunSoon')

  return formatCountdownParts(Math.ceil(diffMs / 1000), t)
}

export function formatMissingFieldLabel(field: string, t: TranslateFn): string {
  const map: Record<string, string> = {
    cpa_base_url: t('cpaSync.cpaBaseUrl'),
    cpa_admin_key: t('cpaSync.cpaAdminKey'),
    mihomo_base_url: t('cpaSync.mihomoBaseUrl'),
    mihomo_secret: t('cpaSync.mihomoSecret'),
    mihomo_strategy_group: t('cpaSync.mihomoStrategyGroup'),
  }
  return map[field] ?? field
}

export function buildMissingFieldsHint(fields: string[], t: TranslateFn): string {
  return t('cpaSync.missingFieldsHint', {
    fields: joinLocalizedItems(fields.map((field) => formatMissingFieldLabel(field, t))),
  })
}

export function hasMissingConfigMessage(
  message: string | null | undefined,
  t: TranslateFn
): boolean {
  const normalized = (message ?? '').trim()
  if (!normalized) return false
  const localizedPrefix = t('cpaSync.missingFieldsHint', { fields: '' }).trim()
  return /missing config:/i.test(normalized) || normalized.startsWith(localizedPrefix)
}

export function localizeConnectionMessage(raw: string | null | undefined, t: TranslateFn): string {
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
    return t('cpaSync.missingFieldsHint', { fields: joinLocalizedItems(fields) })
  })

  return localized
}

export function getConnectionPresentation(
  testStatus: ConnectionTestStatus,
  missingFields: string[],
  isLoading: boolean,
  isStale: boolean,
  t: TranslateFn
): ConnectionPresentation {
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

export function getRuntimeTone(status: string) {
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

export function formatRuntimeStatus(status: string, t: TranslateFn): string {
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

export function formatTestDetailLabel(key: string, t: TranslateFn): string {
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

export function getConfigSummaryLabel(
  cpaMissing: string[],
  mihomoMissing: string[],
  t: TranslateFn
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

export function getDisabledReason(
  type: 'run' | 'switch' | 'test-cpa' | 'test-mihomo',
  options: { running: boolean; cpaMissing: string[]; mihomoMissing: string[] },
  t: TranslateFn
): string | null {
  if (options.running) return t('cpaSync.busyHint')
  if ((type === 'run' || type === 'test-cpa') && options.cpaMissing.length > 0) {
    return t('cpaSync.missingFieldsHint', {
      fields: joinLocalizedItems(options.cpaMissing.map((field) => formatMissingFieldLabel(field, t))),
    })
  }
  if ((type === 'switch' || type === 'test-mihomo') && options.mihomoMissing.length > 0) {
    return t('cpaSync.missingFieldsHint', {
      fields: joinLocalizedItems(options.mihomoMissing.map((field) => formatMissingFieldLabel(field, t))),
    })
  }
  return null
}
