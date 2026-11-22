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
import { z } from 'zod'
import { create } from 'zustand'

const presetSchema = z.object({
  name: z.string().min(1).max(50),
  groups: z.array(z.string().min(1).max(128)).min(1).max(100),
})
const presetsSchema = z
  .array(presetSchema)
  .max(20)
  .refine(
    (presets) =>
      new Set(presets.map((preset) => preset.name)).size === presets.length &&
      presets.every(
        (preset) => new Set(preset.groups).size === preset.groups.length
      )
  )
export type KeyGroupPreset = z.infer<typeof presetSchema>

type PresetState = {
  ownerId: number | null
  presets: KeyGroupPreset[]
  load: (userId: number | null) => void
  save: (userId: number, preset: KeyGroupPreset) => boolean
}

export const useKeyGroupPresetStore = create<PresetState>()((set, get) => ({
  ownerId: null,
  presets: [],
  load: (userId) => {
    if (userId === null || !Number.isSafeInteger(userId) || userId <= 0) {
      set({ ownerId: null, presets: [] })
      return
    }
    let presets: KeyGroupPreset[] = []
    try {
      const raw = localStorage.getItem(`newapi:key-group-presets:v1:${userId}`)
      if (raw && raw.length <= 100_000) {
        const parsed = presetsSchema.safeParse(JSON.parse(raw))
        if (parsed.success) presets = parsed.data
      }
    } catch {
      // Unavailable or malformed storage never changes the routing selection.
    }
    set({ ownerId: userId, presets })
  },
  save: (userId, preset) => {
    if (!Number.isSafeInteger(userId) || userId <= 0) return false
    const parsed = presetSchema.safeParse(preset)
    if (
      !parsed.success ||
      new Set(preset.groups).size !== preset.groups.length
    ) {
      return false
    }
    if (get().ownerId !== userId) get().load(userId)
    const others = get().presets.filter((item) => item.name !== preset.name)
    if (others.length >= 20) return false
    const presets = [...others, parsed.data]
    try {
      localStorage.setItem(
        `newapi:key-group-presets:v1:${userId}`,
        JSON.stringify(presets)
      )
    } catch {
      return false
    }
    set({ ownerId: userId, presets })
    return true
  },
}))
