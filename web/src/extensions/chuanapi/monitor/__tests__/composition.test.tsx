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
import i18next from 'i18next'
import { describe, expect, test } from 'vitest'

import '@/extensions/bootstrap'
import { extensionMonitorPage } from '@/extensions'

import { chuanapiMonitorNavigationExtension } from '../../navigation'
import { resultStatusKey, resultTime } from '../result-state'
import type { MonitorResult } from '../types'

describe('Model monitor composition', () => {
  test('formats a Unix timestamp with its time zone and rejects invalid dates', () => {
    expect(resultTime(1792083600, 'en-GB')).toBe('16/10, 01:00 GMT+8')
    expect(resultTime(Number.NaN, 'en-GB')).toBe('—')
  })

  test('registers a lazy page and one internal navigation entry without changing existing links', () => {
    expect(extensionMonitorPage().id).toBe('chuanapi/monitor')
    expect(typeof extensionMonitorPage().page).toBe('function')
    const links = [{ title: 'Docs', href: '/help' }]
    const context = { isAuthenticated: false, status: null }
    const applied = chuanapiMonitorNavigationExtension.apply(links, context)
    expect(applied).toEqual([
      { title: i18next.t('monitor.title'), href: '/model-monitor' },
      ...links,
    ])
    expect(chuanapiMonitorNavigationExtension.apply(applied, context)).toEqual(
      applied
    )
    expect(links).toHaveLength(1)
  })

  test.each([
    ['ready', 'passed', 'monitor.passed'],
    ['ready', 'failed', 'monitor.notPassed'],
    ['ready', 'suspicious', 'monitor.needsVerification'],
    ['ready', '', 'monitor.awaitingReview'],
    ['incorrect', '', 'monitor.notPassed'],
    ['failed', '', 'monitor.requestFailed'],
    ['running', '', 'monitor.running'],
    ['queued', '', 'monitor.queued'],
    ['unrecognized', '', 'monitor.unknown'],
  ])('maps %s with review %s to %s', (status, quality_status, expected) => {
    expect(resultStatusKey({ status, quality_status } as MonitorResult)).toBe(
      expected
    )
  })
})
