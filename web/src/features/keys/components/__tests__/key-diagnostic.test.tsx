import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { KeyDiagnosticButton } from '../key-diagnostic-dialog'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  sessionStorage.clear()
})
function setup(tokenId?: number) {
  const options = {
    keys: [{ id: 9, name: 'Personal' }],
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
  const get = vi
    .spyOn(api, 'get')
    .mockImplementation(async (url) => ({
      data: {
        success: true,
        data: url.endsWith('/options') ? options : ['chat-model'],
      },
    }))
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({
      data: {
        success: true,
        data: {
          status: 'completed',
          answer: '<svg>only text</svg>',
          request_id: 'receipt-id',
        },
      },
    })
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <KeyDiagnosticButton tokenId={tokenId} />
    </QueryClientProvider>
  )
  return { get, post, user: userEvent.setup() }
}
test('opening a row reads only metadata; manually starting submits the selected key once and renders source as text', async () => {
  const { user, get, post } = setup(9)
  await user.click(screen.getByRole('button', { name: 'Key diagnostic' }))
  expect(await screen.findByDisplayValue('Personal (#9)')).toBeDisabled()
  expect(post).not.toHaveBeenCalled()
  await user.selectOptions(
    screen.getByLabelText('Model'),
    await screen.findByRole('option', { name: 'chat-model' })
  )
  expect(screen.getByText('Draw a cycling pelican in SVG.')).toBeVisible()
  await user.selectOptions(
    screen.getByLabelText('Test question'),
    'candy-logic-v1'
  )
  expect(screen.getByText('Draw the candies.')).toBeVisible()
  await user.dblClick(screen.getByRole('button', { name: 'Start test' }))
  expect(await screen.findByText('<svg>only text</svg>')).toBeInTheDocument()
  expect(post).toHaveBeenCalledTimes(1)
  expect(post).toHaveBeenCalledWith(
    '/api/user/key-diagnostics/runs',
    {
      token_id: 9,
      question: 'candy-logic-v1',
      model: 'chat-model',
      confirmed: true,
      request_id: expect.any(String),
    },
    expect.anything()
  )
  expect(get.mock.calls.every(([url]) => !url.endsWith('/key'))).toBe(true)
})
test('top entry requires choosing a key and model and dismissal never dispatches', async () => {
  const { user, get, post } = setup()
  await user.click(screen.getByRole('button', { name: 'Key diagnostic' }))
  await screen.findByRole('option', { name: 'Personal (#9)' })
  expect(screen.getByRole('button', { name: 'Start test' })).toBeDisabled()
  expect(get.mock.calls.some(([url]) => url.endsWith('/models'))).toBe(false)
  await user.selectOptions(screen.getByLabelText('API Key'), '9')
  await screen.findByRole('option', { name: 'chat-model' })
  expect(screen.getByRole('button', { name: 'Start test' })).toBeDisabled()
  await user.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(post).not.toHaveBeenCalled()
})
