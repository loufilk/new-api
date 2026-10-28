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
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'

import { CHANNEL_TYPE_CLINE } from '../../constants'
import { ChannelProviderPicker } from '../drawers/channel-provider-picker'

function renderPicker(onSelect: (target: unknown) => void) {
  return render(
    <ChannelProviderPicker
      isCreating
      plugins={[]}
      canBindPlugin={false}
      loading={false}
      failed={false}
      disabled={false}
      onRetry={vi.fn()}
      onSelect={onSelect}
    />
  )
}

test('creation lists the Cline provider with its description and selects type 64', async () => {
  const select = vi.fn()
  const user = userEvent.setup()
  renderPicker(select)

  const option = screen.getByRole('option', { name: 'Cline Built-in #64' })
  expect(option).toBeVisible()
  expect(option).toHaveAccessibleDescription(
    'Connect to the Cline API with an API key or access token'
  )
  expect(screen.getByRole('option', { name: /^Cline / })).toBeInTheDocument()
  expect(CHANNEL_TYPE_CLINE).toBe(64)

  await user.click(option)
  expect(select).toHaveBeenCalledWith({ kind: 'builtin', type: 64 })
})

test('searching Cline or its type number keeps the provider selectable', async () => {
  const select = vi.fn()
  const user = userEvent.setup()
  renderPicker(select)

  const search = screen.getByRole('combobox')
  await user.type(search, 'cline')
  const byName = screen.getByRole('option', { name: 'Cline Built-in #64' })
  await user.click(byName)
  expect(select).toHaveBeenLastCalledWith({ kind: 'builtin', type: 64 })

  await user.clear(search)
  await user.type(search, '64')
  await user.keyboard('{ArrowDown}{Enter}')
  expect(select).toHaveBeenLastCalledWith({ kind: 'builtin', type: 64 })
})
