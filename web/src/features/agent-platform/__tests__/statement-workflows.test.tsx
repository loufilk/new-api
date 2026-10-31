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

import { StatementLineReview } from '../components/statement-line-review'
import { StatementPanel } from '../components/statements'
import { STATEMENT_CSV_HEADER } from '../lib/financial-inputs'
import {
  mountExpansion,
  ok,
  page,
  setupExpansionUser,
} from './expansion-fixtures'

const period = {
  id: 9,
  state: 'open',
  version: 3,
  account_dossier_reference: 'bank:account-01',
  currency: 'USD',
  starts_at: 1699999999,
  ends_at: 1700000100,
  created_by: 42,
  closed_by: 0,
}
beforeEach(() => setupExpansionUser())
describe('manual statement workflows', () => {
  test('an import preserves original CSV bytes and binds the selected period version before MFA', async () => {
    vi.mocked(api.get).mockImplementation(async (url) =>
      ok(
        String(url).includes('/policy/')
          ? { manual_reconciliation_enabled: true }
          : page([period])
      )
    )
    await mountExpansion(<StatementPanel />)
    await userEvent.click(
      await screen.findByRole('button', { name: 'Import bank statement' })
    )
    fireEvent.change(
      await screen.findByRole('textbox', {
        name: 'Controlled statement dossier ID',
      }),
      { target: { value: 'statement:oct-01' } }
    )
    const csv = `${STATEMENT_CSV_HEADER}\nbank:txn-001,1700000000,incoming,USD,9007199254740993,purchase:001,,evidence:001\n`
    fireEvent.change(screen.getByRole('textbox', { name: 'Statement CSV' }), {
      target: { value: csv },
    })
    fireEvent.change(screen.getByRole('textbox', { name: 'Reason' }), {
      target: { value: 'Reviewed October source' },
    })
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
    await screen.findByRole('button', { name: 'Authorize transaction' })
    expect(vi.mocked(api.request).mock.calls[0][0].data).toMatchObject({
      action: 'statement.import',
      payload: {
        period_id: 9,
        expected_version: 3,
        csv,
        source_reference: 'statement:oct-01',
      },
    })
  })
  test('invalid CSV amounts are rejected without preparing an import or closing the form', async () => {
    vi.mocked(api.get).mockImplementation(async (url) =>
      ok(
        String(url).includes('/policy/')
          ? { manual_reconciliation_enabled: true }
          : page([period])
      )
    )
    await mountExpansion(<StatementPanel />)
    await userEvent.click(
      await screen.findByRole('button', { name: 'Import bank statement' })
    )
    fireEvent.change(
      await screen.findByRole('textbox', {
        name: 'Controlled statement dossier ID',
      }),
      { target: { value: 'statement:oct-01' } }
    )
    fireEvent.change(screen.getByRole('textbox', { name: 'Statement CSV' }), {
      target: {
        value: `${STATEMENT_CSV_HEADER}\nbank:txn-001,1700000000,incoming,USD,1e4,purchase:001,,evidence:001\n`,
      },
    })
    fireEvent.change(screen.getByRole('textbox', { name: 'Reason' }), {
      target: { value: 'Reviewed October source' },
    })
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
    expect(
      await screen.findByText(
        'Invalid statement CSV. Check the exact header, integer amounts, dates, currencies and controlled references.'
      )
    ).toBeInTheDocument()
    expect(api.request).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Continue' })).toBeEnabled()
  })
  test('a late CSV file read cannot overwrite a newer manual edit and cancellation sends no request', async () => {
    vi.mocked(api.get).mockImplementation(async (url) =>
      ok(
        String(url).includes('/policy/')
          ? { manual_reconciliation_enabled: true }
          : page([period])
      )
    )
    await mountExpansion(<StatementPanel />)
    await userEvent.click(
      await screen.findByRole('button', { name: 'Import bank statement' })
    )
    const file = new File(['original'], 'statement.csv', { type: 'text/csv' })
    let finish!: (value: string) => void
    Object.defineProperty(file, 'text', {
      value: () =>
        new Promise<string>((resolve) => {
          finish = resolve
        }),
    })
    fireEvent.change(screen.getByLabelText('Choose statement CSV file'), {
      target: { files: [file] },
    })
    const input = screen.getByRole('textbox', { name: 'Statement CSV' })
    fireEvent.change(input, {
      target: { value: 'newer manually reviewed source' },
    })
    await act(async () => finish('obsolete file bytes'))
    expect(input).toHaveValue('newer manually reviewed source')
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(api.request).not.toHaveBeenCalled()
  })
  test('an oversized CSV file is never read and leaves the previous draft untouched', async () => {
    vi.mocked(api.get).mockImplementation(async (url) =>
      ok(
        String(url).includes('/policy/')
          ? { manual_reconciliation_enabled: true }
          : page([period])
      )
    )
    await mountExpansion(<StatementPanel />)
    await userEvent.click(
      await screen.findByRole('button', { name: 'Import bank statement' })
    )
    const file = new File(['a'.repeat(49153)], 'oversized.csv', {
      type: 'text/csv',
    })
    const read = vi.fn()
    Object.defineProperty(file, 'text', { value: read })
    fireEvent.change(screen.getByLabelText('Choose statement CSV file'), {
      target: { files: [file] },
    })
    expect(read).not.toHaveBeenCalled()
    expect(screen.getByRole('textbox', { name: 'Statement CSV' })).toHaveValue(
      `${STATEMENT_CSV_HEADER}\n`
    )
    expect(api.request).not.toHaveBeenCalled()
  })
  test('a match proposer cannot independently confirm their own evidence even with an exact candidate', async () => {
    vi.mocked(api.get).mockImplementation(async (url) => {
      const path = String(url)
      if (path.endsWith('/lines/3')) {
        return ok({
          id: 3,
          import_id: 4,
          period_id: 9,
          currency: 'USD',
          amount_minor: '100',
          review: { state: 'proposed', version: 2, match_id: 5 },
        })
      }
      if (path.endsWith('/imports/4')) return ok({ id: 4, imported_by: 41 })
      if (path.endsWith('/candidates')) {
        return ok([
          {
            target_type: 'purchase',
            target_id: 8,
            amount_minor: '100',
            currency: 'USD',
            business_reference: 'purchase:8',
            exception_code: '',
          },
        ])
      }
      if (path.endsWith('/matches')) {
        return ok(page([{ id: 5, proposed_by: 42, owner_user_id: 8 }]))
      }
      return ok(page([]))
    })
    await mountExpansion(
      <StatementLineReview actionsEnabled lineId={3} onClose={vi.fn()} />
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Confirm statement match' })
      ).toBeDisabled()
    )
    expect(
      screen.getByText('Another independent operator must review this match.')
    ).toBeInTheDocument()
    expect(api.request).not.toHaveBeenCalled()
  })
})
