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
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { beforeEach, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'

import { SelfTestDialog } from '../self-test-dialog'
import { eligibleTestTokens } from '../token-selection'
import type { TestToken } from '../types'

const tokens: TestToken[] = [
  {
    id: 1,
    name: 'Matching',
    group: 'gpt',
    model_limits_enabled: false,
    model_limits: [],
    eligible_models: ['gpt-test'],
  },
  {
    id: 2,
    name: 'Other group',
    group: 'other',
    model_limits_enabled: false,
    model_limits: [],
    eligible_models: ['gpt-test'],
  },
  {
    id: 3,
    name: 'Other model',
    group: 'gpt',
    model_limits_enabled: true,
    model_limits: ['claude'],
    eligible_models: [],
  },
  {
    id: 4,
    name: 'Allowed model',
    group: 'gpt',
    model_limits_enabled: true,
    model_limits: ['gpt-test'],
    eligible_models: ['gpt-test'],
  },
]
beforeEach(async () => {
  i18next.addResourceBundle('en', 'translation', en.translation, true, true)
  await i18next.changeLanguage('en')
})
test('key selection excludes other groups and disallowed model limits even when supplied by a stale response', () => {
  expect(
    eligibleTestTokens(tokens, 'gpt', 'gpt-test').map((token) => token.id)
  ).toEqual([1, 4])
})
test('self-test requires explicit key selection and submits only ID after reviewing private billing text', async () => {
  const estimate = vi.fn()
  const user = userEvent.setup()
  render(
    <SelfTestDialog
      model='gpt-test'
      group='gpt'
      tokens={tokens}
      maxOutputTokens={2048}
      pending={false}
      disabled={false}
      onClose={() => undefined}
      onEstimate={estimate}
    />
  )
  const button = screen.getByRole('button', { name: 'Review cost and start' })
  expect(button).toBeDisabled()
  expect(screen.getByText(/Results are private to your account/)).toBeVisible()
  expect(
    screen.queryByRole('option', { name: /Other group|Other model/ })
  ).not.toBeInTheDocument()
  await user.selectOptions(
    screen.getByLabelText('Select an existing API key'),
    '4'
  )
  await user.click(button)
  expect(estimate).toHaveBeenCalledExactlyOnceWith({
    model: 'gpt-test',
    group: 'gpt',
    max_output_tokens: 2048,
    token_id: 4,
  })
})
test('pending or disabled eligibility cannot approve an estimate', async () => {
  const estimate = vi.fn()
  const user = userEvent.setup()
  render(
    <SelfTestDialog
      model='gpt-test'
      group='gpt'
      tokens={tokens}
      maxOutputTokens={2048}
      pending={false}
      disabled
      onClose={() => undefined}
      onEstimate={estimate}
    />
  )
  await user.selectOptions(
    screen.getByLabelText('Select an existing API key'),
    '1'
  )
  expect(
    screen.getByRole('button', { name: 'Review cost and start' })
  ).toBeDisabled()
  expect(estimate).not.toHaveBeenCalled()
})

test('without an eligible key the modal links to key creation and blocks estimates', async () => {
  const estimate = vi.fn()
  const router = createRouter({
    routeTree: createRootRoute({
      component: () => (
        <SelfTestDialog
          model='gpt-test'
          group='gpt'
          tokens={[]}
          maxOutputTokens={2048}
          pending={false}
          disabled={false}
          onClose={() => undefined}
          onEstimate={estimate}
        />
      ),
    }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  render(<RouterProvider router={router} />)
  expect(await screen.findByText('No eligible key in this group')).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Create an API key' })
  ).toHaveAttribute('href', '/keys')
  expect(
    screen.getByRole('button', { name: 'Review cost and start' })
  ).toBeDisabled()
  expect(estimate).not.toHaveBeenCalled()
})

test('server-resolved model eligibility admits supported aliases without trusting exact model-limit names', () => {
  const token = { ...tokens[2], eligible_models: ['gpt-test'] }
  expect(eligibleTestTokens([token], 'gpt', 'gpt-test')).toEqual([token])
  expect(
    eligibleTestTokens(
      [{ ...tokens[0], eligible_models: [] }],
      'gpt',
      'gpt-test'
    )
  ).toEqual([])
})
