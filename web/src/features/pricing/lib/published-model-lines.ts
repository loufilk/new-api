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
import type {
  MarketGroupCatalog,
  PricingData,
  PublishedModelLinesResponse,
} from '../types'

// Merge only the line IDs which have explicit, non-empty capabilities for this
// public model. Base price tiers are preserved even if the overlay fails.
export function mergePublishedModelLines(
  data: PricingData,
  overlay?: PublishedModelLinesResponse
): PricingData {
  if (!overlay || !Array.isArray(overlay.public_lines) || !Array.isArray(overlay.models))
    return data

  const lines = new Map(
    overlay.public_lines
      .filter(
        (line) =>
          /^line-[0-9a-f]{32}$/.test(line.id) && line.label.trim() &&
          Number.isFinite(line.ratio) && line.ratio >= 0
      )
      .map((line) => [line.id, line] as const)
  )
  const membership = new Map(overlay.models.map((model) => [model.model_name, model]))
  const groupCatalog: MarketGroupCatalog = {}
  const groupRatio = { ...data.group_ratio }
  const usableGroup = { ...data.usable_group }
  const rows = data.data.map((model) => {
    const support = membership.get(model.model_name)
    const capabilities: Record<string, string[]> = {}
    for (const id of support?.public_line_ids ?? []) {
      const line = lines.get(id)
      const kinds = support?.supported_endpoint_types_by_public_line?.[id]
      if (!line || !kinds?.length) continue
      capabilities[id] = kinds
      groupRatio[id] = line.ratio
      usableGroup[id] = { desc: line.label, ratio: line.ratio }
      groupCatalog[id] = {
        label: line.label,
        badges: line.badges,
        sort_order: line.sort_order,
        public_line: line,
      }
    }
    const publicLineIDs = Object.keys(capabilities)
    if (!publicLineIDs.length) return model
    return {
      ...model,
      enable_groups: [...publicLineIDs, ...model.enable_groups],
      supported_endpoint_types_by_group: {
        ...model.supported_endpoint_types_by_group,
        ...capabilities,
      },
    }
  })
  return {
    ...data,
    data: rows,
    group_catalog: groupCatalog,
    group_ratio: groupRatio,
    usable_group: usableGroup,
  }
}
