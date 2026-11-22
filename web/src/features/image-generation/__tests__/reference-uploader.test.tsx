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
import userEvent from '@testing-library/user-event'
import { describe, expect, test, vi } from 'vitest'

import { ReferenceUploader } from '../components/ReferenceUploader'

type UploaderProps = Parameters<typeof ReferenceUploader>[0]

function renderUploader(overrides: Partial<UploaderProps> = {}) {
  const props: UploaderProps = {
    items: [],
    maxAllowed: 4,
    disabled: false,
    errorKey: null,
    onAddFiles: vi.fn(),
    onRemove: vi.fn(),
    ...overrides,
  }
  render(<ReferenceUploader {...props} />)
  return props
}

function zone() {
  return screen.getByRole('button', { name: 'Add reference images' })
}

function spyOnPickerClick() {
  return vi
    .spyOn(HTMLInputElement.prototype, 'click')
    .mockImplementation(() => undefined)
}

describe('reference uploader opens the picker from a single operable zone', () => {
  test('mouse click on the zone opens the file picker', async () => {
    const user = userEvent.setup()
    const pickerClick = spyOnPickerClick()
    renderUploader()

    await user.click(zone())

    expect(pickerClick).toHaveBeenCalledTimes(1)
  })

  test('Enter and Space keys open the file picker', () => {
    const pickerClick = spyOnPickerClick()
    renderUploader()
    const target = zone()

    fireEvent.keyDown(target, { key: 'Enter' })
    fireEvent.keyDown(target, { key: ' ' })
    fireEvent.keyDown(target, { key: 'a' })

    expect(pickerClick).toHaveBeenCalledTimes(2)
  })

  test('disabled zone never opens the picker by mouse or keyboard', async () => {
    const user = userEvent.setup()
    const pickerClick = spyOnPickerClick()
    renderUploader({ disabled: true })
    const target = zone()

    expect(target).toHaveAttribute('aria-disabled', 'true')
    await user.click(target)
    fireEvent.keyDown(target, { key: 'Enter' })
    fireEvent.keyDown(target, { key: ' ' })

    expect(pickerClick).not.toHaveBeenCalled()
  })

  test('dropping files forwards them without opening the picker', () => {
    const pickerClick = spyOnPickerClick()
    const props = renderUploader()
    const dropped = new File(['img'], 'dropped.png', { type: 'image/png' })

    fireEvent.drop(zone(), { dataTransfer: { files: [dropped] } })

    expect(props.onAddFiles).toHaveBeenCalledTimes(1)
    expect(props.onAddFiles).toHaveBeenCalledWith([dropped])
    expect(pickerClick).not.toHaveBeenCalled()
  })

  test('pasting files forwards them to the pending batch', () => {
    const props = renderUploader()
    const pasted = new File(['img'], 'pasted.png', { type: 'image/png' })

    fireEvent.paste(zone(), { clipboardData: { files: [pasted] } })

    expect(props.onAddFiles).toHaveBeenCalledWith([pasted])
  })

  test('models without reference support render no upload zone', () => {
    renderUploader({ maxAllowed: 0 })

    expect(
      screen.queryByRole('button', { name: 'Add reference images' })
    ).not.toBeInTheDocument()
  })
})
