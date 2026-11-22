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
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { getSupportTicketAttachment } from '../api'
import { SupportAttachments } from '../components/support-attachments'

vi.mock('../api', () => ({ getSupportTicketAttachment: vi.fn() }))
const fetchImage = vi.mocked(getSupportTicketAttachment)
const attachment = {
  id: 'private-id',
  media_type: 'image/png',
  size_bytes: 3,
  width: 1,
  height: 1,
}

beforeEach(() => {
  fetchImage.mockReset()
  useAuthStore.getState().auth.setUser({ id: 1, username: 'owner', role: 1 })
  vi.stubGlobal(
    'URL',
    class extends URL {
      static createObjectURL = vi.fn(() => 'blob:private-synthetic')
      static revokeObjectURL = vi.fn()
    }
  )
})
afterEach(() => {
  useAuthStore.getState().auth.reset()
  vi.unstubAllGlobals()
})

test('private image uses a fetched Blob instead of a public URL and cleans up on unmount', async () => {
  fetchImage.mockResolvedValue(new Blob(['png'], { type: 'image/png' }))
  const { unmount } = render(
    <SupportAttachments admin={false} ticketId={7} attachments={[attachment]} />
  )
  const thumbnail = await screen.findByAltText('Support attachment 1')
  expect(thumbnail).toHaveAttribute('src', 'blob:private-synthetic')
  expect(fetchImage).toHaveBeenCalledWith(
    false,
    7,
    'private-id',
    expect.any(AbortSignal)
  )
  await userEvent
    .setup()
    .click(screen.getByRole('button', { name: 'View support attachment 1' }))
  expect(await screen.findByRole('dialog')).toBeVisible()
  await userEvent.setup().keyboard('{Escape}')
  unmount()
  expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:private-synthetic')
  expect(fetchImage.mock.calls[0][3]?.aborted).toBe(true)
})

test('changing account aborts the prior download and never displays its late response', async () => {
  let finish!: (blob: Blob) => void
  fetchImage.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  fetchImage.mockRejectedValueOnce(new Error('unauthorized'))
  render(
    <SupportAttachments admin={false} ticketId={7} attachments={[attachment]} />
  )
  await waitFor(() => expect(fetchImage).toHaveBeenCalledOnce())
  const oldSignal = fetchImage.mock.calls[0][3]
  act(() =>
    useAuthStore.getState().auth.setUser({ id: 2, username: 'other', role: 1 })
  )
  await waitFor(() => expect(fetchImage).toHaveBeenCalledTimes(2))
  expect(oldSignal?.aborted).toBe(true)
  await act(async () => finish(new Blob(['png'], { type: 'image/png' })))
  expect(await screen.findByText('Unable to load private image.')).toBeVisible()
  expect(URL.createObjectURL).not.toHaveBeenCalled()
  expect(screen.queryByRole('img')).not.toBeInTheDocument()
})

test('a failed private fetch has a local retry and admin downloads use the admin scope', async () => {
  fetchImage
    .mockRejectedValueOnce(new Error('failed'))
    .mockResolvedValueOnce(new Blob(['png'], { type: 'image/png' }))
  render(<SupportAttachments admin ticketId={7} attachments={[attachment]} />)
  expect(await screen.findByText('Unable to load private image.')).toBeVisible()
  await userEvent.setup().click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByAltText('Support attachment 1')).toBeVisible()
  expect(fetchImage.mock.calls.every(([admin]) => admin)).toBe(true)
})
