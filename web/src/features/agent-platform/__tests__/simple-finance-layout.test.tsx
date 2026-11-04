import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { SimpleAgentFinance } from '../simple-finance'

const clients: QueryClient[] = []

afterEach(() => {
  clients.splice(0).forEach((client) => client.clear())
})

function renderFinance(total = 0) {
  vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
    let data: unknown = { items: [], total: 0 }
    if (url.endsWith('/finance/agents')) {
      const params = config?.params as { p?: number } | undefined
      const page = Number(params?.p ?? 1)
      data = {
        items:
          total > 0 && page === 1
            ? [
                {
                  agent_user_id: 73,
                  username: 'Example agent',
                  status: 'active',
                  available_quota: 0,
                  available_credit_quota: 0,
                  amount_due_quota: 0,
                  overdue_quota: 0,
                },
              ]
            : [],
        total: page === 1 ? total : 0,
        page,
        page_size: 20,
      }
    } else if (url.endsWith('/simple/payment-fees')) {
      data = []
    }
    return { data: { success: true, data } }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <SimpleAgentFinance />
    </QueryClientProvider>
  )
}

test('finance actions share a wrapping horizontal group outside the list card', async () => {
  renderFinance()
  const actions = await screen.findByRole('group', { name: 'Agent management' })
  expect(actions).toHaveClass('flex', 'flex-wrap', 'max-w-full', 'gap-2')
  expect(actions.closest('[data-slot="card"]')).toBeNull()
  expect(within(actions).getAllByRole('button')).toHaveLength(4)
  for (const name of [
    'Open agent',
    'Payment channel fees',
    'Record user withdrawal',
    'All financial history',
  ]) {
    expect(within(actions).getByRole('button', { name })).toBeVisible()
  }
})

test('an empty first page uses a compact empty state without redundant paging', async () => {
  renderFinance()
  const empty = await screen.findByText('No agents found')
  expect(empty.closest('[data-slot="empty"]')).toHaveClass(
    'min-h-48',
    'sm:min-h-60'
  )
  expect(
    screen.queryByRole('button', { name: 'Previous' })
  ).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Next' })).not.toBeInTheDocument()
})

test('paging remains available to return from a later page that becomes empty', async () => {
  renderFinance(21)
  fireEvent.click(await screen.findByRole('button', { name: 'Next' }))
  await screen.findByText('No agents found')
  expect(screen.getByRole('button', { name: 'Previous' })).toBeEnabled()
  fireEvent.click(screen.getByRole('button', { name: 'Previous' }))
  expect(await screen.findByText('Example agent')).toBeVisible()
})

test.each([
  ['Open agent', 'Open agent'],
  ['Payment channel fees', 'Payment channel fees'],
  ['Record user withdrawal', 'Record agent finance action'],
  ['All financial history', 'All financial history'],
])(
  '%s can be opened, dismissed and reopened without a financial mutation',
  async (action, title) => {
    renderFinance()
    const mutation = vi.spyOn(api, 'request')
    const trigger = await screen.findByRole('button', { name: action })
    fireEvent.click(trigger)
    const dialog = await screen.findByRole('dialog', { name: title })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }))
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    fireEvent.click(trigger)
    expect(await screen.findByRole('dialog', { name: title })).toBeVisible()
    expect(mutation).not.toHaveBeenCalled()
  }
)
