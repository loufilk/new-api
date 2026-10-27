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
import { t } from 'i18next'

import { fetchTokenKey, getApiKeys } from '@/features/keys/api'
import type { ApiKey } from '@/features/keys/types'
import { getPricing } from '@/features/pricing/api'
import { api } from '@/lib/api'
import { apiResourceUrl } from '@/lib/api-url'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  getManagedObjectUrlMimeType,
  isBlobSchemeUrl,
  isDataSchemeUrl,
} from './lib/managed-object-urls'
import { getModelCapability } from './lib/model-capabilities'
import { readReferenceImageDataUrl } from './lib/reference-data-url'
import { validateReferenceFiles } from './lib/reference-validation'
import {
  assertSafeImageUrl,
  buildEditsFormData,
  buildGenerationsBody,
  isSafeImageUrl,
  normalizeImageResponse,
} from './lib/request-builders'
import type { GeneratedImage, ImageApiKeyOption } from './types'

export { isSafeImageUrl }

export function toImageKeyOption(key: ApiKey): ImageApiKeyOption {
  return {
    id: key.id,
    name: key.name,
    group: key.group ?? '',
    auto_groups: key.auto_groups,
    model_limits_enabled: key.model_limits_enabled,
    model_limits: key.model_limits ?? '',
    status: key.status,
  }
}

const IMAGE_KEYS_PAGE_SIZE = 100

export async function loadImagePageBootstrap(): Promise<{
  keys: ImageApiKeyOption[]
  pricingModels: { model_name: string; supported_endpoint_types?: string[] }[]
}> {
  const [firstPage, pricingRes] = await Promise.all([
    getApiKeys({ p: 1, size: IMAGE_KEYS_PAGE_SIZE }),
    getPricing(),
  ])

  const firstItems = firstPage.data?.items ?? []
  const total = firstPage.data?.total ?? firstItems.length
  const pageSize =
    firstPage.data?.page_size && firstPage.data.page_size > 0
      ? firstPage.data.page_size
      : IMAGE_KEYS_PAGE_SIZE
  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  let items = firstItems
  if (totalPages > 1) {
    const rest = await Promise.all(
      Array.from({ length: totalPages - 1 }, (_, index) =>
        getApiKeys({ p: index + 2, size: pageSize })
      )
    )
    for (const page of rest) {
      items = [...items, ...(page.data?.items ?? [])]
    }
  }

  const keys = items.filter((item) => item.status === 1).map(toImageKeyOption)

  return { keys, pricingModels: pricingRes.data ?? [] }
}

export async function loadImageTokenModels(id: number): Promise<string[]> {
  const res = await api.get(`/api/token/${id}/models`, {
    skipErrorHandler: true,
  })
  const data = requireServerSuccess(res.data)
  if (!Array.isArray(data?.data)) {
    throw new Error(t('Failed to load models'))
  }
  return (data.data as unknown[]).filter(
    (item): item is string => typeof item === 'string'
  )
}

export async function loadFullTokenKey(
  id: number,
  signal?: AbortSignal
): Promise<string> {
  const res = await fetchTokenKey(id, signal ? { signal } : undefined)
  const key = res.data?.key
  if (signal?.aborted) {
    throw new DOMException('The operation was aborted.', 'AbortError')
  }
  if (!res.success || !key) {
    throw new Error(res.message || 'Failed to load API key')
  }
  return key
}

export async function requestImageGeneration(params: {
  fullKey: string
  model: string
  prompt: string
  count: number
  size: string
  quality: string
  style: string
  images: File[]
  asyncMode?: boolean
  signal?: AbortSignal
}): Promise<GeneratedImage[]> {
  const hasReference = params.images.length > 0
  const headers = { Authorization: `Bearer ${params.fullKey}` }

  params.signal?.throwIfAborted()
  let body:
    | (ReturnType<typeof buildGenerationsBody> & { images?: { url: string }[] })
    | FormData
  if (!hasReference) {
    body = buildGenerationsBody(params)
  } else if (
    getModelCapability(params.model).requestFormat === 'grok-console'
  ) {
    const validation = validateReferenceFiles({
      files: params.images,
      existingCount: 0,
      existingTotalBytes: 0,
      model: params.model,
    })
    if (validation) throw new Error(t(validation.messageKey))
    const urls = await Promise.all(
      params.images.map((file) =>
        readReferenceImageDataUrl(file, params.signal)
      )
    )
    params.signal?.throwIfAborted()
    body = {
      ...buildGenerationsBody(params),
      images: urls.map((url) => ({ url })),
    }
  } else {
    body = buildEditsFormData(params)
  }
  const operation = hasReference ? 'edits' : 'generations'
  const res = await api.post(
    params.asyncMode
      ? `/v1/images/tasks/${operation}`
      : `/v1/images/${operation}`,
    body,
    {
      headers,
      signal: params.signal,
      skipErrorHandler: true,
      skipAuthRefresh: true,
      preserveAuthorization: true,
    }
  )
  let result = res.data
  if (params.asyncMode) {
    const id = res.data?.id
    if (
      res.status !== 202 ||
      typeof id !== 'string' ||
      !/^[a-f0-9]{48}$/.test(id)
    ) {
      throw new Error(t('Image generation failed'))
    }
    const deadline = Date.now() + 330_000
    for (;;) {
      params.signal?.throwIfAborted()
      // Session authentication lets the owner retrieve the result even when
      // generation spent the selected API key's final quota. Never send that
      // API key to the polling endpoint or follow a server-provided URL.
      const response = await api.get(`/v1/images/tasks/${id}`, {
        signal: params.signal,
        timeout: 15_000,
        skipErrorHandler: true,
      })
      const task = response.data
      if (task?.status === 'succeeded') {
        result = task.result
        break
      }
      if (task?.status === 'failed') {
        throw new Error(
          task.result?.error?.message || t('Image generation failed')
        )
      }
      if (task?.status !== 'running' || Date.now() >= deadline) {
        throw new Error(t('Image generation failed'))
      }
      await waitForImageTask(params.signal)
    }
  }
  return normalizeImageResponse({
    data: result,
    prompt: params.prompt,
    model: params.model,
    size: params.size,
  })
}

function waitForImageTask(signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    signal?.throwIfAborted()
    const abort = () => {
      clearTimeout(timer)
      signal?.removeEventListener('abort', abort)
      reject(new DOMException('The operation was aborted.', 'AbortError'))
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', abort)
      resolve()
    }, 2000)
    signal?.addEventListener('abort', abort, { once: true })
  })
}

export function resolveDownloadUrl(url: string): string {
  assertSafeImageUrl(url)
  return url
}

export async function downloadImage(
  url: string,
  filename: string
): Promise<void> {
  assertSafeImageUrl(url)
  let href = url
  let mimeType = ''
  let temporaryUrl: string | null = null
  const extensions: Record<string, string> = {
    'image/png': 'png',
    'image/jpeg': 'jpg',
    'image/jpg': 'jpg',
    'image/webp': 'webp',
  }
  if (isDataSchemeUrl(url)) {
    mimeType = /^data:([^;]+);/i.exec(url)?.[1].toLowerCase() ?? ''
  } else if (isBlobSchemeUrl(url)) {
    mimeType = getManagedObjectUrlMimeType(url)
  } else {
    // Third-party absolute URLs pass through untouched; app-relative links
    // resolve against the configured API origin.
    const response = await fetch(apiResourceUrl(url))
    if (!response.ok) throw new Error(`Download failed: ${response.status}`)
    const blob = await response.blob()
    mimeType = blob.type.split(';', 1)[0].trim().toLowerCase()
    if (!extensions[mimeType]) throw new Error(t('Download failed'))
    temporaryUrl = URL.createObjectURL(blob)
    href = temporaryUrl
  }
  const extension = extensions[mimeType]
  const downloadName = extension
    ? `${filename.replace(/\.(?:png|jpe?g|webp)$/i, '')}.${extension}`
    : filename
  const anchor = document.createElement('a')
  try {
    anchor.href = href
    anchor.download = downloadName
    document.body.appendChild(anchor)
    anchor.click()
  } finally {
    anchor.remove()
    if (temporaryUrl) URL.revokeObjectURL(temporaryUrl)
  }
}
