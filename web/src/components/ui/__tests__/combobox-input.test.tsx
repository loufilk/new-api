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
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useRef, useState } from 'react'
import { describe, expect, it, vi } from 'vitest'

import { Combobox } from '../combobox'
import { PortalContainerContext } from '../portal-container'

const options = [
  { value: 'default', label: 'Default' },
  { value: 'disabled', label: 'Unavailable', disabled: true },
  { value: 'premium', label: 'Premium' },
]

function Fixture(props: { disabled?: boolean }) {
  const [value, setValue] = useState('default')
  return (
    <Combobox
      options={options}
      value={value}
      onValueChange={(next) => setValue(next ?? '')}
      allowCustomValue
      disabled={props.disabled}
      aria-label='Group'
    />
  )
}

describe('custom-value combobox selection', () => {
  it('disables the text input and popup when the control is disabled', () => {
    render(<Fixture disabled />)
    const input = screen.getByRole('combobox', { name: 'Group' })
    expect(input).toBeDisabled()
    fireEvent.click(input)
    expect(input).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  })

  it('skips disabled options during keyboard selection', async () => {
    render(<Fixture />)
    const user = userEvent.setup()
    const input = screen.getByRole('combobox', { name: 'Group' })
    await user.click(input)
    expect(screen.getByRole('option', { name: 'Unavailable' })).toHaveAttribute(
      'aria-disabled',
      'true'
    )
    await user.keyboard('{ArrowDown}{ArrowDown}{Enter}')
    expect(input).toHaveValue('Premium')
    expect(input).toHaveAttribute('aria-expanded', 'false')
  })

  it('does not select a touch target until the tap is completed', async () => {
    render(<Fixture />)
    const user = userEvent.setup()
    const input = screen.getByRole('combobox', { name: 'Group' })
    await user.click(input)
    const option = screen.getByRole('option', { name: 'Premium' })
    fireEvent.pointerDown(option, { pointerType: 'touch', pointerId: 1 })
    fireEvent.pointerMove(option, {
      pointerType: 'touch',
      pointerId: 1,
      clientY: 90,
    })
    fireEvent.pointerCancel(option, { pointerType: 'touch', pointerId: 1 })
    expect(input).toHaveValue('default')
    expect(input).toHaveAttribute('aria-expanded', 'true')
  })

  it('closes suggestions on Escape and restores all choices when reopened', async () => {
    render(<Fixture />)
    const user = userEvent.setup()
    const input = screen.getByRole('combobox', { name: 'Group' })
    await user.click(input)
    await user.clear(input)
    await user.type(input, 'prem')
    expect(
      screen.queryByRole('option', { name: 'Default' })
    ).not.toBeInTheDocument()
    await user.keyboard('{Escape}')
    expect(input).toHaveAttribute('aria-expanded', 'false')
    expect(input).toHaveValue('prem')
    await user.click(input)
    expect(screen.getByRole('option', { name: 'Default' })).toBeVisible()
  })
})

it.each([
  { inputTop: 500, popupTop: 136 },
  { inputTop: 770, popupTop: 266 },
])(
  'positions the list inside a transformed drawer at input top $inputTop without viewport overflow',
  async ({ inputTop, popupTop }) => {
    const bounds = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect')
    bounds.mockImplementation(function (this: HTMLElement) {
      if (this.getAttribute('role') === 'dialog')
        return new DOMRect(0, 400, 390, 444)
      if (this.getAttribute('role') === 'combobox')
        return new DOMRect(16, inputTop, 358, 32)
      return new DOMRect(0, 0, 358, 100)
    })
    vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockImplementation(
      function (this: HTMLElement) {
        return this.getAttribute('role') === 'dialog' ? 390 : 358
      }
    )
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(
      function (this: HTMLElement) {
        return this.getAttribute('role') === 'dialog' ? 444 : 100
      }
    )
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(390)
    vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(844)

    function DrawerFixture() {
      const container = useRef<HTMLDivElement>(null)
      return (
        <div
          ref={container}
          role='dialog'
          aria-label='Filters'
          style={{ transform: 'translateY(0)', position: 'fixed' }}
        >
          <PortalContainerContext.Provider value={container}>
            <Fixture />
          </PortalContainerContext.Provider>
        </div>
      )
    }
    render(<DrawerFixture />)
    await userEvent.click(screen.getByRole('combobox', { name: 'Group' }))
    const dialog = screen.getByRole('dialog', { name: 'Filters' })
    const list = within(dialog).getByRole('listbox')
    const popup = list.closest('[data-slot=combobox-input-popup]')
    expect(popup).not.toBeNull()
    expect(popup).toHaveClass(
      'max-h-(--available-height)',
      'w-(--anchor-width)',
      'max-w-(--available-width)'
    )
    expect(list).toHaveClass('overflow-y-auto', 'overscroll-contain')
    await waitFor(() =>
      expect(popup?.parentElement).toHaveStyle({
        transform: `translate(16px, ${popupTop}px)`,
      })
    )
  }
)
