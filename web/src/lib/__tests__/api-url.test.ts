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
import { afterEach, describe, expect, test, vi } from 'vitest'

async function loadApiUrl(apiBaseUrl: string) {
  vi.resetModules()
  vi.stubEnv('VITE_API_BASE_URL', apiBaseUrl)
  return await import('../api-url')
}

afterEach(() => {
  vi.unstubAllEnvs()
  vi.resetModules()
})

describe('api url resolution', () => {
  test('an empty base keeps app-owned requests on the frontend origin', async () => {
    const { apiUrl, apiResourceUrl, hasExternalApiBaseUrl, API_BASE_URL } =
      await loadApiUrl('')

    expect(API_BASE_URL).toBe('')
    expect(hasExternalApiBaseUrl()).toBe(false)
    expect(apiUrl('/api/status')).toBe('/api/status')
    expect(apiUrl('api/status')).toBe('api/status')
    expect(apiResourceUrl('/v1/tasks/t/artifacts/a/content')).toBe(
      '/v1/tasks/t/artifacts/a/content'
    )
  })

  test('a configured base prefixes app-relative paths only', async () => {
    const { apiUrl, apiResourceUrl, hasExternalApiBaseUrl, API_BASE_URL } =
      await loadApiUrl('https://api.example.com/')

    expect(API_BASE_URL).toBe('https://api.example.com')
    expect(hasExternalApiBaseUrl()).toBe(true)
    expect(apiUrl('/api/status')).toBe('https://api.example.com/api/status')
    expect(apiUrl('api/status')).toBe('https://api.example.com/api/status')

    expect(apiUrl('https://cdn.example.com/logo.png')).toBe(
      'https://cdn.example.com/logo.png'
    )
    expect(apiResourceUrl('data:image/png;base64,AAAA')).toBe(
      'data:image/png;base64,AAAA'
    )
    expect(apiResourceUrl('blob:https://web.example.com/id')).toBe(
      'blob:https://web.example.com/id'
    )
    expect(apiResourceUrl('//cdn.example.com/logo.png')).toBe(
      '//cdn.example.com/logo.png'
    )
    expect(apiResourceUrl('/v1/tasks/t/artifacts/a/content?access=token')).toBe(
      'https://api.example.com/v1/tasks/t/artifacts/a/content?access=token'
    )
  })
})
