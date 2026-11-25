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
/**
 * LobeHub Icon Loader
 * Dynamically load and render icons from @lobehub/icons
 *
 * Supports:
 * - Basic: "OpenAI", "OpenAI.Color"
 * - Chained properties: "OpenAI.Avatar.type={'platform'}"
 * - Size parameter: getLobeIcon("OpenAI", 20)
 */
import GeminiColor from '@lobehub/icons/es/Gemini/components/Color.js'
import GeminiMono from '@lobehub/icons/es/Gemini/components/Mono.js'
import GoogleColor from '@lobehub/icons/es/Google/components/Color.js'
import GoogleMono from '@lobehub/icons/es/Google/components/Mono.js'
import OpenAIMono from '@lobehub/icons/es/OpenAI/components/Mono.js'
import {
  lazy,
  Suspense,
  type ComponentType,
  type LazyExoticComponent,
  type ReactNode,
} from 'react'

import sglangLogo from '@/assets/brand-icons/sglang.svg'
import { IconSub2api } from '@/assets/custom/icon-sub2api'
import { IconWan } from '@/assets/custom/icon-wan'

import { catalogIconVariants } from './catalog-icon-registry'

export { getLobeIconNames } from './catalog-icon-registry'

const CUSTOM_ICONS: Record<string, ComponentType<{ size?: number }>> = {
  SGLang: (props) => (
    <img
      src={sglangLogo}
      alt=''
      aria-hidden='true'
      width={props.size ?? 20}
      height={props.size ?? 20}
      className='object-contain'
    />
  ),
  Sub2API: IconSub2api,
  Wan: IconWan,
}

// These small, common glyphs must paint with the first frame (including in
// filter chips and virtualized rows). Do not import a whole brand or the UI
// toolkit: only the exact SVG modules below are eager.
const inlineBrands = new Map<string, ComponentType<Record<string, unknown>>>([
  ['OpenAI.Mono', OpenAIMono],
  ['Gemini.Mono', GeminiMono],
  ['Gemini.Color', GeminiColor],
  ['Google.Mono', GoogleMono],
  ['Google.Color', GoogleColor],
])

const LAZY_ICONS = new Map<
  string,
  LazyExoticComponent<ComponentType<Record<string, unknown>>>
>()

function renderLobeIconFallback(
  name: string,
  size: number | string
): ReactNode {
  // A heavier variant (e.g. a configured Avatar) can be loading or offline.
  // Keep its brand visible rather than swapping in a misleading initial.
  const Brand =
    inlineBrands.get(`${name}.Color`) ?? inlineBrands.get(`${name}.Mono`)
  if (Brand) return <Brand size={size} />
  return (
    <div
      className='bg-muted text-muted-foreground flex items-center justify-center rounded-full text-xs font-medium'
      style={{ width: size, height: size }}
    >
      {name.charAt(0).toUpperCase() || '?'}
    </div>
  )
}

/**
 * Parse a property value from string to appropriate type
 * @param raw - Raw string value
 * @returns Parsed value (boolean, number, or string)
 */
function parseValue(raw: string | undefined | null): string | number | boolean {
  if (raw == null) return true

  let v = String(raw).trim()

  // Remove curly braces
  if (v.startsWith('{') && v.endsWith('}')) {
    v = v.slice(1, -1).trim()
  }

  // Remove quotes
  if (
    (v.startsWith('"') && v.endsWith('"')) ||
    (v.startsWith("'") && v.endsWith("'"))
  ) {
    return v.slice(1, -1)
  }

  // Boolean
  if (v === 'true') return true
  if (v === 'false') return false

  // Number
  if (/^-?\d+(?:\.\d+)?$/.test(v)) return Number(v)

  // Return as string
  return v
}

/**
 * Get LobeHub icon component by name
 * @param iconName - Icon name/description (e.g., "OpenAI", "OpenAI.Color", "Claude.Avatar")
 * @param size - Icon size (default: 20)
 * @returns Icon component or fallback
 *
 * @example
 * getLobeIcon("OpenAI", 24)
 * getLobeIcon("OpenAI.Color", 20)
 * getLobeIcon("Claude.Avatar.type={'platform'}", 32)
 */
export function getLobeIcon(
  iconName: string | undefined | null,
  size: number = 20
): ReactNode {
  const trimmedName = typeof iconName === 'string' ? iconName.trim() : ''
  const fallback = renderLobeIconFallback(trimmedName, size)
  if (!trimmedName) return fallback

  const segments = trimmedName.split('.')
  const baseKey = segments[0]
  const CustomIcon = Object.hasOwn(CUSTOM_ICONS, baseKey)
    ? CUSTOM_ICONS[baseKey]
    : null
  if (CustomIcon) return <CustomIcon size={size} />

  const variants = catalogIconVariants.get(baseKey)
  if (!variants) return fallback
  const variant = variants.includes(segments[1]) ? segments[1] : 'Mono'
  const propStartIndex =
    segments.length > 1 && /^[A-Z]/.test(segments[1]) ? 2 : 1
  // Parse chained properties (e.g., "type={'platform'}", "shape='square'")
  const props: Record<string, string | number | boolean> = {}

  for (let i = propStartIndex; i < segments.length; i++) {
    const seg = segments[i]
    if (!seg) continue

    const eqIdx = seg.indexOf('=')
    if (eqIdx === -1) {
      props[seg.trim()] = true
      continue
    }

    const key = seg.slice(0, eqIdx).trim()
    const valRaw = seg.slice(eqIdx + 1).trim()
    props[key] = parseValue(valRaw)
  }

  // Set size if not explicitly specified in the string
  if (props.size == null && size != null) {
    props.size = size
  }

  const cacheKey = `${baseKey}.${variant}`
  const InlineIcon = inlineBrands.get(cacheKey)
  if (InlineIcon) return <InlineIcon {...props} />

  let IconComponent = LAZY_ICONS.get(cacheKey)
  if (!IconComponent) {
    // Load the selected SVG variant, avoiding the brand index's Avatar/UI dependencies.
    // The v6 compatibility exports expose .js aliases, but Rspack enumerates
    // physical files (.mjs) when constructing a dynamic import context.
    IconComponent = lazy(() =>
      import(
        /* webpackInclude: /[\\/]components[\\/](Mono|Avatar|Brand|BrandColor|Color|Combine|Text|TextCn|TextColor|Simple|Morden)\.mjs$/ */
        `../../node_modules/@lobehub/icons/es/${baseKey}/components/${variant}.mjs`
      ).catch(() => ({
        default: (props: Record<string, unknown>) =>
          renderLobeIconFallback(
            baseKey,
            typeof props.size === 'number' || typeof props.size === 'string'
              ? props.size
              : 20
          ),
      }))
    )
    LAZY_ICONS.set(cacheKey, IconComponent)
  }

  return (
    <Suspense
      fallback={renderLobeIconFallback(
        baseKey,
        typeof props.size === 'number' || typeof props.size === 'string'
          ? props.size
          : size
      )}
    >
      <IconComponent {...props} />
    </Suspense>
  )
}
