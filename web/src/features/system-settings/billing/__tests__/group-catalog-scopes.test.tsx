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
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { GroupCatalogSettings } from '../group-catalog-settings'
import { GroupPricingWorkspace } from '../group-pricing-workspace'

const catalogue = {
  revision: 7,
  groups: {
    standard: {
      label: 'Standard',
      platform: 'openai',
      sort_order: 4,
      badges: [],
      exclusive: false,
    },
    othernormal: {
      label: 'Standard',
      platform: 'other',
      sort_order: 0,
      badges: [],
      exclusive: false,
    },
    limited: {
      label: 'Access only',
      platform: 'other',
      exclusive: true,
      badges: [],
    },
  },
}
let client: QueryClient
afterEach(() => {
  cleanup()
  client?.clear()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

function open() {
  useAuthStore.getState().auth.setUser({ id: 1, role: 100, username: 'admin' })
  let current = structuredClone(catalogue)
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/option/group-catalog') {
      return { data: { success: true, data: current } }
    }
    if (url === '/api/pricing') {
      return {
        data: {
          success: true,
          group_ratio: { standard: 0 },
          group_rate_source: { standard: 'personal' },
          usable_group: { standard: 'enabled' },
          data: [
            {
              model_name: 'visible-model',
              model_ratio: 2,
              completion_ratio: 3,
              quota_type: 0,
              enable_groups: ['standard'],
            },
          ],
        },
      }
    }
    if (url.startsWith('/api/user/search?')) {
      return {
        data: {
          success: true,
          data: { items: [{ id: 11, username: 'operator' }] },
        },
      }
    }
    if (url === '/api/option/group-entitlements/11') {
      return { data: { success: true, data: { revision: 7, groups: [] } } }
    }
    if (url === '/api/option/user-group-pricing') {
      return {
        data: {
          success: true,
          data: {
            revision: 3,
            enabled: true,
            rules: [],
            available_groups: [
              { group: 'standard', inherited_ratio: 2.5, effective_ratio: 2.5 },
            ],
          },
        },
      }
    }
    throw new Error(`Unexpected GET: ${url}`)
  })
  const patch = vi
    .spyOn(api, 'patch')
    .mockImplementation(async (_url, body) => {
      const submitted = body as typeof catalogue
      current = { groups: submitted.groups, revision: submitted.revision + 1 }
      return { data: { success: true, data: current } }
    })
  const put = vi.spyOn(api, 'put').mockImplementation(async (url) => {
    if (url === '/api/option/user-group-pricing') {
      return { data: { success: true, data: { revision: 4, enabled: true } } }
    }
    if (url === '/api/option/group-entitlements/11') {
      return {
        data: { success: true, data: { revision: 8, groups: ['limited'] } },
      }
    }
    throw new Error(`Unexpected PUT: ${url}`)
  })
  const post = vi.spyOn(api, 'post')
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <GroupPricingWorkspace
        renderRatio={() => null}
        renderCatalog={(panel) => (
          <GroupCatalogSettings
            groupRatios='{"standard":2.5,"limited":4,"othernormal":0.75}'
            panel={panel}
          />
        )}
      />
    </QueryClientProvider>
  )
  return { user: userEvent.setup(), patch, put, post, get }
}

it('shows the actual viewer quote separately from the base ratio and previews a label before writing any data', async () => {
  const { user, patch, post, put } = open()
  await user.click(
    await screen.findByRole('button', {
      name: 'Open group details: Standard (standard)',
    })
  )
  const preview = within(
    screen.getByRole('region', { name: 'Group display preview' })
  )
  expect(await preview.findByText('visible-model')).toBeVisible()
  expect(preview.getByText(/Base group rate: 2.5×/)).toBeVisible()
  expect(preview.getByText(/Effective rate: 0×/)).toBeVisible()
  expect(preview.getByText('Price source: Personal rate')).toBeVisible()
  await user.clear(screen.getByRole('textbox', { name: 'Display name' }))
  await user.type(
    screen.getByRole('textbox', { name: 'Display name' }),
    'New name'
  )
  await user.selectOptions(
    screen.getByRole('combobox', { name: /Icon source/ }),
    'deepseek'
  )
  expect(preview.getAllByText('New name')).toHaveLength(2)
  expect(screen.queryByRole('checkbox', { name: /Exclusive group/ })).toBeNull()
  expect(patch).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Save group display' }))
  expect(await screen.findByText('Group display saved.')).toBeVisible()
  expect(screen.getByRole('textbox', { name: 'Display name' })).toHaveValue(
    'New name'
  )
  expect(
    screen.getByRole('button', { name: 'Save group display' })
  ).toBeDisabled()
  expect(patch).toHaveBeenCalledWith('/api/option/group-catalog', {
    revision: 7,
    groups: {
      ...catalogue.groups,
      standard: expect.objectContaining({
        label: 'New name',
        icon: 'deepseek',
        platform: 'openai',
        exclusive: false,
      }),
    },
  })
  expect(post).not.toHaveBeenCalled()
  expect(put).not.toHaveBeenCalled()
})

it('offers Traceable as ordered presentation only and never as an automatic status', async () => {
  const { user, patch, post, put } = open()
  await user.click(
    await screen.findByRole('button', {
      name: 'Open group details: Standard (standard)',
    })
  )
  const checkbox = screen.getByRole('checkbox', { name: 'Traceable' })
  expect(checkbox).not.toBeChecked()
  expect(screen.queryByRole('checkbox', { name: 'New' })).toBeNull()
  expect(
    screen.getByRole('combobox', { name: 'Icon source' })
  ).not.toHaveTextContent('Traceable')
  const preview = within(
    screen.getByRole('region', { name: 'Group display preview' })
  )
  await user.click(screen.getByRole('checkbox', { name: 'Authentic' }))
  await user.click(checkbox)
  expect(preview.getByText('Traceable')).toHaveClass('bg-amber-800')
  expect(
    preview.getByText('Traceable').querySelector('svg.lucide-award')
  ).not.toBeNull()
  await user.click(screen.getByRole('button', { name: 'Move Traceable up' }))
  expect(
    preview
      .getByText('Traceable')
      .previousElementSibling?.querySelector('title')?.textContent
  ).toBe('OpenAI')
  expect(patch).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Save group display' }))
  expect(await screen.findByText('Group display saved.')).toBeVisible()
  expect(checkbox).toBeChecked()
  expect(patch).toHaveBeenCalledWith('/api/option/group-catalog', {
    revision: 7,
    groups: {
      ...catalogue.groups,
      standard: expect.objectContaining({
        ...catalogue.groups.standard,
        badges: ['traceable', 'authentic'],
      }),
    },
  })
  expect(post).not.toHaveBeenCalled()
  expect(put).not.toHaveBeenCalled()
})

it('does not quote a model or create a fallback 1× when the configured group is absent from the viewer response', async () => {
  const { user, get } = open()
  await user.click(
    await screen.findByRole('button', {
      name: 'Open group details: Access only (limited)',
    })
  )
  const preview = within(
    screen.getByRole('region', { name: 'Group display preview' })
  )
  expect(
    await preview.findByText('No authorized quote available for this group.')
  ).toBeVisible()
  expect(preview.queryByText('visible-model')).toBeNull()
  expect(preview.queryByText(/Effective rate:|×1/)).toBeNull()
  expect(get.mock.calls.some(([url]) => url.includes('/public-api/'))).toBe(
    false
  )
})

it('requires explicit confirmation for group visibility, keeps the unsaved display draft and rebases only its display fields', async () => {
  const { user, patch, post, put } = open()
  await user.click(
    await screen.findByRole('button', {
      name: 'Open group details: Standard (standard)',
    })
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Display name' }),
    ' draft'
  )
  await user.keyboard('{Escape}')
  await user.click(
    screen.getByRole('button', { name: 'Exclusive group access' })
  )
  await user.selectOptions(
    screen.getByRole('combobox', { name: /Group to edit/ }),
    'standard'
  )
  await user.click(screen.getByRole('checkbox', { name: /Exclusive group/ }))
  await user.click(
    screen.getByRole('button', { name: 'Save visibility policy' })
  )
  const dialog = await screen.findByRole('alertdialog')
  expect(patch).not.toHaveBeenCalled()
  expect(dialog).toHaveTextContent(
    'existing fixed API keys may return 403 and Auto will skip this group'
  )
  await user.click(
    within(dialog).getByRole('button', { name: 'Save visibility policy' })
  )
  expect(await screen.findByText('Group visibility saved.')).toBeVisible()
  expect(patch.mock.calls[0][1]).toMatchObject({
    revision: 7,
    groups: { standard: { label: 'Standard', exclusive: true } },
  })
  await user.click(screen.getByRole('button', { name: 'Marketplace display' }))
  await user.click(screen.getByRole('button', { name: 'Resume' }))
  expect(screen.getByRole('textbox', { name: 'Display name' })).toHaveValue(
    'Standard draft'
  )
  expect(
    screen.getByRole('button', { name: 'Save group display' })
  ).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Load latest settings' }))
  await user.click(
    await screen.findByRole('button', { name: 'Continue editing after review' })
  )
  await user.click(screen.getByRole('button', { name: 'Save group display' }))
  expect(await screen.findByText('Group display saved.')).toBeVisible()
  expect(patch.mock.calls[1][1]).toMatchObject({
    revision: 8,
    groups: { standard: { label: 'Standard draft', exclusive: true } },
  })
  expect(post).not.toHaveBeenCalled()
  expect(put).not.toHaveBeenCalled()
})

it('saves a personal price without granting a pending exclusive group, then saves the access draft independently', async () => {
  const { user, put, patch, post } = open()
  await user.click(
    screen.getByRole('button', { name: 'Exclusive group access' })
  )
  await user.type(
    await screen.findByRole('textbox', { name: 'Username or user ID' }),
    'operator'
  )
  await user.click(screen.getByRole('button', { name: 'Search users' }))
  await user.selectOptions(
    await screen.findByRole('combobox', { name: /Target user/ }),
    '11'
  )
  await user.click(await screen.findByRole('checkbox', { name: /Access only/ }))
  await user.click(screen.getByRole('button', { name: 'Personal pricing' }))
  expect(screen.queryByRole('button', { name: 'Save user access' })).toBeNull()
  await user.click(
    await screen.findByRole('button', { name: /Standard.*2.5×/ })
  )
  await user.type(screen.getByPlaceholderText('e.g. 0.5'), '0.25')
  await user.click(screen.getByRole('button', { name: 'Save personal rate' }))
  expect(await screen.findByText('Personal rate saved.')).toBeVisible()
  expect(put.mock.calls).toEqual([
    [
      '/api/option/user-group-pricing',
      {
        user_id: 11,
        group: 'standard',
        ratio: '0.25',
        expected_revision: 3,
      },
    ],
  ])
  await user.click(
    screen.getByRole('button', { name: 'Exclusive group access' })
  )
  expect(screen.getByRole('checkbox', { name: /Access only/ })).toBeChecked()
  await user.click(screen.getByRole('button', { name: 'Save user access' }))
  expect(
    await screen.findByText(
      'Exclusive group access saved. Personal rates have not changed.'
    )
  ).toBeVisible()
  expect(put.mock.calls[1]).toEqual([
    '/api/option/group-entitlements/11',
    { revision: 7, groups: ['limited'] },
  ])
  expect(patch).not.toHaveBeenCalled()
  expect(post).not.toHaveBeenCalled()
})

it('finds groups by ID or display name and filters Other independently of exclusive access', async () => {
  const { user, patch } = open()
  const search = await screen.findByRole('textbox', { name: 'Search groups' })
  await user.type(search, 'STANDARD')
  expect(
    screen.getByRole('button', {
      name: 'Open group details: Standard (standard)',
    })
  ).toBeVisible()
  expect(
    screen.getByRole('button', {
      name: 'Open group details: Standard (othernormal)',
    })
  ).toBeVisible()
  await user.clear(search)
  await user.type(search, 'othernormal')
  expect(
    screen.queryByRole('button', {
      name: 'Open group details: Standard (standard)',
    })
  ).toBeNull()
  await user.clear(search)
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Platform' }),
    'other'
  )
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Group access' }),
    'normal'
  )
  expect(
    screen.queryByRole('button', {
      name: 'Open group details: Access only (limited)',
    })
  ).toBeNull()
  const target = screen.getByRole('button', {
    name: 'Open group details: Standard (othernormal)',
  })
  expect(target).toHaveTextContent('Base group rate: 0.75×')
  await user.click(target)
  const sheet = await screen.findByRole('dialog', {
    name: 'Group details: Standard',
  })
  expect(sheet).toHaveTextContent('ID: othernormal')
  expect(sheet).toHaveClass('w-full', 'sm:max-w-xl')
  await user.keyboard('{Escape}')
  expect(target).toHaveFocus()
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Group access' }),
    'exclusive'
  )
  expect(
    screen.getByRole('button', {
      name: 'Open group details: Access only (limited)',
    })
  ).toBeVisible()
  await user.type(search, 'noSuchGroup')
  expect(screen.getByText('No matching groups')).toBeVisible()
  expect(patch).not.toHaveBeenCalled()
})

it('parks a closed unsaved draft and disables other rows until the draft is discarded', async () => {
  const { user, patch } = open()
  await user.click(
    await screen.findByRole('button', {
      name: 'Open group details: Standard (standard)',
    })
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Display name' }),
    ' draft'
  )
  await user.keyboard('{Escape}')
  expect(
    screen.getByRole('button', {
      name: 'Open group details: Access only (limited)',
    })
  ).toBeDisabled()
  const search = screen.getByRole('textbox', { name: 'Search groups' })
  await user.type(search, 'limited')
  await user.click(screen.getByRole('button', { name: 'Resume' }))
  expect(screen.getByRole('textbox', { name: 'Display name' })).toHaveValue(
    'Standard draft'
  )
  await user.click(
    screen.getByRole('button', { name: 'Discard display draft' })
  )
  await user.keyboard('{Escape}')
  expect(
    screen.getByRole('button', {
      name: 'Open group details: Access only (limited)',
    })
  ).toBeEnabled()
  expect(patch).not.toHaveBeenCalled()
})
