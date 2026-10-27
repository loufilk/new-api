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
  ImageGenerationSettings,
  ImageModelCapability,
  ImageOrientation,
} from '../types'

const DALLE3_SIZES = ['1024x1024', '1792x1024', '1024x1792']
const DALLE3_QUALITIES = ['standard', 'hd']
const DALLE3_STYLES = ['vivid', 'natural']

const DALLE_SIZES = ['256x256', '512x512', '1024x1024']

const GPT_IMAGE_QUALITY = ['auto', 'low', 'medium', 'high']

function emptyCapability(
  overrides: Partial<ImageModelCapability> & { maxN: number }
): ImageModelCapability {
  return {
    sizes: [],
    qualities: [],
    styles: [],
    maxReferenceImages: 0,
    supportsResponseFormat: false,
    ...overrides,
  }
}

export function getModelCapability(model: string): ImageModelCapability {
  const name = model.trim().toLowerCase()
  // Dedicated Console route: do not apply these capabilities to the Web model.
  if (name === 'grok-imagine-image-2.0-console') {
    return {
      sizes: ['auto', '1:1', '16:9', '9:16', '4:3', '3:4', '3:2', '2:3'],
      qualities: ['low', 'medium'],
      styles: [],
      maxN: 4,
      maxReferenceImages: 3,
      supportsResponseFormat: true,
      requestFormat: 'grok-console',
      responseFormat: 'b64_json',
    }
  }
  if (name.includes('dall-e-3') || name.includes('dalle-3')) {
    return {
      sizes: [...DALLE3_SIZES],
      qualities: [...DALLE3_QUALITIES],
      styles: [...DALLE3_STYLES],
      maxN: 1,
      maxReferenceImages: 0,
      supportsResponseFormat: true,
    }
  }
  if (name.includes('dall-e') || name.includes('dalle')) {
    return {
      sizes: [...DALLE_SIZES],
      qualities: [],
      styles: [],
      maxN: 4,
      maxReferenceImages: 1,
      supportsResponseFormat: true,
    }
  }
  if (name.includes('gpt-image-2-4k')) {
    return {
      sizes: ['auto', '3840x2160', '2160x3840', '2880x2880'],
      qualities: [...GPT_IMAGE_QUALITY],
      styles: [],
      maxN: 4,
      maxReferenceImages: 4,
      supportsResponseFormat: false,
    }
  }
  if (name.includes('gpt-image-2-2k')) {
    return {
      sizes: ['auto', '2048x2048', '2560x1440', '1440x2560'],
      qualities: [...GPT_IMAGE_QUALITY],
      styles: [],
      maxN: 4,
      maxReferenceImages: 4,
      supportsResponseFormat: false,
    }
  }
  if (name.includes('gpt-image-2')) {
    return {
      sizes: [
        'auto',
        '1024x1024',
        '1536x864',
        '864x1536',
        '2048x2048',
        '2560x1440',
        '1440x2560',
        '3840x2160',
        '2160x3840',
        '2880x2880',
      ],
      qualities: [...GPT_IMAGE_QUALITY],
      styles: [],
      maxN: 4,
      maxReferenceImages: 4,
      supportsResponseFormat: false,
    }
  }
  if (name.includes('gpt-image')) {
    return {
      sizes: ['auto', '1024x1024', '1536x1024', '1024x1536'],
      qualities: [...GPT_IMAGE_QUALITY],
      styles: [],
      maxN: 4,
      maxReferenceImages: 4,
      supportsResponseFormat: false,
    }
  }
  if (name.startsWith('grok-imagine-image')) {
    return emptyCapability({
      maxN: 4,
      supportsResponseFormat: true,
      responseFormat: 'b64_json',
    })
  }
  if (name.includes('grok')) {
    return emptyCapability({ maxN: 4, maxReferenceImages: 0 })
  }
  return {
    sizes: ['auto', '1024x1024', '1536x1024', '1024x1536'],
    qualities: [],
    styles: [],
    maxN: 4,
    maxReferenceImages: 0,
    supportsResponseFormat: false,
  }
}

export function resolveSettingsForModel(
  model: string,
  current: Pick<ImageGenerationSettings, 'size' | 'quality' | 'style' | 'count'>
): Pick<ImageGenerationSettings, 'size' | 'quality' | 'style' | 'count'> {
  const capability = getModelCapability(model)
  let count = Math.floor(current.count)
  if (!Number.isFinite(count)) count = 1
  count = Math.max(1, Math.min(capability.maxN, 4, count))

  let size = current.size
  if (capability.sizes.length === 0) {
    size = ''
  } else if (!capability.sizes.includes(size)) {
    size = capability.sizes[0]
  }

  let quality = current.quality
  if (capability.qualities.length === 0) {
    quality = ''
  } else if (!capability.qualities.includes(quality)) {
    quality = capability.qualities[0]
  }

  let style = current.style
  if (capability.styles.length === 0) {
    style = ''
  } else if (!capability.styles.includes(style)) {
    style = capability.styles[0]
  }

  return { size, quality, style, count }
}

export function getImageOrientation(
  width?: number,
  height?: number
): ImageOrientation {
  if (!width || !height || width <= 0 || height <= 0) return 'unknown'
  if (width === height) return 'square'
  return width > height ? 'landscape' : 'portrait'
}

export function parseSizeDimensions(size: string): {
  width?: number
  height?: number
} {
  const match = /^(\d+)x(\d+)$/.exec(size.trim().toLowerCase())
  if (!match) return {}
  const width = Number.parseInt(match[1], 10)
  const height = Number.parseInt(match[2], 10)
  if (!Number.isFinite(width) || !Number.isFinite(height)) return {}
  return { width, height }
}
