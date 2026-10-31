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
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { CreditPanel } from '../components/credit'
import { GovernancePanel } from '../components/governance'
import { ManualRefundInstructionsButton } from '../components/manual-refund-instructions'
import { PurchaseRefunds } from '../components/purchase-refunds'
import { StatementLineReview } from '../components/statement-line-review'
import { StatementPanel } from '../components/statements'
import {
  mountExpansion,
  ok,
  page,
  setupExpansionUser,
} from './expansion-fixtures'

const emptyCredit = {
  account: null,
  current_limit: null,
  available_minor: '0',
  overdue_minor: '0',
  draw_allowed: false,
  audits: [],
  limits: [],
  limit_requests: [],
  draws: [],
  receivables: [],
  invoices: [],
  repayments: [],
  adjustments: [],
  ledger: [],
  offset: 0,
  limit: 100,
  has_more: false,
  next_offset: 0,
}
async function target() {
  await userEvent.click(
    await screen.findByRole('button', { name: 'Find user' })
  )
  fireEvent.change(screen.getByRole('textbox', { name: 'User ID' }), {
    target: { value: '8' },
  })
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
}
beforeEach(() => setupExpansionUser())
describe('current controller response contracts', () => {
  test.each([
    { findings: [] },
    { findings: [{ code: 'missing_credit_journal', resource_id: 71 }] },
  ])(
    'credit reconciliation reads the real findings envelope, never an inferred bank-complete result: %j',
    async ({ findings }) => {
      vi.mocked(api.get).mockImplementation(async (url) => {
        if (String(url).endsWith('/reconciliation')) {
          return ok({ findings, external_bank_reconciled: false })
        }
        if (String(url).endsWith('/credit')) return ok(emptyCredit)
        return ok({})
      })
      await mountExpansion(<CreditPanel finance />)
      await target()
      await userEvent.click(
        await screen.findByRole('tab', { name: 'Credit consistency findings' })
      )
      await waitFor(() =>
        expect(api.get).toHaveBeenCalledWith(
          '/api/agent-platform/credit/reconciliation',
          expect.objectContaining({ params: { agent_user_id: 8 } })
        )
      )
      expect(
        await screen.findByText(
          'These read-only findings check this account’s internal credit journals. No findings does not prove complete bank reconciliation and never changes money.'
        )
      ).toBeInTheDocument()
      if (findings.length) {
        expect(
          await screen.findByText('missing_credit_journal')
        ).toBeInTheDocument()
      } else {
        expect(await screen.findByText('No records found')).toBeInTheDocument()
      }
    }
  )
  test('singular statement line DTO renders and binds its authoritative review version', async () => {
    vi.mocked(api.get).mockImplementation(async (url) => {
      if (String(url).endsWith('/lines/3')) {
        return ok({
          id: 3,
          import_id: 4,
          period_id: 9,
          currency: 'JPY',
          amount_minor: '9007199254740993',
          review: { line_id: 3, state: 'unmatched', version: 6, match_id: 0 },
        })
      }
      if (String(url).endsWith('/imports/4')) {
        return ok({ id: 4, imported_by: 41 })
      }
      if (String(url).endsWith('/candidates')) return ok([])
      return ok(page([]))
    })
    await mountExpansion(
      <StatementLineReview actionsEnabled lineId={3} onClose={vi.fn()} />
    )
    expect(
      await screen.findByText(/JPY.*9,007,199,254,740,993/)
    ).toBeInTheDocument()
    await userEvent.click(
      await screen.findByRole('button', { name: 'Propose statement match' })
    )
    fireEvent.change(
      await screen.findByRole('combobox', { name: 'Match target type' }),
      { target: { value: 'purchase' } }
    )
    fireEvent.change(screen.getByRole('textbox', { name: 'Match target ID' }), {
      target: { value: '12' },
    })
    fireEvent.change(screen.getByRole('textbox', { name: 'Reason' }), {
      target: { value: 'Original verified receipt reviewed' },
    })
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
    await screen.findByRole('button', { name: 'Authorize transaction' })
    expect(vi.mocked(api.request).mock.calls[0][0].data).toMatchObject({
      action: 'statement.match.propose',
      payload: {
        line_id: 3,
        expected_version: 6,
        target_type: 'purchase',
        target_id: 12,
      },
    })
  })
  test('receipt evidence recovery stays reachable with manual reconciliation disabled and risk approved', async () => {
    vi.mocked(api.get).mockImplementation(async (url) =>
      ok(
        String(url).includes('/policy/')
          ? { manual_reconciliation_enabled: false, risk_policy_approved: true }
          : page([])
      )
    )
    await mountExpansion(<StatementPanel />)
    expect(
      await screen.findByRole('button', {
        name: 'Create reconciliation period',
      })
    ).toBeDisabled()
    await userEvent.click(
      screen.getByRole('tab', { name: 'Supplementary receipt evidence' })
    )
    expect(
      await screen.findByRole('button', {
        name: 'Propose missing receipt evidence',
      })
    ).toBeEnabled()
  })
  test('a rejection clears the review schedule and keeps explicit disabled zero risk caps', async () => {
    vi.mocked(api.get).mockImplementation(async (url) =>
      ok(
        String(url).includes('/policy/')
          ? { governance_enabled: true }
          : {
              profile: { version: 4 },
              governance: {
                user_id: 8,
                status: 'pending',
                version: 2,
                submission_id: 5,
                risk_limits: {
                  enabled: false,
                  daily_issuance_quota: 0,
                  daily_transfer_quota: 0,
                },
              },
              submission: null,
              review_required: true,
              review_reasons: [],
              audit: [],
            }
      )
    )
    await mountExpansion(<GovernancePanel finance />)
    await target()
    await userEvent.click(
      await screen.findByRole('button', {
        name: 'Review qualification dossier',
      })
    )
    fireEvent.change(
      await screen.findByRole('combobox', { name: 'Review decision' }),
      { target: { value: 'rejected' } }
    )
    for (const name of [
      'Daily gross issuance cap',
      'Daily gross transfer cap',
    ]) {
      fireEvent.change(screen.getByRole('textbox', { name }), {
        target: { value: '0' },
      })
    }
    fireEvent.change(screen.getByRole('textbox', { name: 'Reason' }), {
      target: { value: 'Missing independently verified agreement' },
    })
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
    await screen.findByRole('button', { name: 'Authorize transaction' })
    expect(vi.mocked(api.request).mock.calls[0][0].data).toMatchObject({
      action: 'governance.review',
      payload: {
        expected_version: 2,
        expected_profile_version: 4,
        decision: 'rejected',
        review_due_at: 0,
        risk_limits: {
          enabled: false,
          daily_issuance_quota: 0,
          daily_transfer_quota: 0,
        },
      },
    })
  })
  test('an approved never-submitted refund can be rejected independently without paying or resubmitting', async () => {
    vi.mocked(api.get).mockResolvedValue(
      ok(
        page([
          {
            id: 6,
            agent_user_id: 8,
            requested_by: 8,
            state: 'approved',
            submitted_by: 0,
            submitted_at: 0,
            version: 3,
            business_key: 'purchase-refund:6',
            currency: 'USD',
            currency_exponent: 2,
            principal_minor: '100',
            fee_minor: '0',
            net_minor: '100',
            quota: 500000,
          },
        ])
      )
    )
    await mountExpansion(<PurchaseRefunds finance />)
    await screen.findByRole('button', { name: 'Reject' })
    await act(async () => {})
    await userEvent.click(screen.getByRole('button', { name: 'Reject' }))
    const decision = await screen.findByRole('combobox', {
      name: 'Review decision',
    })
    expect(
      screen.queryByRole('option', { name: 'Approve' })
    ).not.toBeInTheDocument()
    fireEvent.change(decision, { target: { value: 'reject' } })
    fireEvent.change(
      screen.getByRole('textbox', { name: 'Review evidence reference' }),
      { target: { value: 'dossier:cancel-never-paid' } }
    )
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
    await screen.findByRole('button', { name: 'Authorize transaction' })
    expect(vi.mocked(api.request).mock.calls[0][0].data).toMatchObject({
      action: 'purchase.refund.reject',
      payload: { refund_id: 6, version: 3, decision: 'reject' },
    })
    expect(api.request).toHaveBeenCalledTimes(1)
  })
  test.each(['payment_pending', 'payment_unknown'])(
    'an in-flight %s refund cannot use prepayment rejection to release its hold',
    async (state) => {
      vi.mocked(api.get).mockResolvedValue(
        ok(
          page([
            {
              id: 6,
              agent_user_id: 8,
              requested_by: 8,
              state,
              submitted_by: 43,
              submitted_at: 1700000000,
              version: 3,
              business_key: 'purchase-refund:6',
              currency: 'USD',
              currency_exponent: 2,
              principal_minor: '100',
              fee_minor: '0',
              net_minor: '100',
              quota: 500000,
            },
          ])
        )
      )
      await mountExpansion(<PurchaseRefunds finance />)
      await screen.findByRole('button', { name: 'Reconcile payment result' })
      expect(
        screen.queryByRole('button', { name: 'Reject' })
      ).not.toBeInTheDocument()
      expect(
        screen.queryByRole('button', { name: 'Record payment submission' })
      ).not.toBeInTheDocument()
    }
  )
  const refund = {
    id: 3,
    business_key: 'deposit-refund:3',
    state: 'approved',
    version: 2,
    currency: 'USD',
    currency_exponent: 2,
    amount_minor: '12345',
  }
  test('restricted refund instructions are fetched only on demand and removed when closed', async () => {
    vi.mocked(api.get).mockResolvedValue(
      ok({
        ...refund,
        provider: 'bank_transfer',
        merchant_scope: 'bank:finance-01',
        environment: 'live',
        recipient_kind: 'original_payer_dossier',
        recipient_digest: 'a'.repeat(64),
        payer_dossier_reference: 'dossier:original-01',
        manual_only: true,
      })
    )
    await mountExpansion(
      <ManualRefundInstructionsButton kind='deposit' target={refund} />
    )
    await act(async () => {})
    expect(api.get).not.toHaveBeenCalled()
    await userEvent.click(
      screen.getByRole('button', {
        name: 'View restricted manual refund instructions',
      })
    )
    expect(await screen.findByText('dossier:original-01')).toBeInTheDocument()
    expect(sessionStorage.length).toBe(0)
    await userEvent.keyboard('{Escape}')
    await waitFor(() =>
      expect(screen.queryByText('dossier:original-01')).not.toBeInTheDocument()
    )
    expect(api.request).not.toHaveBeenCalled()
  })
  test('legacy in-flight refund instructions explicitly prohibit another payment', async () => {
    const target = { ...refund, state: 'payment_unknown' }
    vi.mocked(api.get).mockResolvedValue(
      ok({
        ...target,
        provider: 'bank_transfer',
        merchant_scope: 'bank:finance-01',
        environment: 'live',
        recipient_kind: 'original_payer_dossier',
        recipient_digest: 'a'.repeat(64),
        payer_dossier_reference: 'dossier:original-01',
        manual_only: true,
        reconciliation_only: true,
      })
    )
    await mountExpansion(
      <ManualRefundInstructionsButton kind='purchase' target={target} />
    )
    await act(async () => {})
    await userEvent.click(
      screen.getByRole('button', {
        name: 'View restricted manual refund instructions',
      })
    )
    expect(
      await screen.findByText(
        'Historical in-flight refund: investigate and reconcile the original payment only. Do not send another payment.'
      )
    ).toBeInTheDocument()
    expect(screen.getByText('deposit-refund:3')).toBeInTheDocument()
    expect(screen.getByText(/USD.*123.45/)).toBeInTheDocument()
    expect(api.request).not.toHaveBeenCalled()
  })
  test('changed refund version never displays a stale controlled payer reference', async () => {
    vi.mocked(api.get).mockResolvedValue(
      ok({
        ...refund,
        version: 3,
        recipient_kind: 'original_payer_dossier',
        recipient_digest: 'a'.repeat(64),
        payer_dossier_reference: 'dossier:stale-001',
        manual_only: true,
      })
    )
    await mountExpansion(
      <ManualRefundInstructionsButton kind='deposit' target={refund} />
    )
    await act(async () => {})
    await userEvent.click(
      screen.getByRole('button', {
        name: 'View restricted manual refund instructions',
      })
    )
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(1))
    expect(
      await screen.findByRole('button', { name: /retry/i })
    ).toBeInTheDocument()
    expect(screen.queryByText('dossier:stale-001')).not.toBeInTheDocument()
  })
})
