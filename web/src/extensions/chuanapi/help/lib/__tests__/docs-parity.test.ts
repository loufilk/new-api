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
import { describe, expect, it } from 'vitest'

import { allEndpoints } from '../../content/endpoints'
import { guides } from '../../content/guides'
import {
  getAdjacentGuides,
  getGuide,
  getGuideGroup,
  orderedGuides,
} from '../../content/nav'
import { getDocHeadings } from '../doc-headings'
import { searchDocs } from '../docs-search'

const existingSlugs = [
  'quick-start',
  'authentication',
  'console-tour',
  'billing',
  'playground',
  'claude-code',
  'codex-cli',
  'cc-switch',
  'cherry-studio',
  'cline-roo',
  'immersive-translate',
  'openai-sdk',
  'anthropic-sdk',
  'compatibility',
  'streaming',
  'function-calling',
  'vision',
  'troubleshooting',
]

describe('public documentation corpus', () => {
  it('keeps the 18 preexisting guide routes and all 26 API reference rows', () => {
    expect(existingSlugs.every((slug) => getGuide(slug))).toBe(true)
    expect(allEndpoints).toHaveLength(26)
    expect(new Set(orderedGuides.map((guide) => guide.slug)).size).toBe(
      guides.length
    )
    expect(orderedGuides).toHaveLength(48)
  })

  it('assigns usable navigation and valid internal docs links to every guide', () => {
    const endpointIds = new Set(allEndpoints.map((endpoint) => endpoint.id))
    for (const guide of orderedGuides) {
      const { slug, body } = guide
      expect(getGuideGroup(slug), slug).toBeDefined()
      expect(getDocHeadings(body).length, slug).toBeGreaterThan(0)
      for (const match of body.matchAll(/\]\(\/help\/guides\/([a-z0-9-]+)/g)) {
        expect(getGuide(match[1]), `${slug}: ${match[0]}`).toBeDefined()
      }
      for (const match of body.matchAll(
        /\]\(\/help\/api\?endpoint=([a-z0-9-]+)/g
      )) {
        expect(endpointIds.has(match[1]), `${slug}: ${match[0]}`).toBe(true)
      }
    }
    for (const guide of orderedGuides.slice(1, -1)) {
      const neighbors = getAdjacentGuides(guide.slug)
      expect(getAdjacentGuides(neighbors.previous?.slug ?? '').next).toEqual(
        guide
      )
      expect(getAdjacentGuides(neighbors.next?.slug ?? '').previous).toEqual(
        guide
      )
    }
  })

  it('searches original guide content, API paths and mixed-case terms', () => {
    expect(searchDocs('  WoRKBuddy ')[0]).toMatchObject({
      type: 'guide',
      id: 'workbuddy',
    })
    expect(searchDocs('原生 MIME').map((result) => result.id)).toContain(
      'gemini-images'
    )
    expect(
      searchDocs('POST /v1/responses').map((result) => result.id)
    ).toContain('responses')
    expect(searchDocs('unknown-match-7f3d39')).toEqual([])
    expect(searchDocs('  ')).toEqual([])
  })

  it('builds stable shareable headings without mistaking fenced commands for headings', () => {
    expect(
      getDocHeadings(
        '# title\n\n## 配置\n\n```sh\n## ignored\n```\n\n### [设置](https://example.com)\n## 配置'
      )
    ).toEqual([
      { id: 'section-配置', label: '配置', depth: 2 },
      { id: 'section-设置', label: '设置', depth: 3 },
      { id: 'section-配置-2', label: '配置', depth: 2 },
    ])
  })
})
