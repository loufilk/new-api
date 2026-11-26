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
        aria-label={`${props.panel} draft`}
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
  it('keeps separate base and advanced forms and preserves each draft across all sections', async () => {
    const user = userEvent.setup()
    const nav = renderWorkspace()
    const catalog = screen.getByRole('textbox', { name: 'Catalog draft' })
    await user.type(catalog, 'local draft')
    await user.click(nav.getByRole('button', { name: 'Base group rates' }))
    const ratio = screen.getByRole('textbox', { name: 'pricing draft' })
    await user.type(ratio, '0.50')
    expect(catalog).not.toBeVisible()

    await user.click(nav.getByRole('button', { name: 'Advanced Rules' }))
    const advanced = screen.getByRole('textbox', { name: 'advanced draft' })
    expect(advanced).toHaveValue('')
    await user.type(advanced, 'different draft')
    for (const [section, panel] of [
      ['Exclusive group access', 'access'],
      ['Personal pricing', 'personal'],
      ['Marketplace display', 'groups'],
    ]) {
      await user.click(nav.getByRole('button', { name: section }))
      expect(screen.getByTestId('catalog-state')).toHaveTextContent(panel)
      expect(ratio).not.toBeVisible()
      expect(advanced).not.toBeVisible()
    }
    expect(catalog).toHaveValue('local draft')
    await user.click(nav.getByRole('button', { name: 'Base group rates' }))
    expect(ratio).toBeVisible()
    expect(ratio).toHaveValue('0.50')
    expect(advanced).not.toBeVisible()
    expect(advanced).toHaveValue('different draft')
  })

  it('supports keyboard section discovery and explicit activation', async () => {
    const user = userEvent.setup()
    const nav = renderWorkspace()
    const groups = nav.getByRole('button', { name: 'Marketplace display' })
    const people = nav.getByRole('button', {
      name: 'Base group rates',
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
      screen.getByRole('region', { name: 'Base group rates' })
    ).toHaveAttribute('id', people.getAttribute('aria-controls'))

    await user.keyboard('{End}')
    expect(advanced).toHaveFocus()
    await user.keyboard('{Home}')
    expect(groups).toHaveFocus()
  })
})
