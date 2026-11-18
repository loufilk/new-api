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
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { useState } from 'react'
import { I18nextProvider } from 'react-i18next'
import { afterEach, describe, expect, it } from 'vitest'

import type { ApiKeyGroupOption } from '@/features/keys/components/api-key-group-combobox'

import { ApiKeyGroupSelection } from '../group-selection'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})
const options: ApiKeyGroupOption[] = [
  {
    value: 'default',
    label: 'default',
    desc: 'Standard access',
    ratio: 0.6,
    health: {
      name: 'default',
      ratio: 0.6,
      available: true,
      success_rate: 0.98,
      history: [0.98],
    },
  },
  { value: 'vip', label: 'vip', desc: 'Priority access', ratio: 0 },
  { value: 'fast', label: 'fast', desc: 'Low latency route', ratio: 0.4 },
]

function Harness(props: {
  initial?: string[]
  maxCount?: number
  disabled?: boolean
  options?: ApiKeyGroupOption[]
}) {
  const [values, setValues] = useState(props.initial ?? [])
  return (
    <I18nextProvider i18n={i18n}>
      <ApiKeyGroupSelection
        aria-label='Group'
        options={props.options ?? options}
        values={values}
        onChange={setValues}
        maxCount={props.maxCount ?? 3}
        disabled={props.disabled}
      />
    </I18nextProvider>
  )
}

afterEach(cleanup)

describe('Key group selection panel', () => {
  it('searches real descriptions and selects a group with the keyboard before returning focus to the trigger', async () => {
    const user = userEvent.setup()
    render(<Harness initial={['default']} />)
    const trigger = screen.getByRole('combobox', { name: 'Group' })
    trigger.focus()
    await user.keyboard('{Enter}')
    const dialog = screen.getByRole('dialog', {
      name: 'Select groups in routing order',
    })
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
    expect(trigger).toHaveAttribute('aria-haspopup', 'dialog')

    const search = within(dialog).getByPlaceholderText('Search group name')
    await user.type(search, 'Priority access')
    expect(within(dialog).getAllByRole('option')).toHaveLength(1)
    await user.keyboard('{Enter}')
    expect(
      within(dialog).getByRole('option', { name: /^vip/ })
    ).toHaveAttribute('aria-checked', 'true')
    await user.keyboard('{Escape}')

    await waitFor(() => expect(trigger).toHaveFocus())
    expect(trigger).toHaveTextContent('default → vip')
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
  })

  it('shows only selected catalog groups while retaining their manual queue order', async () => {
    const user = userEvent.setup()
    render(<Harness initial={['vip', 'default']} />)
    await user.click(screen.getByRole('combobox', { name: 'Group' }))
    await user.click(screen.getByRole('button', { name: 'Selected groups' }))
    expect(
      screen.getByRole('button', { name: 'Selected groups' })
    ).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getAllByRole('option')).toHaveLength(2)
    expect(
      screen.queryByRole('option', { name: /^fast/ })
    ).not.toBeInTheDocument()
    const queue = screen.getByRole('region', { name: 'Routing queue' })
    const drag = within(queue).getByRole('button', {
      name: 'Drag default to reorder',
    })
    drag.focus()
    await user.keyboard('{ArrowUp}')
    await user.click(screen.getByRole('button', { name: 'Done' }))
    expect(screen.getByRole('combobox', { name: 'Group' })).toHaveTextContent(
      'default → vip'
    )
  })

  it('blocks new selections at the limit and lets the user remove an existing group', async () => {
    const user = userEvent.setup()
    render(<Harness initial={['default', 'vip']} maxCount={2} />)
    await user.click(screen.getByRole('combobox', { name: 'Group' }))
    expect(screen.getByRole('option', { name: /^fast/ })).toHaveAttribute(
      'aria-disabled',
      'true'
    )
    await user.click(screen.getByRole('button', { name: 'Remove vip' }))
    expect(screen.getByRole('option', { name: /^fast/ })).not.toHaveAttribute(
      'aria-disabled',
      'true'
    )
    await user.click(screen.getByRole('option', { name: /^fast/ }))
    await user.click(screen.getByRole('button', { name: 'Done' }))
    expect(screen.getByRole('combobox', { name: 'Group' })).toHaveTextContent(
      'default → fast'
    )
  })

  it('retains revoked groups in the queue with an unavailable state until explicitly removed', async () => {
    const user = userEvent.setup()
    render(<Harness initial={['revoked', 'default']} />)
    await user.click(screen.getByRole('combobox', { name: 'Group' }))
    const queue = screen.getByRole('region', { name: 'Routing queue' })
    expect(within(queue).getAllByText('Unavailable').length).toBeGreaterThan(0)
    expect(
      within(queue).getByRole('button', { name: 'Remove revoked' })
    ).toBeEnabled()
    expect(
      screen.queryByRole('option', { name: /^revoked/ })
    ).not.toBeInTheDocument()
    await user.click(
      within(queue).getByRole('button', { name: 'Remove default' })
    )
    await user.click(screen.getByRole('button', { name: 'Done' }))
    expect(screen.getByRole('combobox', { name: 'Group' })).toHaveTextContent(
      'revoked'
    )
  })

  it('shows a zero effective multiplier and unknown performance without inventing metrics', async () => {
    const user = userEvent.setup()
    render(<Harness />)
    await user.click(screen.getByRole('combobox', { name: 'Group' }))
    const card = screen.getByRole('option', { name: /^vip/ })
    expect(within(card).getByText('0x')).toBeVisible()
    expect(within(card).getByText('Availability unknown')).toBeVisible()
    expect(within(card).getByText('No performance data')).toBeVisible()
    expect(within(card).queryByText(/% success rate/)).not.toBeInTheDocument()
  })

  it('shows an empty search result without changing the selected queue', async () => {
    const user = userEvent.setup()
    render(<Harness initial={['default']} />)
    await user.click(screen.getByRole('combobox', { name: 'Group' }))
    await user.type(
      screen.getByPlaceholderText('Search group name'),
      'missing-group'
    )
    expect(screen.getByText('No group found.')).toBeVisible()
    expect(screen.queryByRole('option')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Remove default' })).toBeEnabled()
    await user.click(screen.getByRole('button', { name: 'Done' }))
    expect(screen.getByRole('combobox', { name: 'Group' })).toHaveTextContent(
      'default'
    )
  })

  it('closes an open selector when disabled and preserves the selected groups', async () => {
    const user = userEvent.setup()
    const view = render(<Harness initial={['default']} />)
    await user.click(screen.getByRole('combobox', { name: 'Group' }))
    view.rerender(<Harness initial={['default']} disabled />)
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    expect(screen.getByRole('combobox', { name: 'Group' })).toBeDisabled()
    expect(screen.getByRole('combobox', { name: 'Group' })).toHaveTextContent(
      'default'
    )
  })

  it('keeps an empty catalog usable without creating a default selection', async () => {
    const user = userEvent.setup()
    render(<Harness options={[]} />)
    await user.click(screen.getByRole('combobox', { name: 'Group' }))
    expect(screen.getByText('No group found.')).toBeVisible()
    expect(screen.getByText('Select a group')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Done' }))
    expect(screen.getByRole('combobox', { name: 'Group' })).toHaveTextContent(
      'Select groups in routing order'
    )
  })
})
