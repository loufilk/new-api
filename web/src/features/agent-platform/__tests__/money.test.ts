/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { describe, expect, it } from 'vitest'

import { toIntlLocale } from '@/i18n/languages'
import { useSystemConfigStore } from '@/stores/system-config-store'

import {
  agentRepaymentAmount,
  formatMinorMoney,
  minorMoneyInput,
  parseMinorMoney,
  parseAgentQuota,
  formatAgentCash,
  formatAgentQuota,
} from '../lib/money'

describe('original-currency money', () => {
  it('keeps sub-fen debt visible and rounds only the actual payment up to fen', () => {
    const previous = useSystemConfigStore.getState().config
    try {
      useSystemConfigStore.getState().setConfig({
        currency: { ...previous.currency, quotaPerUnit: 500_000 },
      })
      expect(formatAgentCash(4500)).toBe('¥0.009')
      expect(formatAgentCash(2000)).toBe('¥0.004')
      expect(formatAgentCash(1)).toBe('¥0.000002')
      expect(formatAgentCash(5000)).toBe('¥0.01')
      expect(agentRepaymentAmount(1)).toBe('0.01')
      expect(agentRepaymentAmount(4500)).toBe('0.01')
      expect(agentRepaymentAmount(4_525_001)).toBe('9.06')
      expect(agentRepaymentAmount(4_525_000)).toBe('9.05')
      expect(agentRepaymentAmount(0)).toBe('')
      expect(agentRepaymentAmount(-1)).toBe('')
      expect(agentRepaymentAmount(Number.MAX_SAFE_INTEGER + 1)).toBe('')
    } finally {
      useSystemConfigStore.getState().setConfig(previous)
    }
  })
  it('agent face values and CNY debt ignore the wallet display exchange rate', () => {
    const previous = useSystemConfigStore.getState().config
    try {
      useSystemConfigStore.getState().setConfig({
        currency: {
          displayInCurrency: true,
          customCurrencySymbol: 'X',
          customCurrencyExchangeRate: 7.3,
          quotaDisplayType: 'CNY',
          quotaPerUnit: 500_000,
          usdExchangeRate: 7.3,
        },
      })
      expect(parseAgentQuota('100.01')).toBe(50_005_000)
      expect(parseAgentQuota('9.05')).toBe(4_525_000)
      expect(formatAgentCash(4_525_000)).toBe('¥9.05')
      expect(formatAgentQuota(50_005_000)).toBe('$100.01')
      expect(parseAgentQuota('100.001')).toBeNull()
      expect(parseAgentQuota('9007199254740992')).toBeNull()
    } finally {
      useSystemConfigStore.getState().setConfig(previous)
    }
  })
  it('parses cents without binary rounding and retains int64 precision', () => {
    expect(parseMinorMoney('0.29', 2)).toBe('29')
    expect(parseMinorMoney('92233720368547758.07', 2)).toBe(
      '9223372036854775807'
    )
    expect(minorMoneyInput('9223372036854775807', 2)).toBe(
      '92233720368547758.07'
    )
  })
  it.each([
    '1.001',
    '1e3',
    '-1',
    '0',
    'NaN',
    'Infinity',
    '92233720368547758.08',
  ])('rejects invalid or unsafe monetary input %s', (value) => {
    expect(parseMinorMoney(value, 2)).toBeNull()
  })
  it('does not apply current exchange rates to original payment amounts', () => {
    expect(formatMinorMoney('12345', 'CNY', 2, 'en')).toBe('CNY 123.45')
    expect(formatMinorMoney('9223372036854775807', 'USD', 2, 'en')).toBe(
      'USD 92,233,720,368,547,758.07'
    )
    expect(formatMinorMoney('-1', 'USD', 2, 'en')).toBe('-USD 0.01')
  })
  it.each(['zhCN', 'zhTW', 'en', 'fr', 'ru', 'ja', 'vi', 'invalid_locale'])(
    'formats a fixed source amount for interface locale %s',
    (language) => {
      expect(
        formatMinorMoney('12345', 'USD', 2, toIntlLocale(language))
      ).toContain('USD')
    }
  )
  it('keeps integer currencies exact and rejects malformed server amounts', () => {
    expect(parseMinorMoney('123', 0)).toBe('123')
    expect(parseMinorMoney('123.0', 0)).toBeNull()
    expect(formatMinorMoney('123', 'JPY', 0, 'ja')).toContain('123')
    expect(formatMinorMoney('12e3', 'USD', 2, 'en')).toBe('—')
  })
})
