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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'

import { BudgetDialog } from '../budget-dialog'
import type { TestEstimate } from '../types'

const quote: TestEstimate = {
  token_id: 7,
  model: 'test',
  group: 'default',
  max_output_tokens: 2048,
  estimated_quota: 50,
  quota_per_unit: 500000,
  estimate_is_cap: false,
  timeout_seconds: 90,
  benchmark_id: 'pelican-static-v1',
  confirmation_token: 'ticket',
  expires_at: 1,
}
beforeEach(async () => {
  i18next.addResourceBundle('en', 'translation', en.translation, true, true)
  await i18next.changeLanguage('en')
})
describe('test budget confirmation', () => {
  test('expired estimates cannot start a paid run and explain how to recover', () => {
    const confirm = vi.fn()
    render(
      <BudgetDialog
        quote={quote}
        onClose={() => undefined}
        onConfirm={confirm}
      />
    )
    expect(
      screen.getByRole('button', { name: 'Accept charges and run once' })
    ).toBeDisabled()
    expect(
      screen.getByText('Estimate expired. Close and request a new estimate.')
    ).toBeVisible()
    expect(confirm).not.toHaveBeenCalled()
  })
  test('keyboard cancellation closes the quote without approving a run', async () => {
    const confirm = vi.fn()
    const close = vi.fn()
    render(
      <BudgetDialog
        quote={{ ...quote, expires_at: Math.floor(Date.now() / 1000) + 300 }}
        onClose={close}
        onConfirm={confirm}
      />
    )
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(close).toHaveBeenCalled()
    expect(confirm).not.toHaveBeenCalled()
  })
})

test('a no-limit quote states there is no output cap and still requires explicit paid confirmation', () => {
  const confirm = vi.fn()
  render(
    <BudgetDialog
      quote={{
        ...quote,
        max_output_tokens: 0,
        estimated_quota: null,
        expires_at: Math.floor(Date.now() / 1000) + 300,
      }}
      onClose={() => undefined}
      onConfirm={confirm}
    />
  )
  expect(
    screen.getByText(
      i18next.t('fingerprint.unlimitedOutput', {
        seconds: quote.timeout_seconds,
      })
    )
  ).toBeVisible()
  expect(screen.queryByText(/Output limit: 0 tokens/)).not.toBeInTheDocument()
  expect(
    screen.getByRole('button', { name: 'Accept charges and run once' })
  ).toBeEnabled()
  expect(confirm).not.toHaveBeenCalled()
})
