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
import { expect, test, vi } from 'vitest'

import { PromptInput, PromptInputTextarea } from '../prompt-input'

test('long mixed-language input wraps without modifying the submitted text', async () => {
  const text = `https://example.test/${'x'.repeat(200)} 中文\nconst a = 1`
  const submit = vi.fn()
  render(
    <PromptInput onSubmit={submit}>
      <PromptInputTextarea aria-label='Prompt' />
    </PromptInput>
  )
  const input = screen.getByRole('textbox', { name: 'Prompt' })
  expect(input).toHaveClass('break-all', 'max-h-48')
  fireEvent.change(input, { target: { value: text } })
  expect(input).toHaveValue(text)
  fireEvent.compositionStart(input)
  fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' })
  expect(submit).not.toHaveBeenCalled()
  fireEvent.compositionEnd(input)
  fireEvent.keyDown(input, { key: 'Enter', code: 'Enter', shiftKey: true })
  expect(submit).not.toHaveBeenCalled()
  fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' })
  await waitFor(() => expect(submit).toHaveBeenCalledTimes(1))
  expect(submit).toHaveBeenCalledWith({ text, files: [] }, expect.anything())
})
