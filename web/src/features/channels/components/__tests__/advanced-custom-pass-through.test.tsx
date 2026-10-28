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
import { describe, expect, test, vi } from 'vitest'

import { stringifyAdvancedCustomConfig } from '../../lib/advanced-custom'
import type { AdvancedCustomConfig } from '../../types'
import { AdvancedCustomEditorDialog } from '../dialogs/advanced-custom-editor-dialog'

const nativeChatRoute = {
  incoming_path: '/v1/chat/completions',
  upstream_path: '/v1/chat/completions',
  converter: 'none' as const,
  models: ['gpt-4o'],
}

const nativeChatFallbackRoute = {
  incoming_path: '/v1/chat/completions',
  upstream_path: '/v1/chat/completions',
  converter: 'none' as const,
  models: ['gpt-4o-mini'],
}

function renderEditor(config: AdvancedCustomConfig) {
  const onSave = vi.fn()
  render(
    <AdvancedCustomEditorDialog
      open
      value={stringifyAdvancedCustomConfig(config)}
      onOpenChange={vi.fn()}
      onSave={onSave}
    />
  )
  return onSave
}

function getConverterTrigger() {
  const trigger = screen
    .getAllByRole('combobox')
    .find((element) => element.textContent?.includes('Native forwarding'))
  if (!trigger) throw new Error('Converter select trigger not found')
  return trigger
}

describe('advanced custom pass-through editor', () => {
  test('a native route switch is off by default and saves the enabled route', async () => {
    const user = userEvent.setup()
    const onSave = renderEditor({
      advanced_routes: [nativeChatRoute],
    })

    const toggle = screen.getByRole('switch', { name: 'Pass-through' })
    expect(toggle).not.toBeChecked()
    expect(toggle).not.toHaveAttribute('aria-disabled', 'true')
    await user.click(toggle)
    expect(toggle).toBeChecked()

    await user.click(screen.getByRole('button', { name: 'Save changes' }))
    const saved = JSON.parse(onSave.mock.calls[0][0]) as AdvancedCustomConfig
    expect(saved.advanced_routes?.[0].pass_through_body_enabled).toBe(true)
  })

  test('selecting a converter disables and clears pass-through', async () => {
    const user = userEvent.setup()
    const onSave = renderEditor({
      advanced_routes: [
        { ...nativeChatRoute, pass_through_body_enabled: true },
      ],
    })

    const toggle = screen.getByRole('switch', { name: 'Pass-through' })
    expect(toggle).toBeChecked()

    await user.click(getConverterTrigger())
    await user.click(
      screen.getByRole('option', { name: 'OpenAI Chat to Anthropic Messages' })
    )

    expect(toggle).not.toBeChecked()
    expect(toggle).toHaveAttribute('aria-disabled', 'true')

    await user.click(screen.getByRole('button', { name: 'Save changes' }))
    const saved = JSON.parse(onSave.mock.calls[0][0]) as AdvancedCustomConfig
    expect(saved.advanced_routes?.[0].converter).toBe(
      'openai_chat_completions_to_anthropic_messages'
    )
    expect(saved.advanced_routes?.[0]).not.toHaveProperty(
      'pass_through_body_enabled'
    )
  })

  test('native routes on the same path toggle independently', async () => {
    const user = userEvent.setup()
    const onSave = renderEditor({
      advanced_routes: [nativeChatRoute, nativeChatFallbackRoute],
    })

    const toggles = screen.getAllByRole('switch', { name: 'Pass-through' })
    expect(toggles).toHaveLength(2)
    expect(toggles[0]).not.toBeChecked()
    expect(toggles[1]).not.toBeChecked()

    await user.click(toggles[1])
    expect(toggles[0]).not.toBeChecked()
    expect(toggles[1]).toBeChecked()

    await user.click(screen.getByRole('button', { name: 'Save changes' }))
    const saved = JSON.parse(onSave.mock.calls[0][0]) as AdvancedCustomConfig
    expect(saved.advanced_routes?.[0]).not.toHaveProperty(
      'pass_through_body_enabled'
    )
    expect(saved.advanced_routes?.[1].pass_through_body_enabled).toBe(true)
  })
})
