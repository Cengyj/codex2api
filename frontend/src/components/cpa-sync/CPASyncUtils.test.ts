import { describe, expect, it } from 'vitest'
import { AlertCircle, CheckCircle2 } from 'lucide-react'
import {
  buildMissingFieldsHint,
  formatNextRunCountdown,
  getMihomoServiceSettingsSignature,
  getConnectionPresentation,
  localizeConnectionMessage,
} from './CPASyncUtils'

const t = (key: string, options?: Record<string, unknown>) => {
  switch (key) {
    case 'cpaSync.missingFieldsHint':
      return `missing:${String(options?.fields ?? '')}`
    case 'cpaSync.cpaBaseUrl':
      return 'CPA 地址'
    case 'cpaSync.cpaAdminKey':
      return 'CPA 密钥'
    case 'cpaSync.mihomoBaseUrl':
      return 'Mihomo 地址'
    case 'cpaSync.mihomoSecret':
      return 'Mihomo 密钥'
    case 'cpaSync.testing':
      return '测试中'
    case 'cpaSync.incomplete':
      return '未完成'
    case 'cpaSync.pendingRetest':
      return '待重测'
    case 'cpaSync.connectionOk':
      return '连接正常'
    case 'cpaSync.connectionFailed':
      return '连接失败'
    case 'cpaSync.unknown':
      return '未知'
    case 'cpaSync.nextRunDisabled':
      return '已停用'
    case 'cpaSync.runningNow':
      return '执行中'
    case 'cpaSync.nextRunPending':
      return '等待中'
    case 'cpaSync.nextRunSoon':
      return '即将执行'
    case 'common.countdownHoursMinutes':
      return `${options?.hours}小时${options?.minutes}分`
    case 'common.countdownMinutesSeconds':
      return `${options?.minutes}分${options?.seconds}秒`
    case 'common.inSecondsLong':
      return `${options?.count}秒`
    case 'cpaSync.cpaAuthFilesFound':
      return `找到${options?.count}个授权文件`
    case 'cpaSync.parseMihomoFailed':
      return `解析失败:${options?.reason}`
    default:
      return key
  }
}

describe('CPASyncUtils', () => {
  it('formats missing fields hint with localized separator', () => {
    expect(buildMissingFieldsHint(['cpa_base_url', 'mihomo_secret'], t)).toBe('missing:CPA 地址 / Mihomo 密钥')
  })

  it('computes next run countdown across states', () => {
    expect(formatNextRunCountdown(undefined, Date.now(), false, false, t)).toBe('已停用')
    expect(formatNextRunCountdown(undefined, Date.now(), true, true, t)).toBe('执行中')
    expect(formatNextRunCountdown(new Date(Date.now() + 65_000).toISOString(), Date.now(), true, false, t)).toBe('1分5秒')
  })

  it('localizes backend connection messages and missing config', () => {
    const message = localizeConnectionMessage('CPA connection OK, found 3 auth files, missing config: cpa_base_url, mihomo_secret', t)
    expect(message).toContain('找到3个授权文件')
    expect(message).toContain('missing:CPA 地址 / Mihomo 密钥')
  })

  it('builds non-reversible signatures instead of raw secret JSON', () => {
    const signature = getMihomoServiceSettingsSignature({
      mihomo_base_url: 'http://mihomo.local',
      mihomo_secret: 'super-secret-token',
    })

    expect(signature).toMatch(/^fnv1a64:[0-9a-f]{16}$/)
    expect(signature).not.toContain('super-secret-token')
    expect(signature).not.toContain('mihomo.local')
  })

  it('returns expected connection presentation priorities', () => {
    const loading = getConnectionPresentation({ ok: true, message: '', tested_at: '', details: {} }, [], true, false, t)
    expect(loading.label).toBe('测试中')

    const incomplete = getConnectionPresentation({ ok: true, message: '', tested_at: '', details: {} }, ['cpa_base_url'], false, false, t)
    expect(incomplete.label).toBe('未完成')
    expect(incomplete.icon).toBe(AlertCircle)

    const success = getConnectionPresentation({ ok: true, message: '', tested_at: '', details: {} }, [], false, false, t)
    expect(success.label).toBe('连接正常')
    expect(success.icon).toBe(CheckCircle2)
  })
})
