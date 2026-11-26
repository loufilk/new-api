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
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

import { pricingSearchSchema } from '../../lib/search'
import type { PricingModel } from '../../types'
import { useFilters } from '../use-filters'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

const base = {
  quota_type: 0,
  model_ratio: 1,
  completion_ratio: 1,
  enable_groups: ['premium'],
  vendor_name: 'Vendor',
  tags: 'featured',
}
const models: PricingModel[] = [
  { ...base, id: 1, model_name: 'text-only', output_modalities: ['text'] },
  {
    ...base,
    id: 2,
    model_name: 'image-only',
    supported_endpoint_types: ['image-generation'],
  },
  {
    ...base,
    id: 3,
    model_name: 'mixed',
    enable_groups: ['premium', 'second', 'private_id'],
    group_ratio: { premium: 8, second: 3, private_id: 0 },
    output_modalities: ['text', 'image'],
  },
  {
    ...base,
    id: 4,
    model_name: 'unknown',
    enable_groups: ['unavailable'],
    supported_endpoint_types: ['openai'],
  },
]
function FilterFixture() {
  const filters = useFilters(
    models,
    {
      premium: { platform: 'openai' },
      second: { platform: 'openai' },
      unavailable: { platform: 'openai' },
      private_id: { exclusive: true },
    },
    'public',
    ['premium', 'second', 'private_id']
  )
  return (
    <>
      <Button onClick={() => filters.setPlatform('openai')}>OpenAI</Button>
      <Button onClick={() => filters.setGroupFilter('private_id')}>
        Private
      </Button>
      <output aria-label='Sort'>{filters.sortBy}</output>
      <output aria-label='Group'>{filters.groupFilter}</output>
      <Input
        aria-label='Search'
        value={filters.searchInput}
        onChange={(event) => filters.setSearchInput(event.target.value)}
      />
      <Button onClick={filters.clearFilters}>Reset filters</Button>
      <Button
        onClick={() => {
          filters.clearFilters()
          filters.clearSearch()
        }}
      >
        Reset all
      </Button>
      <output aria-label='Results'>
        {filters.filteredModels.map((model) => model.model_name).join(',')}
      </output>
    </>
  )
}
async function renderFilters(entry = '/pricing/') {
  const root = createRootRoute()
  const pricing = createRoute({
    getParentRoute: () => root,
    path: '/pricing/',
    validateSearch: pricingSearchSchema,
    component: FilterFixture,
  })
  const router = createRouter({
    routeTree: root.addChildren([pricing]),
    history: createMemoryHistory({ initialEntries: [entry] }),
  })
  render(<RouterProvider router={router} />)
  await screen.findByRole('button', { name: 'OpenAI' })
  return router
}

describe('marketplace navigation', () => {
  it.each(['', '?sort=requests', '?sort=tokens', '?sort=recommended'])(
    'displays name sorting when the public projection has no ranking for %s',
    async (search) => {
      await renderFilters(`/pricing/${search}`)
      expect(screen.getByLabelText('Sort')).toHaveTextContent('name')
    }
  )

  it('ignores retired category bookmarks, retaining query, sort and active group', async () => {
    const router = await renderFilters(
      '/pricing/?category=images&group=premium&sort=price-low'
    )
    expect(screen.getByLabelText('Results')).toHaveTextContent('text-only')
    expect(screen.getByLabelText('Results')).toHaveTextContent('image-only')
    await userEvent.click(screen.getByRole('button', { name: 'OpenAI' }))
    await waitFor(() =>
      expect(router.state.location.search.platform).toBe('openai')
    )
    expect(router.state.location.search.sort).toBe('price-low')
    expect(screen.getByLabelText('Group')).toHaveTextContent('all')
    act(() => router.history.back())
    await waitFor(() =>
      expect(screen.getByLabelText('Group')).toHaveTextContent('premium')
    )
  })
  it('intersects the platform with available groups, counts a shared model once, and sorts by its matching price', async () => {
    await renderFilters('/pricing/?platform=openai&sort=input-price-low')
    expect(screen.getByLabelText('Results').textContent).toBe(
      'text-only,image-only,mixed'
    )
    expect(screen.getByLabelText('Group')).toHaveTextContent('all')
  })

  it('keeps private group identifiers out of URL and history state, retaining only a live scoped reference', async () => {
    const router = await renderFilters()
    await userEvent.click(screen.getByRole('button', { name: 'Private' }))
    await waitFor(() =>
      expect(screen.getByLabelText('Group')).toHaveTextContent('private_id')
    )
    expect(router.state.location.href).not.toContain('private_id')
    expect(JSON.stringify(router.state.location.state)).not.toContain(
      'private_id'
    )
    await userEvent.click(screen.getByRole('button', { name: 'OpenAI' }))
    await waitFor(() =>
      expect(screen.getByLabelText('Group')).toHaveTextContent('all')
    )
    act(() => router.history.back())
    await waitFor(() =>
      expect(screen.getByLabelText('Group')).toHaveTextContent('private_id')
    )
  })
})
