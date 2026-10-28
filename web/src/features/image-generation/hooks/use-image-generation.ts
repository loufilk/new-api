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
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { handleServerError } from '@/lib/handle-server-error'

import { loadFullTokenKey, requestImageGeneration } from '../api'
import { isAbortError } from '../lib/request-builders'
import type { GeneratedImage } from '../types'

type GenerateInput = {
  apiKeyId: number | null
  model: string
  prompt: string
  count: number
  size: string
  quality: string
  style: string
  images: File[]
  asyncMode?: boolean
}

export function useImageGeneration() {
  const { t } = useTranslation()
  const [isGenerating, setIsGenerating] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [results, setResults] = useState<GeneratedImage[]>([])
  const abortRef = useRef<AbortController | null>(null)
  const mountedRef = useRef(true)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      abortRef.current?.abort()
      abortRef.current = null
    }
  }, [])

  const cancel = useCallback(() => {
    abortRef.current?.abort()
  }, [])

  const generate = useCallback(
    async (input: GenerateInput): Promise<GeneratedImage[] | null> => {
      if (abortRef.current && !abortRef.current.signal.aborted) return null
      if (input.apiKeyId === null || input.apiKeyId === undefined) {
        if (mountedRef.current) setError(t('Please select an API key'))
        return null
      }
      if (!input.model) {
        if (mountedRef.current) setError(t('Please select a model'))
        return null
      }
      if (!input.prompt.trim()) {
        if (mountedRef.current) setError(t('Please enter a prompt'))
        return null
      }

      const controller = new AbortController()
      abortRef.current = controller
      if (mountedRef.current) {
        setIsGenerating(true)
        setError(null)
      }
      try {
        const fullKey = await loadFullTokenKey(
          input.apiKeyId,
          controller.signal
        )
        if (controller.signal.aborted) {
          throw new DOMException('The operation was aborted.', 'AbortError')
        }
        const images = await requestImageGeneration({
          fullKey,
          model: input.model,
          prompt: input.prompt.trim(),
          count: input.count,
          size: input.size,
          quality: input.quality,
          style: input.style,
          images: input.images,
          asyncMode: input.asyncMode,
          signal: controller.signal,
        })
        if (!mountedRef.current || controller.signal.aborted) return null
        setResults(images)
        return images
      } catch (err) {
        if (isAbortError(err) || controller.signal.aborted) {
          if (mountedRef.current) setError(t('Generation was cancelled'))
          return null
        }
        if (!mountedRef.current) return null
        handleServerError(err)
        const message =
          err instanceof Error && err.message
            ? err.message
            : t('Image generation failed')
        if (mountedRef.current) setError(message)
        return null
      } finally {
        if (abortRef.current === controller) abortRef.current = null
        if (mountedRef.current) setIsGenerating(false)
      }
    },
    [t]
  )

  return {
    isGenerating,
    error,
    setError,
    results,
    setResults,
    generate,
    cancel,
  }
}
