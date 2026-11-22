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
import { describe, expect, test } from 'vitest'

import {
  CHANNEL_TYPE_CLINE,
  CHANNEL_TYPE_OPTIONS,
  CHANNEL_TYPE_SUB2API,
  CHANNEL_TYPE_TASK_PLUGIN,
  CLAUDE_FIELD_PASSTHROUGH_TYPES,
  FIELD_PASSTHROUGH_TYPES,
  MODEL_FETCHABLE_TYPES,
  OPENAI_FIELD_PASSTHROUGH_TYPES,
} from '../../constants'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  transformFormDataToCreatePayload,
} from '../channel-form'
import {
  getChannelTypeConfig,
  getChannelTypeHints,
} from '../channel-type-config'
import { getChannelTypeIcon, getKeyPromptForType } from '../channel-utils'
import { supportsResponsesWebSocket } from '../responses-websocket'

function clineForm(baseUrl: string) {
  return {
    ...CHANNEL_FORM_DEFAULT_VALUES,
    name: 'Cline primary',
    type: CHANNEL_TYPE_CLINE,
    base_url: baseUrl,
    key: 'cline-secret',
    models: 'cline-free/deepseek-v4.1-flash,cline-pass/kimi-k3',
  }
}

describe('Cline channel', () => {
  test('registers selection, ordering, icon, and model discovery metadata', () => {
    expect(CHANNEL_TYPE_CLINE).toBe(64)
    expect(CHANNEL_TYPE_OPTIONS).toContainEqual({
      value: CHANNEL_TYPE_CLINE,
      label: 'Cline',
    })

    const order = CHANNEL_TYPE_OPTIONS.map((option) => option.value)
    expect(order.indexOf(CHANNEL_TYPE_CLINE)).toBeGreaterThan(
      order.indexOf(CHANNEL_TYPE_SUB2API)
    )
    expect(order.indexOf(CHANNEL_TYPE_CLINE)).toBeLessThan(
      order.indexOf(CHANNEL_TYPE_TASK_PLUGIN)
    )

    expect(MODEL_FETCHABLE_TYPES.has(CHANNEL_TYPE_CLINE)).toBe(true)
    expect(getChannelTypeIcon(CHANNEL_TYPE_CLINE)).toBe('Cline')
    expect(getChannelTypeConfig(CHANNEL_TYPE_CLINE).icon).toBe('Cline')
    expect(getKeyPromptForType(CHANNEL_TYPE_CLINE)).toBe(
      'Cline API key or access token'
    )
  })

  test('defaults to the Cline API base URL and keeps credentials in the secret field', () => {
    const hints = getChannelTypeHints(CHANNEL_TYPE_CLINE)
    expect(hints.baseUrl).toBe('Cline API base URL, including /api/v1')
    expect(hints.key).toBe('Cline API key or access token')
    expect(hints.models).toBe(
      'Models fetched from the Cline recommended-models catalog'
    )

    const parsed = channelFormSchema.parse(clineForm(''))
    const payload = transformFormDataToCreatePayload(parsed)
    expect(payload.channel).toMatchObject({
      type: CHANNEL_TYPE_CLINE,
      base_url: null,
      key: 'cline-secret',
      models: 'cline-free/deepseek-v4.1-flash,cline-pass/kimi-k3',
    })
  })

  test('preserves explicit Cline model prefixes and submits a normalized base URL', () => {
    const parsed = channelFormSchema.parse(
      clineForm(' https://api.cline.bot/api/v1/ ')
    )
    const payload = transformFormDataToCreatePayload(parsed)
    expect(payload.channel.base_url).toBe('https://api.cline.bot/api/v1')
    expect(payload.channel.models).toBe(
      'cline-free/deepseek-v4.1-flash,cline-pass/kimi-k3'
    )
  })

  test('does not advertise field passthrough for the chat-only bridge', () => {
    expect(FIELD_PASSTHROUGH_TYPES.has(CHANNEL_TYPE_CLINE)).toBe(false)
    expect(OPENAI_FIELD_PASSTHROUGH_TYPES.has(CHANNEL_TYPE_CLINE)).toBe(false)
    expect(CLAUDE_FIELD_PASSTHROUGH_TYPES.has(CHANNEL_TYPE_CLINE)).toBe(false)
  })

  test('does not advertise native Responses WebSocket support', () => {
    expect(supportsResponsesWebSocket(CHANNEL_TYPE_CLINE)).toBe(false)
  })
})
