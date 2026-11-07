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
import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { beforeEach, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import viLocale from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'
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
    run('failed', 1800000100, { status: 'failed', fingerprint_result: null }),
    run('unknown', 1800000101, {
      fingerprint_result: { ...result, status: 'unscorable' },
    })
  )
  const effective = effectiveFingerprintRuns(runs)
  expect(effective).toHaveLength(60)
  expect(effective[0].id).toBe('r5')
  expect(effective.at(-1)?.id).toBe('r64')
})
test('a short timeline fills unused slots from the latest result and opens actual reports by keyboard oldest first', async () => {
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
  const timeline = screen.getByRole('list', { name: /User tests/ })
  const points = within(timeline).getAllByRole('button')
  expect(
    within(timeline).getAllByRole('listitem', { hidden: true })
  ).toHaveLength(60)
  expect(points).toHaveLength(2)
  expect(timeline.querySelector('.bg-muted')).not.toBeInTheDocument()
  expect(timeline.firstElementChild?.querySelector('span')).toHaveClass(
    'bg-emerald-500'
  )
  expect(
    screen.getByText('Filled from the latest full-capability result')
  ).toBeVisible()
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

test.each(['site', 'user'] as const)(
  'a public %s result without a report does not read or display cached owner details',
  (source) => {
    const get = vi.spyOn(api, 'get')
    const client = new QueryClient()
    const publicRun = run('public-metadata', 1800000000, {
      source,
      visibility: 'public',
      status: 'unknown',
      fingerprint_result: null,
    })
    client.setQueryData(
      ['model-tests', 'detail', publicRun.id, publicRun.status],
      {
        ...publicRun,
        visibility: undefined,
        fingerprint_result: {
          ...result,
          top_candidates: [
            {
              ...result.top_candidates[0],
              display_name: 'Private cached reference',
            },
          ],
        },
        artwork_html: '',
        prompt: 'Private cached prompt',
      }
    )
    render(
      <QueryClientProvider client={client}>
        <TestResultDialog run={publicRun} onClose={() => undefined} />
      </QueryClientProvider>
    )
    expect(get).not.toHaveBeenCalled()
    expect(
      screen.getByText(en.translation['fingerprint.noReport'])
    ).toBeVisible()
    expect(
      screen.queryByText('Private cached reference')
    ).not.toBeInTheDocument()
    expect(screen.queryByText('Private cached prompt')).not.toBeInTheDocument()
  }
)

test('a target awaiting its first result has green initialization slots without creating reports or starting paid tests', () => {
  const estimate = vi.fn()
  render(
    <ModelTestCard
      model='long-model-name-that-must-wrap-on-mobile'
      group='default'
      runs={[]}
      siteRuns={[]}
      disabled={false}
      estimating={false}
      allowTest={false}
      onEstimate={estimate}
      onSelect={() => undefined}
    />
  )
  const timelines = screen.getAllByRole('list', {
    name: new RegExp(en.translation['intelligence.timeline']),
  })
  expect(timelines).toHaveLength(2)
  for (const timeline of timelines) {
    expect(
      within(timeline).getAllByRole('listitem', { hidden: true })
    ).toHaveLength(60)
    expect(within(timeline).queryByRole('button')).not.toBeInTheDocument()
    expect(timeline.querySelector('.bg-muted')).not.toBeInTheDocument()
    expect(timeline.firstElementChild?.querySelector('span')).toHaveClass(
      'bg-emerald-500/30'
    )
  }
  expect(screen.getAllByText('Awaiting first result')).toHaveLength(2)
  expect(screen.queryByText('Full capability')).not.toBeInTheDocument()
  expect(estimate).not.toHaveBeenCalled()
  expect(
    screen.queryByRole('button', { name: 'Self-test' })
  ).not.toBeInTheDocument()
})

test.each([1, 2])(
  'a timeout after %s valid samples shows the available full-capability assessment',
  (accepted) => {
    render(
      <ModelTestCard
        model='requested-alias'
        group='default'
        runs={[
          run('partial', 1800000000, {
            status: 'timeout',
            fingerprint_result: {
              ...result,
              status: 'partial',
              used_outputs: accepted,
              top_candidates: result.top_candidates.map((candidate) => ({
                ...candidate,
                probability: null,
              })),
            },
          }),
        ]}
        disabled={false}
        estimating={false}
        allowTest={false}
        onEstimate={() => undefined}
        onSelect={() => undefined}
      />
    )
    const timeline = screen.getByRole('list', { name: /User tests/ })
    expect(
      within(timeline).getByRole('button', { name: /^Full capability/ })
    ).toBeVisible()
    expect(screen.queryByText('Reduced capability')).not.toBeInTheDocument()
  }
)

test('a reduced-capability result stays red after a later full result fills the leading slots', async () => {
  const select = vi.fn()
  const reduced = run('reduced', 1800000001, {
    status: 'failed',
    fingerprint_result: {
      ...result,
      status: 'unscorable',
      used_outputs: 0,
      top_candidates: [],
      sample_quality: result.sample_quality.map((sample) => ({
        ...sample,
        accepted: false,
        parsed_numbers: 10,
        reason: 'insufficient_numbers',
      })),
    },
  })
  render(
    <ModelTestCard
      model='requested-alias'
      group='default'
      runs={[reduced, run('full', 1800000002)]}
      disabled={false}
      estimating={false}
      allowTest={false}
      onEstimate={() => undefined}
      onSelect={select}
    />
  )
  const timeline = screen.getByRole('list', { name: /User tests/ })
  const reducedPoint = within(timeline).getByRole('button', {
    name: /^Reduced capability/,
  })
  expect(reducedPoint.querySelector('span')).toHaveClass('bg-red-500')
  expect(timeline.firstElementChild?.querySelector('span')).toHaveClass(
    'bg-emerald-500'
  )
  await userEvent.click(reducedPoint)
  expect(select).toHaveBeenCalledWith(reduced)
})

test.each(['request_failed', 'non_model_response'])(
  'an upstream %s produces a test-error detail without a reduced-capability verdict',
  (reason) => {
    render(
      <ModelTestCard
        model='requested-alias'
        group='default'
        runs={[
          run('error', 1800000000, {
            status: 'failed',
            fingerprint_result: {
              ...result,
              status: 'unscorable',
              used_outputs: 0,
              top_candidates: [],
              sample_quality: result.sample_quality.map((sample) => ({
                ...sample,
                accepted: false,
                parsed_numbers: 0,
                reason,
              })),
            },
          }),
        ]}
        disabled={false}
        estimating={false}
        allowTest={false}
        onEstimate={() => undefined}
        onSelect={() => undefined}
      />
    )
    expect(screen.getByText('Test error')).toBeVisible()
    expect(screen.queryByText('Reduced capability')).not.toBeInTheDocument()
    expect(
      within(screen.getByRole('list', { name: /User tests/ })).queryByRole(
        'button'
      )
    ).not.toBeInTheDocument()
  }
)

test('changing the interface language updates the assessment label and report date', async () => {
  i18next.addResourceBundle('zhCN', 'translation', zh.translation, true, true)
  render(<FingerprintReport run={run('one', 1800000000)} />)
  expect(screen.getByText('Full capability')).toBeVisible()
  await act(async () => {
    await i18next.changeLanguage('zhCN')
  })
  expect(screen.getByText('满血')).toBeVisible()
  expect(screen.queryByText('Full capability')).not.toBeInTheDocument()
  expect(
    screen.getByText(new Date(1800000000 * 1000).toLocaleString('zh-CN'))
  ).toBeVisible()
})

test('site and community rows share the same sixty-result limit for one target', () => {
  render(
    <ModelTestCard
      model='requested-alias'
      group='default'
      siteRuns={Array.from({ length: 31 }, (_, index) =>
        run(`site-${index}`, 1800000000 + index * 2)
      )}
      runs={Array.from({ length: 31 }, (_, index) =>
        run(`user-${index}`, 1800000001 + index * 2)
      )}
      disabled={false}
      estimating={false}
      allowTest={false}
      onEstimate={() => undefined}
      onSelect={() => undefined}
    />
  )
  const timelines = screen.getAllByRole('list', {
    name: new RegExp(en.translation['intelligence.timeline']),
  })
  for (const timeline of timelines) {
    expect(within(timeline).getAllByRole('button')).toHaveLength(30)
  }
})

test('an assessed non-model sequence is red while blocked non-model replies are excluded', () => {
  const sequence = run('sequence', 1800000000, {
    status: 'failed',
    fingerprint_result: {
      ...result,
      status: 'not_model',
      top_candidates: [],
    },
  })
  render(
    <ModelTestCard
      model='requested-alias'
      group='default'
      runs={[sequence]}
      disabled={false}
      estimating={false}
      allowTest={false}
      onEstimate={() => undefined}
      onSelect={() => undefined}
    />
  )
  const point = screen.getByRole('button', { name: /^Reduced capability/ })
  expect(point.querySelector('span')).toHaveClass('bg-red-500')
  expect(
    screen
      .getByRole('list', { name: /User tests/ })
      .firstElementChild?.querySelector('span')
  ).toHaveClass('bg-emerald-500/30')
})

test('a newly reduced result retains a red current badge while earlier full results supply green padding', () => {
  render(
    <ModelTestCard
      model='requested-alias'
      group='default'
      runs={[
        run('full', 1800000000),
        run('reduced', 1800000001, {
          status: 'failed',
          fingerprint_result: {
            ...result,
            status: 'not_model',
            top_candidates: [],
          },
        }),
      ]}
      disabled={false}
      estimating={false}
      allowTest={false}
      onEstimate={() => undefined}
      onSelect={() => undefined}
    />
  )
  const timeline = screen.getByRole('list', { name: /User tests/ })
  expect(timeline.firstElementChild?.querySelector('span')).toHaveClass(
    'bg-emerald-500'
  )
  expect(
    within(timeline)
      .getByRole('button', { name: /^Reduced capability/ })
      .querySelector('span')
  ).toHaveClass('bg-red-500')
  expect(screen.getByText('Reduced capability')).toBeVisible()
  expect(screen.queryByText('Full capability')).not.toBeInTheDocument()
})

test('a network failure after a successful test replaces the current badge with an error and keeps the earlier report', () => {
  render(
    <ModelTestCard
      model='requested-alias'
      group='default'
      runs={[
        run('full', 1800000000),
        run('failed', 1800000001, {
          status: 'timeout',
          fingerprint_result: null,
        }),
      ]}
      disabled={false}
      estimating={false}
      allowTest={false}
      onEstimate={() => undefined}
      onSelect={() => undefined}
    />
  )
  expect(screen.getByText('Test error')).toBeVisible()
  expect(screen.queryByText('Full capability')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: /^Full capability/ })).toBeVisible()
})

test.each([
  { language: 'en', expected: '75%' },
  { language: 'zhCN', expected: '75%' },
  { language: 'zhTW', expected: '75%' },
  { language: 'fr', expected: '75\u00a0%' },
  { language: 'ru', expected: '75\u00a0%' },
  { language: 'ja', expected: '75%' },
  { language: 'vi', expected: '75%' },
  { language: 'invalid_language', expected: '75%' },
])(
  'report percentages follow $language typography with a safe fallback',
  async ({ language, expected }) => {
    const resources: Record<string, typeof en.translation> = {
      en: en.translation,
      zhCN: zh.translation,
      zhTW: zhTW.translation,
      fr: fr.translation,
      ru: ru.translation,
      ja: ja.translation,
      vi: viLocale.translation,
    }
    i18next.addResourceBundle(
      language,
      'translation',
      resources[language] ?? en.translation,
      true,
      true
    )
    await i18next.changeLanguage(language)
    render(<FingerprintReport run={run('one', 1800000000)} />)
    expect(
      screen.getByText(expected, { exact: true, normalizer: (text) => text })
    ).toBeVisible()
  }
)
