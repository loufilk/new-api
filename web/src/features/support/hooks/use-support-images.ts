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

import type { ReferenceImageItem } from '@/features/image-generation/types'

import { supportImageError } from '../lib/attachments'

export function useSupportImages() {
  const [items, setItems] = useState<ReferenceImageItem[]>([])
  const [error, setError] = useState<string | null>(null)
  const current = useRef<ReferenceImageItem[]>([])
  const clear = useCallback(() => {
    current.current.forEach((item) => URL.revokeObjectURL(item.previewUrl))
    current.current = []
    setItems([])
    setError(null)
  }, [])
  useEffect(
    () => () => {
      current.current.forEach((item) => URL.revokeObjectURL(item.previewUrl))
      current.current = []
    },
    []
  )
  const add = (files: File[]) => {
    const failure = supportImageError(files, current.current.length)
    setError(failure)
    if (failure) return
    const next = files.map((file) => ({
      id: crypto.randomUUID(),
      file,
      previewUrl: URL.createObjectURL(file),
      name: file.name,
      size: file.size,
      mime: file.type,
    }))
    current.current = [...current.current, ...next]
    setItems(current.current)
  }
  const remove = (id: string) => {
    const target = current.current.find((item) => item.id === id)
    if (target) URL.revokeObjectURL(target.previewUrl)
    current.current = current.current.filter((item) => item.id !== id)
    setItems(current.current)
    setError(null)
  }
  return { items, error, add, remove, clear }
}
