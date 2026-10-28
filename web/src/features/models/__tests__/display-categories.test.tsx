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
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import { modelDisplayCategoriesSchema } from '@/lib/model-display-categories'

import { ModelMutateDrawer } from '../components/drawers/model-mutate-drawer'
import type { Model } from '../types'

const original: Model = {
  id: 7,
  model_name: 'synthetic-category-model',
  status: 1,
  sync_official: 1,
  name_rule: 0,
  created_time: 1,
  updated_time: 1,
  input_modalities: '["text","image"]',
  output_modalities: '["text"]',
  capabilities: '["vision"]',
  context_length: 128000,
  max_output_tokens: 4096,
  knowledge_cutoff: '2025-01',
  release_date: '2025-06',
  parameter_count: '8B',
  bonus_eligible: false,
  endpoints: '{"openai":{"path":"/v1/chat/completions","method":"POST"}}',
}

afterEach(cleanup)

function renderEditor(override: Partial<Model> = {}) {
  let stored = { ...original, ...override }
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: url === '/api/models/7' ? stored : { items: [] },
    },
  }))
  const put = vi.spyOn(api, 'put').mockImplementation(async (_url, payload) => {
    stored = { ...stored, ...(payload as Partial<Model>) }
    return { data: { success: true, data: stored } }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  function Editor() {
    const [open, setOpen] = useState(true)
    return (
      <>
        <Button onClick={() => setOpen(true)}>Reopen editor</Button>
        <ModelMutateDrawer
          open={open}
          onOpenChange={setOpen}
          currentRow={original}
        />
      </>
    )
  }
  render(
    <QueryClientProvider client={client}>
      <Editor />
    </QueryClientProvider>
  )
  return { put, client }
}

describe('model display category editing', () => {
  it('starts automatic, saves multiple categories and preserves catalog capabilities and endpoints', async () => {
    const { put, client } = renderEditor()
    const user = userEvent.setup()
    const select = await screen.findByRole('combobox', {
      name: 'Display categories',
    })
    expect(select).toHaveAttribute('placeholder', 'Automatic classification')
    expect(
      screen.getByRole('button', { name: 'Restore automatic classification' })
    ).toBeDisabled()
    await user.click(select)
    expect(
      screen.queryByRole('option', { name: 'All' })
    ).not.toBeInTheDocument()
    await user.click(
      await screen.findByRole('option', { name: 'Image Generation' })
    )
    await user.click(await screen.findByRole('option', { name: 'Video' }))
    await user.click(
      await screen.findByRole('option', { name: 'Decision Models' })
    )
    await user.keyboard('{Escape}')
    await user.click(screen.getByRole('button', { name: 'Save metadata' }))
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(
        '/api/models/',
        expect.objectContaining({
          display_categories: ['images', 'video', 'decision'],
          input_modalities: original.input_modalities,
          output_modalities: original.output_modalities,
          capabilities: original.capabilities,
          endpoints: original.endpoints,
          context_length: original.context_length,
          max_output_tokens: original.max_output_tokens,
          knowledge_cutoff: original.knowledge_cutoff,
          release_date: original.release_date,
          parameter_count: original.parameter_count,
          bonus_eligible: false,
        }),
        expect.anything()
      )
    )
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    await user.click(screen.getByRole('button', { name: 'Reopen editor' }))
    await screen.findByRole('combobox', { name: 'Display categories' })
    expect(screen.getByText('Image Generation')).toBeVisible()
    expect(screen.getByText('Video')).toBeVisible()
    expect(screen.getByText('Decision Models')).toBeVisible()
    client.clear()
  })

  it('explicitly clears the saved override when restoring automatic classification', async () => {
    const { put, client } = renderEditor({
      display_categories: ['images', 'video'],
    })
    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', {
        name: 'Restore automatic classification',
      })
    )
    expect(
      screen.getByRole('combobox', { name: 'Display categories' })
    ).toHaveAttribute('placeholder', 'Automatic classification')
    await user.click(screen.getByRole('button', { name: 'Save metadata' }))
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(
        '/api/models/',
        expect.objectContaining({
          display_categories: [],
          output_modalities: original.output_modalities,
        }),
        expect.anything()
      )
    )
    client.clear()
  })

  it('discards unsaved category edits on Close and reloads the saved selection on reopen', async () => {
    const { put, client } = renderEditor({ display_categories: ['audio'] })
    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', {
        name: 'Restore automatic classification',
      })
    )
    await user.click(screen.getAllByRole('button', { name: 'Close' })[0])
    expect(await screen.findByText('Discard unsaved changes?')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Discard changes' }))
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    await user.click(screen.getByRole('button', { name: 'Reopen editor' }))
    await screen.findByRole('combobox', { name: 'Display categories' })
    expect(screen.getByText('Audio')).toBeVisible()
    expect(put).not.toHaveBeenCalled()
    client.clear()
  })

  it('retains the selection and shows a rejected-save error instead of closing the drawer', async () => {
    const { put, client } = renderEditor({ display_categories: ['audio'] })
    put.mockResolvedValue({
      data: { success: false, message: 'Category save rejected' },
    })
    const user = userEvent.setup()
    await screen.findByRole('combobox', { name: 'Display categories' })
    await user.click(screen.getByRole('button', { name: 'Save metadata' }))
    expect(await screen.findByText('Category save rejected')).toBeVisible()
    expect(screen.getByText('Audio')).toBeVisible()
    expect(screen.getByRole('dialog')).toBeVisible()
    client.clear()
  })

  it('disables category changes and repeated saves while a save is pending', async () => {
    const { put, client } = renderEditor({ display_categories: ['audio'] })
    let finish!: (value: Awaited<ReturnType<typeof api.put>>) => void
    put.mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = resolve
        })
    )
    const user = userEvent.setup()
    await screen.findByRole('combobox', { name: 'Display categories' })
    await user.click(screen.getByRole('button', { name: 'Save metadata' }))
    const saving = screen.getByRole('button', { name: 'Saving...' })
    expect(saving).toBeDisabled()
    expect(
      screen.getByRole('combobox', { name: 'Display categories' })
    ).toBeDisabled()
    expect(
      screen.getByRole('button', { name: 'Restore automatic classification' })
    ).toBeDisabled()
    await user.click(saving)
    expect(put).toHaveBeenCalledTimes(1)
    await act(async () =>
      finish({
        data: {
          success: true,
          data: { ...original, display_categories: ['audio'] },
        },
      })
    )
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    client.clear()
  })

  it('allows clearing but rejects aggregate, unknown and oversized category values', () => {
    expect(modelDisplayCategoriesSchema.safeParse([]).success).toBe(true)
    expect(
      modelDisplayCategoriesSchema.safeParse([
        'text',
        'images',
        'video',
        'audio',
        'vectors',
        'decision',
      ]).success
    ).toBe(true)
    for (const value of [
      ['all'],
      ['unknown'],
      ['text', 'images', 'video', 'audio', 'vectors', 'decision', 'text'],
    ]) {
      expect(modelDisplayCategoriesSchema.safeParse(value).success).toBe(false)
    }
  })
})
