import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
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
  quotes: [
    {
      platform: 'openai',
      group: 'synthetic-group',
      rate: '0.375',
      models: 'model-alpha',
      description: 'Quote-only notes',
    },
  ],
  contact_type: 'wechat',
  contact: 'Synthetic request contact',
  description: 'Synthetic site notes',
  review_note: 'Private reviewer note',
  events: [
    { id: 999, to_status: 'submitted', actor_id: 42, created_at: 1780000000 },
  ],
}

function openManagement(user: AuthUser, detail = () => application) {
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
      return { data: { success: true, data: detail() } }
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
  const panel = within(await screen.findByRole('dialog'))
  expect(
    panel.getByText('This application is read-only for your account.')
  ).toBeVisible()
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

test('opens a responsive detail after the queue, keeps contact out of the list, and returns focus to the row', async () => {
  const user = userEvent.setup()
  openManagement({ ...admin, role: ROLE.SUPER_ADMIN })
  const list = await screen.findByRole('table')
  const settings = await screen.findByRole('switch', {
    name: 'Accept partner applications',
  })
  expect(
    list.compareDocumentPosition(settings) & Node.DOCUMENT_POSITION_FOLLOWING
  ).toBeTruthy()
  expect(
    screen.queryByText(/Synthetic request contact|Private reviewer note/)
  ).not.toBeInTheDocument()
  const trigger = within(list).getByRole('button', {
    name: 'View application #12',
  })
  await user.click(trigger)
  const drawer = await screen.findByRole('dialog', {
    name: 'Partner application #12',
  })
  expect(drawer).toHaveClass('w-full', 'sm:max-w-2xl')
  const detail = within(drawer)
  const site = detail.getByRole('heading', { name: 'Site information' })
  const quotes = detail.getByRole('heading', { name: 'Submitted quotes' })
  const history = detail.getByRole('heading', { name: 'Application history' })
  expect(
    site.compareDocumentPosition(quotes) & Node.DOCUMENT_POSITION_FOLLOWING
  ).toBeTruthy()
  expect(
    quotes.compareDocumentPosition(history) & Node.DOCUMENT_POSITION_FOLLOWING
  ).toBeTruthy()
  expect(detail.getByText(/Synthetic request contact/)).toBeVisible()
  expect(detail.getByText('×0.375')).toBeVisible()
  expect(detail.getByText('Quote-only notes')).toBeVisible()
  expect(detail.getByText(/Private reviewer note/)).toBeVisible()
  await user.keyboard('{Escape}')
  await waitFor(() => expect(trigger).toHaveFocus())
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

test('a conflicting review preserves its note and requires an explicit reload before another decision', async () => {
  const user = userEvent.setup()
  let reads = 0
  openManagement({ ...admin, role: ROLE.SUPER_ADMIN }, () => ({
    ...application,
    status: 'reviewing',
    revision: ++reads,
  }))
  const post = vi
    .spyOn(api, 'post')
    .mockRejectedValueOnce({
      response: { status: 409, data: { success: false } },
    })
    .mockResolvedValueOnce({
      data: {
        success: true,
        data: { ...application, status: 'approved', revision: 3 },
      },
    })
  await user.click(
    await screen.findByRole('button', { name: 'View application #12' })
  )
  const panel = within(
    await screen.findByRole('dialog', { name: 'Partner application #12' })
  )
  const note = panel.getByLabelText('Review note')
  await user.type(note, 'Keep this review draft')
  await user.click(panel.getByRole('button', { name: 'Approve application' }))
  const refresh = await panel.findByRole('button', {
    name: 'Reload latest application',
  })
  expect(
    panel.getByRole('button', { name: 'Approve application' })
  ).toBeDisabled()
  expect(note).toHaveValue('Keep this review draft')
  expect(reads).toBe(1)
  expect(post).toHaveBeenCalledTimes(1)
  expect(post.mock.calls[0]).toEqual([
    `${base}/12/review`,
    { action: 'approve', expected_revision: 1, note: 'Keep this review draft' },
  ])
  await user.click(refresh)
  const approve = panel.getByRole('button', { name: 'Approve application' })
  await waitFor(() => expect(approve).toBeEnabled())
  expect(post).toHaveBeenCalledTimes(1)
  expect(note).toHaveValue('Keep this review draft')
  await user.click(approve)
  await waitFor(() => expect(post).toHaveBeenCalledTimes(2))
  expect(post.mock.calls[1]).toEqual([
    `${base}/12/review`,
    { action: 'approve', expected_revision: 2, note: 'Keep this review draft' },
  ])
})
