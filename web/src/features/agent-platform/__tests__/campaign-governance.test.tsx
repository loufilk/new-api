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
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { CampaignPanel } from '../components/campaign'
import { ContributionsPanel } from '../components/contributions'
import { GovernancePanel } from '../components/governance'
import { PendingOperations } from '../components/pending-operations'
import { pendingOperations } from '../lib/recovery'
import {
  mountExpansion,
  ok,
  page,
  setupExpansionUser,
} from './expansion-fixtures'

const campaign = {
  id: 8,
  name: 'Reviewed benefit',
  status: 'approved',
  grant_quota: 500000,
  claim_start_at: 1700000000,
  claim_end_at: 2000000000,
  expires_at: 2000001000,
  terms_hash: 'approved-terms-hash',
  manual_review: true,
  can_claim: true,
  eligibility_reason: 'eligible_subject_to_budget',
  allowed_actions: [],
}
beforeEach(() => setupExpansionUser(1))
describe('campaign grants and account governance', () => {
  test('ordinary finance cannot propose a root-owned campaign and the default policy enables no proposal', async () => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 42, username: 'finance', role: 10 })
    vi.mocked(api.get).mockImplementation(async (url) =>
      ok(
        String(url).includes('/policy/')
          ? { campaign_enabled: true, campaign_terms_approved: true }
          : page([])
      )
    )
    await mountExpansion(<CampaignPanel finance />)
    await screen.findByRole('tab', { name: 'Campaigns' })
    expect(
      screen.queryByRole('button', { name: 'Propose campaign version' })
    ).not.toBeInTheDocument()
  })
  test('a cancelled campaign claim does not request a grant and preserves displayed terms', async () => {
    vi.mocked(api.get).mockImplementation(async (url) =>
      ok(String(url).endsWith('/campaigns') ? page([campaign]) : page([]))
    )
    await mountExpansion(<CampaignPanel />)
    await screen.findByRole('button', { name: 'View details' })
    await act(async () => {})
    await userEvent.click(screen.getByRole('button', { name: 'View details' }))
    await userEvent.click(
      await screen.findByRole('button', { name: 'Review campaign claim' })
    )
    const confirmation = await screen.findByRole('alertdialog', {
      name: 'Review campaign claim',
    })
    expect(
      within(confirmation).getByText('Reviewed benefit')
    ).toBeInTheDocument()
    await userEvent.click(
      within(confirmation).getByRole('button', { name: 'Cancel' })
    )
    expect(api.request).not.toHaveBeenCalled()
  })
  test('an interrupted noncash claim is sent once and recovered by its same request ID without another grant', async () => {
    let rejectRequest!: (reason?: unknown) => void
    vi.mocked(api.request).mockImplementation(
      () =>
        new Promise((_resolve, reject) => {
          rejectRequest = reject
        })
    )
    vi.mocked(api.get).mockImplementation(async (url) => {
      if (String(url).includes('/operations/')) {
        return ok({
          request_id: String(url).split('/').at(-1),
          action: 'campaign.claim',
          status: 'completed',
          resource_type: 'campaign_claim',
          resource_id: '9',
          resource_state: 'review_pending',
          resource: { id: 9, status: 'review_pending' },
        })
      }
      return ok(
        String(url).endsWith('/campaigns') ? page([campaign]) : page([])
      )
    })
    await mountExpansion(
      <>
        <PendingOperations />
        <CampaignPanel />
      </>
    )
    await screen.findByRole('button', { name: 'View details' })
    await act(async () => {})
    await userEvent.click(screen.getByRole('button', { name: 'View details' }))
    await userEvent.click(
      await screen.findByRole('button', { name: 'Review campaign claim' })
    )
    const confirm = await screen.findByRole('button', {
      name: 'Confirm noncash claim',
    })
    await userEvent.click(confirm)
    await userEvent.click(confirm)
    await waitFor(() => expect(api.request).toHaveBeenCalledTimes(1))
    const body = vi.mocked(api.request).mock.calls[0][0].data as {
      request_id: string
    }
    expect(body).toMatchObject({
      campaign_id: 8,
      terms_hash: 'approved-terms-hash',
      confirm_noncash: true,
    })
    expect(pendingOperations(42)[0].request_id).toBe(body.request_id)
    await act(async () => rejectRequest(new Error('connection lost')))
    expect(
      await screen.findByRole('button', { name: 'Confirm noncash claim' })
    ).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    await userEvent.click(
      await screen.findByRole('button', { name: 'Check request status' })
    )
    await waitFor(() => expect(pendingOperations(42)).toEqual([]))
    expect(api.request).toHaveBeenCalledTimes(1)
  })
  test('qualification submission rejects raw identity numbers and does not change ordinary user roles', async () => {
    vi.mocked(api.get).mockImplementation(async (url) =>
      ok(
        String(url).includes('/policy/')
          ? { governance_enabled: true }
          : {
              profile: null,
              governance: null,
              submission: null,
              review_required: true,
              review_reasons: ['not_submitted'],
              audit: [],
            }
      )
    )
    await mountExpansion(<GovernancePanel />)
    await userEvent.click(
      await screen.findByRole('button', {
        name: 'Submit qualification dossier',
      })
    )
    fireEvent.change(
      await screen.findByRole('textbox', {
        name: 'Identity dossier reference',
      }),
      { target: { value: 'dossier:123456789012345678' } }
    )
    fireEvent.change(
      screen.getByRole('textbox', { name: 'Accepted agreement reference' }),
      { target: { value: 'agreement:terms-0001' } }
    )
    fireEvent.change(
      screen.getByRole('textbox', {
        name: 'Accepted agreement SHA-256 digest',
      }),
      { target: { value: 'a'.repeat(64) } }
    )
    await userEvent.click(
      screen.getByRole('checkbox', {
        name: 'I accept the referenced agreement and retain my existing customer obligations',
      })
    )
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
    expect(
      await screen.findByText(
        'Enter an opaque reference with the required prefix and at least eight identifier characters.'
      )
    ).toBeInTheDocument()
    expect(api.request).not.toHaveBeenCalled()
    expect(useAuthStore.getState().auth.user?.role).toBe(1)
  })
  test('contributions retain each original currency and exact principal rather than producing a mixed-currency total', async () => {
    const currencies = ['JPY', 'BHD'].map((currency) => ({
      currency,
      currency_exponent: currency === 'JPY' ? 0 : 3,
      confirmed_topup_principal_minor: '9007199254740993',
      refunded_principal_minor: '0',
      net_topup_principal_minor: '9007199254740993',
      commission_earned_minor: '1000',
      commission_reversed_minor: '0',
      commission_net_minor: '1000',
    }))
    vi.mocked(api.get).mockResolvedValue(
      ok({
        ...page([
          { customer_user_id: 7, display_name: 'Referred user', currencies },
        ]),
        coverage: {
          scope: 'verified_live_topup_receipts_only',
          historical_complete: false,
          commission_cutover_at: 0,
          earliest_verified_receipt_at: 0,
        },
      })
    )
    await mountExpansion(<ContributionsPanel />)
    expect(await screen.findByText('Referred user')).toBeInTheDocument()
    expect(
      screen.getAllByText(/JPY.*9,007,199,254,740,993/).length
    ).toBeGreaterThan(0)
    expect(
      screen.getAllByText(/BHD.*9,007,199,254,740\.993/).length
    ).toBeGreaterThan(0)
  })
})
