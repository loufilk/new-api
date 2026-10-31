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
import i18next from 'i18next'
import { z } from 'zod'

import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import { validatePreparedOperation } from './lib/operation-validation'
import type {
  OperationDraft,
  OperationStatus,
  PreparedOperation,
  OperationPayload,
} from './types'

type Response<T> = { success: boolean; data: T; message?: string }
export const PLATFORM_ROOT = '/api/agent-platform'

export async function getPlatform<T>(
  path: string,
  params?: Record<string, string | number | undefined>,
  signal?: AbortSignal
): Promise<T> {
  const response = await api.get<Response<T>>(`${PLATFORM_ROOT}${path}`, {
    params,
    signal,
  })
  return requireServerSuccess(response.data).data
}

export async function postPlatform<T>(
  path: string,
  payload: unknown,
  proof?: string,
  method: 'post' | 'patch' | 'put' | 'delete' = 'post'
): Promise<T> {
  const response = await api.request<Response<T>>({
    method,
    url: `${PLATFORM_ROOT}${path}`,
    data: payload,
    ...(proof
      ? {
          headers: { 'X-Security-Proof': proof },
          skipAuthRefresh: true,
          singleUseAuthorization: true,
        }
      : {}),
  })
  return requireServerSuccess(response.data).data
}

export async function prepareOperation(
  draft: OperationDraft
): Promise<PreparedOperation> {
  const prepared = await postPlatform<unknown>('/operations/prepare', {
    action: draft.action,
    request_id: draft.request_id,
    payload: draft.payload,
  })
  return validatePreparedOperation(prepared, draft)
}

export async function submitOperation(
  draft: OperationDraft,
  proof: string
): Promise<unknown> {
  if (draft.action === 'commission.convert.legacy') {
    const response = await api.post<Response<unknown>>(
      '/api/user/aff_transfer',
      { quota: draft.payload.quota },
      {
        headers: {
          'Idempotency-Key': draft.request_id,
          'X-Security-Proof': proof,
        },
        skipAuthRefresh: true,
        singleUseAuthorization: true,
      }
    )
    return requireServerSuccess(response.data).data
  }
  if (
    draft.action.startsWith('redemption.') ||
    draft.action === 'invitation.policy.update'
  ) {
    const payload: OperationPayload = {
      ...draft.payload,
      request_id: draft.request_id,
    }
    let url = ''
    let method: 'post' | 'put' | 'delete' = 'post'
    let params: Record<string, string | number | boolean> | undefined
    if (draft.action === 'redemption.allocate') {
      url = '/api/agent-redemption/allocations'
    } else if (draft.action === 'invitation.policy.update') {
      url = '/api/agent-redemption/invitation-policy'
      method = 'put'
    } else if (/^redemption\.(?:admin\.)?create$/.test(draft.action)) {
      url = '/api/redemption/'
    } else if (/^redemption\.(?:admin\.)?update$/.test(draft.action)) {
      url = '/api/redemption/'
      method = 'put'
      params = payload.status_only === true ? { status_only: true } : undefined
      delete payload.status_only
    } else if (/^redemption\.(?:admin\.)?delete$/.test(draft.action)) {
      if (payload.invalid_only === true) {
        url = '/api/redemption/invalid'
        method = 'delete'
        params = {
          request_id: draft.request_id,
          agent_user_id: Number(payload.agent_user_id ?? 0),
        }
      } else if (
        Array.isArray(payload.ids) &&
        payload.ids.length === 1 &&
        draft.endpoint !== '/api/redemption/batch'
      ) {
        const id = payload.ids[0]
        if (typeof id !== 'number' || !Number.isSafeInteger(id) || id <= 0) {
          throw new Error(i18next.t('Invalid transaction target'))
        }
        url = `/api/redemption/${id}/`
        method = 'delete'
        params = { request_id: draft.request_id }
      } else {
        url = '/api/redemption/batch'
        delete payload.invalid_only
        delete payload.agent_user_id
      }
    }
    if (!url) throw new Error(i18next.t('Invalid transaction target'))
    const response = await api.request<Response<unknown>>({
      url,
      method,
      params,
      ...(method !== 'delete' ? { data: payload } : {}),
      headers: { 'X-Security-Proof': proof },
      skipAuthRefresh: true,
      singleUseAuthorization: true,
    })
    return requireServerSuccess(response.data).data
  }
  if (
    !draft.endpoint.startsWith('/') ||
    draft.endpoint.startsWith('//') ||
    draft.endpoint.includes('..')
  ) {
    throw new Error(i18next.t('Invalid transaction target'))
  }
  const body: OperationPayload = {
    ...draft.payload,
    request_id: draft.request_id,
  }
  if (draft.action === 'profile.update') delete body.user_id
  return postPlatform(draft.endpoint, body, proof, draft.method)
}

export async function recoverOperation(
  draft: Pick<OperationDraft, 'request_id'> &
    Partial<Pick<OperationDraft, 'action'>>
): Promise<OperationStatus> {
  const raw = await getPlatform<unknown>(
    `/operations/${encodeURIComponent(draft.request_id)}`
  )
  const result = z
    .object({
      request_id: z.literal(draft.request_id),
      action: z.string().default(''),
      status: z.enum(['not_observed', 'pending', 'completed', 'failed']),
      resource_type: z.string().default(''),
      resource_id: z.string().default(''),
      resource_state: z.string().default(''),
      resource: z.unknown().optional(),
    })
    .safeParse(raw)
  if (!result.success) {
    throw new Error(
      i18next.t('The transaction review is incomplete. Nothing was authorized.')
    )
  }
  if (
    result.data.status !== 'not_observed' &&
    draft.action &&
    result.data.action !== draft.action
  ) {
    throw new Error(
      i18next.t(
        'The transaction review does not match the requested operation. Nothing was authorized.'
      )
    )
  }
  return result.data
}
