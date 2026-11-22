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
import { describe, expect, test } from 'vitest'

import { StaticArtwork } from '../static-artwork'
import { buildStaticArtworkDocument } from '../static-artwork-document'

describe('static model artwork isolation', () => {
  test('strips scripts, navigation, forms and SVG reference mutation', () => {
    const document = new DOMParser().parseFromString(
      buildStaticArtworkDocument(
        `<script>parent.alert(1)</script><meta http-equiv="refresh" content="0;url=https://evil.example"><base href="https://evil.example"><form action="/api/user"><input></form><a href="https://evil.example" target="_top">link</a><img src="https://evil.example"><svg onload="alert(1)"><foreignObject><iframe srcdoc="evil"></iframe></foreignObject><a href="https://evil.example"><animate attributeName="href" to="javascript:alert(1)" /></a><use href="https://evil.example/a.svg#x"/><circle cx="5" cy="5" r="4" /></svg>`
      ),
      'text/html'
    )
    expect(
      document.querySelector(
        'script, form, iframe, input, a, img, base, use, animate, foreignObject'
      )
    ).toBeNull()
    expect(
      document.querySelector('[href], [src], [onload], [target]')
    ).toBeNull()
    expect(document.querySelector('circle')).not.toBeNull()
    const csp = document
      .querySelector('meta[http-equiv="Content-Security-Policy"]')
      ?.getAttribute('content')
    expect(csp).toContain("default-src 'none'")
    expect(csp).toContain("script-src 'none'")
    expect(csp).toContain("connect-src 'none'")
    expect(csp).toContain("form-action 'none'")
    expect(document.querySelector('meta[http-equiv="refresh"]')).toBeNull()
  })
  test('preserves static SVG and document CSS in full HTML artwork', () => {
    const html = buildStaticArtworkDocument(
      '<!doctype html><html><head><style>.bird{color:orange}</style></head><body><svg class="bird"><path d="M0 0L10 10" /></svg></body></html>'
    )
    expect(html).toContain('.bird{color:orange}')
    expect(html).toContain('<path')
  })
  test('uses an opaque-origin iframe without scripts or same-origin permission', () => {
    const { container } = render(
      <StaticArtwork html='<svg><circle r="5"/></svg><script>evil()</script>' />
    )
    const iframe = screen.getByTitle('intelligence.artwork')
    expect(iframe).toHaveAttribute('sandbox', '')
    expect(iframe).toHaveAttribute('referrerpolicy', 'no-referrer')
    expect(iframe).not.toHaveAttribute('src')
    expect(iframe.getAttribute('srcdoc')).toContain("default-src 'none'")
    expect(container.querySelector('script, svg')).toBeNull()
  })
})
