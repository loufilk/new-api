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

import {
  loadFullTokenKey,
  pollAcceptedImageTask,
  requestImageGeneration,
} from '../api'
import { isAbortError } from '../lib/request-builders'
import type { PendingImageTask } from '../storage/pending-task'
import type { GeneratedImage } from '../types'

type GenerateInput = {
  apiKeyId: number | null
  model: string
  prompt: string
  count: number
  size: string
  resolution?: string
  quality: string
  style: string
  images: File[]
  acceptedTask?: PendingImageTask
  onAccepted?: (id: string, expiresAt: number) => void
  onTerminalFailure?: () => void
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
      if (!input.acceptedTask && input.apiKeyId == null) {
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
        const retrieve = async () => {
          if (input.acceptedTask) {
            return pollAcceptedImageTask({
              ...input.acceptedTask,
              signal: controller.signal,
              onTerminalFailure: input.onTerminalFailure,
            })
          }
          if (input.apiKeyId == null) {
            throw new Error(t('Please select an API key'))
          }
          const fullKey = await loadFullTokenKey(
            input.apiKeyId,
            controller.signal
          )
          if (controller.signal.aborted) {
            throw new DOMException('The operation was aborted.', 'AbortError')
          }
          return requestImageGeneration({
            fullKey,
            model: input.model,
            prompt: input.prompt.trim(),
            count: input.count,
            size: input.size,
            resolution: input.resolution,
            quality: input.quality,
            style: input.style,
            images: input.images,
            asyncMode: input.asyncMode,
            onAccepted: input.onAccepted,
            onTerminalFailure: input.onTerminalFailure,
            signal: controller.signal,
          })
        }
        const images = await retrieve()
        if (
          !mountedRef.current ||
          abortRef.current !== controller ||
          controller.signal.aborted
        ) {
          return null
        }
        setResults(images)
        return images
      } catch (err) {
        if (!mountedRef.current || abortRef.current !== controller) return null
        if (isAbortError(err) || controller.signal.aborted) {
          setError(t('Stopped waiting for the image response'))
          return null
        }
        handleServerError(err)
        const message =
          err instanceof Error && err.message
            ? err.message
            : t('Image generation failed')
        if (mountedRef.current) setError(message)
        return null
      } finally {
        if (abortRef.current === controller) {
          abortRef.current = null
          if (mountedRef.current) setIsGenerating(false)
        }
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
