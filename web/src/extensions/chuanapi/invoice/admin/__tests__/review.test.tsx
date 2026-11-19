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
import { act, render, screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { api, type ApiRequestConfig } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import InvoiceManagement from '..'
import type { AdminDetail, AdminSummary } from '../api'

const base = '/api/admin/invoice/applications'
const summary: AdminSummary = {
  id: 71,
  status: 'pending',
  amount_minor: '9007199254741099',
  currency: 'CNY',
  order_count: 1,
  invoice_type: 'ordinary',
  title_type: 'company',
  category: 'technical_service',
  created_at: 1234,
  updated_at: 1234,
  user_id: 20,
  has_document: false,
}
const detail: AdminDetail = {
  ...summary,
  title: 'Synthetic invoice title',
  email: 'test@example.invalid',
  orders: [
    {
      id: 19,
      reference: 'TEST-71',
      paid_at: 1200,
      amount_minor: summary.amount_minor,
      type: 'topup',
      currency: 'CNY',
    },
  ],
}
function setRole(role: number) {
  useAuthStore
    .getState()
    .auth.setUser({ id: 100 + role, username: 'fixture', role })
}
afterEach(() => {
  useAuthStore.getState().auth.reset()
})
function setup(
  overrides: Partial<AdminDetail> = {},
  role: number = ROLE.SUPER_ADMIN
) {
  setRole(role)
  const client = new QueryClient()
  const read = vi
    .spyOn(api, 'get')
    .mockImplementation(async (url, config: ApiRequestConfig = {}) => {
      if (url === base) {
        const filter = config.params?.status
        const item = {
          ...summary,
          status: overrides.status ?? summary.status,
          processing: overrides.processing,
          processing_by: overrides.processing_by,
          requires_reconciliation: overrides.requires_reconciliation,
        }
        const include =
          !filter ||
          filter === item.status ||
          (filter === 'refund_review' && item.requires_reconciliation)
        return {
          data: {
            success: true,
            data: { items: include ? [item] : [], total: include ? 1 : 0 },
          },
        }
      }
      if (url === `${base}/71`) {
        return { data: { success: true, data: { ...detail, ...overrides } } }
      }
      throw new Error('Unexpected endpoint')
    })
  const write = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: { id: 71 } } })
  const view = render(
    <QueryClientProvider client={client}>
      <InvoiceManagement />
    </QueryClientProvider>
  )
  return { read, write, view, client, user: userEvent.setup() }
}

test('ordinary administrators cannot see the queue or request financial details', () => {
  const { read } = setup({}, ROLE.ADMIN)
  expect(screen.queryByText('Invoice management')).not.toBeInTheDocument()
  expect(read).not.toHaveBeenCalled()
})

test('opening one application reveals its private details only until access is lost', async () => {
  const { read, user } = setup()
  expect(await screen.findByText(/90,071,992,547,410\.99/)).toBeVisible()
  expect(screen.queryByText(detail.title)).not.toBeInTheDocument()
  expect(read.mock.calls.map(([url]) => url)).toEqual([base])
  await user.click(
    await screen.findByRole('button', { name: 'View application #71' })
  )
  expect(await screen.findByText(detail.email)).toBeVisible()
  act(() => setRole(ROLE.ADMIN))
  expect(screen.queryByText(detail.email)).not.toBeInTheDocument()
})

test('saving an issued PDF requires valid evidence and fresh explicit settlement and refund verification', async () => {
  const { write, user } = setup({ processing: true, processing_by: 200 })
  await user.click(
    await screen.findByRole('button', { name: 'View application #71' })
  )
  await user.click(
    await screen.findByRole('button', { name: 'Record issued PDF' })
  )
  const submit = screen.getByRole('button', { name: 'Save issued invoice' })
  expect(submit).toBeDisabled()
  const number = screen.getByLabelText('Invoice number')
  await user.type(number, 'TEST-71')
  const input = screen.getByLabelText(/Issued PDF \(/)
  await user.upload(
    input,
    new File([], 'empty.pdf', { type: 'application/pdf' })
  )
  expect(await screen.findByRole('alert')).toHaveTextContent(/non-empty PDF/)
  const pdf = new File(['synthetic fixture'], 'test.pdf', {
    type: 'application/pdf',
  })
  await user.upload(input, pdf)
  expect(submit).toBeDisabled()
  const checkbox = screen.getByRole('checkbox', {
    name: /settlement and refund records/,
  })
  await user.click(checkbox)
  await waitFor(() => expect(submit).toBeEnabled())
  await user.type(number, 'A')
  expect(checkbox).not.toBeChecked()
  expect(submit).toBeDisabled()
  await user.click(checkbox)
  await user.click(submit)
  await waitFor(() => expect(write).toHaveBeenCalledTimes(1))
  const [url, body, config] = write.mock.calls[0]
  expect(url).toBe(`${base}/71/issue`)
  expect(config).toEqual(
    expect.objectContaining({ singleUseAuthorization: true })
  )
  const form = body as FormData
  expect([...form.keys()].sort()).toEqual([
    'confirm_payment_verified',
    'file',
    'invoice_number',
  ])
  expect(form.get('file')).toHaveProperty('name', 'test.pdf')
  expect(form.get('invoice_number')).toBe('TEST-71A')
  expect(form.get('confirm_payment_verified')).toBe('true')
})

test('cancelling a rejection submits nothing; confirming a nonblank reason sends it once', async () => {
  const { write, user } = setup()
  await user.click(
    await screen.findByRole('button', { name: 'View application #71' })
  )
  await user.click(
    await screen.findByRole('button', { name: 'Reject application' })
  )
  let confirm = within(screen.getByRole('alertdialog'))
  await user.type(confirm.getByRole('textbox'), 'draft')
  await user.click(confirm.getByRole('button', { name: 'Cancel' }))
  expect(write).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Reject application' }))
  confirm = within(screen.getByRole('alertdialog'))
  expect(confirm.getByRole('textbox')).toHaveValue('')
  await user.type(confirm.getByRole('textbox'), '   ')
  expect(
    confirm.getByRole('button', { name: 'Reject application' })
  ).toBeDisabled()
  await user.type(confirm.getByRole('textbox'), 'Incorrect title')
  await user.click(confirm.getByRole('button', { name: 'Reject application' }))
  await waitFor(() =>
    expect(write).toHaveBeenCalledWith(
      `${base}/71/reject`,
      { reason: 'Incorrect title' },
      expect.anything()
    )
  )
})

test('a refund discovered after issuance can only be flagged for manual reconciliation', async () => {
  const { write, user } = setup({ status: 'issued', has_document: true })
  await user.selectOptions(
    screen.getByLabelText('Application status'),
    'issued'
  )
  await user.click(
    await screen.findByRole('button', { name: 'View application #71' })
  )
  await user.click(
    await screen.findByRole('button', {
      name: 'Flag for refund reconciliation',
    })
  )
  const confirm = within(screen.getByRole('alertdialog'))
  const submit = confirm.getByRole('button', {
    name: 'Flag for refund reconciliation',
  })
  expect(submit).toBeDisabled()
  await user.type(
    confirm.getByLabelText('Internal review reason'),
    'Merchant reported refund'
  )
  await user.click(submit)
  await waitFor(() =>
    expect(write).toHaveBeenCalledWith(
      `${base}/71/reconciliation`,
      { reason: 'Merchant reported refund' },
      expect.anything()
    )
  )
})

test('refund review filter keeps the original issued status and does not allow repeated flagging or issuance', async () => {
  const { read, user } = setup({
    status: 'issued',
    requires_reconciliation: true,
    reconciliation_reason: 'Synthetic refund note',
  })
  await user.selectOptions(
    screen.getByLabelText('Application status'),
    'refund_review'
  )
  await user.click(
    await screen.findByRole('button', { name: 'View application #71' })
  )
  expect(await screen.findByText('Synthetic refund note')).toBeVisible()
  const dialog = within(screen.getByRole('dialog'))
  expect(dialog.getByText('Issued')).toBeVisible()
  expect(dialog.getByText('Refund review')).toBeVisible()
  expect(
    dialog.queryByRole('button', { name: 'Record issued PDF' })
  ).not.toBeInTheDocument()
  expect(
    dialog.queryByRole('button', { name: 'Flag for refund reconciliation' })
  ).not.toBeInTheDocument()
  expect(read).toHaveBeenCalledWith(
    base,
    expect.objectContaining({
      params: { p: 1, size: 20, status: 'refund_review' },
    })
  )
})

test('a failed rejection keeps the application open for checking instead of reporting success', async () => {
  const { write, user } = setup()
  write.mockResolvedValue({
    data: { success: false, message: 'Unable to record review' },
  })
  await user.click(
    await screen.findByRole('button', { name: 'View application #71' })
  )
  await user.click(
    await screen.findByRole('button', { name: 'Reject application' })
  )
  const confirm = within(screen.getByRole('alertdialog'))
  await user.type(confirm.getByRole('textbox'), 'Incorrect title')
  await user.click(confirm.getByRole('button', { name: 'Reject application' }))
  await waitFor(() => expect(write).toHaveBeenCalledTimes(1))
  await waitFor(() =>
    expect(confirm.getByRole('button', { name: 'Cancel' })).toBeEnabled()
  )
  await user.click(confirm.getByRole('button', { name: 'Cancel' }))
  expect(await screen.findByText(detail.title)).toBeVisible()
})

test('only a confirmed server claim allows external processing and PDF recording', async () => {
  const state: Partial<AdminDetail> = {}
  const { user, write } = setup(state)
  write.mockImplementation(async () => {
    state.processing = true
    state.processing_by = 200
    return { data: { success: true, data: { ...summary, ...state } } }
  })
  await user.click(
    await screen.findByRole('button', { name: 'View application #71' })
  )
  await user.click(
    await screen.findByRole('button', { name: 'Claim for issuance' })
  )
  expect(
    screen.queryByRole('button', { name: 'Record issued PDF' })
  ).not.toBeInTheDocument()
  expect(write).not.toHaveBeenCalled()
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Claim for issuance',
    })
  )
  expect(
    await screen.findByRole('button', { name: 'Record issued PDF' })
  ).toBeVisible()
  expect(
    within(screen.getByRole('dialog')).getByText('Claimed by you')
  ).toBeVisible()
  expect(write).toHaveBeenCalledWith(`${base}/71/claim`, undefined, {
    singleUseAuthorization: true,
  })
})

test('a conflict with another finance reviewer leaves the application read-only', async () => {
  const state: Partial<AdminDetail> = {}
  const { user, write } = setup(state)
  write.mockImplementation(async () => {
    state.processing = true
    state.processing_by = 299
    throw new Error('Claim unavailable')
  })
  await user.click(
    await screen.findByRole('button', { name: 'View application #71' })
  )
  await user.click(
    await screen.findByRole('button', { name: 'Claim for issuance' })
  )
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Claim for issuance',
    })
  )
  const dialog = within(screen.getByRole('dialog', { hidden: true }))
  expect(await dialog.findByText('Claimed by reviewer #299')).toBeVisible()
  for (const action of [
    'Record issued PDF',
    'Release claim',
    'Reject application',
    'Claim for issuance',
  ]) {
    expect(
      dialog.queryByRole('button', { name: action })
    ).not.toBeInTheDocument()
  }
  expect(write).toHaveBeenCalledTimes(1)
})

test('release requires the claimant to attest no invoice exists and no external issuance is underway', async () => {
  const state: Partial<AdminDetail> = { processing: true, processing_by: 200 }
  const { user, write } = setup(state)
  write.mockImplementation(async () => {
    state.processing = false
    state.processing_by = undefined
    return { data: { success: true, data: { ...summary, ...state } } }
  })
  await user.click(
    await screen.findByRole('button', { name: 'View application #71' })
  )
  await user.click(await screen.findByRole('button', { name: 'Release claim' }))
  const confirm = within(screen.getByRole('alertdialog'))
  const release = confirm.getByRole('button', { name: 'Release claim' })
  expect(release).toBeDisabled()
  expect(write).not.toHaveBeenCalled()
  await user.click(
    confirm.getByRole('checkbox', { name: /no invoice has been issued/ })
  )
  await user.click(release)
  expect(
    await screen.findByRole('button', { name: 'Claim for issuance' })
  ).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Record issued PDF' })
  ).not.toBeInTheDocument()
  expect(write).toHaveBeenCalledWith(
    `${base}/71/release`,
    { confirm_not_issued: true },
    { singleUseAuthorization: true }
  )
})
