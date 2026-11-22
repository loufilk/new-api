import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { PartnerApplicationForm } from '../application-form'

afterEach(() => vi.restoreAllMocks())

test('a supplier retry after a transport failure reuses its idempotency key', async () => {
  const user = userEvent.setup()
  const post = vi
    .spyOn(api, 'post')
    .mockRejectedValueOnce(new Error('Connection interrupted'))
    .mockResolvedValueOnce({
      data: { success: true, data: { id: 14 } },
    })

  render(
    <QueryClientProvider client={new QueryClient()}>
      <PartnerApplicationForm owner={8} disabled={false} />
    </QueryClientProvider>
  )

  await user.type(screen.getByLabelText(/Site name/), 'Example Provider')
  await user.type(screen.getByLabelText(/Contact details/), 'wx-example')
  await user.type(screen.getByLabelText(/Group name/), 'standard')
  await user.click(
    screen.getByRole('button', { name: 'Submit partner application' })
  )
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))

  await user.click(
    screen.getByRole('button', { name: 'Submit partner application' })
  )
  await waitFor(() => expect(post).toHaveBeenCalledTimes(2))

  const firstCall = post.mock.calls[0]
  const secondCall = post.mock.calls[1]
  expect(firstCall?.[0]).toBe('/api/partner-applications')
  expect(secondCall?.[0]).toBe(firstCall?.[0])
  expect(secondCall?.[1]).toEqual(firstCall?.[1])
  expect(secondCall?.[2]).toEqual(firstCall?.[2])
})
