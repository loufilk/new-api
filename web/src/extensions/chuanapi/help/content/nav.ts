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
import {
  Braces,
  Code,
  LayoutGrid,
  Monitor,
  Rocket,
  Terminal,
  Wrench,
  type LucideIcon,
} from 'lucide-react'

import { guides } from './guides'
import type { GuideDoc, GuideGroup } from './types'

/** 分组与排序的唯一事实源；侧栏、首页卡片与上一篇/下一篇均由此派生 */
export const docsGroups: readonly GuideGroup[] = [
  {
    id: 'getting-started',
    title: '快速上手',
    slugs: ['quick-start', 'authentication'],
  },
  {
    id: 'platform',
    title: '平台功能',
    slugs: ['console-tour', 'billing', 'playground'],
  },
  {
    id: 'cli',
    title: '编程 CLI 工具',
    slugs: ['claude-code', 'codex-cli', 'cc-switch'],
  },
  {
    id: 'clients',
    title: '客户端与工具',
    slugs: ['cherry-studio', 'cline-roo', 'immersive-translate'],
  },
  {
    id: 'sdk',
    title: 'SDK 接入',
    slugs: ['openai-sdk', 'anthropic-sdk'],
  },
  {
    id: 'protocol',
    title: '协议与能力',
    slugs: ['compatibility', 'streaming', 'function-calling', 'vision'],
  },
  {
    id: 'troubleshooting',
    title: '故障排查',
    slugs: ['troubleshooting'],
  },
]

export const docsGroupIcons: Record<string, LucideIcon> = {
  'getting-started': Rocket,
  platform: LayoutGrid,
  cli: Terminal,
  clients: Monitor,
  sdk: Code,
  protocol: Braces,
  troubleshooting: Wrench,
}

const guideMap = new Map<string, GuideDoc>(
  guides.map((guide) => [guide.slug, guide])
)

/** 按分组顺序展开的指南列表 */
export const orderedGuides: readonly GuideDoc[] = docsGroups.flatMap((group) =>
  group.slugs
    .map((slug) => guideMap.get(slug))
    .filter((guide): guide is GuideDoc => Boolean(guide))
)

export function getGuide(slug: string): GuideDoc | undefined {
  return guideMap.get(slug)
}

export function getGuideGroup(slug: string): GuideGroup | undefined {
  return docsGroups.find((group) => group.slugs.includes(slug))
}

export function getAdjacentGuides(slug: string): {
  previous?: GuideDoc
  next?: GuideDoc
} {
  const index = orderedGuides.findIndex((guide) => guide.slug === slug)
  if (index === -1) {
    return {}
  }
  return {
    previous: index > 0 ? orderedGuides[index - 1] : undefined,
    next:
      index < orderedGuides.length - 1 ? orderedGuides[index + 1] : undefined,
  }
}
