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
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import { api } from '@/lib/api'

import { AffiliatePage } from '../index'

function readResponses({
  quota = 1000000,
  compliance = true,
  code = 'invite-demo',
}: {
  quota?: number
  compliance?: boolean
  code?: string
} = {}) {
  return vi.spyOn(api, 'get').mockImplementation(async (url) => {
    let data: unknown = {}
    if (url === '/api/user/aff') data = code
    if (url === '/api/user/wallet') {
      data = {
        total: 0,
        paid: 0,
        bonus: 0,
        reward: 0,
        referral_available_quota: quota,
        referral_total_quota: 1500000,
        referral_invite_count: 7,
        rules: {
          referral_bonus_rate: 0.0333,
          referral_bonus_enabled: compliance,
        },
      }
    }
    if (url === '/api/user/topup/info') {
      data = { payment_compliance_confirmed: compliance }
    }
    if (url.startsWith('/api/user/referral/rewards?')) {
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
  vi.restoreAllMocks()
  i18next.addResourceBundle('en', 'translation', en.translation, true, true)
  await i18next.changeLanguage('en')
})

describe('ordinary referral page', () => {
  test('shows configured stats and a visible empty ledger without enabling a zero-reward transfer', async () => {
    readResponses({ quota: 0 })
    const post = vi.spyOn(api, 'post')
    renderPage()
    expect(await screen.findByText('Current referral rate')).toBeVisible()
    expect(screen.getByText('3.33%')).toBeVisible()
    expect(screen.getByText('7', { exact: true })).toBeVisible()
    expect(
      screen.getByRole('textbox', { name: 'Invitation code' })
    ).toHaveValue('invite-demo')
    expect(
      screen.getByRole('button', { name: 'Transfer to Balance' })
    ).toBeDisabled()
    expect(
      screen.getByRole('heading', { name: 'Commission Records' })
    ).toBeVisible()
    expect(await screen.findByText(/No commission records yet/)).toBeVisible()
    expect(post).not.toHaveBeenCalled()
  })

  test('copies the exact displayed invitation values and reloads with reads only', async () => {
    const get = readResponses()
    const post = vi.spyOn(api, 'post')
    const user = userEvent.setup()
    renderPage()
    const link = (await screen.findByRole('textbox', {
      name: 'Invitation Link',
    })) as HTMLInputElement
    expect(link.value).toBe(`${window.location.origin}/sign-up?aff=invite-demo`)
    expect(link).toHaveAttribute('readonly')
    expect(
      screen.getByRole('textbox', { name: 'Invitation code' })
    ).toHaveAttribute('readonly')
    await user.click(
      screen.getByRole('button', { name: 'Copy invitation code' })
    )
    expect(await navigator.clipboard.readText()).toBe('invite-demo')
    await user.click(screen.getByRole('button', { name: 'Copy referral link' }))
    expect(await navigator.clipboard.readText()).toBe(link.value)
    get.mockClear()
    await user.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() =>
      expect(get.mock.calls.map(([url]) => url.split('?')[0])).toEqual(
        expect.arrayContaining([
          '/api/user/wallet',
          '/api/user/topup/info',
          '/api/user/aff',
          '/api/user/referral/rewards',
        ])
      )
    )
    expect(post).not.toHaveBeenCalled()
  })

  test('confirms a bounded transfer only through the idempotent ordinary-reward endpoint', async () => {
    readResponses()
    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValue({ data: { success: true, data: {} } })
    const user = userEvent.setup()
    renderPage()
    const open = await screen.findByRole('button', {
      name: 'Transfer to Balance',
    })
    await user.click(open)
    const dialog = await screen.findByRole('dialog', {
      name: 'Transfer Rewards',
    })
    expect(within(dialog).queryByRole('combobox')).not.toBeInTheDocument()
    const amount = within(dialog).getByRole('spinbutton', {
      name: 'Transfer Amount',
    })
    const confirm = within(dialog).getByRole('button', { name: 'Transfer' })
    expect(post).not.toHaveBeenCalled()
    fireEvent.change(amount, { target: { value: '3' } })
    expect(confirm).toBeDisabled()
    expect(post).not.toHaveBeenCalled()
    fireEvent.change(amount, { target: { value: '1' } })
    await user.click(confirm)
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

  test('gates positive rewards by compliance and shows only the existing manual payout path', async () => {
    readResponses({ compliance: false })
    const post = vi.spyOn(api, 'post')
    renderPage()
    expect(
      await screen.findByRole('button', { name: 'Transfer to Balance' })
    ).toBeDisabled()
    expect(
      screen.getByText(/administrator confirms compliance terms/)
    ).toBeVisible()
    expect(
      screen.getByRole('heading', { name: 'Contact support to withdraw' })
    ).toBeVisible()
    expect(screen.getByRole('link', { name: 'Wallet' })).toHaveAttribute(
      'href',
      '/wallet'
    )
    expect(
      screen.getByText(
        /Minimum \$20 combined, once per calendar month, with a 2% fee/
      )
    ).toBeVisible()
    expect(
      screen.queryByRole('button', { name: /withdraw/i })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByText(/USDT|TRC20|available U/i)
    ).not.toBeInTheDocument()
    expect(post).not.toHaveBeenCalled()
  })

  test('does not show a copyable placeholder when the invitation code is empty', async () => {
    readResponses({ code: '' })
    renderPage()
    expect(await screen.findByText('Not available')).toBeVisible()
    expect(
      screen.queryByRole('button', { name: 'Copy referral link' })
    ).not.toBeInTheDocument()
  })

  test('an invitation-code failure offers retry instead of an empty copyable link', async () => {
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
