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
import { toc } from '@lobehub/icons/es/toc'

// Only variants compiled by the icon loader are selectable. Labels, descriptions
// and URLs in the upstream toc are never published as catalog metadata.
const flags = {
  Avatar: 'hasAvatar',
  Brand: 'hasBrand',
  BrandColor: 'hasBrandColor',
  Color: 'hasColor',
  Combine: 'hasCombine',
  Text: 'hasText',
  TextCn: 'hasTextCn',
  TextColor: 'hasTextColor',
} as const

export const catalogIconVariants = new Map<string, readonly string[]>(
  toc.map((icon) => {
    const variants = Object.entries(flags)
      .filter(([, flag]) => icon.param[flag])
      .map(([variant]) => variant)
    if (icon.id === 'Gemma') variants.push('Simple')
    if (icon.id === 'LobeHub') variants.push('Morden')
    return [icon.id, variants.sort()]
  })
)
// These are local components, not directory entries from @lobehub/icons.
for (const id of ['SGLang', 'Sub2API', 'Wan']) catalogIconVariants.set(id, [])

const knownVariants = new Set([
  'Mono',
  ...Object.keys(flags),
  'Simple',
  'Morden',
])
const legacyProperty =
  /^(?:type|shape|size|role|aria-label|color|background|iconMultiple)=\{?(?:'[\w #-]+'|"[\w #-]+"|[\w#-]+)\}?$/

// Old records may include presentation props. Keep those stored values intact,
// but never forward free-form attributes, URLs or HTML to a catalog icon.
export function catalogIconID(value?: string | null): string {
  if (typeof value !== 'string') return ''
  const key = value.trim()
  if (!key || key.length > 128) return ''
  const [id, ...segments] = key.split('.')
  const variants = catalogIconVariants.get(id)
  if (!variants) return ''
  let canonical = id
  if (segments.length && knownVariants.has(segments[0])) {
    const variant = segments[0]
    segments.shift()
    if (variants.includes(variant)) canonical = `${id}.${variant}`
  }
  return segments.every((segment) => legacyProperty.test(segment))
    ? canonical
    : ''
}

export function getLobeIconNames(): string[] {
  return [...catalogIconVariants]
    .flatMap(([id, variants]) => [
      id,
      ...variants.map((variant) => `${id}.${variant}`),
    ])
    .sort()
}
