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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { CreateTicketDialog } from '../components/create-ticket-dialog'
import type { SupportTicketCreateSubmission } from '../types'

function setup(
  onSubmit = vi.fn(
    async (_values: SupportTicketCreateSubmission): Promise<void> => undefined
  )
) {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'customer',
    role: 1,
    email: 'customer@example.com',
  })
  const onOpenChange = vi.fn()
  return {
    onSubmit,
    onOpenChange,
    user: userEvent.setup(),
    ...render(
      <CreateTicketDialog
        open
        pending={false}
        onOpenChange={onOpenChange}
        onSubmit={onSubmit}
      />
    ),
  }
}

beforeEach(() => {
  vi.stubGlobal(
    'URL',
    class extends URL {
      static createObjectURL = vi.fn(() => 'blob:synthetic-preview')
      static revokeObjectURL = vi.fn()
    }
  )
})
afterEach(() => {
  useAuthStore.getState().auth.reset()
  vi.unstubAllGlobals()
})

test('new ticket defaults account email and lets customer choose problem category or explicitly clear contact email', async () => {
  const { user, onSubmit } = setup()
  expect(screen.getByLabelText('Contact email (optional)')).toHaveValue(
    'customer@example.com'
  )
  await user.clear(screen.getByLabelText('Contact email (optional)'))
  await user.type(screen.getByLabelText('Subject'), 'Account access issue')
  await user.type(
    screen.getByLabelText('Message'),
    'Please review my account settings.'
  )
  await user.selectOptions(
    screen.getByLabelText('Problem type'),
    'account_access'
  )
  await user.click(screen.getByRole('button', { name: 'Create ticket' }))
  await waitFor(() => expect(onSubmit).toHaveBeenCalledOnce())
  expect(onSubmit.mock.calls[0][0]).toMatchObject({
    category: 'account_access',
    contact_email: '',
    subject: 'Account access issue',
  })
})

test('image selection rejects more than four files and canceling clears previews', async () => {
  const { user, onSubmit, rerender, onOpenChange } = setup()
  const input = document.querySelector('input[type=file]') as HTMLInputElement
  const files = Array.from(
    { length: 5 },
    (_, index) => new File(['png'], `${index}.png`, { type: 'image/png' })
  )
  fireEvent.change(input, { target: { files } })
  expect(
    await screen.findByText('Attach at most 4 images per message.')
  ).toBeVisible()
  expect(onSubmit).not.toHaveBeenCalled()
  fireEvent.change(input, { target: { files: files.slice(0, 1) } })
  expect(await screen.findByAltText(/Support image/)).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(onOpenChange).toHaveBeenCalledWith(false)
  rerender(
    <CreateTicketDialog
      open={false}
      pending={false}
      onOpenChange={onOpenChange}
      onSubmit={onSubmit}
    />
  )
  await waitFor(() =>
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:synthetic-preview')
  )
})

test('failed submission retains files and text and reuses its UUID only for unchanged retry', async () => {
  const onSubmit = vi.fn(async (_values: SupportTicketCreateSubmission) => {
    throw new Error('uncertain delivery')
  })
  const { user } = setup(onSubmit)
  await user.type(screen.getByLabelText('Subject'), 'Billing issue')
  await user.type(
    screen.getByLabelText('Message'),
    'Please check this synthetic request.'
  )
  fireEvent.change(
    document.querySelector('input[type=file]') as HTMLInputElement,
    {
      target: {
        files: [new File(['png'], 'synthetic.png', { type: 'image/png' })],
      },
    }
  )
  await user.click(screen.getByRole('button', { name: 'Create ticket' }))
  await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
  expect(screen.getByLabelText('Message')).toHaveValue(
    'Please check this synthetic request.'
  )
  expect(screen.getByAltText(/Support image/)).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Create ticket' }))
  await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(2))
  expect(onSubmit.mock.calls[1][0].request_id).toBe(
    onSubmit.mock.calls[0][0].request_id
  )
  expect(onSubmit.mock.calls[1][0].request_id).toMatch(/^[0-9a-f-]{36}$/)
  await user.type(screen.getByLabelText('Message'), ' Changed.')
  await user.click(screen.getByRole('button', { name: 'Create ticket' }))
  await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(3))
  expect(onSubmit.mock.calls[2][0].request_id).not.toBe(
    onSubmit.mock.calls[0][0].request_id
  )
})

test('pending request prevents duplicate submit and accidental dialog dismissal', async () => {
  let finish!: () => void
  const onSubmit = vi.fn(
    (_values: SupportTicketCreateSubmission) =>
      new Promise<void>((resolve) => {
        finish = resolve
      })
  )
  const { user, onOpenChange } = setup(onSubmit)
  await user.type(screen.getByLabelText('Subject'), 'Synthetic request')
  await user.type(screen.getByLabelText('Message'), 'A required description.')
  await user.click(screen.getByRole('button', { name: 'Create ticket' }))
  await waitFor(() => expect(onSubmit).toHaveBeenCalledOnce())
  fireEvent.submit(document.querySelector('form') as HTMLFormElement)
  await user.keyboard('{Escape}')
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
  expect(onOpenChange).not.toHaveBeenCalled()
  expect(onSubmit).toHaveBeenCalledOnce()
  finish()
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Create ticket' })
    ).not.toBeDisabled()
  )
})

test('image picker is keyboard operable but an image alone cannot create a ticket', async () => {
  const { user, onSubmit } = setup()
  const input = document.querySelector('input[type=file]') as HTMLInputElement
  const click = vi.spyOn(input, 'click')
  screen.getByRole('button', { name: 'Add support images' }).focus()
  await user.keyboard('{Enter}')
  expect(click).toHaveBeenCalledOnce()
  fireEvent.change(input, {
    target: {
      files: [new File(['png'], 'synthetic.png', { type: 'image/png' })],
    },
  })
  await user.click(screen.getByRole('button', { name: 'Create ticket' }))
  expect(
    await screen.findByText(
      'Message is required and must not exceed 10000 characters'
    )
  ).toBeVisible()
  expect(onSubmit).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Remove synthetic.png' }))
  expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:synthetic-preview')
})
