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
import { act, fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { useState } from 'react'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { expect, test, vi } from 'vitest'

import { Textarea } from '@/components/ui/textarea'
import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'

import { AnnouncementEditorTools } from '../announcement-editor-tools'

function Editor(props: { initial?: string; maxLength?: number }) {
  const [value, setValue] = useState(props.initial ?? 'Old announcement')
  return (
    <>
      <Textarea
        aria-label='Announcement content'
        value={value}
        onChange={(event) => setValue(event.target.value)}
      />
      <AnnouncementEditorTools
        value={value}
        onChange={setValue}
        maxLength={props.maxLength}
      />
    </>
  )
}

test('inserting an HTTPS image preserves existing text and previews only on request', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.type(
    screen.getByLabelText('Image URL'),
    'https://images.example.test/a(1).png?size=wide'
  )
  await user.type(
    screen.getByLabelText('Image description'),
    'Maintenance schedule'
  )
  await user.click(screen.getByRole('button', { name: 'Insert image' }))
  expect(
    screen.getByRole('textbox', { name: 'Announcement content' })
  ).toHaveValue(
    'Old announcement\n\n![Maintenance schedule](https://images.example.test/a%281%29.png?size=wide)'
  )
  expect(screen.queryByRole('img')).toBeNull()
  await user.click(screen.getByRole('button', { name: 'Preview' }))
  const preview = screen.getByRole('region', { name: 'Preview' })
  expect(
    await within(preview).findByRole('img', { name: 'Maintenance schedule' })
  ).toHaveAttribute('referrerpolicy', 'no-referrer')
  expect(preview).toHaveTextContent('Old announcement')
  await user.click(screen.getByRole('button', { name: 'Preview' }))
  expect(screen.queryByRole('region', { name: 'Preview' })).toBeNull()
})

test.each([
  'http://images.example.test/a.png',
  'javascript:alert(1)',
  'data:image/png;base64,a',
  'https://images.example.test/a.svg',
])('unsupported image URL %s does not alter content', async (url) => {
  const user = userEvent.setup()
  render(<Editor />)
  fireEvent.change(screen.getByLabelText('Image URL'), {
    target: { value: url },
  })
  await user.type(screen.getByLabelText('Image description'), 'Schedule')
  await user.click(screen.getByRole('button', { name: 'Insert image' }))
  expect(screen.getByRole('alert')).toBeVisible()
  expect(
    screen.getByRole('textbox', { name: 'Announcement content' })
  ).toHaveValue('Old announcement')
})

test('image insertion respects the total Markdown length limit', async () => {
  const user = userEvent.setup()
  render(<Editor maxLength={20} />)
  fireEvent.change(screen.getByLabelText('Image URL'), {
    target: { value: 'https://images.example.test/a.png' },
  })
  await user.type(screen.getByLabelText('Image description'), 'Schedule')
  await user.click(screen.getByRole('button', { name: 'Insert image' }))
  expect(screen.getByRole('alert')).toHaveTextContent(
    'Content cannot exceed 20 characters.'
  )
  expect(
    screen.getByRole('textbox', { name: 'Announcement content' })
  ).toHaveValue('Old announcement')
})

test('preview reflects later text edits and escaped image descriptions stay text', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  fireEvent.change(screen.getByLabelText('Image URL'), {
    target: { value: 'https://images.example.test/a.png' },
  })
  fireEvent.change(screen.getByLabelText('Image description'), {
    target: { value: 'Schedule [updated] <script>' },
  })
  await user.click(screen.getByRole('button', { name: 'Insert image' }))
  await user.click(screen.getByRole('button', { name: 'Preview' }))
  expect(
    await screen.findByRole('img', { name: 'Schedule [updated] <script>' })
  ).toBeVisible()
  fireEvent.change(
    screen.getByRole('textbox', { name: 'Announcement content' }),
    { target: { value: 'Changed content' } }
  )
  expect(screen.getByRole('region', { name: 'Preview' })).toHaveTextContent(
    'Changed content'
  )
  expect(screen.queryByRole('img')).toBeNull()
})

test('Enter in image fields inserts the image instead of submitting the settings form', async () => {
  const user = userEvent.setup()
  const submit = vi.fn((event) => event.preventDefault())
  render(
    <form onSubmit={submit}>
      <Editor />
    </form>
  )
  fireEvent.change(screen.getByLabelText('Image URL'), {
    target: { value: 'https://images.example.test/a.png' },
  })
  await user.type(
    screen.getByLabelText('Image description'),
    'Keyboard image{Enter}'
  )
  expect(submit).not.toHaveBeenCalled()
  expect(
    screen.getByRole('textbox', { name: 'Announcement content' })
  ).toHaveValue(
    'Old announcement\n\n![Keyboard image](https://images.example.test/a.png)'
  )
})

test('image URL insertion preserves entity-like query parameters in the rendered request URL', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  const url = 'https://images.example.test/a.png?literal=&quot;&width=800'
  fireEvent.change(screen.getByLabelText('Image URL'), {
    target: { value: url },
  })
  fireEvent.change(screen.getByLabelText('Image description'), {
    target: { value: 'Query image' },
  })
  await user.click(screen.getByRole('button', { name: 'Insert image' }))
  await user.click(screen.getByRole('button', { name: 'Preview' }))
  expect(
    await screen.findByRole('img', { name: 'Query image' })
  ).toHaveAttribute('src', url)
})

test('image controls and failure text respond to the selected interface language', async () => {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({
    lng: 'en',
    resources: { en, zh },
    interpolation: { escapeValue: false },
  })
  render(
    <I18nextProvider i18n={i18n}>
      <Editor initial='![Schedule](http://images.example.test/a.png)' />
    </I18nextProvider>
  )
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: en.translation.Preview }))
  expect(
    await screen.findByText(`${en.translation['Image unavailable']}: Schedule`)
  ).toBeVisible()
  await act(() => i18n.changeLanguage('zh'))
  expect(
    screen.getByRole('button', { name: zh.translation['Insert image'] })
  ).toBeVisible()
  expect(
    screen.getByLabelText(zh.translation['Image description'])
  ).toBeVisible()
  expect(
    screen.getByText(`${zh.translation['Image unavailable']}: Schedule`)
  ).toBeVisible()
})
