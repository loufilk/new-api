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
import type { AxiosResponse, InternalAxiosRequestConfig } from 'axios'
import { afterEach, describe, expect, test } from 'vitest'

import { api } from '@/lib/http-client'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

function makeBundle(accessToken: string): AuthBundle {
  return {
    access_token: accessToken,
    token_type: 'Bearer',
    access_expires_at: Math.floor(Date.now() / 1000) + 600,
    user: { id: 1, username: 'tester', role: 1 },
    session: {
      sid: 'session-test',
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'vitest',
      created_at: 1,
      last_active_at: 1,
      expires_at: 9999999999,
    },
  }
}

function captureAdapter(
  onCapture: (config: InternalAxiosRequestConfig) => void
) {
  return async (config: InternalAxiosRequestConfig) => {
    onCapture(config)
    return {
      data: { success: true, data: {} },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    } as AxiosResponse
  }
}

afterEach(() => {
  useAuthStore.getState().auth.reset('idle')
})

describe('image relay keeps its API key through the real auth interceptor chain', () => {
  test('preserves caller Authorization when preserveAuthorization is set', async () => {
    useAuthStore.getState().auth.setBundle(makeBundle('session-token'))

    let captured: unknown
    await api.post(
      '/v1/images/generations',
      { model: 'dall-e-3', prompt: 'a lake', n: 1 },
      {
        headers: { Authorization: 'Bearer sk-relay-key' },
        preserveAuthorization: true,
        skipAuthRefresh: true,
        skipErrorHandler: true,
        skipBusinessError: true,
        adapter: captureAdapter((config) => {
          captured = config.headers?.Authorization
        }),
      }
    )

    expect(captured).toBe('Bearer sk-relay-key')
  })

  test('keeps applying the session token for ordinary backend APIs', async () => {
    useAuthStore.getState().auth.setBundle(makeBundle('session-token'))

    let captured: unknown
    await api.get('/api/user/self', {
      skipErrorHandler: true,
      skipBusinessError: true,
      adapter: captureAdapter((config) => {
        captured = config.headers?.Authorization
      }),
    })

    expect(captured).toBe('Bearer session-token')
  })

  test('preserves multipart relay Authorization for edits', async () => {
    useAuthStore.getState().auth.setBundle(makeBundle('session-token'))
    const form = new FormData()
    form.append('model', 'gpt-image-1')
    form.append('prompt', 'edit this')

    let captured: unknown
    await api.post('/v1/images/edits', form, {
      headers: { Authorization: 'Bearer sk-edit-key' },
      preserveAuthorization: true,
      skipAuthRefresh: true,
      skipErrorHandler: true,
      skipBusinessError: true,
      adapter: captureAdapter((config) => {
        captured = config.headers?.Authorization
      }),
    })

    expect(captured).toBe('Bearer sk-edit-key')
  })
})
