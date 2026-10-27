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

// JSON image-edit APIs require inline image URLs rather than multipart files.
export function readReferenceImageDataUrl(
  file: File,
  signal?: AbortSignal
): Promise<string> {
  signal?.throwIfAborted()
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    const abort = () => reader.abort()
    reader.addEventListener(
      'load',
      () => {
        if (typeof reader.result === 'string') resolve(reader.result)
        else reject(new Error(t('Failed to read reference image')))
      },
      { once: true }
    )
    reader.addEventListener(
      'error',
      () => reject(new Error(t('Failed to read reference image'))),
      { once: true }
    )
    reader.addEventListener(
      'abort',
      () =>
        reject(new DOMException('The operation was aborted.', 'AbortError')),
      { once: true }
    )
    reader.addEventListener(
      'loadend',
      () => signal?.removeEventListener('abort', abort),
      { once: true }
    )
    signal?.addEventListener('abort', abort, { once: true })
    // The uploader accepts files with an empty MIME type when the extension is known.
    let mime = file.type
    if (!mime) {
      const extension = file.name.split('.').at(-1)?.toLowerCase()
      if (extension === 'png') mime = 'image/png'
      else if (extension === 'webp') mime = 'image/webp'
      else mime = 'image/jpeg'
    }
    try {
      reader.readAsDataURL(file.slice(0, file.size, mime))
    } catch (error) {
      signal?.removeEventListener('abort', abort)
      reject(error)
    }
  })
}
