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
import { render, screen } from '@testing-library/react'
import { userEvent } from '@testing-library/user-event'
import { describe, it, expect, vi } from 'vitest'

import { RiskAcknowledgementDialog } from '../risk-acknowledgement-dialog'

describe('RiskAcknowledgementDialog', () => {
  it('should render items with duplicate text without key warnings', () => {
    const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {})

    render(
      <RiskAcknowledgementDialog
        open
        onOpenChange={() => {}}
        title='Test'
        items={['Delete all data', 'Delete all data', 'Delete all data']}
        onConfirm={() => {}}
      />
    )

    const listItems = screen.getAllByText('Delete all data')
    expect(listItems).toHaveLength(3)
    expect(consoleSpy).not.toHaveBeenCalledWith(
      expect.stringMatching(/key.*duplicate/i),
      expect.anything()
    )

    consoleSpy.mockRestore()
  })

  it('should render checklist with duplicate text without key warnings', () => {
    const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {})

    render(
      <RiskAcknowledgementDialog
        open
        onOpenChange={() => {}}
        title='Test'
        checklist={[
          'I understand the risks',
          'I understand the risks',
          'I accept responsibility',
        ]}
        onConfirm={() => {}}
      />
    )

    const checkboxes = screen.getAllByRole('checkbox')
    expect(checkboxes).toHaveLength(3)
    expect(consoleSpy).not.toHaveBeenCalledWith(
      expect.stringMatching(/key.*duplicate/i),
      expect.anything()
    )

    consoleSpy.mockRestore()
  })

  it('should maintain correct checked state by index even with duplicate text', async () => {
    const user = userEvent.setup()

    render(
      <RiskAcknowledgementDialog
        open
        onOpenChange={() => {}}
        title='Test'
        checklist={['Same text', 'Same text', 'Same text']}
        onConfirm={() => {}}
      />
    )

    const checkboxes = screen.getAllByRole('checkbox')

    await user.click(checkboxes[0])
    expect(checkboxes[0]).toBeChecked()
    expect(checkboxes[1]).not.toBeChecked()
    expect(checkboxes[2]).not.toBeChecked()

    await user.click(checkboxes[2])
    expect(checkboxes[0]).toBeChecked()
    expect(checkboxes[1]).not.toBeChecked()
    expect(checkboxes[2]).toBeChecked()

    await user.click(checkboxes[1])
    expect(checkboxes[0]).toBeChecked()
    expect(checkboxes[1]).toBeChecked()
    expect(checkboxes[2]).toBeChecked()
  })

  it('should preserve stable keys across re-renders with same props', () => {
    const { rerender } = render(
      <RiskAcknowledgementDialog
        open
        onOpenChange={() => {}}
        title='Test'
        items={['Item A', 'Item A', 'Item B']}
        checklist={['Check A', 'Check A']}
        onConfirm={() => {}}
      />
    )

    const firstRenderItems = screen.getAllByRole('listitem')
    const firstRenderCheckboxes = screen.getAllByRole('checkbox')

    rerender(
      <RiskAcknowledgementDialog
        open
        onOpenChange={() => {}}
        title='Test'
        items={['Item A', 'Item A', 'Item B']}
        checklist={['Check A', 'Check A']}
        onConfirm={() => {}}
      />
    )

    const secondRenderItems = screen.getAllByRole('listitem')
    const secondRenderCheckboxes = screen.getAllByRole('checkbox')

    expect(firstRenderItems).toHaveLength(secondRenderItems.length)
    expect(firstRenderCheckboxes).toHaveLength(secondRenderCheckboxes.length)
  })

  it('should preserve checklist selection when items are reordered', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    const onConfirm = vi.fn()
    const { rerender } = render(
      <RiskAcknowledgementDialog
        open
        onOpenChange={onOpenChange}
        title='Test'
        checklist={['Keep selected', 'Leave clear']}
        onConfirm={onConfirm}
      />
    )

    await user.click(screen.getByRole('checkbox', { name: 'Keep selected' }))
    expect(
      screen.getByRole('checkbox', { name: 'Keep selected' })
    ).toBeChecked()

    rerender(
      <RiskAcknowledgementDialog
        open
        onOpenChange={onOpenChange}
        title='Test'
        checklist={['Leave clear', 'Keep selected']}
        onConfirm={onConfirm}
      />
    )

    expect(
      screen.getByRole('checkbox', { name: 'Keep selected' })
    ).toBeChecked()
    expect(
      screen.getByRole('checkbox', { name: 'Leave clear' })
    ).not.toBeChecked()
  })
})
