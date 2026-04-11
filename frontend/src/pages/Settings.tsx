import type { ChangeEvent } from 'react'

import { useCallback, useState } from 'react'

import { useTranslation } from 'react-i18next'

import { api, resetAdminAuthState, setAdminKey } from '../api'

import { getTimezone, setTimezone } from '../utils/time'

import PageHeader from '../components/PageHeader'

import StateShell from '../components/StateShell'

import ToastNotice from '../components/ToastNotice'

import { useDataLoader } from '../hooks/useDataLoader'

import { useToast } from '../hooks/useToast'

import type { HealthResponse, SystemSettings, ProxyMode, ProxyProtocol } from '../types'

import { getErrorMessage } from '../utils/error'

import { Card, CardContent } from '@/components/ui/card'

import { Badge } from '@/components/ui/badge'

import { Button } from '@/components/ui/button'

import { Input } from '@/components/ui/input'

import { Select } from '@/components/ui/select'

const defaultSettingsForm: SystemSettings = {

  max_concurrency: 2,

  global_rpm: 0,

  test_model: '',

  test_concurrency: 50,

  refresh_scan_enabled: true,

  refresh_scan_interval_seconds: 120,

  refresh_pre_expire_seconds: 300,

  refresh_max_concurrency: 10,

  refresh_on_import_enabled: true,

  refresh_on_import_concurrency: 4,

  usage_probe_enabled: true,

  usage_probe_stale_after_seconds: 600,

  usage_probe_max_concurrency: 4,

  recovery_probe_enabled: true,

  recovery_probe_min_interval_seconds: 1800,

  recovery_probe_max_concurrency: 2,

  pg_max_conns: 50,

  redis_pool_size: 30,

  auto_clean_unauthorized: false,

  auto_clean_rate_limited: false,

  auto_clean_error: false,

  auto_clean_expired: false,

  admin_secret: '',

  admin_auth_source: 'disabled',

  auto_clean_full_usage: false,

  proxy_pool_enabled: false,

  max_retries: 2,

  allow_remote_migration: false,

  database_driver: 'postgres',

  database_label: 'PostgreSQL',

  cache_driver: 'redis',

  cache_label: 'Redis',

  cpa_sync_enabled: false,

  cpa_base_url: '',

  cpa_admin_key: '',

  cpa_min_accounts: 0,

  cpa_max_accounts: 0,

  cpa_max_uploads_per_hour: 0,

  cpa_switch_after_uploads: 0,

  cpa_sync_interval_seconds: 300,

  mihomo_base_url: '',

  mihomo_secret: '',

  mihomo_strategy_group: '',

  mihomo_delay_test_url: '',

  mihomo_delay_timeout_ms: 5000,

  proxy_default_mode: 'static',

  proxy_dynamic_provider_url: '',

  proxy_default_protocol: 'http',

  proxy_rotation_hours: 24,

}

const parseNumberValue = (value: string, fallback: number, min = 0) => {

  const parsed = Number.parseInt(value, 10)

  if (Number.isNaN(parsed)) return fallback

  return parsed < min ? min : parsed

}

const secondsToMinuteValue = (seconds: number | undefined, fallbackSeconds: number, minMinutes = 0) => {

  const safeSeconds = typeof seconds === 'number' ? seconds : fallbackSeconds

  const fallbackMinutes = Math.max(minMinutes, Math.round(fallbackSeconds / 60))

  if (!Number.isFinite(safeSeconds)) return fallbackMinutes

  return Math.max(minMinutes, Math.round(safeSeconds / 60))

}

const parseMinutesToSecondsValue = (value: string, fallbackSeconds: number, minMinutes = 0) => {

  const fallbackMinutes = Math.max(minMinutes, Math.round(fallbackSeconds / 60))

  return parseNumberValue(value, fallbackMinutes, minMinutes) * 60

}




export default function Settings() {

  const { t } = useTranslation()

  const booleanOptions = [

    { label: t('common.disabled'), value: 'false' },

    { label: t('common.enabled'), value: 'true' },

  ]

  const proxyModeOptions = [

    { value: 'static', label: t('accounts.proxyModeStatic') },

    { value: 'dynamic', label: t('accounts.proxyModeDynamic') },

    { value: 'auto', label: t('accounts.proxyModeAuto') },

    { value: 'none', label: t('accounts.proxyModeNone') },

  ]

  const proxyProtocolOptions = [

    { value: 'http', label: t('accounts.proxyProtocolHttp') },

    { value: 'https', label: t('accounts.proxyProtocolHttps') },

    { value: 'socks5', label: t('accounts.proxyProtocolSocks5') },

    { value: 'socks4', label: t('accounts.proxyProtocolSocks4') },

    { value: 'auto', label: t('accounts.proxyProtocolAuto') },

  ]

  const [settingsForm, setSettingsForm] = useState<SystemSettings>(defaultSettingsForm)

  const [savingSettings, setSavingSettings] = useState(false)

  const [loadedAdminSecret, setLoadedAdminSecret] = useState('')

  const [modelList, setModelList] = useState<string[]>([])

  const { toast, showToast } = useToast()



  const loadSettingsData = useCallback(async () => {

    const [health, settings, modelsResp] = await Promise.all([api.getHealth(), api.getSettings(), api.getModels()])

    const mergedSettings = { ...defaultSettingsForm, ...settings }

    setSettingsForm(mergedSettings)

    setLoadedAdminSecret(mergedSettings.admin_secret ?? '')

    setModelList(modelsResp.models ?? [])

    return {

      health,

    }

  }, [])



  const { data, loading, error, reload } = useDataLoader<{

    health: HealthResponse | null

  }>({

    initialData: {

      health: null,

    },

    load: loadSettingsData,

  })









  const handleSaveSettings = async () => {

    setSavingSettings(true)

    try {

      const adminSecretChanged = settingsForm.admin_auth_source !== 'env' && settingsForm.admin_secret !== loadedAdminSecret

      const updated = await api.updateSettings(settingsForm)

      const mergedSettings = { ...defaultSettingsForm, ...updated }

      setSettingsForm(mergedSettings)

      setLoadedAdminSecret(mergedSettings.admin_secret ?? '')

      if (adminSecretChanged) {

        resetAdminAuthState()

        return

      }

      if (mergedSettings.admin_auth_source !== 'env') {

        setAdminKey(mergedSettings.admin_secret ?? '')

      }

      if (mergedSettings.expired_cleaned && mergedSettings.expired_cleaned > 0) {

        showToast(t('settings.expiredCleanedResult', { count: mergedSettings.expired_cleaned }))

      } else {

        showToast(t('settings.saveSuccess'))

      }

    } catch (error) {

      showToast(`${t('settings.saveFailed')}: ${getErrorMessage(error)}`, 'error')

    } finally {

      setSavingSettings(false)

    }

  }



  const { health } = data

  const isExternalDatabase = settingsForm.database_driver === 'postgres'

  const isExternalCache = settingsForm.cache_driver === 'redis'

  const showConnectionPool = isExternalDatabase || isExternalCache

  const canConfigureRemoteMigration = settingsForm.admin_auth_source === 'env' || settingsForm.admin_secret.trim() !== ''

  return (

    <StateShell

      variant="page"

      loading={loading}

      error={error}

      onRetry={() => void reload()}

      loadingTitle={t('settings.loadingTitle')}

      loadingDescription={t('settings.loadingDesc')}

      errorTitle={t('settings.errorTitle')}

    >

      <>

        <PageHeader

          title={t('settings.title')}

          description={t('settings.description')}

        />



        {/* System Status */}

        <Card className="mb-4">

          <CardContent className="p-6">

            <h3 className="text-base font-semibold text-foreground mb-4">{t('settings.systemStatus')}</h3>

            <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-3.5">

              <div className="flex flex-col gap-1.5 p-3.5 rounded-2xl bg-white/40 border border-border">

                <label className="text-xs font-bold text-muted-foreground">{t('settings.service')}</label>

                <div className="text-[15px] font-semibold">

                  <Badge variant={health?.status === 'ok' ? 'default' : 'destructive'} className="gap-1.5">

                    <span className={`size-1.5 rounded-full ${health?.status === 'ok' ? 'bg-emerald-500' : 'bg-red-400'}`} />

                    {health?.status === 'ok' ? t('common.running') : t('common.error')}

                  </Badge>

                </div>

              </div>

              <div className="flex flex-col gap-1.5 p-3.5 rounded-2xl bg-white/40 border border-border">

                <label className="text-xs font-bold text-muted-foreground">{t('settings.accountsLabel')}</label>

                <div className="text-[15px] font-semibold">{health?.available ?? 0} / {health?.total ?? 0}</div>

              </div>

              <div className="flex flex-col gap-1.5 p-3.5 rounded-2xl bg-white/40 border border-border">

                <label className="text-xs font-bold text-muted-foreground">{settingsForm.database_label}</label>

                <div className="text-[15px] font-semibold">

                  <Badge variant="default" className="gap-1.5">

                    <span className="size-1.5 rounded-full bg-emerald-500" />

                    {isExternalDatabase ? t('common.connected') : t('common.running')}

                  </Badge>

                </div>

              </div>

              <div className="flex flex-col gap-1.5 p-3.5 rounded-2xl bg-white/40 border border-border">

                <label className="text-xs font-bold text-muted-foreground">{settingsForm.cache_label}</label>

                <div className="text-[15px] font-semibold">

                  <Badge variant="default" className="gap-1.5">

                    <span className="size-1.5 rounded-full bg-emerald-500" />

                    {isExternalCache ? t('common.connected') : t('common.running')}

                  </Badge>

                </div>

              </div>

            </div>

          </CardContent>

        </Card>



        {/* Proxy Defaults */}

        <Card className="mb-4">

          <CardContent className="p-6">

            <div className="flex items-center justify-between gap-4 mb-4">

              <h3 className="text-base font-semibold text-foreground">{t('settings.proxyDefaultsTitle')}</h3>

              <p className="text-xs text-muted-foreground max-w-[420px]">

                {t('settings.proxyDefaultsDesc')}

              </p>

            </div>

            <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4">

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.proxyModeDefaultLabel')}</label>

                <Select

                  value={settingsForm.proxy_default_mode ?? 'static'}

                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, proxy_default_mode: value as ProxyMode }))}

                  options={proxyModeOptions}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.proxyModeDefaultDesc')}</p>

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.proxyProtocolLabel')}</label>

                <Select

                  value={settingsForm.proxy_default_protocol ?? 'http'}

                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, proxy_default_protocol: value as ProxyProtocol }))}

                  options={proxyProtocolOptions}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.proxyProtocolDesc')}</p>

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.proxyProviderUrlLabel')}</label>

                <Input

                  placeholder={t('accounts.proxyProviderUrlPlaceholder')}

                  value={settingsForm.proxy_dynamic_provider_url ?? ''}

                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm(f => ({ ...f, proxy_dynamic_provider_url: e.target.value }))}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.proxyProviderUrlDesc')}</p>

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.proxyRotationLabel')}</label>

                <Input

                  type="number"

                  min={0}

                  value={settingsForm.proxy_rotation_hours ?? 0}

                  onChange={(e: ChangeEvent<HTMLInputElement>) => {

                    const parsed = parseInt(e.target.value, 10)

                    setSettingsForm(f => ({ ...f, proxy_rotation_hours: Number.isNaN(parsed) ? 0 : parsed }))

                  }}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.proxyRotationDesc')}</p>

              </div>

            </div>

          </CardContent>

        </Card>



        <Card className="mb-4">

          <CardContent className="p-6">

            <div className="mb-4">

              <h3 className="text-base font-semibold text-foreground">{t('settings.refreshProbeTitle')}</h3>

              <p className="mt-1 text-xs text-muted-foreground max-w-[760px]">{t('settings.refreshProbeDesc')}</p>

            </div>

            <h4 className="text-sm font-semibold text-foreground mb-4">{t('settings.refreshScanTitle')}</h4>

            <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4 mb-6">

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.refreshScanEnabled')}</label>

                <Select

                  value={settingsForm.refresh_scan_enabled ? 'true' : 'false'}

                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, refresh_scan_enabled: value === 'true' }))}

                  options={booleanOptions}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.refreshScanEnabledDesc')}</p>

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.refreshScanIntervalSeconds')}</label>

                <Input

                  type="number"

                  min={1}

                  value={secondsToMinuteValue(settingsForm.refresh_scan_interval_seconds, 120, 1)}

                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm((f) => ({ ...f, refresh_scan_interval_seconds: parseMinutesToSecondsValue(e.target.value, 120, 1) }))}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.refreshScanIntervalSecondsDesc')}</p>

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.refreshPreExpireSeconds')}</label>

                <Input

                  type="number"

                  min={0}

                  value={secondsToMinuteValue(settingsForm.refresh_pre_expire_seconds, 300, 0)}

                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm((f) => ({ ...f, refresh_pre_expire_seconds: parseMinutesToSecondsValue(e.target.value, 300, 0) }))}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.refreshPreExpireSecondsDesc')}</p>

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.refreshMaxConcurrency')}</label>

                <Input

                  type="number"

                  min={1}

                  value={settingsForm.refresh_max_concurrency ?? 10}

                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm((f) => ({ ...f, refresh_max_concurrency: parseNumberValue(e.target.value, 10, 1) }))}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.refreshMaxConcurrencyDesc')}</p>

              </div>

            </div>

            <h4 className="text-sm font-semibold text-foreground mb-4">{t('settings.refreshOnImportTitle')}</h4>

            <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4 mb-6">

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.refreshOnImportEnabled')}</label>

                <Select

                  value={settingsForm.refresh_on_import_enabled ? 'true' : 'false'}

                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, refresh_on_import_enabled: value === 'true' }))}

                  options={booleanOptions}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.refreshOnImportEnabledDesc')}</p>

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.refreshOnImportConcurrency')}</label>

                <Input

                  type="number"

                  min={1}

                  value={settingsForm.refresh_on_import_concurrency ?? 4}

                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm((f) => ({ ...f, refresh_on_import_concurrency: parseNumberValue(e.target.value, 4, 1) }))}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.refreshOnImportConcurrencyDesc')}</p>

              </div>

            </div>

            <h4 className="text-sm font-semibold text-foreground mb-4">{t('settings.usageProbeTitle')}</h4>

            <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4 mb-6">

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.usageProbeEnabled')}</label>

                <Select

                  value={settingsForm.usage_probe_enabled ? 'true' : 'false'}

                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, usage_probe_enabled: value === 'true' }))}

                  options={booleanOptions}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.usageProbeEnabledDesc')}</p>

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.usageProbeStaleAfterSeconds')}</label>

                <Input

                  type="number"

                  min={1}

                  value={settingsForm.usage_probe_stale_after_seconds ?? 600}

                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm((f) => ({ ...f, usage_probe_stale_after_seconds: parseNumberValue(e.target.value, 600, 1) }))}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.usageProbeStaleAfterSecondsDesc')}</p>

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.usageProbeMaxConcurrency')}</label>

                <Input

                  type="number"

                  min={1}

                  value={settingsForm.usage_probe_max_concurrency ?? 4}

                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm((f) => ({ ...f, usage_probe_max_concurrency: parseNumberValue(e.target.value, 4, 1) }))}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.usageProbeMaxConcurrencyDesc')}</p>

              </div>

            </div>

            <h4 className="text-sm font-semibold text-foreground mb-4">{t('settings.recoveryProbeTitle')}</h4>

            <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4">

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.recoveryProbeEnabled')}</label>

                <Select

                  value={settingsForm.recovery_probe_enabled ? 'true' : 'false'}

                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, recovery_probe_enabled: value === 'true' }))}

                  options={booleanOptions}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.recoveryProbeEnabledDesc')}</p>

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.recoveryProbeMinIntervalSeconds')}</label>

                <Input

                  type="number"

                  min={1}

                  value={settingsForm.recovery_probe_min_interval_seconds ?? 1800}

                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm((f) => ({ ...f, recovery_probe_min_interval_seconds: parseNumberValue(e.target.value, 1800, 1) }))}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.recoveryProbeMinIntervalSecondsDesc')}</p>

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.recoveryProbeMaxConcurrency')}</label>

                <Input

                  type="number"

                  min={1}

                  value={settingsForm.recovery_probe_max_concurrency ?? 2}

                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm((f) => ({ ...f, recovery_probe_max_concurrency: parseNumberValue(e.target.value, 2, 1) }))}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.recoveryProbeMaxConcurrencyDesc')}</p>

              </div>

            </div>

          </CardContent>

        </Card>



        {/* Protection Settings */}

        <Card className="mb-4">

          <CardContent className="p-6">

            <h3 className="text-base font-semibold text-foreground mb-4">{t('settings.trafficProtection')}</h3>

            <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4 mb-4">

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.maxConcurrency')}</label>

                <Input

                  type="number"

                  min={1}

                  max={50}

                  value={settingsForm.max_concurrency}

                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm(f => ({ ...f, max_concurrency: parseInt(e.target.value) || 1 }))}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.maxConcurrencyRange')}</p>

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.globalRpm')}</label>

                <Input

                  type="number"

                  min={0}

                  value={settingsForm.global_rpm}

                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm(f => ({ ...f, global_rpm: parseInt(e.target.value) || 0 }))}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.globalRpmRange')}</p>

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.maxRetries')}</label>

                <Input

                  type="number"

                  min={0}

                  max={10}

                  value={settingsForm.max_retries}

                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm(f => ({ ...f, max_retries: parseInt(e.target.value) || 0 }))}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.maxRetriesRange')}</p>

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.testModelLabel')}</label>

                <Select

                  value={settingsForm.test_model}

                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, test_model: value }))}

                  options={modelList.map((model) => ({ label: model, value: model }))}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.testModelHint')}</p>

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.testConcurrency')}</label>

                <Input

                  type="number"

                  min={1}

                  max={200}

                  value={settingsForm.test_concurrency}

                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm(f => ({ ...f, test_concurrency: parseInt(e.target.value) || 1 }))}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.testConcurrencyRange')}</p>

              </div>

            </div>

            {showConnectionPool ? (

              <>

                <h3 className="text-base font-semibold text-foreground mb-4 mt-6">{t('settings.connectionPool')}</h3>

                <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4 mb-4">

                  {isExternalDatabase ? (

                    <div>

                      <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.pgMaxConns')}</label>

                      <Input

                        type="number"

                        min={5}

                        max={500}

                        value={settingsForm.pg_max_conns}

                        onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm(f => ({ ...f, pg_max_conns: parseInt(e.target.value) || 50 }))}

                      />

                      <p className="text-xs text-muted-foreground mt-1">{t('settings.pgMaxConnsRange')}</p>

                    </div>

                  ) : null}

                  {isExternalCache ? (

                    <div>

                      <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.redisPoolSize')}</label>

                      <Input

                        type="number"

                        min={5}

                        max={500}

                        value={settingsForm.redis_pool_size}

                        onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm(f => ({ ...f, redis_pool_size: parseInt(e.target.value) || 30 }))}

                      />

                      <p className="text-xs text-muted-foreground mt-1">{t('settings.redisPoolSizeRange')}</p>

                    </div>

                  ) : null}

                </div>

              </>

            ) : null}

            <h3 className="text-base font-semibold text-foreground mb-4 mt-6">{t('settings.autoCleanup')}</h3>

            <div className="grid grid-cols-[repeat(auto-fit,minmax(280px,1fr))] gap-4 mb-4">

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.autoCleanUnauthorized')}</label>

                <Select

                  value={settingsForm.auto_clean_unauthorized ? 'true' : 'false'}

                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, auto_clean_unauthorized: value === 'true' }))}

                  options={booleanOptions}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.autoCleanUnauthorizedDesc')}</p>

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.autoCleanRateLimited')}</label>

                <Select

                  value={settingsForm.auto_clean_rate_limited ? 'true' : 'false'}

                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, auto_clean_rate_limited: value === 'true' }))}

                  options={booleanOptions}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.autoCleanRateLimitedDesc')}</p>

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.autoCleanFullUsage')}</label>

                <Select

                  value={settingsForm.auto_clean_full_usage ? 'true' : 'false'}

                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, auto_clean_full_usage: value === 'true' }))}

                  options={booleanOptions}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.autoCleanFullUsageDesc')}</p>

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.autoCleanError')}</label>

                <Select

                  value={settingsForm.auto_clean_error ? 'true' : 'false'}

                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, auto_clean_error: value === 'true' }))}

                  options={booleanOptions}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.autoCleanErrorDesc')}</p>

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.autoCleanExpired')}</label>

                <Select

                  value={settingsForm.auto_clean_expired ? 'true' : 'false'}

                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, auto_clean_expired: value === 'true' }))}

                  options={booleanOptions}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.autoCleanExpiredDesc')}</p>

              </div>

            </div>

            <h3 className="text-base font-semibold text-foreground mb-4 mt-6">{t('settings.display')}</h3>

            <div className="grid grid-cols-[repeat(auto-fit,minmax(280px,1fr))] gap-4 mb-4">

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.timezone')}</label>

                <Select

                  value={getTimezone()}

                  onValueChange={(value) => {

                    setTimezone(value)

                    window.location.reload()

                  }}

                  options={[

                    { label: t('settings.timezoneAuto'), value: Intl.DateTimeFormat().resolvedOptions().timeZone },

                    { label: '(UTC) UTC', value: 'UTC' },

                    { label: '(GMT+08:00) Asia/Shanghai', value: 'Asia/Shanghai' },

                    { label: '(GMT+09:00) Asia/Tokyo', value: 'Asia/Tokyo' },

                    { label: '(GMT+09:00) Asia/Seoul', value: 'Asia/Seoul' },

                    { label: '(GMT+08:00) Asia/Singapore', value: 'Asia/Singapore' },

                    { label: '(GMT+08:00) Asia/Hong_Kong', value: 'Asia/Hong_Kong' },

                    { label: '(GMT+08:00) Asia/Taipei', value: 'Asia/Taipei' },

                    { label: '(GMT+07:00) Asia/Bangkok', value: 'Asia/Bangkok' },

                    { label: '(GMT+04:00) Asia/Dubai', value: 'Asia/Dubai' },

                    { label: '(GMT+05:30) Asia/Kolkata', value: 'Asia/Kolkata' },

                    { label: '(GMT+01:00) Europe/London', value: 'Europe/London' },

                    { label: '(GMT+02:00) Europe/Paris', value: 'Europe/Paris' },

                    { label: '(GMT+02:00) Europe/Berlin', value: 'Europe/Berlin' },

                    { label: '(GMT+03:00) Europe/Moscow', value: 'Europe/Moscow' },

                    { label: '(GMT+02:00) Europe/Amsterdam', value: 'Europe/Amsterdam' },

                    { label: '(GMT+02:00) Europe/Rome', value: 'Europe/Rome' },

                    { label: '(GMT-04:00) America/New_York', value: 'America/New_York' },

                    { label: '(GMT-07:00) America/Los_Angeles', value: 'America/Los_Angeles' },

                    { label: '(GMT-05:00) America/Chicago', value: 'America/Chicago' },

                    { label: '(GMT-03:00) America/Sao_Paulo', value: 'America/Sao_Paulo' },

                    { label: '(GMT+10:00) Australia/Sydney', value: 'Australia/Sydney' },

                    { label: '(GMT+12:00) Pacific/Auckland', value: 'Pacific/Auckland' },

                  ]}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.timezoneDesc')}</p>

              </div>

            </div>

            <h3 className="text-base font-semibold text-foreground mb-4 mt-6">{t('settings.security')}</h3>

            <div className="grid grid-cols-[repeat(auto-fit,minmax(280px,1fr))] gap-4 mb-4">

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.adminSecret')}</label>

                <Input

                  type="text"

                  placeholder={t('settings.adminSecretPlaceholder')}

                  value={settingsForm.admin_secret}

                  disabled={settingsForm.admin_auth_source === 'env'}

                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm(f => {

                    const nextSecret = e.target.value

                    return {

                      ...f,

                      admin_secret: nextSecret,

                      allow_remote_migration: nextSecret.trim() === '' ? false : f.allow_remote_migration,

                    }

                  })}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.adminSecretDesc')}</p>

                {settingsForm.admin_auth_source === 'env' ? (

                  <p className="text-xs text-amber-600 mt-1">{t('settings.adminSecretEnvOverride')}</p>

                ) : null}

              </div>

              <div>

                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.allowRemoteMigration')}</label>

                <Select

                  value={settingsForm.allow_remote_migration ? 'true' : 'false'}

                  disabled={!canConfigureRemoteMigration}

                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, allow_remote_migration: value === 'true' }))}

                  options={booleanOptions}

                />

                <p className="text-xs text-muted-foreground mt-1">{t('settings.allowRemoteMigrationDesc')}</p>

                {!canConfigureRemoteMigration ? (

                  <p className="text-xs text-amber-600 mt-1">{t('settings.allowRemoteMigrationRequiresSecret')}</p>

                ) : null}

              </div>

            </div>

            <Button onClick={() => void handleSaveSettings()} disabled={savingSettings}>

              {savingSettings ? t('common.saving') : t('settings.saveSettings')}

            </Button>

          </CardContent>

        </Card>




        <ToastNotice toast={toast} />

      </>

    </StateShell>

  )

}

