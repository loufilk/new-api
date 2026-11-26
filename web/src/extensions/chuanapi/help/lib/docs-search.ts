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
import { getDocSections } from './doc-headings'

export type DocsSearchResult =
  | {
      type: 'guide'
      id: string
      title: string
      description: string
      sectionId?: string
    }
  | { type: 'endpoint'; id: string; title: string; description: string }

interface SearchEntry {
  result: DocsSearchResult
  headline: string
  context: string
  body: string
}

function normalize(text: string): string {
  return text.normalize('NFKC').toLowerCase().trim()
}

// The corpus is bundled, but only load the section index when search is used.
let searchIndex: SearchEntry[] | undefined

function buildSearchIndex(): SearchEntry[] {
  return [
    ...orderedGuides.flatMap((guide): SearchEntry[] => {
      const guideHeadline = normalize(`${guide.title} ${guide.slug}`)
      return [
        {
          result: {
            type: 'guide',
            id: guide.slug,
            title: guide.title,
            description: guide.description,
          },
          headline: guideHeadline,
          context: '',
          body: normalize(`${guide.description} ${guide.body}`),
        },
        ...getDocSections(guide.body).map((section): SearchEntry => ({
          result: {
            type: 'guide',
            id: guide.slug,
            sectionId: section.id,
            title: section.label,
            description: guide.title,
          },
          headline: normalize(section.label),
          context: normalize(`${guideHeadline} ${guide.description}`),
          body: normalize(section.body),
        })),
      ]
    }),
    ...allEndpoints.map((endpoint): SearchEntry => ({
      result: {
        type: 'endpoint',
        id: endpoint.id,
        title: `${endpoint.method} ${endpoint.path}`,
        description: endpoint.name,
      },
      headline: normalize(
        `${endpoint.method} ${endpoint.path} ${endpoint.name}`
      ),
      context: '',
      body: normalize(
        `${endpoint.description} ${endpoint.relayFormat} ${endpoint.notes.join(' ')}`
      ),
    })),
  ]
}

export function searchDocs(query: string): readonly DocsSearchResult[] {
  const normalized = normalize(query)
  if (!normalized) return []
  const terms = normalized.split(/\s+/u)
  searchIndex ??= buildSearchIndex()
  const matches = searchIndex
    .flatMap((entry) => {
      const headings = terms.filter((term) => entry.headline.includes(term))
      const direct = terms.filter(
        (term) => entry.headline.includes(term) || entry.body.includes(term)
      )
      const result = entry.result
      const section = result.type === 'guide' && Boolean(result.sectionId)
      if (
        !direct.length ||
        !terms.every(
          (term) =>
            entry.headline.includes(term) ||
            entry.body.includes(term) ||
            entry.context.includes(term)
        )
      ) {
        return []
      }
      // Prefer named pages and API paths, then the most relevant subsection.
      // A parent's name can disambiguate a section but cannot match by itself.
      let score = direct.length + headings.length * 10
      if (!section && headings.length === terms.length) score += 400
      else if (section) {
        score +=
          200 + terms.filter((term) => entry.context.includes(term)).length * 4
      }
      if (normalize(result.title) === normalized) score += 80
      return [{ score, result, section }]
    })
    .sort((a, b) => b.score - a.score)

  const results: DocsSearchResult[] = []
  const coveredGuides = new Set<string>()
  for (const match of matches) {
    if (match.result.type === 'guide') {
      if (!match.section && coveredGuides.has(match.result.id)) continue
      if (match.section) coveredGuides.add(match.result.id)
    }
    results.push(match.result)
    if (results.length === 12) break
  }
  return results
}
