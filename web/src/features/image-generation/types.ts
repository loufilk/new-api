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
export type ImageApiKeyOption = {
  id: number
  name: string
  group: string
  auto_groups?: string[] | null
  model_limits_enabled: boolean
  model_limits: string
  status: number
}

export type ImageGenerationSettings = {
  prompt: string
  apiKeyId: number | null
  model: string
  size: string
  quality: string
  style: string
  count: number
}

export type ImageModelCapability = {
  sizes: string[]
  qualities: string[]
  styles: string[]
  maxN: number
  maxReferenceImages: number
  supportsResponseFormat: boolean
  responseFormat?: 'url' | 'b64_json'
  requestFormat?: 'grok-console'
}

export type GeneratedImage = {
  id: string
  url: string
  source: 'url' | 'b64'
  prompt: string
  model: string
  width?: number
  height?: number
  createdAt: number
}

export type HistoryResultRecord = {
  id: string
  type: 'url' | 'b64' | 'blob'
  source: string
  width?: number
  height?: number
  // Older records inferred dimensions from the request, not decoded pixels.
  dimensionsVerified?: true
  blobKey?: string
  keptLocally: boolean
  createdAt: number
}

export type ImageHistoryEntry = {
  id: string
  prompt: string
  model: string
  settings: {
    size: string
    quality: string
    style: string
    count: number
  }
  createdAt: number
  results: HistoryResultRecord[]
}

export type ReferenceImageItem = {
  id: string
  file: File
  previewUrl: string
  name: string
  size: number
  mime: string
}

export type ImageOrientation = 'square' | 'landscape' | 'portrait' | 'unknown'
