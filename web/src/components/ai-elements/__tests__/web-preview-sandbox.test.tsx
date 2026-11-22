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
import { render } from '@testing-library/react'
import { describe, expect, test } from 'vitest'

import { WebPreview, WebPreviewBody, WEB_PREVIEW_SANDBOX } from '../web-preview'

function sandboxTokens(value: string | null | undefined): string[] {
  return (value ?? '').split(/\s+/).filter(Boolean)
}

describe('web preview sandbox contract', () => {
  test('never combines allow-scripts with allow-same-origin', () => {
    const tokens = sandboxTokens(WEB_PREVIEW_SANDBOX)

    expect(tokens.includes('allow-scripts')).toBe(true)
    expect(
      tokens.includes('allow-scripts') && tokens.includes('allow-same-origin')
    ).toBe(false)
  })

  test('renders the preview iframe with the least-privilege sandbox', () => {
    render(
      <WebPreview defaultUrl='https://example.com/preview'>
        <WebPreviewBody />
      </WebPreview>
    )

    const frame = document.querySelector('iframe')
    const tokens = sandboxTokens(frame?.getAttribute('sandbox'))

    expect(frame?.getAttribute('src')).toBe('https://example.com/preview')
    expect(tokens).toEqual(WEB_PREVIEW_SANDBOX.split(' '))
    expect(
      tokens.includes('allow-scripts') && tokens.includes('allow-same-origin')
    ).toBe(false)
  })
})
