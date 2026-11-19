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
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsDialog } from '../settings-dialog'

const path = '/api/admin/invoice/settings'
const defaults = {
  enabled: true,
  fee_enabled: false,
  fee_rate_bps: 0,
  tax_rate_bps: 0,
}
const settings = { version: 5, ordinary: defaults, special: defaults }
const result = (data: unknown) => ({ data: { success: true, data } })

function setup(initial: unknown = settings) {
  const read = vi.spyOn(api, 'get').mockResolvedValue(result(initial))
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue(result({ ...settings, version: 6 }))
  const close = vi.fn()
  const view = render(
    <QueryClientProvider client={new QueryClient()}>
      <SettingsDialog reviewer={99} onClose={close} />
    </QueryClientProvider>
  )
  return { user: userEvent.setup(), put, read, close, view }
}

test('root changes the two policies independently and saves exact percentage points without submitting capabilities', async () => {
  const { user, put, close } = setup({
    ...settings,
    fee_payment_available: false,
  })
  const ordinary = within(
    await screen.findByRole('group', { name: 'Ordinary invoice' })
  )
  const special = within(
    screen.getByRole('group', { name: 'Special VAT invoice' })
  )
  await user.click(
    ordinary.getByRole('switch', { name: 'Accept applications' })
  )
  const tax = special.getByRole('textbox', { name: 'Configured tax rate (%)' })
  await user.clear(tax)
  await user.type(tax, '13')
  await user.click(
    special.getByRole('switch', { name: 'Charge an invoice fee' })
  )
  const fee = special.getByRole('textbox', { name: 'Fee rate (%)' })
  await user.clear(fee)
  await user.type(fee, '2.01')
  expect(
    special.getByText(/new applications for this invoice type will be blocked/i)
  ).toBeVisible()
  expect(ordinary.queryByText(/will be blocked/i)).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(close).toHaveBeenCalledOnce())
  expect(put).toHaveBeenCalledExactlyOnceWith(
    path,
    {
      version: 5,
      ordinary: { ...defaults, enabled: false },
      special: {
        ...defaults,
        fee_enabled: true,
        fee_rate_bps: 201,
        tax_rate_bps: 1300,
      },
    },
    { singleUseAuthorization: true }
  )
})

test('when a fee payment method is available, the configured fee does not show an outage warning', async () => {
  setup({
    ...settings,
    fee_payment_available: true,
    ordinary: { ...defaults, fee_enabled: true, fee_rate_bps: 123 },
  })
  const ordinary = within(
    await screen.findByRole('group', { name: 'Ordinary invoice' })
  )
  expect(ordinary.getByRole('textbox', { name: 'Fee rate (%)' })).toHaveValue(
    '1.23'
  )
  expect(ordinary.queryByText(/will be blocked/i)).not.toBeInTheDocument()
})

test('a conflict holds the old draft and disables saving until the administrator explicitly reloads successfully', async () => {
  const { user, put, read, close } = setup()
  put.mockResolvedValueOnce({
    data: { success: false, code: 'INVOICE_SETTINGS_CONFLICT' },
  })
  const special = within(
    await screen.findByRole('group', { name: 'Special VAT invoice' })
  )
  const tax = special.getByRole('textbox', { name: 'Configured tax rate (%)' })
  await user.clear(tax)
  await user.type(tax, '16.3')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  const reload = await screen.findByRole('button', {
    name: 'Reload latest settings',
  })
  expect(tax).toHaveValue('16.3')
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
  expect(read).toHaveBeenCalledTimes(1)
  read.mockRejectedValueOnce(new Error('temporarily unavailable'))
  await user.click(reload)
  expect(await screen.findByText('temporarily unavailable')).toBeVisible()
  expect(tax).toHaveValue('16.3')
  read.mockResolvedValueOnce(
    result({
      ...settings,
      version: 7,
      special: { ...defaults, tax_rate_bps: 246 },
    })
  )
  await user.click(reload)
  await waitFor(() => expect(tax).toHaveValue('2.46'))
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
  await user.click(special.getByRole('switch', { name: 'Accept applications' }))
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(close).toHaveBeenCalledOnce())
  expect(put).toHaveBeenLastCalledWith(
    path,
    {
      version: 7,
      ordinary: defaults,
      special: { ...defaults, enabled: false, tax_rate_bps: 246 },
    },
    { singleUseAuthorization: true }
  )
})

test('invalid rate precision blocks save and ordinary failures preserve the corrected draft', async () => {
  const { user, put, close } = setup()
  put.mockRejectedValueOnce(new Error('Service offline'))
  const dialog = within(
    await screen.findByRole('dialog', { name: 'Invoice settings' })
  )
  const ordinary = within(
    await dialog.findByRole('group', { name: 'Ordinary invoice' })
  )
  const fee = ordinary.getByRole('textbox', { name: 'Fee rate (%)' })
  await user.clear(fee)
  await user.type(fee, '1.111')
  expect(fee).toHaveAttribute('aria-invalid', 'true')
  expect(dialog.getByRole('button', { name: 'Save' })).toBeDisabled()
  await user.clear(fee)
  await user.type(fee, '0.01')
  await user.click(dialog.getByRole('button', { name: 'Save' }))
  expect(await dialog.findByText('Service offline')).toBeVisible()
  expect(fee).toHaveValue('0.01')
  expect(dialog.getByRole('button', { name: 'Save' })).toBeEnabled()
  expect(close).not.toHaveBeenCalled()
})

test('a malformed read does not expose guessed defaults or a save control', async () => {
  const { read, user } = setup({ version: 5 })
  expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible()
  expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument()
  read.mockResolvedValueOnce(result(settings))
  await user.click(screen.getByRole('button', { name: 'Retry' }))
  expect(
    await screen.findByRole('group', { name: 'Ordinary invoice' })
  ).toBeVisible()
})

test('a saving dialog ignores Escape and repeated submissions until the server confirms the write', async () => {
  const { user, put, close } = setup()
  let finish!: () => void
  put.mockImplementationOnce(async () => {
    await new Promise<void>((resolve) => {
      finish = resolve
    })
    return result({ ...settings, version: 6 })
  })
  const ordinary = within(
    await screen.findByRole('group', { name: 'Ordinary invoice' })
  )
  await user.click(
    ordinary.getByRole('switch', { name: 'Accept applications' })
  )
  const save = screen.getByRole('button', { name: 'Save' })
  await user.dblClick(save)
  await waitFor(() => expect(save).toBeDisabled())
  await user.keyboard('{Escape}')
  expect(close).not.toHaveBeenCalled()
  expect(put).toHaveBeenCalledOnce()
  finish()
  await waitFor(() => expect(close).toHaveBeenCalledOnce())
})
