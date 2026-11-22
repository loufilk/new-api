import { api } from '@/lib/api'
import { createServerError } from '@/lib/server-error-message'

import type {
  PartnerApplication,
  PartnerApplicationInput,
  PartnerApplicationPage,
  PartnerApplicationSettings,
  PartnerApplicationStatus,
} from './types'

export const PARTNER_PAGE_SIZE = 20
export const partnerKey = ['partner-applications'] as const

export const privateRead = {
  gcTime: 0,
  staleTime: 0,
  retry: false,
  meta: { redirectOnInternalServerError: false },
} as const

interface Envelope<T> {
  success: boolean
  message?: string
  data?: T
}

function result<T>(envelope: Envelope<T>): T {
  if (!envelope?.success || envelope.data == null) {
    throw createServerError(envelope)
  }
  return envelope.data
}

function queryPath(admin: boolean) {
  return admin ? '/api/admin/partner-applications' : '/api/partner-applications'
}

export async function getPartnerSettings(signal: AbortSignal) {
  const response = await api.get<Envelope<PartnerApplicationSettings>>(
    `${queryPath(false)}/settings`,
    { signal, disableDuplicate: true }
  )
  return result(response.data)
}

export async function listPartnerApplications(params: {
  admin: boolean
  page: number
  status: PartnerApplicationStatus | ''
  signal: AbortSignal
}) {
  const response = await api.get<Envelope<PartnerApplicationPage>>(
    queryPath(params.admin),
    {
      params: { p: params.page, size: PARTNER_PAGE_SIZE, status: params.status },
      signal: params.signal,
      disableDuplicate: true,
    }
  )
  return result(response.data)
}

export async function getPartnerApplication(
  admin: boolean,
  id: number,
  signal: AbortSignal
) {
  const response = await api.get<Envelope<PartnerApplication>>(
    `${queryPath(admin)}/${id}`,
    { signal, disableDuplicate: true }
  )
  const application = result(response.data)
  if (application.id !== id) throw createServerError(null)
  return application
}

export async function submitPartnerApplication(
  input: PartnerApplicationInput,
  idempotencyKey: string
) {
  const response = await api.post<Envelope<PartnerApplication>>(
    queryPath(false),
    input,
    { headers: { 'Idempotency-Key': idempotencyKey } }
  )
  return result(response.data)
}

export async function cancelPartnerApplication(
  id: number,
  expectedRevision: number
) {
  const response = await api.post<Envelope<PartnerApplication>>(
    `${queryPath(false)}/${id}/cancel`,
    { action: 'cancel', expected_revision: expectedRevision }
  )
  return result(response.data)
}

export async function getAdminPartnerSettings(signal: AbortSignal) {
  const response = await api.get<Envelope<PartnerApplicationSettings>>(
    `${queryPath(true)}/settings`,
    { signal, disableDuplicate: true }
  )
  return result(response.data)
}

export async function saveAdminPartnerSettings(
  settings: Pick<PartnerApplicationSettings, 'revision' | 'partner_enabled' | 'partner_instructions'>
) {
  const response = await api.put<Envelope<PartnerApplicationSettings>>(
    `${queryPath(true)}/settings`,
    settings
  )
  return result(response.data)
}

export async function reviewPartnerApplication(input: {
  id: number
  expectedRevision: number
  action: 'review' | 'approve' | 'reject'
  note: string
}) {
  const response = await api.post<Envelope<PartnerApplication>>(
    `${queryPath(true)}/${input.id}/review`,
    {
      action: input.action,
      expected_revision: input.expectedRevision,
      note: input.note,
    }
  )
  return result(response.data)
}
