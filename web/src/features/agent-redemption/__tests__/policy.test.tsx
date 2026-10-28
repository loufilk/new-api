import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { InvitationPolicySettings } from '../components/invitation-policy'

const clients: QueryClient[] = []
function setup(role: number) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'operator', role })
  useSystemConfigStore.getState().setConfig({ currency: { quotaDisplayType: 'USD', quotaPerUnit: 500000, usdExchangeRate: 1 } })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  clients.push(client)
  render(<QueryClientProvider client={client}><InvitationPolicySettings /></QueryClientProvider>)
}
afterEach(() => { for (const client of clients) client.clear(); clients.length = 0; useAuthStore.getState().auth.reset(); vi.restoreAllMocks() })

it.each([ROLE.USER, ROLE.AGENT, ROLE.ADMIN])('does not render or request root-only invitation policy for role %s', (role) => {
  const get = vi.spyOn(api, 'get')
  setup(role)
  expect(screen.queryByRole('switch')).not.toBeInTheDocument()
  expect(get).not.toHaveBeenCalled()
})

it('loads disabled zero grants and prevents enabling a zero grant', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data: { enabled: false, quota: 0, updated_by: 0, updated_at: 0, revision: 0 } } })
  const put = vi.spyOn(api, 'put')
  setup(ROLE.SUPER_ADMIN)
  const enabled = await screen.findByRole('switch', { name: 'Enable invitation test quota' })
  expect(enabled).not.toBeChecked()
  await userEvent.click(enabled)
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(await screen.findByText('Enabled test grants require a positive quota')).toBeInTheDocument()
  expect(put).not.toHaveBeenCalled()
})

it('saves root policy in integer quota units and disables repeat submission', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data: { enabled: false, quota: 0, updated_by: 0, updated_at: 0, revision: 0 } } })
  let finish!: (value: unknown) => void
  const put = vi.spyOn(api, 'put').mockImplementation(() => new Promise((resolve) => { finish = resolve }))
  setup(ROLE.SUPER_ADMIN)
  await userEvent.click(await screen.findByRole('switch', { name: 'Enable invitation test quota' }))
  fireEvent.change(screen.getByRole('spinbutton', { name: 'Test quota (USD)' }), { target: { value: '0.25' } })
  await userEvent.dblClick(screen.getByRole('button', { name: 'Save changes' }))
  expect(screen.getByRole('button', { name: 'Saving...' })).toBeDisabled()
  expect(put).toHaveBeenCalledTimes(1)
  expect(put).toHaveBeenCalledWith('/api/agent-redemption/invitation-policy', { enabled: true, quota: 125000 })
  finish({ data: { success: true, data: { enabled: true, quota: 125000 } } })
  await waitFor(() => expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled())
})
