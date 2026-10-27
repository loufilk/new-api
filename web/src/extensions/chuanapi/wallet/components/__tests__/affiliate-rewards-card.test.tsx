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
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { beforeAll, describe, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'

import type { ReferralCommissionItem, UserWalletData } from '../../types'
import { AffiliateRewardsCard } from '../affiliate-rewards-card'

const affiliateLink = 'https://example.test/sign-up?aff=demo'

const testI18n = createInstance()

beforeAll(async () => {
  await testI18n.init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: en.translation } },
    interpolation: { escapeValue: false },
  })
})

const user: UserWalletData = {
  id: 1,
  username: 'inviter',
  quota: 0,
  used_quota: 0,
  request_count: 0,
  aff_count: 2,
  aff_quota: 100000,
  aff_history_quota: 300000,
  group: 'default',
}

const record: ReferralCommissionItem = {
  id: 11,
  source_user_id: 2,
  source_name: 'a***',
  paid_quota: 1000000,
  reward_quota: 50000,
  rate: 0.05,
  created_at: 1735689600,
}

function renderCard(overrides: Record<string, unknown> = {}) {
  const onTransfer = vi.fn()
  const onRecordsPageChange = vi.fn()
  const onRecordsRetry = vi.fn()
  const result = render(
    <I18nextProvider i18n={testI18n}>
      <AffiliateRewardsCard
        user={user}
        affiliateLink={affiliateLink}
        onTransfer={onTransfer}
        complianceConfirmed
        referralRate={0.05}
        records={[record]}
        recordsTotal={1}
        recordsPage={1}
        onRecordsPageChange={onRecordsPageChange}
        onRecordsRetry={onRecordsRetry}
        {...overrides}
      />
    </I18nextProvider>
  )
  return { ...result, onTransfer, onRecordsPageChange, onRecordsRetry }
}

describe('affiliate rewards card', () => {
  test('shows the invitation link, native reward stats, and configured rule', () => {
    renderCard()

    expect(screen.getByDisplayValue(affiliateLink)).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Copy referral link' })
    ).toBeInTheDocument()
    expect(screen.getByText('Pending Rewards')).toBeInTheDocument()
    expect(screen.getByText('Total Rewards')).toBeInTheDocument()
    expect(screen.getByText('Invited Users')).toBeInTheDocument()
    expect(screen.getByText('2')).toBeInTheDocument()

    expect(
      screen.getByText(
        /For the first 3 recharges of an invited friend, you will receive 5\.0% of the recharge amount as a reward\./
      )
    ).toBeInTheDocument()
  })

  test('keeps records in a bounded, keyboard-focusable scroll area without hiding the pager', async () => {
    const keyboard = userEvent.setup()
    renderCard({ recordsTotal: 6 })
    const records = screen.getByRole('region', { name: 'Commission Records' })
    expect(records).toHaveClass('overflow-auto', 'max-h-24')
    expect(records).toHaveAttribute('tabindex', '0')
    screen.getByRole('button', { name: 'Copy referral link' }).focus()
    await keyboard.tab()
    expect(records).toHaveFocus()
    expect(records).not.toContainElement(
      screen.getByRole('button', { name: 'Go to next page' })
    )
  })

  test('copies the invitation link with the shared copy button', async () => {
    const user = userEvent.setup()
    renderCard()

    await user.click(screen.getByRole('button', { name: 'Copy referral link' }))
    expect(await navigator.clipboard.readText()).toBe(affiliateLink)
  })

  test('derives the displayed rate from the configured rule instead of hardcoding 5%', () => {
    renderCard({ referralRate: 0.0333 })

    expect(
      screen.getByText(
        /you will receive 3\.33% of the recharge amount as a reward/
      )
    ).toBeInTheDocument()
  })

  test('renders a 0% rule when referral rewards are disabled', () => {
    renderCard({ referralRate: 0 })

    expect(
      screen.getByText(/you will receive 0% of the recharge amount/)
    ).toBeInTheDocument()
  })

  test('opens the transfer dialog from the button when pending rewards exist', () => {
    const { onTransfer } = renderCard()

    fireEvent.click(screen.getByRole('button', { name: 'Transfer to Balance' }))
    expect(onTransfer).toHaveBeenCalledTimes(1)
  })

  test('disables the transfer button until compliance is confirmed', () => {
    renderCard({ complianceConfirmed: false })

    expect(
      screen.getByRole('button', { name: 'Transfer to Balance' })
    ).toBeDisabled()
    expect(
      screen.getByText(
        'Referral reward transfer is disabled until the administrator confirms compliance terms.'
      )
    ).toBeInTheDocument()
  })

  test('hides the transfer button when there are no pending rewards', () => {
    renderCard({ user: { ...user, aff_quota: 0 } })

    expect(
      screen.queryByRole('button', { name: 'Transfer to Balance' })
    ).not.toBeInTheDocument()
  })

  test('renders commission records with masked source and pagination', () => {
    const { onRecordsPageChange } = renderCard({
      records: [record],
      recordsTotal: 6,
      recordsPage: 1,
    })

    expect(screen.getByText('Commission Records')).toBeInTheDocument()
    expect(screen.getByText('a***')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Go to next page' }))
    expect(onRecordsPageChange).toHaveBeenCalledWith(2)
  })

  test('disables the previous page link on the first records page', () => {
    renderCard({ records: [record], recordsTotal: 6, recordsPage: 1 })

    expect(
      screen.getByRole('button', { name: 'Go to previous page' })
    ).toHaveAttribute('aria-disabled', 'true')
    expect(
      screen.getByRole('button', { name: 'Go to next page' })
    ).not.toHaveAttribute('aria-disabled', 'true')
  })

  test('supports keyboard activation on the records pager', async () => {
    const user = userEvent.setup()
    const { onRecordsPageChange } = renderCard({
      records: [record],
      recordsTotal: 6,
      recordsPage: 1,
    })

    screen.getByRole('button', { name: 'Go to next page' }).focus()
    await user.keyboard('{Enter}')

    expect(onRecordsPageChange).toHaveBeenCalledWith(2)
  })

  test('keeps a back path when an empty records page is not the first page', () => {
    const { onRecordsPageChange, onRecordsRetry } = renderCard({
      records: [],
      recordsTotal: 0,
      recordsPage: 2,
    })

    expect(
      screen.getByText(
        'No commission records yet. Invite friends to recharge and they will appear here.'
      )
    ).toBeInTheDocument()
    const previous = screen.getByRole('button', { name: 'Go to previous page' })
    expect(previous).not.toHaveAttribute('aria-disabled', 'true')

    fireEvent.click(previous)
    expect(onRecordsPageChange).toHaveBeenCalledWith(1)
    expect(onRecordsRetry).not.toHaveBeenCalled()
  })

  test('shows the error state instead of an empty list when records fail to load', () => {
    const { onRecordsRetry } = renderCard({
      records: [],
      recordsTotal: 0,
      recordsError: new Error('records unavailable'),
    })

    expect(screen.getByText('Oops! Something went wrong')).toBeInTheDocument()
    expect(
      screen.queryByText(
        'No commission records yet. Invite friends to recharge and they will appear here.'
      )
    ).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onRecordsRetry).toHaveBeenCalledTimes(1)
  })

  test('shows the empty state when there are no commission records', () => {
    renderCard({ records: [], recordsTotal: 0 })

    expect(
      screen.getByText(
        'No commission records yet. Invite friends to recharge and they will appear here.'
      )
    ).toBeInTheDocument()
  })

  test('replaces content with loading placeholders while data is pending', () => {
    const { container } = renderCard({ loading: true })

    expect(container.querySelectorAll('[data-slot="skeleton"]')).toHaveLength(5)
    expect(screen.queryByDisplayValue(affiliateLink)).not.toBeInTheDocument()
  })
})
