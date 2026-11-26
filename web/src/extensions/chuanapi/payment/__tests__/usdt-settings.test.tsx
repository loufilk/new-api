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
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { UsdtSettingsSection } from '../usdt-settings-section'

const policy = {
  enabled: false,
  min_amount: '10.00',
  max_amount: '10000.00',
  configured: true,
  networks: [
    { id: 'bsc', enabled: false, bonus_bps: 0 },
    { id: 'polygon', enabled: false, bonus_bps: 0 },
    { id: 'tron', enabled: false, bonus_bps: 0 },
  ],
}

function renderUsdt(
  options: { configured?: boolean; compliance?: boolean } = {}
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { ...policy, configured: options.configured ?? true },
    },
  })
  render(
    <QueryClientProvider client={client}>
      <UsdtSettingsSection complianceConfirmed={options.compliance ?? true} />
    </QueryClientProvider>
  )
  return { user: userEvent.setup(), get }
}

const trigger = () => screen.getByRole('button', { name: 'USDT recharge' })
const settings = () => screen.findByRole('form', { name: 'USDT recharge' })
afterEach(() => vi.restoreAllMocks())

describe('administrator USDT recharge settings', () => {
  it('loads initially inactive, zero-bonus settings when opened by keyboard', async () => {
    const { user, get } = renderUsdt()
    expect(trigger()).toHaveAttribute('aria-expanded', 'false')
    expect(get).not.toHaveBeenCalled()
    await user.tab()
    expect(trigger()).toHaveFocus()
    await user.keyboard('{Enter}')
    const form = await settings()
    expect(trigger()).toHaveAttribute('aria-expanded', 'true')
    expect(get).toHaveBeenCalledWith('/api/option/usdt', expect.any(Object))
    expect(
      within(form).getByRole('switch', { name: 'Enable USDT recharge' })
    ).not.toBeChecked()
    expect(
      within(form).getByRole('textbox', { name: 'USDT-BSC bonus (%)' })
    ).toHaveValue('0.00')
    expect(
      within(form).getByRole('button', { name: 'Save USDT settings' })
    ).toBeDisabled()
    expect(form.querySelector('input[type="password"]')).toBeNull()
  })

  it('writes only the USDT policy and exact basis points when saving', async () => {
    const { user } = renderUsdt()
    const expected = {
      enabled: true,
      min_amount: '10.00',
      max_amount: '10000.00',
      networks: [
        { id: 'bsc', enabled: false, bonus_bps: 0 },
        { id: 'polygon', enabled: true, bonus_bps: 103 },
        { id: 'tron', enabled: false, bonus_bps: 0 },
      ],
    }
    const put = vi.spyOn(api, 'put').mockResolvedValue({
      data: { success: true, data: { ...expected, configured: true } },
    })
    await user.click(trigger())
    const form = within(await settings())
    await user.click(form.getByRole('switch', { name: 'Enable USDT recharge' }))
    await user.click(form.getByRole('switch', { name: 'USDT-POLYGON' }))
    const bonus = form.getByRole('textbox', { name: 'USDT-POLYGON bonus (%)' })
    await user.clear(bonus)
    await user.type(bonus, '1.03')
    const save = form.getByRole('button', { name: 'Save USDT settings' })
    await user.click(save)
    await waitFor(() => expect(save).toBeDisabled())
    expect(put).toHaveBeenCalledExactlyOnceWith('/api/option/usdt', expected)
  })

  it.each([
    [{ configured: false }, 'Gateway configuration missing'],
    [
      { compliance: false },
      'Confirm the payment compliance terms before enabling USDT.',
    ],
  ] as const)(
    'blocks activation when a prerequisite is missing (%s)',
    async (options, message) => {
      const { user } = renderUsdt(options)
      await user.click(trigger())
      const form = within(await settings())
      const toggle = form.getByRole('switch', { name: 'Enable USDT recharge' })
      expect(toggle).toHaveAttribute('aria-disabled', 'true')
      await user.click(toggle)
      expect(toggle).not.toBeChecked()
      expect(form.getByText(message)).toBeVisible()
    }
  )

  it.each([
    ['Maximum recharge (CNY)', '9.99', 'Maximum cannot be lower than minimum.'],
    [
      'Minimum recharge (CNY)',
      '0.01',
      'Enter 0.02 to 1,000,000 CNY with up to two decimal places.',
    ],
  ])(
    'reports invalid %s input %s without sending a write',
    async (label, value, message) => {
      const { user } = renderUsdt()
      const put = vi.spyOn(api, 'put')
      await user.click(trigger())
      const form = within(await settings())
      const input = form.getByRole('textbox', { name: label })
      await user.clear(input)
      await user.type(input, value)
      await user.click(form.getByRole('button', { name: 'Save USDT settings' }))
      expect(await form.findByText(message)).toBeVisible()
      expect(input).toHaveAttribute('aria-invalid', 'true')
      expect(put).not.toHaveBeenCalled()
    }
  )

  it('requires at least one network before enabling', async () => {
    const { user } = renderUsdt()
    const put = vi.spyOn(api, 'put')
    await user.click(trigger())
    const form = within(await settings())
    await user.click(form.getByRole('switch', { name: 'Enable USDT recharge' }))
    await user.click(form.getByRole('button', { name: 'Save USDT settings' }))
    expect(
      await form.findByText('Enable at least one USDT network.')
    ).toBeVisible()
    expect(put).not.toHaveBeenCalled()
  })

  it('preserves the draft when the server rejects the update', async () => {
    const { user } = renderUsdt()
    vi.spyOn(api, 'put').mockResolvedValue({
      data: { success: false, message: 'The policy changed.' },
    })
    await user.click(trigger())
    const form = within(await settings())
    const minimum = form.getByRole('textbox', {
      name: 'Minimum recharge (CNY)',
    })
    await user.clear(minimum)
    await user.type(minimum, '22.50')
    await user.click(form.getByRole('button', { name: 'Save USDT settings' }))
    expect(await form.findByRole('alert')).toHaveTextContent(
      'The policy changed.'
    )
    expect(minimum).toHaveValue('22.50')
  })
})
