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
import { useCallback, useRef } from 'react'

// Keep the same operation identity after a lost response. Only a changed payload
// or a confirmed completion starts a new operation; nothing is persisted locally.
export function useSupportRequestId() {
  const previous = useRef<{
    payload: string
    files: File[]
    id: string
  } | null>(null)
  const reset = useCallback(() => {
    previous.current = null
  }, [])
  const get = (payload: string, files: File[]) => {
    const current = previous.current
    if (
      current?.payload === payload &&
      current.files.length === files.length &&
      current.files.every((file, index) => file === files[index])
    ) {
      return current.id
    }
    const id = crypto.randomUUID()
    previous.current = { payload, files: [...files], id }
    return id
  }
  return { get, reset }
}
