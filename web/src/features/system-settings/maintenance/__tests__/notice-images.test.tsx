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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { NoticeSection } from '../notice-section'

function NoticeFixture() {
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  return (
    <>
      <div ref={setActions} />
      <SettingsPageProvider actionsContainer={actions}>
        <NoticeSection defaultValue='Existing notice' />
      </SettingsPageProvider>
    </>
  )
}

test('system notice uses the same image insertion and preview, then saves Markdown to the existing option', async () => {
  const request = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const client = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <NoticeFixture />
    </QueryClientProvider>
  )
  const user = userEvent.setup()
  fireEvent.change(screen.getByLabelText('Image URL'), {
    target: { value: 'https://images.example.test/notice.png' },
  })
  await user.type(screen.getByLabelText('Image description'), 'Notice image')
  await user.click(screen.getByRole('button', { name: 'Insert image' }))
  await user.click(screen.getByRole('button', { name: 'Preview' }))
  expect(
    await screen.findByRole('img', { name: 'Notice image' })
  ).toHaveAttribute('referrerpolicy', 'no-referrer')
  expect(request).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Save notice' }))
  await waitFor(() =>
    expect(request).toHaveBeenCalledWith('/api/option/', {
      key: 'Notice',
      value:
        'Existing notice\n\n![Notice image](https://images.example.test/notice.png)',
    })
  )
})
