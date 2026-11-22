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
  act,
  cleanup,
  fireEvent,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError } from 'axios'
import { afterEach, expect, it, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { callInterceptionKey } from '../api'
import { config, renderInterception, setupRequests } from './fixtures'

async function saveSettings() {
  await userEvent.click(
    screen.getByRole('button', { name: 'Save interception settings' })
  )
  await userEvent.click(
    await screen.findByRole('button', { name: 'Apply settings' })
  )
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it('starts disabled and rejects enabling without a nonblank reply', async () => {
  const { put } = setupRequests()
  renderInterception()
  const toggle = await screen.findByRole('switch', {
    name: 'Enable call interception',
  })
  expect(toggle).not.toBeChecked()
  toggle.focus()
  await userEvent.keyboard(' ')
  expect(toggle).toBeChecked()
  await userEvent.click(
    screen.getByRole('button', { name: 'Save interception settings' })
  )
  const reply = screen.getByRole('textbox', { name: 'Custom reply' })
  await waitFor(() => expect(reply).toHaveAttribute('aria-invalid', 'true'))
  expect(screen.getByRole('alert')).toHaveTextContent(
    'A custom reply is required when call interception is enabled'
  )
  expect(put).not.toHaveBeenCalled()
})

it('saves exact multiline bilingual text with the loaded revision after confirmation', async () => {
  const { put } = setupRequests({ ...config, revision: 4 })
  renderInterception()
  await userEvent.click(
    await screen.findByRole('switch', { name: 'Enable call interception' })
  )
  const reply = screen.getByRole('textbox', { name: 'Custom reply' })
  fireEvent.change(reply, {
    target: {
      value: '请联系管理员。\nPlease contact your administrator.\n谢谢。',
    },
  })
  await saveSettings()
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/user/12/call-interception', {
      enabled: true,
      reply: '请联系管理员。\nPlease contact your administrator.\n谢谢。',
      expected_revision: 4,
    })
  )
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Save interception settings' })
    ).toBeDisabled()
  )
  expect(screen.getByText('Configuration revision: 5')).toBeInTheDocument()
})

it('turns interception off without clearing the saved reply', async () => {
  const { put } = setupRequests({
    ...config,
    enabled: true,
    reply: 'Keep this draft',
    revision: 2,
  })
  renderInterception()
  await userEvent.click(
    await screen.findByRole('switch', { name: 'Enable call interception' })
  )
  await saveSettings()
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/user/12/call-interception', {
      enabled: false,
      reply: 'Keep this draft',
      expected_revision: 2,
    })
  )
})

it('rejects a reply exceeding the UTF-8 byte limit even when its character count is smaller', async () => {
  const { put } = setupRequests()
  renderInterception()
  const reply = await screen.findByRole('textbox', { name: 'Custom reply' })
  fireEvent.change(reply, { target: { value: '汉'.repeat(5334) } })
  await userEvent.click(
    screen.getByRole('button', { name: 'Save interception settings' })
  )
  await waitFor(() => expect(reply).toHaveAttribute('aria-invalid', 'true'))
  expect(screen.getByRole('alert')).toHaveTextContent(
    'Custom reply must not exceed 16000 UTF-8 bytes'
  )
  expect(put).not.toHaveBeenCalled()
})

it('keeps unsaved text after a revision conflict and reloads only after explicit discard confirmation', async () => {
  const { get, put } = setupRequests({
    ...config,
    reply: 'Saved reply',
    revision: 3,
  })
  put.mockRejectedValue(
    new AxiosError('Conflict', 'ERR_BAD_RESPONSE', undefined, undefined, {
      data: {
        success: false,
        message:
          'Call interception configuration changed; reload and try again',
      },
      status: 409,
      statusText: 'Conflict',
      headers: {},
      config: { headers: {} } as never,
    })
  )
  renderInterception()
  const reply = await screen.findByRole('textbox', { name: 'Custom reply' })
  fireEvent.change(reply, { target: { value: 'Unsaved reply' } })
  await saveSettings()
  await screen.findByText(
    'Call interception configuration changed; reload and try again'
  )
  expect(reply).toHaveValue('Unsaved reply')
  expect(get).toHaveBeenCalledTimes(1)
  await userEvent.click(screen.getByRole('button', { name: 'Reload settings' }))
  await userEvent.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Cancel',
    })
  )
  expect(reply).toHaveValue('Unsaved reply')
  get.mockResolvedValue({
    data: {
      success: true,
      data: {
        ...config,
        enabled: true,
        reply: 'Latest saved reply',
        revision: 4,
      },
    },
  })
  await userEvent.click(screen.getByRole('button', { name: 'Reload settings' }))
  await userEvent.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Reload settings',
    })
  )
  await waitFor(() => expect(reply).toHaveValue('Latest saved reply'))
  expect(
    screen.getByRole('switch', { name: 'Enable call interception' })
  ).toBeChecked()
})

it('keeps draft text when reload fails instead of replacing the editor with an error state', async () => {
  const { get } = setupRequests()
  renderInterception()
  const reply = await screen.findByRole('textbox', { name: 'Custom reply' })
  fireEvent.change(reply, { target: { value: 'Do not lose this' } })
  get.mockRejectedValue(new Error('Offline'))
  await userEvent.click(screen.getByRole('button', { name: 'Reload settings' }))
  await userEvent.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Reload settings',
    })
  )
  await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  await waitFor(() =>
    expect(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Cancel',
      })
    ).toBeEnabled()
  )
  await userEvent.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Cancel',
    })
  )
  expect(reply).toHaveValue('Do not lose this')
  expect(screen.getByRole('alert')).toHaveTextContent(
    'Failed to load interception settings'
  )
})

it('prevents duplicate writes and disables fields while saving', async () => {
  const { put } = setupRequests()
  let resolveSave!: (value: unknown) => void
  put.mockImplementation(
    () =>
      new Promise((resolve) => {
        resolveSave = resolve
      })
  )
  renderInterception()
  const reply = await screen.findByRole('textbox', { name: 'Custom reply' })
  fireEvent.change(reply, { target: { value: 'Pending reply' } })
  await userEvent.click(
    screen.getByRole('button', { name: 'Save interception settings' })
  )
  const apply = screen.getByRole('button', { name: 'Apply settings' })
  await userEvent.dblClick(apply)
  expect(put).toHaveBeenCalledTimes(1)
  expect(reply).toBeDisabled()
  expect(apply).toBeDisabled()
  resolveSave({
    data: {
      success: true,
      data: { ...config, reply: 'Pending reply', revision: 1 },
    },
  })
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
})

it('treats a business-level save failure as failure and preserves the editable draft', async () => {
  const { put } = setupRequests()
  put.mockResolvedValue({
    data: {
      success: false,
      message: 'Call interception operation failed, please try again later',
    },
  })
  renderInterception()
  const reply = await screen.findByRole('textbox', { name: 'Custom reply' })
  fireEvent.change(reply, { target: { value: 'Preserved on failure' } })
  await saveSettings()
  await screen.findByText(
    'Call interception operation failed, please try again later'
  )
  expect(reply).toHaveValue('Preserved on failure')
  expect(
    screen.getByRole('button', { name: 'Save interception settings' })
  ).toBeEnabled()
})

it('adopts a newer server revision while pristine after a concurrent view saves', async () => {
  const { get } = setupRequests()
  const { client } = renderInterception()
  const reply = await screen.findByRole('textbox', { name: 'Custom reply' })
  get.mockResolvedValue({
    data: {
      success: true,
      data: {
        ...config,
        enabled: true,
        reply: 'Saved in another view',
        revision: 1,
      },
    },
  })
  await act(async () => {
    await client.invalidateQueries({ queryKey: callInterceptionKey(12) })
  })
  await waitFor(() => expect(reply).toHaveValue('Saved in another view'))
  expect(
    screen.getByRole('switch', { name: 'Enable call interception' })
  ).toBeChecked()
  expect(screen.getByText('Configuration revision: 1')).toBeInTheDocument()
})

it('keeps a draft and its original revision when another view refreshes newer settings', async () => {
  const { get, put } = setupRequests()
  const { client } = renderInterception()
  const reply = await screen.findByRole('textbox', { name: 'Custom reply' })
  fireEvent.change(reply, { target: { value: 'My unsaved draft' } })
  get.mockResolvedValue({
    data: {
      success: true,
      data: { ...config, reply: 'New server reply', revision: 1 },
    },
  })
  await act(async () => {
    await client.invalidateQueries({ queryKey: callInterceptionKey(12) })
  })
  expect(reply).toHaveValue('My unsaved draft')
  await screen.findByText(
    'Call interception configuration changed; reload and try again'
  )
  await saveSettings()
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/user/12/call-interception', {
      enabled: false,
      reply: 'My unsaved draft',
      expected_revision: 0,
    })
  )
})
