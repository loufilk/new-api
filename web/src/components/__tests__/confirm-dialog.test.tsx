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
import { describe, expect, test, vi } from 'vitest'

import { ConfirmDialog } from '../confirm-dialog'

describe('confirm dialog', () => {
  test('shows title and description when open', () => {
    render(
      <ConfirmDialog
        open
        onOpenChange={() => undefined}
        title='Delete user'
        desc='This action cannot be undone.'
        handleConfirm={() => undefined}
      />
    )

    expect(screen.getByText('Delete user')).toBeInTheDocument()
    expect(
      screen.getByText('This action cannot be undone.')
    ).toBeInTheDocument()
  })

  test('applies a custom className to the dialog content once', () => {
    render(
      <ConfirmDialog
        open
        onOpenChange={() => undefined}
        title='Delete user'
        desc='Confirm deletion.'
        className='custom-confirm-width'
        handleConfirm={() => undefined}
      />
    )

    const content = document.querySelector('[data-slot="alert-dialog-content"]')
    expect(content?.className.includes('custom-confirm-width')).toBe(true)
  })

  test('calls handleConfirm when the confirm button is clicked', () => {
    const handleConfirm = vi.fn()
    render(
      <ConfirmDialog
        open
        onOpenChange={() => undefined}
        title='Delete user'
        desc='Confirm deletion.'
        handleConfirm={handleConfirm}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: 'Continue' }))
    expect(handleConfirm).toHaveBeenCalledTimes(1)
  })
})
