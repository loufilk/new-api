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
import { screen, within } from '@testing-library/react'
import { expect, test } from 'vitest'

import { detail, setup } from './review-fixture'

test('finance sees the original fee and configured tax rate on a versioned application', async () => {
  const { user } = setup({
    settings_version: 6,
    fee_minor: '345',
    fee_rate_bps: 301,
    tax_rate_bps: 1300,
  })
  await user.click(
    await screen.findByRole('button', { name: 'View application #71' })
  )
  const dialog = within(
    await screen.findByRole('dialog', { name: /Invoice application/ })
  )
  expect(await dialog.findByText(/CNY\s*3\.45/)).toBeVisible()
  expect(dialog.getByText('3.01%')).toBeVisible()
  expect(dialog.getByText('13%')).toBeVisible()
})

test('legacy details without a snapshot do not misrepresent the current settings as historic rates', async () => {
  const { user } = setup({
    settings_version: 0,
    fee_minor: '0',
    fee_rate_bps: 0,
    tax_rate_bps: 0,
  })
  await user.click(
    await screen.findByRole('button', { name: 'View application #71' })
  )
  expect(await screen.findByText(detail.title)).toBeVisible()
  expect(
    screen.queryByText('Configured tax rate at application')
  ).not.toBeInTheDocument()
})

test.each([
  { fee_state: 'unpaid', fee_rail: '', rejectable: true },
  { fee_state: 'checkout', fee_rail: 'epay', rejectable: false },
] as const)(
  'awaiting payment: $fee_state cannot be claimed; only an unpinned unpaid fee can be rejected',
  async ({ fee_state, fee_rail, rejectable }) => {
    const { user } = setup({
      status: 'awaiting_payment',
      settings_version: 4,
      fee_minor: '100',
      fee_paid_minor: '0',
      fee_state,
      fee_rail,
    })
    await user.selectOptions(
      screen.getByLabelText('Application status'),
      'awaiting_payment'
    )
    await user.click(
      await screen.findByRole('button', { name: 'View application #71' })
    )
    const dialog = within(
      await screen.findByRole('dialog', { name: /Invoice application/ })
    )
    expect(await dialog.findByText(detail.title)).toBeVisible()
    expect(
      dialog.queryByRole('button', { name: 'Claim for issuance' })
    ).not.toBeInTheDocument()
    expect(
      dialog.queryByRole('button', { name: 'Record issued PDF' })
    ).not.toBeInTheDocument()
    if (rejectable) {
      expect(
        dialog.getByRole('button', { name: 'Reject application' })
      ).toBeVisible()
    } else {
      expect(
        dialog.queryByRole('button', { name: 'Reject application' })
      ).not.toBeInTheDocument()
    }
  }
)

test.each([
  { fee_rail: 'wallet', description: /returns the paid fee/ },
  { fee_rail: 'epay', description: /No cash refund is sent/ },
] as const)(
  'paid $fee_rail fee: rejection confirmation explains the refund result',
  async ({ fee_rail, description }) => {
    const { user } = setup({
      settings_version: 4,
      fee_minor: '100',
      fee_state: 'paid',
      fee_rail,
      fee_paid_minor: '100',
      fee_paid_at: 1234,
    })
    await user.click(
      await screen.findByRole('button', { name: 'View application #71' })
    )
    await user.click(
      await screen.findByRole('button', { name: 'Reject application' })
    )
    expect(await screen.findByText(description)).toBeVisible()
  }
)

test('the fee refund filter opens cash fee review without an issue or cash payout action', async () => {
  const { user } = setup({
    status: 'rejected',
    settings_version: 4,
    fee_state: 'refund_review',
    fee_rail: 'epay',
    fee_minor: '230',
    fee_trade_no: 'FEE-INVOICE-TEST',
    fee_provider_reference: 'FEE-PROVIDER-TEST',
    fee_paid_minor: '230',
    fee_paid_at: 1234,
  })
  await user.selectOptions(
    screen.getByLabelText('Application status'),
    'fee_refund_review'
  )
  await user.click(
    await screen.findByRole('button', { name: 'View application #71' })
  )
  const dialog = within(
    await screen.findByRole('dialog', { name: /Invoice application/ })
  )
  expect(
    await dialog.findByText(/No automatic cash refund has been sent/)
  ).toBeVisible()
  expect(dialog.getByText('FEE-INVOICE-TEST')).toBeVisible()
  expect(dialog.getByText('FEE-PROVIDER-TEST')).toBeVisible()
  expect(
    dialog.queryByRole('button', { name: /Refund|issued|claim/i })
  ).not.toBeInTheDocument()
})
