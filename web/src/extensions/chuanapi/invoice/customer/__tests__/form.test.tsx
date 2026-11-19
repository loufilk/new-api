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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { ApplicationForm } from '../application-form'
import { invoiceSchema } from '../form-schema'
import { customerConfigSchema } from '../policy'

const free = {
  enabled: true,
  fee_enabled: false,
  fee_rate_bps: 0,
  tax_rate_bps: 0,
}
const policy = customerConfigSchema.parse({
  enabled: true,
  version: 7,
  ordinary: free,
  special: free,
})

afterEach(() => vi.restoreAllMocks())

test('an ordinary company application submits its reviewed version and reuses the same key after transport failure', async () => {
  const user = userEvent.setup()
  const post = vi
    .spyOn(api, 'post')
    .mockRejectedValueOnce(new Error('Connection interrupted'))
    .mockResolvedValueOnce({ data: { success: true, data: { id: 14 } } })
  const success = vi.fn()
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ApplicationForm
        policy={policy}
        selection={
          new Map([
            [21, '101'],
            [17, '22'],
          ])
        }
        onBusy={vi.fn()}
        onSuccess={success}
        onConflict={vi.fn()}
      />
    </QueryClientProvider>
  )
  expect(screen.getByText(/1\.23/)).toBeInTheDocument()
  await user.type(screen.getByLabelText('Tax ID'), '123456789012345')
  expect(screen.queryByLabelText('Title type')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('option', { name: 'Individual' })
  ).not.toBeInTheDocument()
  await user.type(screen.getByLabelText('Invoice title'), 'Example')
  await user.type(
    screen.getByLabelText('Email address'),
    'test@example.invalid'
  )
  await user.click(screen.getByRole('button', { name: 'Submit application' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Connection interrupted'
  )
  await user.click(screen.getByRole('button', { name: 'Submit application' }))
  await waitFor(() => expect(success).toHaveBeenCalledOnce())
  expect(post).toHaveBeenCalledTimes(2)
  const [, payload] = post.mock.calls[0]
  expect(payload).toEqual({
    order_ids: [17, 21],
    idempotency_key: expect.any(String),
    invoice_type: 'ordinary',
    title_type: 'company',
    tax_id: '123456789012345',
    settings_version: 7,
    category: 'technical_service',
    title: 'Example',
    email: 'test@example.invalid',
  })
  expect(post.mock.calls[1]).toEqual(['/api/invoice/applications', payload])
})
test('switching to special VAT requires a company, tax ID, contact and bank details', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post')
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ApplicationForm
        policy={policy}
        selection={new Map([[21, '1']])}
        onBusy={vi.fn()}
        onSuccess={vi.fn()}
        onConflict={vi.fn()}
      />
    </QueryClientProvider>
  )
  await user.selectOptions(screen.getByLabelText('Invoice type'), 'special')
  expect(
    screen.queryByRole('option', { name: 'Individual' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Submit application' }))
  for (const label of [
    'Registered address',
    'Registered phone',
    'Bank name',
    'Bank account number',
    'Tax ID',
  ]) {
    expect(screen.getByLabelText(label)).toHaveAttribute('aria-invalid', 'true')
  }
  expect(post).not.toHaveBeenCalled()
})
test('special company invoice preserves leading bank account zeros and normalizes tax ID', () => {
  const data = invoiceSchema.parse({
    invoice_type: 'special',
    title_type: 'company',
    category: 'software_development',
    title: ' Example ',
    email: 'test@example.invalid',
    tax_id: 'abcdefghijklmn0',
    address: 'Example Street',
    phone: '+86 (020) 12345678',
    bank_name: 'Example Bank',
    bank_account: '000123',
  })
  expect(data).toMatchObject({
    title: 'Example',
    tax_id: 'ABCDEFGHIJKLMN0',
    bank_account: '000123',
  })
})

test('only the enabled type can be selected; a positive fee requires an available payment channel', async () => {
  const policyWithFee = {
    ...policy,
    ordinary: { ...free, enabled: false },
    special: {
      ...free,
      fee_enabled: true,
      fee_rate_bps: 100,
      tax_rate_bps: 1300,
    },
  }
  const { rerender } = render(
    <QueryClientProvider client={new QueryClient()}>
      <ApplicationForm
        policy={policyWithFee}
        selection={new Map([[2, '50']])}
        onBusy={vi.fn()}
        onConflict={vi.fn()}
        onSuccess={vi.fn()}
      />
    </QueryClientProvider>
  )
  expect(
    screen.getByRole('option', { name: 'Ordinary invoice' })
  ).toBeDisabled()
  expect(screen.getByLabelText('Invoice type')).toHaveValue('special')
  expect(
    screen.getByText('Estimated invoice fee').parentElement
  ).toHaveTextContent(/0.01/)
  expect(
    screen.getByText('Configured tax rate (reference only)').parentElement
  ).toHaveTextContent('13%')
  expect(
    screen.getByRole('button', { name: 'Submit and choose payment' })
  ).toBeDisabled()
  expect(screen.getByRole('status')).toHaveTextContent(/cannot be submitted/)
  rerender(
    <QueryClientProvider client={new QueryClient()}>
      <ApplicationForm
        policy={{
          ...policyWithFee,
          fee_payment_methods: [
            { id: 'wxpay', name: 'WeChat Pay', rail: 'epay' },
          ],
        }}
        selection={new Map([[2, '50']])}
        onBusy={vi.fn()}
        onConflict={vi.fn()}
        onSuccess={vi.fn()}
      />
    </QueryClientProvider>
  )
  expect(
    screen.getByRole('button', { name: 'Submit and choose payment' })
  ).toBeEnabled()
  expect(screen.getByText(/submitting will not charge you/i)).toBeVisible()
})
test('an ordinary personal payload is invalid even with a company tax ID', () => {
  expect(
    invoiceSchema.safeParse({
      invoice_type: 'ordinary',
      title_type: 'personal',
      title: 'Example',
      email: 'test@example.invalid',
      tax_id: '123456789012345',
      category: 'technical_service',
    }).success
  ).toBe(false)
})

test('renaming a selected category invalidates consent without erasing the typed company title', async () => {
  const user = userEvent.setup()
  const client = new QueryClient()
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: { id: 17 } } })
  const view = (name: string, version: number) => (
    <QueryClientProvider client={client}>
      <ApplicationForm
        policy={{
          ...policy,
          version,
          categories: [{ id: 'hosting', name, enabled: true }],
        }}
        selection={new Map([[1, '100']])}
        onBusy={vi.fn()}
        onConflict={vi.fn()}
        onSuccess={vi.fn()}
      />
    </QueryClientProvider>
  )
  const { rerender } = render(view('Cloud hosting', 4))
  const choose = screen.getByRole('combobox', { name: 'Invoice category' })
  const submit = screen.getByRole('button', { name: 'Submit application' })
  expect(choose).toHaveValue('hosting')
  await user.type(screen.getByLabelText('Tax ID'), '123456789012345')
  await user.type(screen.getByLabelText('Invoice title'), 'Example')
  await user.type(
    screen.getByLabelText('Email address'),
    'test@example.invalid'
  )
  rerender(view('New service', 5))
  expect(choose).toHaveValue('')
  expect(submit).toBeDisabled()
  expect(screen.getByLabelText('Invoice title')).toHaveValue('Example')
  await user.click(submit)
  expect(post).not.toHaveBeenCalled()
  await user.selectOptions(choose, 'New service')
  await user.click(submit)
  await waitFor(() => expect(post).toHaveBeenCalledOnce())
  expect(post.mock.calls[0][1]).toMatchObject({
    category: 'hosting',
    settings_version: 5,
    title: 'Example',
  })
})
