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

import type { ApiKey } from '../../types'
import {
  buildValidatedCCSwitchURL,
  ccSwitchModelsQueryKey,
} from './cc-switch-utils'

const baseToken: ApiKey = {
  id: 1,
  name: 'one',
  key: 'masked',
  status: 1,
  remain_quota: 0,
  used_quota: 0,
  unlimited_quota: true,
  expired_time: -1,
  created_time: 1,
  accessed_time: 1,
  group: 'a',
  auto_groups: ['a'],
  cross_group_retry: false,
  model_limits_enabled: false,
  model_limits: '',
  allow_ips: '',
  allow_user_quota_query: true,
}

describe('CC Switch token-scoped models', () => {
  test('query cache identity changes across tokens, routing scope and app', () => {
    const second = { ...baseToken, id: 2, name: 'two' }
    expect(ccSwitchModelsQueryKey(baseToken, 'claude')).not.toEqual(
      ccSwitchModelsQueryKey(second, 'claude')
    )
    expect(ccSwitchModelsQueryKey(baseToken, 'claude')).not.toEqual(
      ccSwitchModelsQueryKey({ ...baseToken, group: 'b' }, 'claude')
    )
    expect(ccSwitchModelsQueryKey(baseToken, 'claude')).not.toEqual(
      ccSwitchModelsQueryKey(
        { ...baseToken, auto_groups: ['a', 'b'] },
        'claude'
      )
    )
    expect(ccSwitchModelsQueryKey(baseToken, 'claude')).not.toEqual(
      ccSwitchModelsQueryKey(baseToken, 'codex')
    )
    expect(ccSwitchModelsQueryKey(baseToken, 'claude')).not.toEqual(
      ccSwitchModelsQueryKey(
        {
          ...baseToken,
          model_limits_enabled: true,
          model_limits: 'gpt-4o',
        },
        'claude'
      )
    )
  })

  test('does not create a deep link when no model is routable', () => {
    expect(
      buildValidatedCCSwitchURL(
        'codex',
        'My Codex',
        { model: 'gpt-4o' },
        'sk-test',
        []
      )
    ).toBeNull()
  })

  test('does not create a deep link for a stale selection', () => {
    expect(
      buildValidatedCCSwitchURL(
        'codex',
        'My Codex',
        { model: 'gpt-4o' },
        'sk-test',
        ['gpt-4.1']
      )
    ).toBeNull()
  })
})
