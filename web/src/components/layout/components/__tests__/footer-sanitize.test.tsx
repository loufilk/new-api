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
import { beforeEach, describe, expect, test, vi } from 'vitest'

const systemConfig = {
  systemName: 'New API',
  logo: '/logo.png',
  footerHtml: undefined as string | undefined,
  demoSiteEnabled: false,
  loading: false,
  logoLoaded: true,
}

vi.mock('@/hooks/use-status', () => ({
  useStatus: () => ({ status: null }),
}))

vi.mock('@/hooks/use-system-config', () => ({
  useSystemConfig: () => systemConfig,
}))

const { Footer } = await import('../footer')

beforeEach(() => {
  systemConfig.footerHtml = undefined
})

describe('footer custom HTML sanitization', () => {
  test('keeps normal admin HTML visible', () => {
    systemConfig.footerHtml = '<p>Custom <strong>footer</strong> text</p>'

    const { container } = render(<Footer />)

    expect(container.querySelector('.custom-footer')?.textContent).toContain(
      'Custom footer text'
    )
    expect(container.querySelector('.custom-footer strong')).not.toBeNull()
  })

  test('strips script tags from admin HTML', () => {
    systemConfig.footerHtml = '<p>hello</p><script>alert("xss")</script>'

    const { container } = render(<Footer />)

    expect(container.querySelector('.custom-footer script')).toBeNull()
    expect(container.querySelector('.custom-footer')?.textContent).toContain(
      'hello'
    )
  })

  test('strips event handlers and javascript: URLs', () => {
    systemConfig.footerHtml =
      '<a href="javascript:alert(1)">click</a><img src="x" onerror="alert(1)">'

    const { container } = render(<Footer />)
    const html = container.querySelector('.custom-footer')?.innerHTML ?? ''

    expect(html.includes('onerror')).toBe(false)
    expect(html.includes('javascript:')).toBe(false)
    expect(container.querySelector('.custom-footer')?.textContent).toContain(
      'click'
    )
  })
})
