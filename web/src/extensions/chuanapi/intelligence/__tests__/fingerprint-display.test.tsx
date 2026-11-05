import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
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
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { beforeEach, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import { api } from '@/lib/api'

import { FingerprintReport } from '../fingerprint-report'
import { ModelTestCard } from '../model-card'
import { TestResultDialog } from '../result-dialog'
import { effectiveFingerprintRuns } from '../timeline'
import type { FingerprintResult, TestRun } from '../types'

const result: FingerprintResult = {
  status: 'scored',
  reason: '',
  top_candidates: [
    {
      model: 'reference-model',
      display_name: 'Reference model',
      family: 'reference',
      family_name: 'Reference',
      score: 2,
      probability: 0.75,
    },
  ],
  sample_quality: [
    {
      challenge_id: 'probe-1',
      expected_count: 300,
      parsed_numbers: 300,
      minimum_numbers: 270,
      accepted: true,
      truncated: false,
      out_of_range_count: 0,
      reason: '',
    },
  ],
  used_outputs: 1,
  data_version: '2026-10-01',
  data_sha256: 'abc123',
  sdk_commit: 'sdk-pinned',
  probability_note: 'Closed set',
  probability_status: 'available',
  strict: true,
}
function run(
  id: string,
  timestamp: number,
  overrides: Partial<TestRun> = {}
): TestRun {
  return {
    id,
    model: 'requested-alias',
    group: 'default',
    source: 'user',
    benchmark_id: 'local-fingerprint-v1',
    status: 'completed',
    assessment: 'statistical',
    created_at: timestamp,
    completed_at: timestamp,
    max_output_tokens: 2048,
    estimated_quota: null,
    request_id: id,
    error_code: '',
    cancel_requested: false,
    fingerprint_result: result,
    ...overrides,
  }
}
beforeEach(async () => {
  i18next.addResourceBundle('en', 'translation', en.translation, true, true)
  await i18next.changeLanguage('en')
})
test('effective history keeps latest sixty scored results oldest first while excluding failures and unknowns', () => {
  const runs = Array.from({ length: 65 }, (_, i) =>
    run(`r${i}`, 1800000000 + i)
  )
  runs.push(
    run('failed', 1800000100, { status: 'failed' }),
    run('unknown', 1800000101, {
      fingerprint_result: { ...result, status: 'unscorable' },
    })
  )
  const effective = effectiveFingerprintRuns(runs)
  expect(effective).toHaveLength(60)
  expect(effective[0].id).toBe('r5')
  expect(effective.at(-1)?.id).toBe('r64')
})
test('timeline has grey empty slots and opens private report by keyboard with oldest point first', async () => {
  const select = vi.fn()
  render(
    <ModelTestCard
      model='requested-alias'
      group='default'
      siteRuns={[]}
      runs={[run('newer', 1800000002), run('older', 1800000001)]}
      disabled={false}
      estimating={false}
      onEstimate={() => undefined}
      onSelect={select}
    />
  )
  const timeline = screen.getByRole('list', { name: /My private results/ })
  const points = within(timeline).getAllByRole('button')
  expect(
    within(timeline).getAllByRole('listitem', { hidden: true })
  ).toHaveLength(60)
  expect(points).toHaveLength(2)
  expect(timeline).toHaveClass(
    'overflow-x-auto',
    'grid-cols-[repeat(60,minmax(2.75rem,1fr))]'
  )
  expect(points[0]).toHaveClass('h-11')
  await userEvent.tab()
  expect(points[0]).toHaveFocus()
  expect(await screen.findByRole('tooltip')).toHaveTextContent(
    'Reference model'
  )
  await userEvent.keyboard('{Enter}')
  expect(select).toHaveBeenCalledWith(expect.objectContaining({ id: 'older' }))
})
test('report labels closed-set probability, quality and version without inventing requested identity matches', () => {
  render(<FingerprintReport run={run('one', 1800000000)} />)
  expect(screen.getByText('75%')).toBeVisible()
  expect(
    screen.getByText(/relative only to models in the bundled reference library/)
  ).toBeVisible()
  expect(screen.getByText('sdk-pinned')).toBeVisible()
  expect(screen.getByText('2026-10-01')).toBeVisible()
  expect(screen.getByText(/Numbers: 300/)).toBeVisible()
  expect(screen.queryByText(/Mismatch|Genuine/)).not.toBeInTheDocument()
})
test('site report opens without requesting an owned-user run detail endpoint', () => {
  const get = vi.spyOn(api, 'get')
  const client = new QueryClient()
  render(
    <QueryClientProvider client={client}>
      <TestResultDialog
        run={run('site-one', 1800000000, { source: 'site' })}
        onClose={() => undefined}
      />
    </QueryClientProvider>
  )
  expect(screen.getByRole('dialog')).toHaveTextContent('Reference model')
  expect(get).not.toHaveBeenCalled()
})
