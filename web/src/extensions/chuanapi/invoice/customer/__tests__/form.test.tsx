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

afterEach(() => vi.restoreAllMocks())

test('an ordinary personal application omits company details and reuses the same key after transport failure', async () => {
  const user = userEvent.setup()
  const post = vi
    .spyOn(api, 'post')
    .mockRejectedValueOnce(new Error('Connection interrupted'))
    .mockResolvedValueOnce({ data: { success: true, data: { id: 14 } } })
  const success = vi.fn()
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ApplicationForm
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
  await user.selectOptions(screen.getByLabelText('Title type'), 'personal')
  expect(screen.queryByLabelText('Tax ID')).not.toBeInTheDocument()
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
    title_type: 'personal',
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
        selection={new Map([[21, '1']])}
        onBusy={vi.fn()}
        onSuccess={vi.fn()}
        onConflict={vi.fn()}
      />
    </QueryClientProvider>
  )
  await user.selectOptions(screen.getByLabelText('Title type'), 'personal')
  await user.selectOptions(screen.getByLabelText('Invoice type'), 'special')
  expect(screen.getByLabelText('Title type')).toHaveValue('company')
  expect(screen.getByRole('option', { name: 'Individual' })).toBeDisabled()
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
