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
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, test } from 'vitest'

import { useSearch } from '../search-context'
import { SearchProvider } from '../search-provider'

function Probe() {
  const { open, setOpen } = useSearch()
  return (
    <>
      <div data-testid='open-state'>{open ? 'open' : 'closed'}</div>
      <button type='button' onClick={() => setOpen(true)}>
        open search
      </button>
    </>
  )
}

describe('search provider', () => {
  test('throws when useSearch is used outside SearchProvider', () => {
    function Outside() {
      useSearch()
      return null
    }

    expect(() => render(<Outside />)).toThrow(
      'useSearch has to be used within SearchProvider'
    )
  })

  test('provides the search state to descendants', () => {
    render(
      <SearchProvider>
        <Probe />
      </SearchProvider>
    )

    expect(screen.getByTestId('open-state').textContent).toBe('closed')

    fireEvent.click(screen.getByRole('button', { name: 'open search' }))

    expect(screen.getByTestId('open-state').textContent).toBe('open')
  })

  test('toggles open with Ctrl+K for keyboard users', () => {
    render(
      <SearchProvider>
        <Probe />
      </SearchProvider>
    )

    expect(screen.getByTestId('open-state').textContent).toBe('closed')

    fireEvent.keyDown(document, { key: 'k', ctrlKey: true })

    expect(screen.getByTestId('open-state').textContent).toBe('open')
  })
})
