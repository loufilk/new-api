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
const settings = {
  version: 5,
  center_enabled: true,
  ordinary: defaults,
  special: defaults,
}
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

test('root changes the center and invoice policies independently without submitting capabilities', async () => {
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
    screen.getByRole('switch', { name: 'Enable invoice center' })
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
      center_enabled: false,
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

test('a legacy server without the center setting keeps the old form and omits the unsupported field', async () => {
  const legacySettings = {
    version: 5,
    ordinary: defaults,
    special: defaults,
  }
  const { user, put, close } = setup(legacySettings)
  put.mockResolvedValueOnce(result({ ...legacySettings, version: 6 }))
  const ordinary = within(
    await screen.findByRole('group', { name: 'Ordinary invoice' })
  )
  await user.click(
    ordinary.getByRole('switch', { name: 'Accept applications' })
  )
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(close).toHaveBeenCalledOnce())
  expect(
    screen.queryByRole('switch', { name: 'Enable invoice center' })
  ).not.toBeInTheDocument()
  expect(put).toHaveBeenCalledExactlyOnceWith(
    path,
    {
      version: 5,
      ordinary: { ...defaults, enabled: false },
      special: defaults,
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
      center_enabled: true,
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

const first = { id: 'technical_service', name: '技术服务费', enabled: true }
const second = { id: 'software_development', name: '软件开发费', enabled: true }

test('editing categories retains accepted IDs and order and saves a new trimmed name under a generated ID with the same CAS version', async () => {
  const { user, put, close } = setup({
    ...settings,
    categories: [first, second],
  })
  const editor = within(
    await screen.findByRole('group', { name: 'Invoice categories' })
  )
  await user.clear(editor.getByRole('textbox', { name: 'Category Name 1' }))
  await user.type(
    editor.getByRole('textbox', { name: 'Category Name 1' }),
    '企业技术服务'
  )
  await user.click(editor.getByRole('switch', { name: 'Enabled 2' }))
  await user.click(editor.getByRole('button', { name: 'Add invoice category' }))
  await user.type(
    editor.getByRole('textbox', { name: 'Category Name 3' }),
    '  项目咨询  '
  )
  put.mockImplementationOnce(async (_, body) =>
    result({ ...(body as object), version: 6 })
  )
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(close).toHaveBeenCalledOnce())
  expect(put).toHaveBeenCalledExactlyOnceWith(
    path,
    {
      ...settings,
      categories: [
        { ...first, name: '企业技术服务' },
        { ...second, enabled: false },
        {
          id: expect.stringMatching(/^cat_[a-f0-9]{16}$/),
          name: '项目咨询',
          enabled: true,
        },
      ],
    },
    { singleUseAuthorization: true }
  )
})

test('a disabled category still reserves its name until renamed and removing an unsaved addition does not change stored categories', async () => {
  const { user, put } = setup({
    ...settings,
    categories: [{ ...first, name: 'API', enabled: false }],
  })
  const editor = within(
    await screen.findByRole('group', { name: 'Invoice categories' })
  )
  await user.click(editor.getByRole('button', { name: 'Add invoice category' }))
  const name = editor.getByRole('textbox', { name: 'Category Name 2' })
  await user.type(name, 'Ａｐｉ')
  expect(
    await editor.findByText('Invoice category names must be unique.')
  ).toBeVisible()
  expect(name).toHaveAttribute('aria-invalid', 'true')
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
  await user.click(editor.getByRole('button', { name: 'Cancel 2' }))
  expect(name).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
  expect(put).not.toHaveBeenCalled()
})

test('a legacy server shows read-only defaults while an explicit empty catalog does not revive defaults', async () => {
  const { view } = setup()
  const legacy = within(
    await screen.findByRole('group', { name: 'Invoice categories' })
  )
  expect(legacy.getByRole('textbox', { name: 'Category Name 1' })).toHaveValue(
    '技术服务费'
  )
  expect(
    legacy.getByRole('button', { name: 'Add invoice category' })
  ).toBeDisabled()
  view.unmount()
  setup({ ...settings, categories: [] })
  const empty = within(
    await screen.findByRole('group', { name: 'Invoice categories' })
  )
  expect(empty.queryByRole('textbox')).not.toBeInTheDocument()
  expect(empty.getByText(/no category is enabled/i)).toBeVisible()
  expect(
    empty.getByRole('button', { name: 'Add invoice category' })
  ).toBeEnabled()
})

test('the root cannot add a thirty-first category, including disabled ones', async () => {
  setup({
    ...settings,
    categories: Array.from({ length: 30 }, (_, i) => ({
      id: `c${i}`,
      name: `Project ${i}`,
      enabled: false,
    })),
  })
  const editor = within(
    await screen.findByRole('group', { name: 'Invoice categories' })
  )
  expect(editor.getAllByRole('textbox')).toHaveLength(30)
  expect(
    editor.getByRole('button', { name: 'Add invoice category' })
  ).toBeDisabled()
})

test('when another root disables a category, a stale rename is not retried and reload replaces both the category and version', async () => {
  const { user, read, put } = setup({ ...settings, categories: [first] })
  put.mockResolvedValueOnce({
    data: { success: false, code: 'INVOICE_SETTINGS_CONFLICT' },
  })
  const editor = within(
    await screen.findByRole('group', { name: 'Invoice categories' })
  )
  const name = editor.getByRole('textbox', { name: 'Category Name 1' })
  await user.clear(name)
  await user.type(name, 'Local rename')
  const save = screen.getByRole('button', { name: 'Save' })
  await user.click(save)
  const reload = await screen.findByRole('button', {
    name: 'Reload latest settings',
  })
  expect(save).toBeDisabled()
  expect(name).toHaveValue('Local rename')
  read.mockResolvedValueOnce(
    result({
      ...settings,
      categories: [{ ...first, enabled: false }],
      version: 6,
    })
  )
  await user.click(reload)
  await waitFor(() => expect(name).toHaveValue(first.name))
  expect(editor.getByRole('switch', { name: 'Enabled 1' })).not.toBeChecked()
  expect(save).toBeDisabled()
  expect(put).toHaveBeenCalledTimes(1)
})

test('a server that omits categories in its save response cannot confirm the catalog was saved', async () => {
  const { user, close } = setup({ ...settings, categories: [first] })
  const editor = within(
    await screen.findByRole('group', { name: 'Invoice categories' })
  )
  await user.click(editor.getByRole('switch', { name: 'Enabled 1' }))
  expect(editor.getByText(/no category is enabled/i)).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Save' }))
  expect(
    await screen.findByText('Update the server to edit invoice categories.')
  ).toBeVisible()
  expect(close).not.toHaveBeenCalled()
})
