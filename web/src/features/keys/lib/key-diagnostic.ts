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
import { sha256 } from '@noble/hashes/sha2.js'
import { bytesToHex } from '@noble/hashes/utils.js'
import { z } from 'zod'

import { api } from '@/lib/api'
import {
  getServerErrorSources,
  getServerErrorStatus,
  requireServerSuccess,
} from '@/lib/server-error-message'

export const diagnosticQuestionLabels = {
  'pelican-svg-v1': 'Cycling pelican (SVG animation)',
  'candy-logic-v1': 'Drawing candies (logic puzzle)',
  'pelican-svg-v2': 'Cycling pelican (SVG animation)',
  'candy-logic-v2': 'Drawing candies (logic puzzle)',
} as const

export const diagnosticStatusLabels = {
  queued: 'Queued',
  running: 'Running',
  completed: 'Completed',
  failed: 'Failed',
  intercepted: 'Intercepted',
  unknown: 'Result unknown',
} as const

export const diagnosticEndpoint = '/api/user/key-diagnostics'
const activeQuestion = z.enum(['pelican-svg-v2', 'candy-logic-v2'])
const question = z.enum([
  'pelican-svg-v1',
  'candy-logic-v1',
  ...activeQuestion.options,
])
const operationId = z
  .string()
  .regex(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/)
const safeInt = z.number().int().min(0).max(Number.MAX_SAFE_INTEGER)
const draft = z.object({
  id: operationId,
  token_id: safeInt.min(1),
  model: z.string().min(1).max(512),
  question,
})
const pendingDraft = draft.extend({
  state: z.literal('rejected-before-save').optional(),
})
const options = z.object({
  keys: z.array(
    z.object({
      id: safeInt.min(1),
      name: z.string(),
      group: z.string().optional(),
      smart_routing: z.boolean().optional(),
      routing_strategy: z
        .enum(['auto', 'success_rate', 'price', 'speed'])
        .optional(),
      auto_groups: z.array(z.string()).nullish(),
    })
  ),
  questions: z.array(
    z
      .object({
        id: activeQuestion,
        prompt: z.string(),
        max_output_tokens: safeInt,
      })
      .refine(
        (item) =>
          item.max_output_tokens ===
          (item.id === 'pelican-svg-v2' ? 32000 : 8192)
      )
  ),
})
const run = z.object({
  id: operationId,
  token_id: safeInt.optional(),
  model: z.string().optional(),
  question: question.optional(),
  status: z.enum([
    'queued',
    'running',
    'completed',
    'failed',
    'intercepted',
    'unknown',
  ]),
  error_code: z.string().optional(),
  request_id: z.string().optional(),
  answer: z.string().optional(),
  created_at: safeInt.optional(),
  completed_at: safeInt.optional(),
  elapsed_ms: safeInt.optional(),
  http_status: safeInt.optional(),
  candy_result: z.enum(['correct', 'incorrect', 'unrecognized']).optional(),
  billing: z
    .object({
      scope: z.literal('generation'),
      status: z.enum(['unconfirmed', 'settled', 'refunded']),
      quota: safeInt.optional(),
    })
    .optional(),
  usage: z
    .object({
      prompt_tokens: safeInt.optional(),
      completion_tokens: safeInt.optional(),
    })
    .optional(),
})

export type DiagnosticDraft = z.infer<typeof draft>
export type PendingDiagnosticDraft = z.infer<typeof pendingDraft>
export type DiagnosticOptions = z.infer<typeof options>
export type DiagnosticRun = z.infer<typeof run>
export type DiagnosticQuestion = z.infer<typeof question>
export type ActiveDiagnosticQuestion = z.infer<typeof activeQuestion>

export function isActiveDiagnosticQuestion(
  value: string
): value is ActiveDiagnosticQuestion {
  return activeQuestion.safeParse(value).success
}

export function isPelicanDiagnosticQuestion(value?: DiagnosticQuestion) {
  return value === 'pelican-svg-v1' || value === 'pelican-svg-v2'
}

export function isCandyDiagnosticQuestion(value?: DiagnosticQuestion) {
  return value === 'candy-logic-v1' || value === 'candy-logic-v2'
}

type Response<T> = { success: boolean; data: T }

async function readDiagnostic<T>(
  path: string,
  schema: z.ZodType<T>,
  signal?: AbortSignal
) {
  const res = await api.get<Response<unknown>>(diagnosticEndpoint + path, {
    signal,
  })
  return schema.parse(requireServerSuccess(res.data).data)
}

export function readDiagnosticOptions(signal: AbortSignal) {
  return readDiagnostic('/options', options, signal)
}

export function readDiagnosticModels(id: number, signal: AbortSignal) {
  return readDiagnostic(`/keys/${id}/models`, z.array(z.string()), signal)
}

export function readDiagnosticHistory(signal: AbortSignal) {
  return readDiagnostic('/runs', z.array(run), signal)
}

export async function readDiagnosticRun(id: string, signal: AbortSignal) {
  const parsed = await readDiagnostic(
    `/runs/${operationId.parse(id)}`,
    run,
    signal
  )
  if (parsed.id !== id) throw new Error('Diagnostic response mismatch')
  return parsed
}

export async function submitDiagnostic(
  attempt: DiagnosticDraft,
  authSessionEpoch: number
) {
  const res = await api.post<Response<unknown>>(
    `${diagnosticEndpoint}/runs`,
    {
      token_id: attempt.token_id,
      model: attempt.model,
      question: attempt.question,
      confirmed: true,
      client_request_id: attempt.id,
    },
    { singleUseAuthorization: true, authSessionEpoch, timeout: 15_000 }
  )
  const parsed = run.parse(requireServerSuccess(res.data).data)
  if (!matchesDiagnosticRun(parsed, attempt)) {
    throw new Error('Diagnostic response mismatch')
  }
  return parsed
}

export function matchesDiagnosticRun(
  run: Pick<DiagnosticRun, 'id' | 'token_id' | 'model' | 'question'>,
  attempt: DiagnosticDraft
): boolean {
  return (
    run.id === attempt.id &&
    run.token_id === attempt.token_id &&
    run.model === attempt.model &&
    run.question === attempt.question
  )
}

export function isDiagnosticMissing(error: unknown): boolean {
  return (
    getServerErrorStatus(error) === 404 &&
    getServerErrorSources(error).some(
      (source) => source.code === 'run_not_found'
    )
  )
}

export function isDiagnosticConflict(error: unknown): boolean {
  return (
    getServerErrorStatus(error) === 409 &&
    getServerErrorSources(error).some(
      (source) => source.code === 'request_id_conflict'
    )
  )
}

export function isDiagnosticRejected(error: unknown): boolean {
  const status = getServerErrorStatus(error)
  // Match only exact HTTP contracts for failures before saving the request.
  return getServerErrorSources(error).some(
    ({ code }) =>
      (status === 400 &&
        (code === 'invalid_selection' ||
          code === 'confirmation_required' ||
          code === 'question_retired')) ||
      (status === 403 &&
        (code === 'token_unavailable' || code === 'model_or_group_unavailable'))
  )
}

function diagnosticDraftKey(owner: number, sessionId: string | undefined) {
  if (!sessionId) return null
  // A login ID is not a credential. Hash it anyway; never persist an auth
  // token or assign an old login's unsaved consent to a new login.
  const scope = bytesToHex(sha256(new TextEncoder().encode(sessionId)))
  return `key-diagnostic:pending:v3:${owner}:${scope}`
}

export function loadDiagnosticDraft(
  owner: number,
  sessionId: string | undefined
): PendingDiagnosticDraft | null {
  const key = diagnosticDraftKey(owner, sessionId)
  if (!key) return null
  try {
    const found = localStorage.getItem(key)
    if (!found) return null
    const parsed = pendingDraft.safeParse(JSON.parse(found))
    return parsed.success ? parsed.data : null
  } catch {
    return null
  }
}

// Do not send a paid request if its recovery identity cannot be written first.
export function saveDiagnosticDraft(
  owner: number,
  sessionId: string | undefined,
  attempt: DiagnosticDraft
): boolean {
  const key = diagnosticDraftKey(owner, sessionId)
  if (!key) return false
  try {
    localStorage.setItem(key, JSON.stringify(attempt))
    const stored = loadDiagnosticDraft(owner, sessionId)
    return (
      !!stored &&
      stored.id === attempt.id &&
      stored.token_id === attempt.token_id &&
      stored.model === attempt.model &&
      stored.question === attempt.question
    )
  } catch {
    return false
  }
}

// Persist only a definitive first-attempt pre-save rejection. A retry error
// cannot prove an earlier attempt with a lost response was never accepted.
export function markDiagnosticDraftRejected(
  owner: number,
  sessionId: string | undefined,
  attempt: DiagnosticDraft
) {
  const key = diagnosticDraftKey(owner, sessionId)
  if (!key) return
  try {
    const stored = loadDiagnosticDraft(owner, sessionId)
    if (stored && matchesDiagnosticRun(stored, attempt)) {
      localStorage.setItem(
        key,
        JSON.stringify({ ...stored, state: 'rejected-before-save' })
      )
    }
  } catch {
    // A subsequent GET can still reconcile the original request ID.
  }
}

export function clearDiagnosticDraft(
  owner: number,
  sessionId: string | undefined,
  id: string
) {
  const key = diagnosticDraftKey(owner, sessionId)
  if (!key) return
  try {
    if (loadDiagnosticDraft(owner, sessionId)?.id === id) {
      localStorage.removeItem(key)
    }
  } catch {
    // Recovery remains safe: later reads still use the existing operation ID.
  }
}

export function diagnosticInProgress(status: DiagnosticRun['status']): boolean {
  return status === 'queued' || status === 'running'
}
