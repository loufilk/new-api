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
import axios, {
  type AxiosRequestConfig,
  type InternalAxiosRequestConfig,
} from 'axios'
import { t } from 'i18next'

import i18n from '@/i18n/config'
import { toBackendLanguage } from '@/i18n/languages'
import { API_BASE_URL } from '@/lib/api-url'
import {
  applyAuthRotation,
  clearAuthentication,
  getAuthSessionEpoch,
  getFreshAuthHeaders,
  refreshAuthentication,
} from '@/lib/auth-session'
import { handleServerError } from '@/lib/handle-server-error'
import {
  getServerErrorMessage,
  safeServerErrorMessage,
} from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

declare module 'axios' {
  export interface AxiosRequestConfig {
    skipBusinessError?: boolean
    skipErrorHandler?: boolean
    disableDuplicate?: boolean
    skipAuthRefresh?: boolean
    authRetry?: boolean
    acceptAuthRotation?: boolean
    preserveAuthorization?: boolean
    singleUseAuthorization?: boolean
    authSessionEpoch?: number
  }
}

export type ApiRequestConfig = AxiosRequestConfig

export const api = axios.create({
  baseURL: API_BASE_URL,
  withCredentials: true,
  headers: {
    // no-store forbids storage; no-cache also revalidates any older cached response.
    'Cache-Control': 'no-cache, no-store',
  },
})

const inFlightGet = new Map<string, Promise<unknown>>()
const originalGet = api.get.bind(api)

api.get = ((url: string, config: ApiRequestConfig = {}) => {
  if (config.disableDuplicate) return originalGet(url, config)

  const params = config.params ? JSON.stringify(config.params) : '{}'
  const sessionSID = useAuthStore.getState().auth.session?.sid || 'anonymous'
  const key = `${getAuthSessionEpoch()}:${sessionSID}:${url}?${params}`
  const existingRequest = inFlightGet.get(key)
  if (existingRequest) return existingRequest

  const request = originalGet(url, config).finally(() => {
    inFlightGet.delete(key)
  })
  inFlightGet.set(key, request)
  return request
}) as typeof api.get

function redirectToSignIn(): void {
  if (
    typeof window !== 'undefined' &&
    window.location.pathname !== '/sign-in'
  ) {
    window.location.replace('/sign-in')
  }
}

function assertCurrentRequestSession(
  config: InternalAxiosRequestConfig | undefined,
  allowRefreshSignOut = false
): void {
  if (!config) return
  config.cancelToken?.throwIfRequested()
  if (config.signal?.aborted) {
    throw new axios.CanceledError('Request canceled', config)
  }
  const startedEpoch = config.authSessionEpoch
  const currentEpoch = getAuthSessionEpoch()
  if (startedEpoch === undefined || startedEpoch === currentEpoch) return
  const auth = useAuthStore.getState().auth
  // A terminal refresh may itself clear this request's session. Preserve the
  // existing expiry notification/redirect only for that one transition, never
  // for a newer signed-in owner (or a logout/login/logout sequence).
  if (
    allowRefreshSignOut &&
    currentEpoch === startedEpoch + 1 &&
    !auth.user &&
    !auth.session
  ) {
    return
  }
  throw new axios.CanceledError('Authentication session changed', config)
}

api.interceptors.response.use(
  (response) => {
    assertCurrentRequestSession(response.config)
    if (response.config.acceptAuthRotation && response.data?.success === true) {
      applyAuthRotation(response.data.data)
    }

    return response
  },
  async (error) => {
    const config = error?.config as InternalAxiosRequestConfig | undefined
    const skipErrorHandler = config?.skipErrorHandler
    const status = error?.response?.status
    assertCurrentRequestSession(config)

    if (status === 401) {
      if (
        config &&
        !config.skipAuthRefresh &&
        !config.preserveAuthorization &&
        !config.authRetry
      ) {
        config.authRetry = true
        const outcome = await refreshAuthentication()
        assertCurrentRequestSession(
          config,
          outcome.kind === 'anonymous' || outcome.kind === 'out_of_sync'
        )
        if (outcome.kind === 'authenticated') {
          const token = useAuthStore.getState().auth.accessToken
          if (token && !config.preserveAuthorization) {
            config.headers.set('Authorization', `Bearer ${token}`)
          }
          return api.request(config)
        }

        if (outcome.kind === 'anonymous' || outcome.kind === 'out_of_sync') {
          if (!skipErrorHandler) {
            handleServerError({
              message: t('Session expired!'),
              [safeServerErrorMessage]: true,
              cause: error,
            })
          }
          redirectToSignIn()
        }
      } else if (config?.authRetry) {
        clearAuthentication(false)
        if (!skipErrorHandler) {
          handleServerError({
            message: t('Session expired!'),
            [safeServerErrorMessage]: true,
            cause: error,
          })
        }
        redirectToSignIn()
      } else if (!skipErrorHandler) {
        handleServerError({
          message: t('Session expired!'),
          [safeServerErrorMessage]: true,
          cause: error,
        })
      }
    }
    if (axios.isAxiosError(error)) error.message = getServerErrorMessage(error)
    throw error
  }
)

api.interceptors.request.use(
  async (config) => {
    assertCurrentRequestSession(config)
    config.headers.set(
      'Accept-Language',
      toBackendLanguage(i18n.resolvedLanguage || i18n.language)
    )
    if (config.preserveAuthorization) return config
    if (
      config.singleUseAuthorization ||
      config.headers.has('X-Security-Proof')
    ) {
      // Refresh before spending a proof/flow, never by replaying its request.
      config.skipAuthRefresh = true
      try {
        const headers = await getFreshAuthHeaders()
        assertCurrentRequestSession(config)
        for (const [name, value] of Object.entries(headers)) {
          config.headers.set(name, value)
        }
      } catch (error) {
        assertCurrentRequestSession(config)
        throw axios.AxiosError.from(error, undefined, config)
      }
      return config
    }
    const accessToken = useAuthStore.getState().auth.accessToken
    if (accessToken) {
      config.headers.Authorization = `Bearer ${accessToken}`
    }
    return config
  },
  undefined,
  {
    runWhen: (config) => {
      // Axios evaluates runWhen synchronously at invocation time, before the
      // asynchronous interceptor chain. Capture here so even a session switch
      // before the first microtask cannot reattribute the caller's operation.
      config.authSessionEpoch ??= getAuthSessionEpoch()
      return true
    },
  }
)
