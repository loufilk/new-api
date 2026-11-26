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
import { allEndpoints } from '../content/endpoints'
import { orderedGuides } from '../content/nav'

export type DocsSearchResult =
  | { type: 'guide'; id: string; title: string; description: string }
  | { type: 'endpoint'; id: string; title: string; description: string }

function normalize(text: string): string {
  return text.normalize('NFKC').toLowerCase().trim()
}

const searchIndex = [
  ...orderedGuides.map((guide) => ({
    result: {
      type: 'guide' as const,
      id: guide.slug,
      title: guide.title,
      description: guide.description,
    },
    headline: normalize(`${guide.title} ${guide.slug}`),
    body: normalize(`${guide.description} ${guide.body}`),
  })),
  ...allEndpoints.map((endpoint) => ({
    result: {
      type: 'endpoint' as const,
      id: endpoint.id,
      title: `${endpoint.method} ${endpoint.path}`,
      description: endpoint.name,
    },
    headline: normalize(`${endpoint.method} ${endpoint.path} ${endpoint.name}`),
    body: normalize(
      `${endpoint.description} ${endpoint.relayFormat} ${endpoint.notes.join(' ')}`
    ),
  })),
]

export function searchDocs(query: string): readonly DocsSearchResult[] {
  const normalized = normalize(query)
  if (!normalized) return []
  const terms = normalized.split(/\s+/u)
  return searchIndex
    .flatMap((entry) => {
      if (
        !terms.every(
          (term) => entry.headline.includes(term) || entry.body.includes(term)
        )
      ) {
        return []
      }
      return [
        {
          score: terms.filter((term) => entry.headline.includes(term)).length,
          result: entry.result,
        },
      ]
    })
    .sort((a, b) => b.score - a.score)
    .slice(0, 12)
    .map((entry) => entry.result)
}
