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

import { ModelCategories } from '../../components/model-categories'
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
  { ...base, id: 3, model_name: 'mixed', output_modalities: ['text', 'image'] },
  {
    ...base,
    id: 4,
    model_name: 'unknown',
    supported_endpoint_types: ['openai'],
  },
]
function FilterFixture() {
  const filters = useFilters(models)
  return (
    <>
      <ModelCategories
        models={models}
        value={filters.category}
        onChange={filters.setCategory}
      />
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
  await screen.findByRole('button', { name: 'All 4' })
  return router
}

describe('pricing category navigation', () => {
  it('keeps typed search in the URL alongside category and clears search plus all filters together', async () => {
    const router = await renderFilters(
      '/pricing/?category=images&group=premium'
    )
    const user = userEvent.setup()
    await user.type(screen.getByLabelText('Search'), 'image')
    await waitFor(() =>
      expect(router.state.location.search.search).toBe('image')
    )
    await waitFor(() =>
      expect(screen.getByLabelText('Results')).toHaveTextContent('image-only')
    )
    expect(router.state.location.search.category).toBe('images')
    await user.click(screen.getByRole('button', { name: 'Reset all' }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'All 4' })).toHaveAttribute(
        'aria-pressed',
        'true'
      )
    )
    expect(screen.getByLabelText('Search')).toHaveValue('')
    expect(router.state.location.search.group).toBeUndefined()
  })
  it('defaults to all and changes the selected category with keyboard input without clearing it on repeated clicks', async () => {
    const user = userEvent.setup()
    await renderFilters()
    expect(screen.getByRole('button', { name: 'All 4' })).toHaveAttribute(
      'aria-pressed',
      'true'
    )
    const images = screen.getByRole('button', { name: 'Image Generation 2' })
    images.focus()
    await user.keyboard('{Enter}')
    await waitFor(() => expect(images).toHaveAttribute('aria-pressed', 'true'))
    expect(screen.getByLabelText('Results')).toHaveTextContent(
      'image-only,mixed'
    )
    await user.click(images)
    expect(images).toHaveAttribute('aria-pressed', 'true')
  })
  it('preserves legacy query filters when category changes and restores selection through Back and Forward', async () => {
    const router = await renderFilters(
      '/pricing/?vendor=Vendor&group=premium&tag=featured&sort=price-low&view=table&tokenUnit=K&rechargePrice=true'
    )
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Image Generation 2' }))
    await waitFor(() =>
      expect(router.state.location.search.category).toBe('images')
    )
    expect(router.state.location.search).toMatchObject({
      vendor: 'Vendor',
      group: 'premium',
      tag: 'featured',
      sort: 'price-low',
      view: 'table',
      tokenUnit: 'K',
      rechargePrice: true,
    })
    await user.click(screen.getByRole('button', { name: 'Text 2' }))
    await waitFor(() =>
      expect(router.state.location.search.category).toBe('text')
    )
    act(() => router.history.back())
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Image Generation 2' })
      ).toHaveAttribute('aria-pressed', 'true')
    )
    act(() => router.history.forward())
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Text 2' })).toHaveAttribute(
        'aria-pressed',
        'true'
      )
    )
  })
  it('honors bookmarked search and category together and resets category without removing the search', async () => {
    const router = await renderFilters('/pricing/?search=image&category=images')
    expect(screen.getByLabelText('Search')).toHaveValue('image')
    expect(screen.getByLabelText('Results')).toHaveTextContent('image-only')
    await userEvent.click(screen.getByRole('button', { name: 'Reset filters' }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'All 4' })).toHaveAttribute(
        'aria-pressed',
        'true'
      )
    )
    expect(router.state.location.search.search).toBe('image')
  })
  it('falls back to all for an unknown category and keeps zero-result categories selectable', async () => {
    await renderFilters('/pricing/?category=unsupported')
    expect(screen.getByRole('button', { name: 'All 4' })).toHaveAttribute(
      'aria-pressed',
      'true'
    )
    await userEvent.click(screen.getByRole('button', { name: 'Vectors 0' }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Vectors 0' })).toHaveAttribute(
        'aria-pressed',
        'true'
      )
    )
    expect(screen.getByText('Embeddings and reranking')).toBeVisible()
    expect(screen.getByLabelText('Results')).toBeEmptyDOMElement()
  })
})

it('shows and counts decision as a manual category without double-counting all', async () => {
  const onChange = vi.fn()
  render(
    <ModelCategories
      models={[
        {
          ...base,
          id: 11,
          model_name: 'decision-name-only',
          output_modalities: ['text'],
        },
        {
          ...base,
          id: 12,
          model_name: 'manual',
          display_categories: ['text', 'decision', 'decision'],
        },
      ]}
      value='decision'
      onChange={onChange}
    />
  )
  expect(
    screen.getByRole('button', { name: 'Decision Models 1' })
  ).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('button', { name: 'All 2' })).toBeVisible()
  expect(pricingSearchSchema.parse({ category: 'decision' }).category).toBe(
    'decision'
  )
  await userEvent.click(screen.getByRole('button', { name: 'Text 2' }))
  expect(onChange).toHaveBeenCalledWith('text')
})
