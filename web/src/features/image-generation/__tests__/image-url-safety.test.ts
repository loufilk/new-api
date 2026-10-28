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
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { downloadImage, resolveDownloadUrl } from '../api'
import {
  clearManagedObjectUrls,
  createManagedObjectUrl,
  revokeManagedObjectUrl,
} from '../lib/managed-object-urls'
import { isSafeImageUrl, normalizeImageResponse } from '../lib/request-builders'

const createdAnchors: HTMLAnchorElement[] = []
const realCreateElement = document.createElement.bind(document)

let managedCounter = 0

beforeEach(() => {
  createdAnchors.length = 0
  managedCounter = 0
  clearManagedObjectUrls()
  URL.createObjectURL = vi.fn(() => {
    managedCounter += 1
    return `blob:managed-${managedCounter}`
  }) as typeof URL.createObjectURL
  URL.revokeObjectURL = vi.fn()
  vi.spyOn(document, 'createElement').mockImplementation(((
    tagName: string,
    ...rest: never[]
  ) => {
    const element = realCreateElement(
      tagName as 'div',
      ...(rest as [])
    ) as HTMLElement
    if (tagName === 'a') createdAnchors.push(element as HTMLAnchorElement)
    return element
  }) as typeof document.createElement)
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(
    () => undefined
  )
})

afterEach(() => {
  clearManagedObjectUrls()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('image URL protocol allowlist', () => {
  test('accepts http/https and strict base64 image data URLs', () => {
    expect(isSafeImageUrl('https://cdn.example/a.png')).toBe(true)
    expect(isSafeImageUrl('http://cdn.example/a.png')).toBe(true)
    expect(isSafeImageUrl('data:image/png;base64,aGVsbG8=')).toBe(true)
    expect(isSafeImageUrl('data:image/jpeg;base64,aGVsbG8=')).toBe(true)
    expect(isSafeImageUrl('data:image/webp;base64,aGVsbG8=')).toBe(true)
  })

  test('rejects unregistered blob URLs', () => {
    expect(isSafeImageUrl('blob:https://app.example/uuid')).toBe(false)
    expect(isSafeImageUrl('blob:managed-999')).toBe(false)
    expect(isSafeImageUrl('BLOB:https://app.example/uuid')).toBe(false)
  })

  test('accepts a managed blob URL and rejects it again after revoke', () => {
    const managed = createManagedObjectUrl(new Blob(['x']))
    expect(isSafeImageUrl(managed)).toBe(true)
    expect(() => resolveDownloadUrl(managed)).not.toThrow()
    revokeManagedObjectUrl(managed)
    expect(isSafeImageUrl(managed)).toBe(false)
    expect(() => resolveDownloadUrl(managed)).toThrow('Image not available')
  })

  test('rejects javascript and other unsafe protocols', () => {
    expect(isSafeImageUrl('javascript:alert(1)')).toBe(false)
    expect(isSafeImageUrl('JaVaScRiPt:alert(1)')).toBe(false)
    expect(isSafeImageUrl('data:text/html;base64,PGI+')).toBe(false)
    expect(isSafeImageUrl('data:image/svg+xml;base64,PHN2Zz4=')).toBe(false)
    expect(isSafeImageUrl('ftp://cdn.example/a.png')).toBe(false)
    expect(isSafeImageUrl('file:///etc/passwd')).toBe(false)
  })

  test('normalize drops malicious urls instead of returning them', () => {
    const images = normalizeImageResponse({
      data: {
        data: [
          { url: 'javascript:alert(1)' },
          { url: 'https://cdn.example/ok.png' },
          { url: 'data:text/html;base64,PGI+' },
        ],
      },
      prompt: 'a lake',
      model: 'dall-e-3',
      size: '1024x1024',
    })
    expect(images).toHaveLength(1)
    expect(images[0].url).toBe('https://cdn.example/ok.png')
  })

  test('normalize drops upstream blob URLs that were never registered', () => {
    const images = normalizeImageResponse({
      data: {
        data: [
          { url: 'blob:https://upstream.example/evil' },
          { url: 'https://cdn.example/ok.png' },
        ],
      },
      prompt: 'a lake',
      model: 'dall-e-3',
      size: '1024x1024',
    })
    expect(images).toHaveLength(1)
    expect(images[0].url).toBe('https://cdn.example/ok.png')
  })

  test('resolveDownloadUrl throws a handleable error for unsafe protocols', () => {
    expect(() => resolveDownloadUrl('javascript:alert(1)')).toThrow(
      'Image not available'
    )
    expect(() => resolveDownloadUrl('blob:https://app.example/uuid')).toThrow(
      'Image not available'
    )
    expect(() => resolveDownloadUrl('https://cdn.example/a.png')).not.toThrow()
  })

  test('downloadImage never fetches or anchors a javascript URL', async () => {
    const fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)

    await expect(
      downloadImage('javascript:alert(1)', 'evil.png')
    ).rejects.toThrow('Image not available')

    expect(fetchSpy).not.toHaveBeenCalled()
    expect(createdAnchors).toHaveLength(0)
  })

  test('downloadImage rejects unsafe data URLs without a network request', async () => {
    const fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)

    await expect(
      downloadImage('data:text/html;base64,PGI+', 'evil.html')
    ).rejects.toThrow('Image not available')

    expect(fetchSpy).not.toHaveBeenCalled()
  })

  test('downloadImage rejects unregistered blob URLs without a network request', async () => {
    const fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)

    await expect(
      downloadImage('blob:https://app.example/uuid', 'evil.png')
    ).rejects.toThrow('Image not available')

    expect(fetchSpy).not.toHaveBeenCalled()
    expect(createdAnchors).toHaveLength(0)
  })

  test('downloadImage saves a managed blob URL through an anchor without fetch', async () => {
    const fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    const managed = createManagedObjectUrl(new Blob(['image-bytes']))

    await downloadImage(managed, 'kept.png')

    expect(fetchSpy).not.toHaveBeenCalled()
    expect(createdAnchors).toHaveLength(1)
    expect(createdAnchors[0].href).toBe(managed)
    expect(createdAnchors[0].download).toBe('kept.png')
  })

  test('a managed JPEG blob downloads as JPEG without fetching a private URL', async () => {
    const fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    const managed = createManagedObjectUrl(
      new Blob(['jpeg'], { type: 'image/jpeg' })
    )
    await downloadImage(managed, 'history.png')
    expect(fetchSpy).not.toHaveBeenCalled()
    expect(createdAnchors[0].download).toBe('history.jpg')
  })

  test('downloadImage rejects a managed blob URL again after revoke', async () => {
    const fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    const managed = createManagedObjectUrl(new Blob(['image-bytes']))
    revokeManagedObjectUrl(managed)

    await expect(downloadImage(managed, 'revoked.png')).rejects.toThrow(
      'Image not available'
    )

    expect(fetchSpy).not.toHaveBeenCalled()
    expect(createdAnchors).toHaveLength(0)
  })

  test('uppercase DATA and managed BLOB URLs never fall through to fetch', async () => {
    const fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)

    await downloadImage('DATA:IMAGE/PNG;BASE64,aGVsbG8=', 'upper-data.png')
    expect(fetchSpy).not.toHaveBeenCalled()
    expect(createdAnchors).toHaveLength(1)
    expect(createdAnchors[0].download).toBe('upper-data.png')

    createdAnchors.length = 0
    const managed = createManagedObjectUrl(new Blob(['image-bytes']))
    const upperManaged = `BLOB:${managed.slice('blob:'.length)}`
    expect(isSafeImageUrl(upperManaged)).toBe(true)
    await downloadImage(upperManaged, 'upper-blob.png')
    expect(fetchSpy).not.toHaveBeenCalled()
    expect(createdAnchors).toHaveLength(1)
    expect(createdAnchors[0].download).toBe('upper-blob.png')
  })
})
