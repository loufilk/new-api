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
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { DocsSearch } from '../docs-search'

const { navigate } = vi.hoisted(() => ({ navigate: vi.fn() }))

vi.mock('@tanstack/react-router', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@tanstack/react-router')>()),
  useNavigate: () => navigate,
}))

beforeEach(() => navigate.mockClear())
afterEach(cleanup)

test('uses the Docs keyboard entry, then opens the matched section', async () => {
  const user = userEvent.setup()
  render(<DocsSearch />)
  const trigger = screen.getByRole('button', { name: 'Search documentation' })
  expect(trigger).toHaveAttribute('aria-keyshortcuts', 'Control+k Meta+k')

  fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
  const input = await screen.findByRole('combobox', {
    name: 'Search documentation',
  })
  await user.type(input, '状态码速查')
  await user.click(await screen.findByRole('option', { name: /状态码速查/ }))
  expect(navigate).toHaveBeenCalledWith({
    to: '/help/guides/$slug',
    params: { slug: 'troubleshooting' },
    hash: 'section-状态码速查',
  })
  await waitFor(() =>
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
  )
})

test('recovers from no results, supports a touch close, and keeps API links', async () => {
  const user = userEvent.setup()
  render(<DocsSearch />)
  const trigger = screen.getByRole('button', { name: 'Search documentation' })
  await user.click(trigger)
  const input = await screen.findByRole('combobox', {
    name: 'Search documentation',
  })
  await user.type(input, 'unknown-match-7f3d39')
  expect(await screen.findByText('No results found.')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Close' }))
  await waitFor(() =>
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
  )
  expect(navigate).not.toHaveBeenCalled()

  await user.click(trigger)
  const reopened = await screen.findByRole('combobox', {
    name: 'Search documentation',
  })
  expect(reopened).toHaveValue('')
  await user.type(reopened, 'POST /v1/responses')
  await user.click(
    await screen.findByText('POST /v1/responses', { exact: true })
  )
  expect(navigate).toHaveBeenCalledWith({
    to: '/help/api',
    search: { endpoint: 'responses' },
  })
})

test('localizes the explicit touch close without changing shared dialogs', async () => {
  const instance = createInstance()
  await instance.init({
    lng: 'zh',
    initAsync: false,
    resources: {
      zh: {
        translation: { Close: '关闭', 'Search documentation': '搜索文档' },
      },
    },
  })
  const user = userEvent.setup()
  render(
    <I18nextProvider i18n={instance}>
      <DocsSearch />
    </I18nextProvider>
  )
  await user.click(screen.getByRole('button', { name: '搜索文档' }))
  await user.click(await screen.findByRole('button', { name: '关闭' }))
  await waitFor(() =>
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
  )
})
