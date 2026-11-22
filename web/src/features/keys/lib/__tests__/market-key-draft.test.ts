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
import { afterEach, expect, test, vi } from 'vitest'

import {
  consumePrivateMarketKeyDraft,
  stagePrivateMarketKeyDraft,
} from '../market-key-draft'

const session = vi.hoisted(() => ({ epoch: 0 }))
vi.mock('@/lib/auth-session', () => ({
  getAuthSessionEpoch: () => session.epoch,
}))

afterEach(() => {
  consumePrivateMarketKeyDraft()
  vi.useRealTimers()
})

test('passes a private draft through in memory a single time', () => {
  stagePrivateMarketKeyDraft('private-group', 'private-model')
  expect(consumePrivateMarketKeyDraft()).toEqual({
    group: 'private-group',
    model: 'private-model',
  })
  expect(consumePrivateMarketKeyDraft()).toBeUndefined()
})

test('does not restore a private draft across account changes or expiry', () => {
  stagePrivateMarketKeyDraft('private-group')
  session.epoch += 1
  expect(consumePrivateMarketKeyDraft()).toBeUndefined()

  vi.useFakeTimers()
  stagePrivateMarketKeyDraft('private-group')
  vi.advanceTimersByTime(60_001)
  expect(consumePrivateMarketKeyDraft()).toBeUndefined()
})
