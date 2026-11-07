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
import { beforeEach, expect, test } from 'vitest'

import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'

import { formatModelTestReason } from '../report-reason'

beforeEach(async () => {
  i18next.addResourceBundle('en', 'translation', en.translation, true, true)
  i18next.addResourceBundle('zhCN', 'translation', zh.translation, true, true)
  await i18next.changeLanguage('en')
})

test.each([
  'accepted',
  'insufficient_numbers',
  'request_failed',
  'response_too_large',
  'non_model_response',
  'strict3_requires_three_complete_samples',
  'no_valid_samples',
])(
  'known sample reason %s is translated using the active interface language',
  async (reason) => {
    const key = `monitoring.reason.${reason}` as keyof typeof en.translation
    expect(formatModelTestReason(reason, i18next.t)).toBe(en.translation[key])
    await i18next.changeLanguage('zhCN')
    expect(formatModelTestReason(reason, i18next.t)).toBe(zh.translation[key])
  }
)

test.each([
  {
    reason: 'Ranked 1 valid sample. Confidence requires three valid samples.',
    count: 1,
  },
  {
    reason: 'Ranked 2 valid samples. Confidence requires three valid samples.',
    count: 2,
  },
])(
  'partial assessment with $count valid samples retains the actual sample count',
  ({ reason, count }) => {
    expect(formatModelTestReason(reason, i18next.t)).toBe(
      i18next.t(
        'Valid samples used: {{count}}. Probabilities require three valid samples.',
        { count }
      )
    )
  }
)

test('an unknown reason code has a readable fallback while a human-readable explanation is retained', () => {
  expect(formatModelTestReason('future_assessment_code', i18next.t)).toBe(
    i18next.t('No assessment is available for this response.')
  )
  expect(
    formatModelTestReason('The second sample was truncated.', i18next.t)
  ).toBe('The second sample was truncated.')
})
