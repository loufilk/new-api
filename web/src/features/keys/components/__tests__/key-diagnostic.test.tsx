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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import {
  type DiagnosticRun,
  loadDiagnosticDraft,
} from '../../lib/key-diagnostic'
import { KeyDiagnosticButton } from '../key-diagnostic-dialog'
import { KeyDiagnosticResult } from '../key-diagnostic-result'

const endpoint = '/api/user/key-diagnostics'
const firstSession = 'synthetic-login-one'
const secondSession = 'synthetic-login-two'
const testID = '01234567-89ab-4cde-89ab-0123456789ab'
const eligibleKeys = [
  { id: 9, name: 'Personal', group: 'group-a' },
  {
    id: 10,
    name: 'Flexible',
    group: 'auto',
    smart_routing: true,
    routing_strategy: 'price',
    auto_groups: [],
  },
]
const options = {
  keys: eligibleKeys,
  questions: [
    {
      id: 'pelican-svg-v1',
      prompt: 'Draw a cycling pelican in SVG.',
      max_output_tokens: 32000,
    },
    {
      id: 'candy-logic-v1',
      prompt: 'Draw the candies.',
      max_output_tokens: 8192,
    },
  ],
}
type Submit = {
  token_id: number
  model: string
  question: DiagnosticRun['question']
  confirmed: boolean
  client_request_id: string
}

function ok(data: unknown) {
  return { data: { success: true, data } }
}

function response(body: Submit): DiagnosticRun {
  return {
    id: body.client_request_id,
    token_id: body.token_id,
    model: body.model,
    question: body.question,
    status: 'queued',
    billing: { scope: 'generation', status: 'unconfirmed' },
  }
}

function signIn(sid: string) {
  useAuthStore.getState().auth.setBundle({
    user: { id: 51, username: 'fixture', role: 1 },
    access_token: 'synthetic-test-token',
    token_type: 'Bearer',
    access_expires_at: 1000,
    session: {
      sid,
      current: true,
      login_method: 'fixture',
      ip: '',
      user_agent: '',
      created_at: 1,
      last_active_at: 1,
      expires_at: 1000,
    },
  })
}

beforeEach(() => {
  signIn(firstSession)
})
afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.reset()
  localStorage.clear()
})

function setup(tokenId?: number) {
  const runs = new Map<string, DiagnosticRun>()
  const get = vi.spyOn(api, 'get').mockImplementation(async (url: string) => {
    if (url === `${endpoint}/options`) return ok(options)
    if (url.endsWith('/keys/9/models')) return ok(['chat-model', 'another'])
    if (url.endsWith('/keys/10/models')) return ok(['group-b-model'])
    if (url === `${endpoint}/runs`) return ok([...runs.values()])
    const found = runs.get(url.split('/').at(-1) ?? '')
    if (!found) {
      throw { response: { status: 404, data: { code: 'run_not_found' } } }
    }
    return ok(found)
  })
  const post = vi.spyOn(api, 'post').mockImplementation(async (_, input) => {
    const body = input as Submit
    const sid = useAuthStore.getState().auth.session?.sid
    expect(loadDiagnosticDraft(51, sid)?.id).toBe(body.client_request_id)
    const queued = response(body)
    runs.set(queued.id, queued)
    return ok(queued)
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const component = (
    <QueryClientProvider client={client}>
      <KeyDiagnosticButton tokenId={tokenId} />
    </QueryClientProvider>
  )
  const rendered = render(component)
  return { get, post, user: userEvent.setup(), runs, rendered }
}

async function selectModel(
  user: ReturnType<typeof userEvent.setup>,
  model: string
) {
  const input = screen.getByRole('combobox', { name: 'Model' })
  await waitFor(() => expect(input).not.toBeDisabled())
  await user.click(input)
  await user.type(input, model)
  await user.click(await screen.findByRole('option', { name: model }))
}

test.each([9, undefined])(
  'keeps the violet reference brain and accessible diagnostic name (Key %s)',
  (tokenId) => {
    setup(tokenId)
    const button = screen.getByRole('button', { name: 'Key diagnostic' })
    const glyph = button.querySelector('svg.lucide-brain')
    expect(glyph).toHaveClass('text-violet-600', 'dark:text-violet-400')
    expect(glyph).toHaveAttribute('aria-hidden', 'true')
  }
)

test('row entry preselects the Key, presents flat fixed questions, and persists only one approved submission', async () => {
  const { user, get, post } = setup(9)
  await user.click(screen.getByRole('button', { name: 'Key diagnostic' }))
  expect(await screen.findByDisplayValue('Personal · group-a')).toBeDisabled()
  expect(screen.getByText(/Output limit: 32,000/)).toBeVisible()
  await user.click(
    screen.getByRole('radio', { name: 'Drawing candies (logic puzzle)' })
  )
  expect(screen.getByText('Draw the candies.')).toBeVisible()
  expect(screen.getByText(/Output limit: 8,192/)).toBeVisible()
  await selectModel(user, 'chat-model')
  expect(screen.getByRole('button', { name: 'Start test' })).toBeDisabled()
  await user.click(
    screen.getByRole('checkbox', {
      name: 'I agree to one paid test with this API key.',
    })
  )
  await user.dblClick(screen.getByRole('button', { name: 'Start test' }))
  expect(await screen.findByText(/running in the background/)).toBeVisible()
  expect(post).toHaveBeenCalledExactlyOnceWith(
    `${endpoint}/runs`,
    {
      token_id: 9,
      question: 'candy-logic-v1',
      model: 'chat-model',
      confirmed: true,
      client_request_id: expect.any(String),
    },
    {
      singleUseAuthorization: true,
      authSessionEpoch: expect.any(Number),
      timeout: 15_000,
    }
  )
  expect(get.mock.calls.every(([url]) => !url.endsWith('/key'))).toBe(true)
})

test('list entry uses per-Key models, resets consent and selection when changing Key, and dismissal is free', async () => {
  const { user, get, post } = setup()
  await user.click(screen.getByRole('button', { name: 'Key diagnostic' }))
  await screen.findByRole('option', { name: 'Personal · group-a' })
  expect(get.mock.calls.some(([url]) => url.endsWith('/models'))).toBe(false)
  await user.selectOptions(screen.getByLabelText('API Key'), '9')
  await selectModel(user, 'chat-model')
  await user.click(
    screen.getByRole('checkbox', {
      name: 'I agree to one paid test with this API key.',
    })
  )
  await user.selectOptions(screen.getByLabelText('API Key'), '10')
  expect(screen.getByText(/Smart Routing: Price first/)).toBeVisible()
  expect(screen.getByRole('combobox', { name: 'Model' })).toHaveValue('')
  expect(screen.getByRole('button', { name: 'Start test' })).toBeDisabled()
  expect(
    screen.getByRole('checkbox', {
      name: 'I agree to one paid test with this API key.',
    })
  ).not.toBeChecked()
  await user.keyboard('{Escape}')
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(post).not.toHaveBeenCalled()
})

test('loading again under the same login only retries the same ID on explicit action', async () => {
  const { user, post, rendered } = setup(9)
  post.mockRejectedValueOnce(new Error('timeout'))
  await user.click(screen.getByRole('button', { name: 'Key diagnostic' }))
  await screen.findByDisplayValue('Personal · group-a')
  await selectModel(user, 'chat-model')
  await user.click(
    screen.getByRole('checkbox', {
      name: 'I agree to one paid test with this API key.',
    })
  )
  await user.click(screen.getByRole('button', { name: 'Start test' }))
  await screen.findByRole('button', { name: 'Retry same request' })
  expect(post).toHaveBeenCalledTimes(1)
  expect(loadDiagnosticDraft(51, firstSession)).toMatchObject({
    token_id: 9,
    model: 'chat-model',
    question: 'pelican-svg-v1',
  })
  expect(loadDiagnosticDraft(52, firstSession)).toBeNull()
  expect(loadDiagnosticDraft(51, secondSession)).toBeNull()
  expect(Object.keys(localStorage).join()).not.toContain(firstSession)

  // Simulate reload: new component/query client, restored login with same SID.
  const firstBody = post.mock.calls[0]?.[1]
  rendered.unmount()
  signIn(firstSession)
  const fresh = setup(9)
  await fresh.user.click(screen.getByRole('button', { name: 'Key diagnostic' }))
  const retry = await screen.findByRole('button', {
    name: 'Retry same request',
  })
  expect(fresh.post).toHaveBeenCalledTimes(1)
  await fresh.user.click(retry)
  expect(await screen.findByText(/running in the background/)).toBeVisible()
  expect(fresh.post).toHaveBeenCalledTimes(2)
  expect(fresh.post.mock.calls[1]?.[1]).toEqual(firstBody)
  expect(loadDiagnosticDraft(51, firstSession)).toBeNull()
})

test('logout and a different login for the same account cannot replay an old unsaved consent', async () => {
  const { user, post, runs } = setup(9)
  post.mockRejectedValueOnce(new Error('timeout'))
  await user.click(screen.getByRole('button', { name: 'Key diagnostic' }))
  await screen.findByDisplayValue('Personal · group-a')
  await selectModel(user, 'chat-model')
  await user.click(screen.getByRole('checkbox'))
  await user.click(screen.getByRole('button', { name: 'Start test' }))
  await screen.findByRole('button', { name: 'Retry same request' })
  const prior = loadDiagnosticDraft(51, firstSession)
  expect(prior).not.toBeNull()
  await user.keyboard('{Escape}')
  act(() => {
    useAuthStore.getState().auth.reset()
    signIn(secondSession)
  })
  // A new login may still open work the server accepted for the same owner.
  runs.set(testID, {
    id: testID,
    model: 'history-model',
    status: 'completed',
  })
  await user.click(screen.getByRole('button', { name: 'Key diagnostic' }))
  await screen.findByDisplayValue('Personal · group-a')
  expect(loadDiagnosticDraft(51, secondSession)).toBeNull()
  expect(
    screen.queryByRole('button', { name: 'Retry same request' })
  ).toBeNull()
  await user.click(screen.getByText('Recent tests'))
  await user.click(await screen.findByRole('button', { name: /history-model/ }))
  await user.click(screen.getByRole('button', { name: 'New test' }))
  await selectModel(user, 'chat-model')
  expect(screen.getByRole('checkbox')).not.toBeChecked()
  expect(screen.getByRole('button', { name: 'Start test' })).toBeDisabled()
  expect(post).toHaveBeenCalledTimes(1)
  await user.click(screen.getByRole('checkbox'))
  await user.click(screen.getByRole('button', { name: 'Start test' }))
  expect(await screen.findByText(/running in the background/)).toBeVisible()
  expect(post).toHaveBeenCalledTimes(2)
  expect(
    (post.mock.calls[1]?.[1] as Submit | undefined)?.client_request_id
  ).not.toBe(prior?.id)
})

test('definitive stale model rejection releases the unsaved draft and requires a fresh choice and consent', async () => {
  const { user, get, post } = setup(9)
  post.mockRejectedValueOnce({
    response: { status: 403, data: { code: 'model_or_group_unavailable' } },
  })
  await user.click(screen.getByRole('button', { name: 'Key diagnostic' }))
  await screen.findByDisplayValue('Personal · group-a')
  await selectModel(user, 'chat-model')
  await user.click(screen.getByRole('checkbox'))
  await user.click(screen.getByRole('button', { name: 'Start test' }))
  expect(
    await screen.findByText(/rejected this test before saving/)
  ).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Retry same request' })
  ).toBeNull()

  const originalBody = post.mock.calls[0]?.[1] as Submit
  let finishModels: ((value: ReturnType<typeof ok>) => void) | undefined
  const originalGet = get.getMockImplementation()
  if (!originalGet) throw new Error('Missing synthetic GET fixture')
  get.mockImplementation(async (url: string, config) => {
    if (url.endsWith('/keys/9/models')) {
      return new Promise((resolve) => {
        finishModels = resolve
      })
    }
    return originalGet(url, config)
  })
  await user.click(screen.getByRole('button', { name: 'New test' }))
  expect(loadDiagnosticDraft(51, firstSession)).toBeNull()
  expect(screen.getByRole('combobox', { name: 'Model' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Start test' })).toBeDisabled()
  await waitFor(() => expect(finishModels).toBeDefined())
  finishModels?.(ok(['another']))
  await selectModel(user, 'another')
  expect(screen.getByRole('checkbox')).not.toBeChecked()
  expect(screen.getByRole('button', { name: 'Start test' })).toBeDisabled()
  expect(post).toHaveBeenCalledTimes(1)
  await user.click(screen.getByRole('checkbox'))
  await user.click(screen.getByRole('button', { name: 'Start test' }))
  expect(await screen.findByText(/running in the background/)).toBeVisible()
  expect(post).toHaveBeenCalledTimes(2)
  expect(post.mock.calls[1]?.[1]).toMatchObject({ model: 'another' })
  expect(
    (post.mock.calls[1]?.[1] as Submit | undefined)?.client_request_id
  ).not.toBe(originalBody.client_request_id)
})

test('reopening a definitively rejected first attempt checks the old ID without offering retry', async () => {
  const { user, get, post, rendered } = setup(9)
  post.mockRejectedValueOnce({
    response: { status: 403, data: { code: 'model_or_group_unavailable' } },
  })
  await user.click(screen.getByRole('button', { name: 'Key diagnostic' }))
  await screen.findByDisplayValue('Personal · group-a')
  await selectModel(user, 'chat-model')
  await user.click(screen.getByRole('checkbox'))
  await user.click(screen.getByRole('button', { name: 'Start test' }))
  await waitFor(() =>
    expect(loadDiagnosticDraft(51, firstSession)?.state).toBe(
      'rejected-before-save'
    )
  )
  const rejectedID = loadDiagnosticDraft(51, firstSession)?.id
  rendered.unmount()
  signIn(firstSession)
  const fresh = setup(9)
  await fresh.user.click(screen.getByRole('button', { name: 'Key diagnostic' }))
  expect(
    await screen.findByText(/rejected this test before saving/)
  ).toBeVisible()
  expect(
    get.mock.calls.some(([url]) => url === `${endpoint}/runs/${rejectedID}`)
  ).toBe(true)
  expect(
    screen.queryByRole('button', { name: 'Retry same request' })
  ).toBeNull()
  await fresh.user.click(screen.getByRole('button', { name: 'New test' }))
  expect(loadDiagnosticDraft(51, firstSession)).toBeNull()
  expect(screen.getByRole('button', { name: 'Start test' })).toBeDisabled()
  expect(post).toHaveBeenCalledTimes(1)
})

test('a stale-model retry cannot discard an earlier ambiguous request', async () => {
  const { user, post } = setup(9)
  post.mockRejectedValueOnce(new Error('timeout'))
  post.mockRejectedValueOnce({
    response: { status: 403, data: { code: 'model_or_group_unavailable' } },
  })
  await user.click(screen.getByRole('button', { name: 'Key diagnostic' }))
  await screen.findByDisplayValue('Personal · group-a')
  await selectModel(user, 'chat-model')
  await user.click(screen.getByRole('checkbox'))
  await user.click(screen.getByRole('button', { name: 'Start test' }))
  await user.click(
    await screen.findByRole('button', { name: 'Retry same request' })
  )
  expect(
    await screen.findByRole('button', { name: 'Retry same request' })
  ).toBeVisible()
  expect(screen.queryByRole('button', { name: 'New test' })).toBeNull()
  expect(screen.queryByText(/rejected this test before saving/)).toBeNull()
  const held = loadDiagnosticDraft(51, firstSession)
  expect(held?.state).toBeUndefined()
  expect(held?.id).toBe(
    (post.mock.calls[0]?.[1] as Submit | undefined)?.client_request_id
  )
  expect(post).toHaveBeenCalledTimes(2)
  expect(post.mock.calls[1]?.[1]).toEqual(post.mock.calls[0]?.[1])
})

test('owner history opens a confirmed result without another paid request', async () => {
  const { user, post, runs } = setup()
  runs.set(testID, {
    id: testID,
    request_id: 'confirmed-gateway-id',
    model: 'saved-model',
    question: 'candy-logic-v1',
    status: 'completed',
    answer: 'Final answer: 10',
    candy_result: 'incorrect',
    elapsed_ms: 1234,
    billing: { scope: 'generation', status: 'settled', quota: 250000 },
  })
  await user.click(screen.getByRole('button', { name: 'Key diagnostic' }))
  await user.click(screen.getByText('Recent tests'))
  await user.click(await screen.findByRole('button', { name: /saved-model/ }))
  expect(await screen.findByText('Incorrect answer')).toBeVisible()
  expect(screen.getByText('confirmed-gateway-id')).toBeVisible()
  expect(screen.getByText(/1,234 ms/)).toBeVisible()
  expect(screen.getByText('Final answer: 10')).toBeVisible()
  expect(post).not.toHaveBeenCalled()
})

test('does not render an unconfirmed charge or classify a failed request as an incorrect answer', () => {
  render(
    <KeyDiagnosticResult
      run={{
        id: testID,
        status: 'failed',
        question: 'candy-logic-v1',
        candy_result: 'incorrect',
        billing: { scope: 'generation', status: 'unconfirmed', quota: 25 },
      }}
    />
  )
  expect(screen.getByText('Settlement not confirmed')).toBeVisible()
  expect(screen.getByText('Not confirmed')).toBeVisible()
  expect(screen.queryByText('Incorrect answer')).not.toBeInTheDocument()
  expect(screen.queryByText('25')).not.toBeInTheDocument()
})

test('a response lost after acceptance is recovered by GET without a new submission', async () => {
  const { user, post, runs } = setup(9)
  post.mockImplementationOnce(async (_, input) => {
    const saved = response(input as Submit)
    runs.set(saved.id, saved)
    throw new Error('connection closed after commit')
  })
  await user.click(screen.getByRole('button', { name: 'Key diagnostic' }))
  await screen.findByDisplayValue('Personal · group-a')
  await selectModel(user, 'chat-model')
  await user.click(
    screen.getByRole('checkbox', {
      name: 'I agree to one paid test with this API key.',
    })
  )
  await user.click(screen.getByRole('button', { name: 'Start test' }))
  expect(await screen.findByText(/running in the background/)).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Retry same request' })
  ).not.toBeInTheDocument()
  expect(post).toHaveBeenCalledTimes(1)
  expect(loadDiagnosticDraft(51, firstSession)).toBeNull()
})

test('storage failure prevents the initial paid request instead of producing an unrecoverable run', async () => {
  const { user, post } = setup(9)
  await user.click(screen.getByRole('button', { name: 'Key diagnostic' }))
  await screen.findByDisplayValue('Personal · group-a')
  await selectModel(user, 'chat-model')
  await user.click(
    screen.getByRole('checkbox', {
      name: 'I agree to one paid test with this API key.',
    })
  )
  const storage = vi
    .spyOn(globalThis.localStorage, 'setItem')
    .mockImplementation(() => {
      throw new Error('private storage unavailable')
    })
  try {
    await user.click(screen.getByRole('button', { name: 'Start test' }))
    expect(screen.getByText(/No test was sent/)).toBeVisible()
    expect(post).not.toHaveBeenCalled()
  } finally {
    storage.mockRestore()
  }
})

test('a generation refund does not claim that all charges were refunded', () => {
  render(
    <KeyDiagnosticResult
      run={{
        id: testID,
        status: 'failed',
        billing: { scope: 'generation', status: 'refunded', quota: 0 },
      }}
    />
  )
  expect(screen.getByText('Generation charge refunded')).toBeVisible()
  expect(
    screen.getByText(
      'Generation settlement only. Other fees are not confirmed here.'
    )
  ).toBeVisible()
})
