import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

import PartnerApplicationManagementPage from '../management-page'

const base = '/api/admin/partner-applications'
const admin = { id: 41, username: 'reviewer', role: ROLE.ADMIN }
const application = {
  id: 12,
  user_id: 42,
  site_name: 'Synthetic supplier',
  status: 'submitted',
  revision: 1,
  quotes: [],
}

function openManagement(user: AuthUser) {
  useAuthStore.getState().auth.setUser(user)
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === `${base}/settings`) {
      return {
        data: {
          success: true,
          data: {
            revision: 2,
            partner_enabled: true,
            partner_instructions: 'Submit example pricing.',
          },
        },
      }
    }
    if (url === base) {
      return {
        data: { success: true, data: { items: [application], total: 1 } },
      }
    }
    if (url === `${base}/${application.id}`) {
      return { data: { success: true, data: application } }
    }
    throw new Error('Unexpected request')
  })
  render(
    <QueryClientProvider client={new QueryClient()}>
      <PartnerApplicationManagementPage />
    </QueryClientProvider>
  )
  return get
}

afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.setUser(null)
  vi.restoreAllMocks()
})

test('settings readers see current values without loading applications or offering edits', async () => {
  const get = openManagement({
    ...admin,
    permissions: { admin_permissions: { partner_settings: { read: true } } },
  })

  expect(await screen.findByText('Submit example pricing.')).toBeVisible()
  expect(screen.getByText('Enabled')).toBeVisible()
  expect(screen.queryByRole('switch')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Save settings' })
  ).not.toBeInTheDocument()
  expect(get.mock.calls.map(([url]) => url)).toEqual([`${base}/settings`])
})

test('application readers can see detail without fetching settings or seeing review actions', async () => {
  const user = userEvent.setup()
  const get = openManagement({
    ...admin,
    permissions: {
      admin_permissions: { partner_applications: { read: true } },
    },
  })

  await user.click(
    await screen.findByRole('button', { name: 'View application #12' })
  )
  await waitFor(() =>
    expect(get.mock.calls.map(([url]) => url)).toEqual([base, `${base}/12`])
  )
  expect(await screen.findByRole('dialog')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Start review' })
  ).not.toBeInTheDocument()
})

test('write-only grants do not fetch private panels or show management controls', async () => {
  const get = openManagement({
    ...admin,
    permissions: {
      admin_permissions: {
        partner_applications: { read: false, review: true },
        partner_settings: { read: false, update: true },
      },
    },
  })

  expect(await screen.findByText('Access Forbidden')).toBeVisible()
  expect(get).not.toHaveBeenCalled()
})

test.each([
  {
    ...admin,
    permissions: {
      admin_permissions: {
        partner_applications: { read: true, review: true },
        partner_settings: { read: true, update: true },
      },
    },
  },
  { ...admin, username: 'owner', role: ROLE.SUPER_ADMIN },
])(
  'a fully granted admin or root can edit settings and review applications: $username',
  async (account) => {
    const user = userEvent.setup()
    openManagement(account)

    const toggle = await screen.findByRole('switch', {
      name: 'Accept partner applications',
    })
    expect(toggle).toBeEnabled()
    await user.click(toggle)
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Save settings' })
      ).toBeEnabled()
    )
    await user.click(
      await screen.findByRole('button', { name: 'View application #12' })
    )
    expect(
      await screen.findByRole('button', { name: 'Start review' })
    ).toBeVisible()
  }
)
