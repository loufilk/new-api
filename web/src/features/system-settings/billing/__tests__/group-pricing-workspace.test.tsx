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
import { useState } from 'react'
import { describe, expect, it } from 'vitest'

import { GroupPricingWorkspace } from '../group-pricing-workspace'

function RatioPane(props: { panel: string; active: boolean }) {
  const [value, setValue] = useState('')
  return (
    <>
      <span data-testid='ratio-state'>
        {props.panel}:{props.active ? 'active' : 'hidden'}
      </span>
      <input
        aria-label='Ratio draft'
        value={value}
        onChange={(event) => setValue(event.target.value)}
      />
    </>
  )
}

function CatalogPane(props: { panel: string }) {
  const [value, setValue] = useState('')
  return (
    <>
      <span data-testid='catalog-state'>{props.panel}</span>
      <input
        aria-label='Catalog draft'
        value={value}
        onChange={(event) => setValue(event.target.value)}
      />
    </>
  )
}

function renderWorkspace() {
  render(
    <GroupPricingWorkspace
      renderRatio={(panel, active) => (
        <RatioPane panel={panel} active={active} />
      )}
      renderCatalog={(panel) => <CatalogPane panel={panel} />}
    />
  )
  return within(
    screen.getByRole('navigation', { name: 'Group pricing sections' })
  )
}

describe('group pricing workspace shell', () => {
  it('keeps a single mounted ratio and catalog editor and preserves their drafts', async () => {
    const user = userEvent.setup()
    const nav = renderWorkspace()
    const ratio = screen.getByRole('textbox', { name: 'Ratio draft' })
    const catalog = screen.getByRole('textbox', { name: 'Catalog draft' })
    await user.type(ratio, '0.50')
    await user.type(catalog, 'local draft')

    await user.click(nav.getByRole('button', { name: 'Advanced Rules' }))
    expect(screen.getByRole('region', { name: 'Advanced Rules' })).toBeVisible()
    expect(screen.getByTestId('ratio-state')).toHaveTextContent(
      'advanced:active'
    )
    expect(screen.queryByRole('textbox', { name: 'Catalog draft' })).toBeNull()

    const people = nav.getByRole('button', {
      name: 'Personal Pricing and Access',
    })
    await user.click(people)
    expect(people).toHaveAttribute('aria-current', 'true')
    expect(screen.queryByRole('textbox', { name: 'Ratio draft' })).toBeNull()
    expect(screen.getByTestId('ratio-state')).toHaveTextContent(
      'advanced:hidden'
    )
    expect(screen.getByTestId('catalog-state')).toHaveTextContent('people')
    expect(screen.getByRole('textbox', { name: 'Catalog draft' })).toBe(catalog)

    await user.click(nav.getByRole('button', { name: 'Groups' }))
    expect(screen.getByTestId('ratio-state')).toHaveTextContent(
      'pricing:active'
    )
    expect(screen.getByTestId('catalog-state')).toHaveTextContent('groups')
    expect(screen.getByRole('textbox', { name: 'Ratio draft' })).toBe(ratio)
    expect(ratio).toHaveValue('0.50')
    expect(catalog).toHaveValue('local draft')
  })

  it('supports keyboard section discovery and explicit activation', async () => {
    const user = userEvent.setup()
    const nav = renderWorkspace()
    const groups = nav.getByRole('button', { name: 'Groups' })
    const people = nav.getByRole('button', {
      name: 'Personal Pricing and Access',
    })
    const advanced = nav.getByRole('button', { name: 'Advanced Rules' })

    await user.tab()
    expect(groups).toHaveFocus()
    await user.keyboard('{Control>}{End}{/Control}')
    expect(groups).toHaveFocus()
    await user.keyboard('{ArrowDown}')
    expect(people).toHaveFocus()
    expect(groups).toHaveAttribute('aria-current', 'true')
    await user.keyboard('{Enter}')
    expect(people).toHaveAttribute('aria-current', 'true')
    expect(groups).not.toHaveAttribute('aria-current')
    expect(
      screen.getByRole('region', { name: 'Personal Pricing and Access' })
    ).toHaveAttribute('id', people.getAttribute('aria-controls'))

    await user.keyboard('{End}')
    expect(advanced).toHaveFocus()
    await user.keyboard('{Home}')
    expect(groups).toHaveFocus()
  })
})
