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
import { useState } from 'react'
import { expect, test } from 'vitest'

import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'

import type { ChannelConfigurationSection } from '../../lib/channel-configuration'
import { ChannelConfiguration } from '../drawers/channel-configuration'

function ConfigurationLayoutHarness() {
  const [section, setSection] =
    useState<ChannelConfigurationSection>('connection')
  return (
    <ChannelConfiguration
      section={section}
      onSectionChange={setSection}
      statuses={{
        connection: 'ready',
        routing: 'idle',
        request: 'idle',
        other: 'idle',
      }}
      connection={
        <div className='flex items-center justify-between gap-3'>
          <Label htmlFor='enabled'>Enabled</Label>
          <Switch id='enabled' defaultChecked />
        </div>
      }
      models={
        <Input
          aria-label='Model name'
          defaultValue='a-long-custom-model-name'
        />
      }
      quickOptions={null}
      routing={<Input aria-label='Model mapping' />}
      request={<Input aria-label='Request override' />}
      other={<Input aria-label='Channel notes' />}
    />
  )
}

test('right-aligned switches reserve their expanded pointer target without clipping the scrollable form', () => {
  render(<ConfigurationLayoutHarness />)
  const toggle = screen.getByRole('switch', { name: 'Enabled' })
  expect(toggle.closest('[data-slot=tabs]')).toHaveClass(
    '[&_[role=switch]]:mx-3'
  )
  expect(toggle).toHaveClass('after:-inset-x-3')
  const panel = screen.getByRole('tabpanel')
  expect(panel).toHaveClass('overflow-y-auto', 'p-1')
  expect(panel).not.toHaveClass('overflow-x-hidden', 'overflow-hidden')
  const tablist = screen.getByRole('tablist', { name: 'Channel configuration' })
  expect(tablist.parentElement).toHaveClass('overflow-x-auto', 'shrink-0')
})

test('keyboard users can toggle a switch and navigate overflowing tabs without losing the form draft', async () => {
  const user = userEvent.setup()
  render(<ConfigurationLayoutHarness />)
  const toggle = screen.getByRole('switch', { name: 'Enabled' })
  toggle.focus()
  await user.keyboard(' ')
  expect(toggle).toHaveFocus()
  expect(toggle).not.toBeChecked()

  const model = screen.getByRole('textbox', { name: 'Model name' })
  await user.clear(model)
  await user.type(model, 'my-channel-model')
  screen.getByRole('tab', { name: /^Connection & Models/ }).focus()
  await user.keyboard('{ArrowRight}')
  expect(screen.getByRole('tab', { name: 'Routing & Mapping' })).toHaveFocus()
  await user.keyboard('{Enter}')
  expect(
    screen.getByRole('tab', { name: 'Routing & Mapping' })
  ).toHaveAttribute('aria-selected', 'true')
  expect(screen.getByRole('textbox', { name: 'Model mapping' })).toBeVisible()
  await user.keyboard('{ArrowLeft}{Enter}')
  expect(screen.getByRole('textbox', { name: 'Model name' })).toHaveValue(
    'my-channel-model'
  )
  expect(screen.getByRole('switch', { name: 'Enabled' })).not.toBeChecked()
})
