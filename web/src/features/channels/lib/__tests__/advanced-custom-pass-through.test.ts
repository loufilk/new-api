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

import { channelSchema, type AdvancedCustomConfig } from '../../types'
import {
  normalizeAdvancedCustomConfig,
  parseAdvancedCustomConfig,
  stringifyAdvancedCustomConfig,
  validateAdvancedCustomConfig,
} from '../advanced-custom'
import {
  buildSettingJSON,
  CHANNEL_FORM_DEFAULT_VALUES,
  transformChannelToFormDefaults,
  transformFormDataToUpdatePayload,
} from '../channel-form'

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

const nativeEmbeddingsRoute = {
  incoming_path: '/v1/embeddings',
  upstream_path: '/v1/embeddings',
  converter: 'none' as const,
}

const claudeConverterRoute = {
  incoming_path: '/v1/messages',
  upstream_path: '/v1/chat/completions',
  converter: 'anthropic_messages_to_openai_chat_completions' as const,
}

const modelListRoute = {
  incoming_path: '/v1/models',
  upstream_path: '/v1/models',
  converter: 'none' as const,
}

function createChannelFixture(overrides: {
  type?: number
  setting?: string
  settings?: string
}) {
  return channelSchema.parse({
    id: 42,
    name: 'Advanced custom channel',
    type: 58,
    key: '',
    status: 1,
    created_time: 1,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    ...overrides,
  })
}

function readSavedSettings(payload: { settings?: string | null }) {
  const saved = JSON.parse(String(payload.settings))
  return saved.advanced_custom as AdvancedCustomConfig
}

describe('advanced custom per-route pass-through', () => {
  test('routes keep pass-through off unless it is explicitly enabled', () => {
    const normalized = normalizeAdvancedCustomConfig({
      advanced_routes: [nativeChatRoute, nativeEmbeddingsRoute],
    })

    expect(normalized.advanced_routes?.[0]).not.toHaveProperty(
      'pass_through_body_enabled'
    )
    expect(normalized.advanced_routes?.[1]).not.toHaveProperty(
      'pass_through_body_enabled'
    )
    expect(
      JSON.parse(buildSettingJSON(CHANNEL_FORM_DEFAULT_VALUES))
        .pass_through_body_enabled
    ).toBe(false)
    expect(
      JSON.parse(
        buildSettingJSON({
          ...CHANNEL_FORM_DEFAULT_VALUES,
          type: 58,
          advanced_custom: stringifyAdvancedCustomConfig({
            advanced_routes: [nativeChatRoute],
          }),
        })
      ).pass_through_body_enabled
    ).toBe(false)
  })

  test('per-route pass-through round-trips independently through normalization and channel save', () => {
    const config: AdvancedCustomConfig = {
      advanced_routes: [
        { ...nativeChatRoute, pass_through_body_enabled: true },
        nativeChatFallbackRoute,
      ],
    }

    const roundTripped = parseAdvancedCustomConfig(
      stringifyAdvancedCustomConfig(config)
    )
    expect(roundTripped?.advanced_routes?.[0].pass_through_body_enabled).toBe(
      true
    )
    expect(roundTripped?.advanced_routes?.[1]).not.toHaveProperty(
      'pass_through_body_enabled'
    )
    expect(validateAdvancedCustomConfig(roundTripped)).toBeNull()

    const channel = createChannelFixture({
      settings: JSON.stringify({ advanced_custom: config }),
    })
    const values = transformChannelToFormDefaults(channel)
    const editable = parseAdvancedCustomConfig(values.advanced_custom)
    expect(editable?.advanced_routes?.[0].pass_through_body_enabled).toBe(true)
    expect(editable?.advanced_routes?.[1]).not.toHaveProperty(
      'pass_through_body_enabled'
    )

    const edited = parseAdvancedCustomConfig(values.advanced_custom)
    if (!edited?.advanced_routes) throw new Error('expected editable routes')
    edited.advanced_routes[0].pass_through_body_enabled = false
    edited.advanced_routes[1].pass_through_body_enabled = true
    values.advanced_custom = stringifyAdvancedCustomConfig(edited)
    const payload = transformFormDataToUpdatePayload(values, 42)
    const saved = readSavedSettings(payload)

    expect(saved.advanced_routes?.[0]).not.toHaveProperty(
      'pass_through_body_enabled'
    )
    expect(saved.advanced_routes?.[1].pass_through_body_enabled).toBe(true)
    expect(JSON.parse(String(payload.setting)).pass_through_body_enabled).toBe(
      false
    )
  })

  test.each([undefined, false, true])(
    'keeps route flag %s independent across a global enable, save, reopen and disable',
    (routeFlag) => {
      const channel = createChannelFixture({
        setting: JSON.stringify({
          pass_through_body_enabled: true,
          model_owned_by: 'fixture-owner',
        }),
        settings: JSON.stringify({
          advanced_custom: {
            advanced_routes: [
              { ...nativeChatRoute, pass_through_body_enabled: routeFlag },
              { ...nativeEmbeddingsRoute, pass_through_body_enabled: true },
              claudeConverterRoute,
              modelListRoute,
            ],
          },
        }),
      })

      const values = transformChannelToFormDefaults(channel)
      const editable = parseAdvancedCustomConfig(values.advanced_custom)
      expect(editable?.advanced_routes?.[0].pass_through_body_enabled).toBe(
        routeFlag === true ? true : undefined
      )
      expect(editable?.advanced_routes?.[1].pass_through_body_enabled).toBe(
        true
      )
      expect(editable?.advanced_routes?.[2]).not.toHaveProperty(
        'pass_through_body_enabled'
      )
      expect(editable?.advanced_routes?.[3]).not.toHaveProperty(
        'pass_through_body_enabled'
      )
      expect(validateAdvancedCustomConfig(editable)).toBeNull()

      values.name = 'Renamed advanced custom channel'
      const enabledPayload = transformFormDataToUpdatePayload(values, 42)
      const enabledRoutes = readSavedSettings(enabledPayload)
      expect(enabledRoutes).toEqual(editable)
      expect(JSON.parse(String(enabledPayload.setting))).toMatchObject({
        pass_through_body_enabled: true,
        model_owned_by: 'fixture-owner',
      })

      const reopened = transformChannelToFormDefaults({
        ...channel,
        ...enabledPayload,
      })
      expect(reopened.pass_through_body_enabled).toBe(true)
      reopened.pass_through_body_enabled = false
      const disabledPayload = transformFormDataToUpdatePayload(reopened, 42)
      expect(JSON.parse(String(disabledPayload.setting))).toMatchObject({
        pass_through_body_enabled: false,
        model_owned_by: 'fixture-owner',
      })
      // Historical explicit true may come from a user or an older migration.
      // Global-off must not guess its origin or erase that route decision.
      expect(readSavedSettings(disabledPayload)).toEqual(enabledRoutes)
      expect(
        transformChannelToFormDefaults({ ...channel, ...disabledPayload })
          .pass_through_body_enabled
      ).toBe(false)
    }
  )

  test('normal channels keep the channel-level pass-through setting', () => {
    const channel = createChannelFixture({
      type: 1,
      setting: JSON.stringify({ pass_through_body_enabled: true }),
    })

    const values = transformChannelToFormDefaults(channel)
    expect(values.pass_through_body_enabled).toBe(true)
    expect(JSON.parse(buildSettingJSON(values)).pass_through_body_enabled).toBe(
      true
    )
  })

  test('invalid pass-through combinations stay rejected', () => {
    const converterError = validateAdvancedCustomConfig({
      advanced_routes: [
        { ...claudeConverterRoute, pass_through_body_enabled: true },
      ],
    })
    expect(converterError).toEqual({
      routeIndex: 0,
      message: 'Pass-through requires native forwarding',
    })

    const managementError = validateAdvancedCustomConfig({
      advanced_routes: [{ ...modelListRoute, pass_through_body_enabled: true }],
    })
    expect(managementError).toEqual({
      routeIndex: 0,
      message: 'OpenAI Models route does not support pass-through',
    })
  })
})
