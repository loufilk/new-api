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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { toast } from 'sonner'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { ModelSquareOrderDialog } from '../components/dialogs/model-square-order-dialog'
import type { ModelSquareOrder as Order } from '../types'
const defaultOrder: Order = {
  sort_mode: 'manual',
  sort_metric: 'tokens',
  sort_days: 7,
  sort_models: [],
}
const path = '/api/option/model-square-order'

function mockApi(
  saved: Partial<Order> | Promise<Partial<Order>>,
  usage: Record<string, { requests: number; tokens: number }> = {}
) {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url: string) => {
    if (url === path) {
      return {
        data: { success: true, data: { ...defaultOrder, ...(await saved) } },
      }
    }
    if (url === '/api/models/square_usage') {
      const data = ['alpha', 'beta', 'gamma'].map((name) => ({
        model_name: name,
        vendor_name: 'Acme',
        requests: usage[name]?.requests ?? 0,
        tokens: usage[name]?.tokens ?? 0,
      }))
      return { data: { success: true, data } }
    }
    return { data: { success: true, data: {} } }
  })
  const patch = vi
    .spyOn(api, 'patch')
    .mockResolvedValue({ data: { success: true } })
  return { get, patch }
}

function renderDialog(onOpenChange = vi.fn(), initiallyOpen = true) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const view = render(
    <QueryClientProvider client={client}>
      <ModelSquareOrderDialog
        open={initiallyOpen}
        onOpenChange={onOpenChange}
      />
    </QueryClientProvider>
  )
  return {
    rerender: (open: boolean) =>
      view.rerender(
        <QueryClientProvider client={client}>
          <ModelSquareOrderDialog open={open} onOpenChange={onOpenChange} />
        </QueryClientProvider>
      ),
  }
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('model square order dialog', () => {
  it('loads the saved order on open before allowing edits', async () => {
    let resolveOptions!: (value: Partial<Order>) => void
    const saved = new Promise<Partial<Order>>((resolve) => {
      resolveOptions = resolve
    })
    mockApi(saved)
    const dialog = renderDialog(vi.fn(), false)
    expect(api.get).not.toHaveBeenCalled()

    dialog.rerender(true)
    const usageMode = screen.getByRole('button', { name: 'Most used' })
    expect(usageMode).toBeDisabled()

    resolveOptions({ sort_models: ['beta'] })
    expect(
      await screen.findByRole('button', { name: 'Remove beta' })
    ).toBeVisible()
    expect(usageMode).toBeEnabled()
  })

  it('keeps list edits as a draft and saves only the changed setting', async () => {
    const { patch } = mockApi({
      sort_mode: 'manual',
      sort_models: ['beta', 'alpha'],
    })
    const user = userEvent.setup()
    renderDialog()

    await user.click(
      await screen.findByRole('button', { name: 'Move alpha up' })
    )
    await user.click(screen.getByRole('button', { name: 'Remove beta' }))
    await user.click(screen.getByRole('combobox', { name: 'Add model' }))
    await user.click(await screen.findByRole('option', { name: /gamma/ }))
    expect(screen.getByRole('button', { name: 'Remove gamma' })).toBeVisible()
    expect(patch).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith(
      path,
      {
        changes: { sort_models: ['alpha', 'gamma'] },
        expected: { sort_models: ['beta', 'alpha'] },
      },
      { skipAuthRefresh: true }
    )
  })

  it('shows one success toast for several changed settings', async () => {
    const { patch } = mockApi({ sort_mode: 'manual' })
    const success = vi.spyOn(toast, 'success')
    const onOpenChange = vi.fn()
    const user = userEvent.setup()
    renderDialog(onOpenChange)

    await user.click(await screen.findByRole('button', { name: 'Most used' }))
    await user.click(screen.getByRole('button', { name: 'Requests' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
    expect(patch).toHaveBeenCalledTimes(1)
    expect(patch).toHaveBeenCalledWith(
      path,
      {
        changes: { sort_mode: 'usage', sort_metric: 'requests' },
        expected: { sort_mode: 'manual', sort_metric: 'tokens' },
      },
      { skipAuthRefresh: true }
    )
    expect(success).toHaveBeenCalledTimes(1)
    expect(success).toHaveBeenCalledWith('Setting updated successfully')
  })

  it('keeps the dialog and the draft open when saving fails', async () => {
    const { patch } = mockApi({ sort_mode: 'manual' })
    patch.mockResolvedValueOnce({
      data: { success: false, message: 'Invalid model square order setting' },
    })
    const success = vi.spyOn(toast, 'success')
    const error = vi.spyOn(toast, 'error')
    const onOpenChange = vi.fn()
    const user = userEvent.setup()
    renderDialog(onOpenChange)

    await user.click(await screen.findByRole('button', { name: 'Most used' }))
    await user.click(screen.getByRole('button', { name: 'Requests' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(error).toHaveBeenCalledTimes(1))
    expect(error).toHaveBeenCalledWith('Invalid model square order setting')
    expect(success).not.toHaveBeenCalled()
    expect(onOpenChange).not.toHaveBeenCalled()
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()
    )
    expect(patch).toHaveBeenCalledTimes(1)
    expect(screen.getByRole('button', { name: 'Requests' })).toHaveAttribute(
      'aria-pressed',
      'true'
    )
  })

  it('requires a fresh saved order every time the dialog opens', async () => {
    const persisted = { sort_models: ['alpha'] }
    const { get, patch } = mockApi(persisted)
    const user = userEvent.setup()
    const dialog = renderDialog()
    await screen.findByRole('button', { name: 'Remove alpha' })

    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    dialog.rerender(false)
    persisted.sort_models = ['gamma']
    dialog.rerender(true)

    expect(screen.getByRole('button', { name: 'Most used' })).toBeDisabled()
    await screen.findByRole('button', { name: 'Remove gamma' })
    expect(screen.queryByRole('button', { name: 'Remove alpha' })).toBeNull()
    expect(get.mock.calls.filter(([url]) => url === path)).toHaveLength(2)
    expect(patch).not.toHaveBeenCalled()
  })

  it('does not replay a write when its response is lost after commit', async () => {
    const persisted = { sort_mode: 'manual', sort_metric: 'tokens' }
    const { patch } = mockApi(persisted)
    patch.mockImplementationOnce(async () => {
      persisted.sort_mode = 'usage'
      persisted.sort_metric = 'requests'
      throw new Error('Connection dropped after commit')
    })
    const onOpenChange = vi.fn()
    const user = userEvent.setup()
    renderDialog(onOpenChange)

    await user.click(await screen.findByRole('button', { name: 'Most used' }))
    await user.click(screen.getByRole('button', { name: 'Requests' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
    expect(patch).toHaveBeenCalledTimes(1)
    expect(persisted).toEqual({ sort_mode: 'usage', sort_metric: 'requests' })
  })

  it('shows another administrator’s conflicting saved order without replaying the draft', async () => {
    const persisted = { sort_mode: 'manual', sort_models: ['alpha'] }
    const { patch } = mockApi(persisted)
    patch.mockImplementationOnce(async () => {
      persisted.sort_mode = 'mixed'
      persisted.sort_models = ['gamma']
      throw new Error('Order changed')
    })
    const onOpenChange = vi.fn()
    const user = userEvent.setup()
    renderDialog(onOpenChange)

    await user.click(await screen.findByRole('button', { name: 'Most used' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await screen.findByRole('button', { name: 'Remove gamma' })
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    expect(patch).toHaveBeenCalledTimes(1)
    expect(onOpenChange).not.toHaveBeenCalled()
  })

  it('discards the draft when the dialog is cancelled', async () => {
    const { patch } = mockApi({ sort_mode: 'manual' })
    const onOpenChange = vi.fn()
    const user = userEvent.setup()
    const dialog = renderDialog(onOpenChange)

    await user.click(await screen.findByRole('button', { name: 'Most used' }))
    expect(screen.getByRole('button', { name: 'Most used' })).toHaveAttribute(
      'aria-pressed',
      'true'
    )
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
    dialog.rerender(false)
    dialog.rerender(true)

    expect(
      await screen.findByRole('button', { name: 'Manual order' })
    ).toHaveAttribute('aria-pressed', 'true')
    expect(patch).not.toHaveBeenCalled()
  })

  it('ranks the other models by the chosen metric after the pinned ones', async () => {
    mockApi(
      {
        sort_mode: 'mixed',
        sort_metric: 'tokens',
        sort_models: ['beta'],
      },
      {
        gamma: { requests: 3, tokens: 9000 },
        alpha: { requests: 8, tokens: 100 },
      }
    )
    const user = userEvent.setup()
    renderDialog()

    const others = await screen.findByRole('list', { name: 'Other models' })
    const rows = () => within(others).getAllByRole('listitem')
    expect(rows()).toHaveLength(2)
    expect(rows()[0]).toHaveTextContent(/^2.*gamma.*9K$/)
    expect(rows()[1]).toHaveTextContent(/^3.*alpha.*100$/)

    await user.click(screen.getByRole('button', { name: 'Requests' }))

    expect(rows()[0]).toHaveTextContent(/^2.*alpha.*8$/)
    expect(rows()[1]).toHaveTextContent(/^3.*gamma.*3$/)
  })

  it('does not display the previous period ranking while the new period loads', async () => {
    const { get } = mockApi(
      { sort_mode: 'usage' },
      { alpha: { requests: 2, tokens: 200 } }
    )
    const defaultGet = get.getMockImplementation()
    if (!defaultGet) throw new Error('Expected the model API fixture')
    let resolveUsage!: () => void
    const blockedUsage = new Promise<void>((resolve) => {
      resolveUsage = resolve
    })
    get.mockImplementation(async (url, config) => {
      const params = config?.params
      if (
        url !== '/api/models/square_usage' ||
        !params ||
        typeof params !== 'object' ||
        !('days' in params) ||
        params.days !== 1
      ) {
        return defaultGet(url, config)
      }
      await blockedUsage
      return {
        data: {
          success: true,
          data: [{ model_name: 'gamma', tokens: 600, requests: 1 }],
        },
      }
    })
    const user = userEvent.setup()
    renderDialog()
    expect(
      await screen.findByRole('list', { name: 'Current order' })
    ).toHaveTextContent('alpha')

    await user.click(screen.getByRole('combobox', { name: 'Period' }))
    await user.click(await screen.findByRole('option', { name: 'Last 1 day' }))
    expect(screen.queryByRole('list', { name: 'Current order' })).toBeNull()
    resolveUsage()
    expect(
      await screen.findByRole('list', { name: 'Current order' })
    ).toHaveTextContent('gamma')
  })

  it('says the order falls back to names when there is no usage', async () => {
    mockApi({ sort_mode: 'usage' })
    renderDialog()

    expect(
      await screen.findByText('No usage data, so models are ordered by name.')
    ).toBeVisible()
  })
})
