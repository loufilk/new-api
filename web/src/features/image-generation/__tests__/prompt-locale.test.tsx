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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { GenerationForm } from '../components/GenerationForm'
import { IMAGE_PROMPT_EXAMPLE_KEYS } from '../constants'

const baseValue = {
  prompt: '',
  apiKeyId: 1,
  model: 'dall-e-3',
  size: '1024x1024',
  quality: 'standard',
  style: 'vivid',
  count: 1,
}

function renderForm(onChange: (patch: { prompt?: string }) => void) {
  return render(
    <GenerationForm
      value={baseValue}
      onChange={onChange}
      keys={[
        {
          id: 1,
          name: 'key-1',
          group: 'default',
          model_limits_enabled: false,
          model_limits: '',
          status: 1,
        },
      ]}
      models={['dall-e-3']}
      sizes={['1024x1024']}
      qualities={['standard', 'hd']}
      styles={['vivid', 'natural']}
      maxCount={1}
      maxReferenceImages={0}
      references={[]}
      referenceErrorKey={null}
      onAddReferenceFiles={() => undefined}
      onRemoveReference={() => undefined}
      isGenerating={false}
      formError={null}
      isInitializing={false}
      onGenerate={() => undefined}
      onCancel={() => undefined}
    />
  )
}

afterEach(async () => {
  await i18next.changeLanguage('en')
})

describe('prompt example chips write translated text', () => {
  test('writes the translated prompt instead of the English key in a non-English locale', async () => {
    const user = userEvent.setup()
    const englishKey = IMAGE_PROMPT_EXAMPLE_KEYS[0]
    const translated = 'TRANSLATED-LAKE-PROMPT-唯一'
    await i18next.changeLanguage('en')
    i18next.addResource('en', 'translation', englishKey, englishKey)
    await i18next.changeLanguage('zh')
    i18next.addResource('zh', 'translation', englishKey, translated)

    const onChange = vi.fn()
    renderForm(onChange)

    await user.click(screen.getByRole('button', { name: translated }))

    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange).toHaveBeenCalledWith({ prompt: translated })
    expect(onChange.mock.calls[0][0].prompt).not.toBe(englishKey)
  })

  test('writes English text when the locale is English', async () => {
    const user = userEvent.setup()
    await i18next.changeLanguage('en')
    const englishKey = IMAGE_PROMPT_EXAMPLE_KEYS[1]
    i18next.addResource('en', 'translation', englishKey, englishKey)

    const onChange = vi.fn()
    renderForm(onChange)

    await user.click(screen.getByRole('button', { name: englishKey }))

    expect(onChange).toHaveBeenCalledWith({ prompt: englishKey })
  })
})
