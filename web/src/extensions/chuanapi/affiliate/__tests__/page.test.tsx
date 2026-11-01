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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import { api } from '@/lib/api'

import { AffiliatePage } from '../index'

function readResponses(quota = 1000000) {
  return vi.spyOn(api, 'get').mockImplementation(async (url) => {
    let data: unknown = {}
    if (url === '/api/user/self') {
      data = {
        id: 1,
        username: 'demo',
        group: 'default',
        quota: 0,
        used_quota: 0,
        request_count: 0,
        aff_count: 2,
        aff_quota: quota,
        aff_history_quota: 1500000,
      }
    }
    if (url === '/api/user/aff') data = 'invite-demo'
    if (url === '/api/user/wallet') {
      data = {
        total: 0,
        paid: 0,
        bonus: 0,
        reward: 0,
        rules: { referral_bonus_rate: 0.0333 },
      }
    }
    if (url === '/api/user/topup/info') {
      data = { payment_compliance_confirmed: true }
    }
    if (String(url).includes('/referral/rewards')) {
      data = { items: [], total: 0 }
    }
    return { data: { success: true, data } }
  })
}
function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  return render(
    <QueryClientProvider client={client}>
      <AffiliatePage />
    </QueryClientProvider>
  )
}
beforeEach(async () => {
  i18next.addResourceBundle('en', 'translation', en.translation, true, true)
  await i18next.changeLanguage('en')
})
describe('ordinary referral page', () => {
  test('shows the configured rate and invitation code, with an explicit disabled transfer for zero rewards', async () => {
    readResponses(0)
    const post = vi.spyOn(api, 'post')
    renderPage()
    expect(await screen.findByText('Current referral rate')).toBeVisible()
    expect(screen.getByText('3.33%')).toBeVisible()
    expect(screen.getByText('invite-demo')).toBeVisible()
    expect(
      screen.getByRole('button', { name: 'Transfer to Balance' })
    ).toBeDisabled()
    expect(screen.getByText(/No commission records yet/)).toBeVisible()
    expect(post).not.toHaveBeenCalled()
  })
  test('opens the shared confirmation and transfers only through the idempotent ordinary-reward endpoint', async () => {
    readResponses()
    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValue({ data: { success: true, data: {} } })
    const user = userEvent.setup()
    renderPage()
    await user.click(
      await screen.findByRole('button', { name: 'Transfer to Balance' })
    )
    const dialog = await screen.findByRole('dialog', {
      name: 'Transfer Rewards',
    })
    expect(post).not.toHaveBeenCalled()
    await user.click(within(dialog).getByRole('button', { name: 'Transfer' }))
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post).toHaveBeenCalledWith(
      '/api/user/aff_transfer',
      { quota: 500000 },
      expect.objectContaining({
        headers: { 'Idempotency-Key': expect.any(String) },
      })
    )
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
  })
  test('an invitation-code failure shows a retry instead of an empty copyable link', async () => {
    const get = readResponses()
    const original = get.getMockImplementation()
    get.mockImplementation(async (url, options) => {
      if (url === '/api/user/aff') {
        return { data: { success: false, message: 'Unavailable' } }
      }
      return original?.(url, options)
    })
    renderPage()
    expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible()
    expect(
      screen.queryByRole('button', { name: 'Copy referral link' })
    ).not.toBeInTheDocument()
  })
})
