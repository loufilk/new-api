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
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import type { ApiKey, CCSwitchApp } from '../../../types'
import { CCSwitchDialog } from '../cc-switch-dialog'
import { ccSwitchModelsQueryKey } from '../cc-switch-utils'

const token: ApiKey = {
  id: 1,
  name: 'Scoped fixture',
  key: 'masked',
  status: 1,
  remain_quota: 0,
  used_quota: 0,
  unlimited_quota: true,
  expired_time: -1,
  created_time: 1,
  accessed_time: 1,
  group: 'default',
  auto_groups: ['default'],
  cross_group_retry: false,
  model_limits_enabled: false,
  model_limits: '',
  allow_ips: '',
  allow_user_quota_query: true,
}

let queryClient: QueryClient

beforeEach(() => {
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity } },
  })
})

afterEach(() => {
  queryClient.clear()
})

function renderDialog(models = ['gpt-5.4', 'claude-sonnet-4-6']) {
  for (const app of ['claude', 'codex', 'gemini'] satisfies CCSwitchApp[]) {
    queryClient.setQueryData(ccSwitchModelsQueryKey(token, app), {
      success: true,
      data: models,
    })
  }
  render(
    <QueryClientProvider client={queryClient}>
      <CCSwitchDialog
        open
        onOpenChange={vi.fn()}
        tokenKey='test-only'
        token={token}
      />
    </QueryClientProvider>
  )
}

describe('CC Switch model selection', () => {
  it('validates models before offering a separate user-activated launch', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: ['gpt-5.4'] },
    })
    const launch = vi.spyOn(window, 'open').mockReturnValue(null)
    renderDialog()
    const user = userEvent.setup()
    await user.click(screen.getByRole('radio', { name: 'Codex' }))
    await user.click(screen.getByRole('combobox', { name: 'Primary Model' }))
    await user.click(await screen.findByRole('option', { name: 'gpt-5.4' }))
    await user.click(screen.getByRole('button', { name: 'Continue' }))
    const open = await screen.findByRole('button', { name: 'Open CC Switch' })
    expect(launch).not.toHaveBeenCalled()
    await user.click(open)
    expect(launch).toHaveBeenCalledWith(
      expect.stringContaining('ccswitch://v1/import?'),
      '_blank',
      'noopener,noreferrer'
    )
  })

  it('discards a prepared launch when the model selection changes', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: ['gpt-5.4', 'claude-sonnet-4-6'] },
    })
    const launch = vi.spyOn(window, 'open').mockReturnValue(null)
    renderDialog()
    const user = userEvent.setup()
    await user.click(screen.getByRole('radio', { name: 'Codex' }))
    await user.click(screen.getByRole('combobox', { name: 'Primary Model' }))
    await user.click(await screen.findByRole('option', { name: 'gpt-5.4' }))
    await user.click(screen.getByRole('button', { name: 'Continue' }))
    await screen.findByRole('button', { name: 'Open CC Switch' })
    await user.click(screen.getByRole('radio', { name: 'Gemini' }))
    expect(screen.getByRole('button', { name: 'Continue' })).toBeVisible()
    expect(launch).not.toHaveBeenCalled()
  })

  it.each(['Claude', 'Codex', 'Gemini'])(
    'opens %s models outside the clipping dialog and keeps the dialog open after selection',
    async (app) => {
      renderDialog()
      const user = userEvent.setup()
      await user.click(screen.getByRole('radio', { name: app }))
      const input = screen.getByRole('combobox', { name: 'Primary Model' })

      await user.click(input)

      expect(input).toHaveAttribute('aria-expanded', 'true')
      const list = await screen.findByRole('listbox')
      const dialog = screen.getByRole('dialog', { name: 'Import to CC Switch' })
      // The dialog is translated and clips overflow. Its popup must escape
      // that containing block to remain aligned and fully visible.
      expect(dialog).not.toContainElement(list)
      await user.click(screen.getByRole('option', { name: 'gpt-5.4' }))
      await waitFor(() => expect(input).toHaveValue('gpt-5.4'))
      expect(input).toHaveAttribute('aria-expanded', 'false')
      expect(dialog).toBeVisible()
    }
  )

  it('filters model names and supports keyboard selection and Escape without closing the dialog', async () => {
    renderDialog()
    const user = userEvent.setup()
    await user.click(screen.getByRole('radio', { name: 'Codex' }))
    const input = screen.getByRole('combobox', { name: 'Primary Model' })
    await user.click(input)
    await user.type(input, 'sonnet')

    expect(
      screen.queryByRole('option', { name: 'gpt-5.4' })
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('option', { name: 'claude-sonnet-4-6' })
    ).toBeVisible()
    await user.keyboard('{ArrowDown}{Enter}')
    await waitFor(() => expect(input).toHaveValue('claude-sonnet-4-6'))
    await user.click(input)
    await user.keyboard('{Escape}')

    expect(input).toHaveValue('claude-sonnet-4-6')
    expect(input).toHaveAttribute('aria-expanded', 'false')
    expect(screen.getByRole('dialog')).toBeVisible()
  })

  it('shows an empty result when no models are available and updates an open dropdown when models arrive', async () => {
    renderDialog([])
    const user = userEvent.setup()
    await user.click(screen.getByRole('radio', { name: 'Codex' }))
    const input = screen.getByRole('combobox', { name: 'Primary Model' })
    await user.click(screen.getByRole('button', { name: 'Primary Model' }))

    expect(await screen.findByText('No models found')).toBeVisible()
    await act(async () => {
      queryClient.setQueryData(ccSwitchModelsQueryKey(token, 'codex'), {
        success: true,
        data: ['gpt-5.4'],
      })
    })
    await user.click(await screen.findByRole('option', { name: 'gpt-5.4' }))
    await waitFor(() => expect(input).toHaveValue('gpt-5.4'))
  })

  it('lets users edit the provider name without opening a model dropdown', async () => {
    renderDialog()
    const user = userEvent.setup()
    const input = screen.getByRole('textbox', { name: 'Name' })

    await user.clear(input)
    await user.type(input, 'Development')

    expect(input).toHaveValue('Development')
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  })
})
