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
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, expect, it } from 'vitest'

import { ApiKeyGroupSelection } from '../group-selection'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})
afterEach(cleanup)

it('stacks three bounded sections on small screens and gives the catalog and queue independent scrolling', () => {
  const longName =
    'group-with-a-long-unbroken-name-that-must-not-expand-the-dialog-width'
  render(
    <I18nextProvider i18n={i18n}>
      <ApiKeyGroupSelection
        aria-label='Group'
        options={[{ value: longName, label: longName, ratio: 0.4 }]}
        values={[longName]}
        onChange={() => undefined}
      />
    </I18nextProvider>
  )
  fireEvent.click(screen.getByRole('combobox', { name: 'Group' }))
  const dialog = screen.getByRole('dialog')
  expect(dialog).toHaveClass('w-[calc(100dvw-2rem)]', 'sm:max-w-[1080px]')
  expect(dialog.querySelector('[data-slot="command"]')).toHaveClass(
    'grid',
    'min-w-0',
    'lg:grid-cols-[10rem_minmax(0,1fr)_20rem]'
  )
  for (const name of ['Group filters', 'Group catalog', 'Routing queue']) {
    expect(screen.getByRole('region', { name })).toHaveClass('min-w-0')
  }
  expect(screen.getByRole('listbox', { name: 'Available groups' })).toHaveClass(
    'max-h-[40dvh]',
    'overflow-y-auto',
    'overscroll-contain'
  )
  expect(screen.getByRole('group', { name: 'Routing queue' })).toHaveClass(
    'max-h-[40dvh]',
    'overflow-y-auto',
    'min-w-0'
  )
  const card = screen.getByRole('option')
  expect(card).toHaveClass('min-w-0')
  expect(card.querySelector('.font-medium')).toHaveClass(
    '[overflow-wrap:anywhere]'
  )
})
