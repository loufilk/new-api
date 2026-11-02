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
import { afterEach, describe, expect, it, vi } from 'vitest'

import { useKeyGroupPresetStore } from '../group-presets'

afterEach(() => {
  localStorage.clear()
  vi.restoreAllMocks()
  useKeyGroupPresetStore.setState({ ownerId: null, presets: [] })
})

describe('account-owned routing group presets', () => {
  it('ignores malformed, secret-shaped and oversized stored values', () => {
    localStorage.setItem('newapi:key-group-presets:v1:7', '{broken')
    useKeyGroupPresetStore.getState().load(7)
    expect(useKeyGroupPresetStore.getState().presets).toEqual([])
    localStorage.setItem(
      'newapi:key-group-presets:v1:7',
      JSON.stringify([{ key: 'synthetic-not-a-real-key' }])
    )
    useKeyGroupPresetStore.getState().load(7)
    expect(useKeyGroupPresetStore.getState().presets).toEqual([])
  })
  it('revalidates user identity, group duplication and storage writes before reporting success', () => {
    const save = useKeyGroupPresetStore.getState().save
    expect(save(0, { name: 'work', groups: ['default'] })).toBe(false)
    expect(save(7, { name: 'work', groups: ['default', 'default'] })).toBe(
      false
    )
    vi.spyOn(localStorage, 'setItem').mockImplementation(() => {
      throw new Error('quota exceeded')
    })
    expect(save(7, { name: 'work', groups: ['default'] })).toBe(false)
    expect(useKeyGroupPresetStore.getState().presets).toEqual([])
  })
  it('does not reuse another account preset list on write', () => {
    const store = useKeyGroupPresetStore.getState()
    expect(store.save(7, { name: 'private', groups: ['vip'] })).toBe(true)
    expect(store.save(8, { name: 'own', groups: ['default'] })).toBe(true)
    expect(useKeyGroupPresetStore.getState().presets).toEqual([
      { name: 'own', groups: ['default'] },
    ])
    store.load(7)
    expect(useKeyGroupPresetStore.getState().presets).toEqual([
      { name: 'private', groups: ['vip'] },
    ])
  })
})
