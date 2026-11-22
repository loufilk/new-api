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
import { API_BASE_URL } from '@/lib/api-url'

import type { ApiKey, CCSwitchApp } from '../../types'

function getServerAddress(): string {
  if (API_BASE_URL) return API_BASE_URL
  try {
    const raw = localStorage.getItem('status')
    if (raw) {
      const status = JSON.parse(raw)
      if (status.server_address) return status.server_address
    }
  } catch {
    /* empty */
  }
  return window.location.origin
}

function buildCCSwitchURL(
  app: CCSwitchApp,
  name: string,
  models: Record<string, string>,
  apiKey: string,
  serverAddress = getServerAddress()
): string {
  const endpoint = app === 'codex' ? `${serverAddress}/v1` : serverAddress
  const params = new URLSearchParams()
  params.set('resource', 'provider')
  params.set('app', app)
  params.set('name', name)
  params.set('endpoint', endpoint)
  params.set('apiKey', apiKey)
  for (const [key, value] of Object.entries(models)) {
    if (value) params.set(key, value)
  }
  params.set('homepage', serverAddress)
  params.set('enabled', 'true')
  return `ccswitch://v1/import?${params.toString()}`
}

export function ccSwitchModelsQueryKey(token: ApiKey | null, app: CCSwitchApp) {
  return [
    'ccswitch-models',
    token?.id ?? 0,
    token?.group ?? '',
    (token?.auto_groups ?? []).join('\u001f'),
    token?.model_limits_enabled ?? false,
    token?.model_limits ?? '',
    app,
  ] as const
}

export function buildValidatedCCSwitchURL(
  app: CCSwitchApp,
  name: string,
  models: Record<string, string>,
  apiKey: string,
  availableModels: string[],
  serverAddress?: string
): string | null {
  if (!models.model || availableModels.length === 0) return null
  const available = new Set(availableModels)
  for (const modelName of Object.values(models)) {
    if (modelName && !available.has(modelName)) return null
  }
  const key = apiKey.startsWith('sk-') ? apiKey : `sk-${apiKey}`
  return buildCCSwitchURL(app, name, models, key, serverAddress)
}
