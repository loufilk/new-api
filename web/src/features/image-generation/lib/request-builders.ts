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
import type { GeneratedImage } from '../types'
import {
  isBlobSchemeUrl,
  isDataSchemeUrl,
  isHttpSchemeUrl,
  isManagedObjectUrl,
} from './managed-object-urls'
import {
  getModelCapability,
  resolveSettingsForModel,
} from './model-capabilities'

export type GenerationsRequestBody = {
  model: string
  prompt: string
  n: number
  response_format?: 'url' | 'b64_json'
  size?: string
  quality?: string
  style?: string
  aspect_ratio?: string
  resolution?: string
}

const SAFE_HTTP_URL = /^https?:\/\//i
const SAFE_DATA_IMAGE_URL =
  /^data:image\/(?:png|jpe?g|webp);base64,[A-Za-z0-9+/=]+$/i

export function isSafeImageUrl(url: string): boolean {
  if (isHttpSchemeUrl(url)) return SAFE_HTTP_URL.test(url.trimStart())
  if (isBlobSchemeUrl(url)) return isManagedObjectUrl(url)
  if (isDataSchemeUrl(url)) {
    return SAFE_DATA_IMAGE_URL.test(url.replaceAll(/\s+/g, ''))
  }
  return false
}

export function assertSafeImageUrl(url: string): void {
  if (!isSafeImageUrl(url)) {
    throw new Error('Image not available')
  }
}

function normalizeCount(count: number, maxN: number): number {
  let next = Math.floor(count)
  if (!Number.isFinite(next)) next = 1
  return Math.max(1, Math.min(maxN, 4, next))
}

export function buildGenerationsBody(params: {
  model: string
  prompt: string
  count: number
  size: string
  quality: string
  style: string
}): GenerationsRequestBody {
  const capability = getModelCapability(params.model)
  const count = normalizeCount(params.count, capability.maxN)

  const body: GenerationsRequestBody = {
    model: params.model,
    prompt: params.prompt,
    n: count,
  }
  if (capability.supportsResponseFormat) {
    body.response_format = capability.responseFormat ?? 'url'
  }
  if (capability.requestFormat === 'grok-console') {
    const settings = resolveSettingsForModel(params.model, params)
    body.aspect_ratio = settings.size
    body.resolution = '1k'
    body.quality = settings.quality
    return body
  }
  if (capability.sizes.length > 0 && params.size) body.size = params.size
  if (capability.qualities.length > 0 && params.quality) {
    body.quality = params.quality
  }
  if (capability.styles.length > 0 && params.style) body.style = params.style
  return body
}

export function buildEditsFormData(params: {
  model: string
  prompt: string
  count: number
  size: string
  images: File[]
  quality?: string
}): FormData {
  const capability = getModelCapability(params.model)
  const count = normalizeCount(params.count, capability.maxN)
  const limited = params.images.slice(0, capability.maxReferenceImages)

  const form = new FormData()
  form.append('model', params.model)
  form.append('prompt', params.prompt)
  if (limited.length === 1) {
    form.append('image', limited[0], limited[0].name)
  } else {
    for (const file of limited) {
      form.append('image[]', file, file.name)
    }
  }
  form.append('n', String(count))
  if (capability.qualities.length > 0 && params.quality) {
    form.append('quality', params.quality)
  }
  if (capability.sizes.length > 0 && params.size) {
    form.append('size', params.size)
  }
  if (capability.supportsResponseFormat) {
    form.append('response_format', capability.responseFormat ?? 'url')
  }
  return form
}

type RawImageItem = {
  url?: string
  b64_json?: string
  mime_type?: string
}

function normalizeDataUrl(value: string, declaredMime?: string): string {
  if (isDataSchemeUrl(value)) return value
  let mime = typeof declaredMime === 'string' ? declaredMime.toLowerCase() : ''
  if (mime && !['image/png', 'image/jpeg', 'image/webp'].includes(mime)) {
    return ''
  }
  if (!mime) {
    try {
      const signature = atob(value.slice(0, 24))
      if (signature.startsWith('\xff\xd8\xff')) mime = 'image/jpeg'
      else if (
        signature.startsWith('RIFF') &&
        signature.slice(8, 12) === 'WEBP'
      ) {
        mime = 'image/webp'
      }
    } catch {
      return ''
    }
  }
  return `data:${mime || 'image/png'};base64,${value}`
}

export function normalizeImageResponse(params: {
  data: unknown
  prompt: string
  model: string
  size: string
}): GeneratedImage[] {
  const raw = params.data as { data?: RawImageItem | RawImageItem[] } | null
  // Some image providers return a single object instead of a one-item array.
  // Keep both shapes on the same URL validation and display path.
  let items: RawImageItem[] = []
  if (Array.isArray(raw?.data)) {
    items = raw.data
  } else if (raw?.data && typeof raw.data === 'object') {
    items = [raw.data]
  }
  const now = Date.now()

  return items
    .map((item, index): GeneratedImage | null => {
      if (!item || typeof item !== 'object') return null
      if (typeof item.url === 'string' && item.url.length > 0) {
        if (!isSafeImageUrl(item.url)) return null
        const isDataUrl = isDataSchemeUrl(item.url)
        return {
          id: `${now}-${index}`,
          url: item.url,
          source: isDataUrl ? ('b64' as const) : ('url' as const),
          prompt: params.prompt,
          model: params.model,
          createdAt: now,
        }
      }
      if (typeof item.b64_json === 'string' && item.b64_json) {
        const normalized = normalizeDataUrl(item.b64_json, item.mime_type)
        if (!isSafeImageUrl(normalized)) return null
        return {
          id: `${now}-${index}`,
          url: normalized,
          source: 'b64' as const,
          prompt: params.prompt,
          model: params.model,
          createdAt: now,
        }
      }
      return null
    })
    .filter((item): item is GeneratedImage => item !== null)
}

export function isAbortError(error: unknown): boolean {
  return (
    (error instanceof DOMException && error.name === 'AbortError') ||
    (typeof error === 'object' &&
      error !== null &&
      'code' in error &&
      (error as { code?: string }).code === 'ERR_CANCELED')
  )
}
