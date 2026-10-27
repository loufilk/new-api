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

import {
  getReferenceTotalBytes,
  validateReferenceFiles,
} from '../lib/reference-validation'
import type { ReferenceImageItem } from '../types'

let referenceCounter = 0

function createPreview(file: File): string {
  return URL.createObjectURL(file)
}

export function useReferenceImages(model: string, maxReferenceImages: number) {
  const [items, setItems] = useState<ReferenceImageItem[]>([])
  const [errorKey, setErrorKey] = useState<string | null>(null)
  const itemsRef = useRef(items)

  useEffect(() => {
    const current = itemsRef.current
    if (current.length > maxReferenceImages) {
      const kept = current.slice(0, maxReferenceImages)
      const removed = current.slice(maxReferenceImages)
      for (const item of removed) URL.revokeObjectURL(item.previewUrl)
      itemsRef.current = kept
      setItems(kept)
    }
  }, [maxReferenceImages, model])

  useEffect(() => {
    return () => {
      for (const item of itemsRef.current) {
        URL.revokeObjectURL(item.previewUrl)
      }
    }
  }, [])

  const addFiles = useCallback(
    (files: File[]) => {
      const current = itemsRef.current
      const validation = validateReferenceFiles({
        files: files.map((file) => ({
          name: file.name,
          size: file.size,
          type: file.type,
        })),
        existingTotalBytes: getReferenceTotalBytes(current),
        existingCount: current.length,
        model,
      })
      if (validation) {
        setErrorKey(validation.messageKey)
        return false
      }
      setErrorKey(null)
      const next: ReferenceImageItem[] = files.map((file) => {
        referenceCounter += 1
        return {
          id: `${Date.now()}-${referenceCounter}`,
          file,
          previewUrl: createPreview(file),
          name: file.name,
          size: file.size,
          mime: file.type,
        }
      })
      itemsRef.current = [...current, ...next]
      setItems(itemsRef.current)
      return true
    },
    [model]
  )

  const removeItem = useCallback((id: string) => {
    const target = itemsRef.current.find((item) => item.id === id)
    if (target) URL.revokeObjectURL(target.previewUrl)
    itemsRef.current = itemsRef.current.filter((item) => item.id !== id)
    setItems(itemsRef.current)
  }, [])

  const clear = useCallback(() => {
    for (const item of itemsRef.current) URL.revokeObjectURL(item.previewUrl)
    itemsRef.current = []
    setItems([])
    setErrorKey(null)
  }, [])

  return { items, errorKey, setErrorKey, addFiles, removeItem, clear }
}
