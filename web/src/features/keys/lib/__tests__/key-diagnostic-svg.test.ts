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
import { expect, test } from 'vitest'

import { diagnosticSvgImage } from '../key-diagnostic-svg'

test('renders bounded SVG image data while omitting all script, remote resources and HTML', () => {
  const uri = diagnosticSvgImage(
    '<html><iframe src="https://untrusted.invalid/outer"></iframe><svg xmlns="http://www.w3.org/2000/svg" onload="response-data" viewBox="0 0 100 100"><style>@import "https://untrusted.invalid/body";</style><script>response-data</script><foreignObject><img src="https://untrusted.invalid/x"/></foreignObject><image href="https://untrusted.invalid/s"/><a href="https://untrusted.invalid"><path d="M1 1"/></a><defs><linearGradient id="local"><stop offset="0" stop-color="red"/></linearGradient></defs><rect id="wheel" fill="url(#local)" width="16" height="16" style="background:url(https://untrusted.invalid/p)"/><animate attributeName="href" values="https://untrusted.invalid/animate"/></svg></html>'
  )
  expect(uri).toMatch(/^data:image\/svg\+xml;charset=utf-8,/)
  const content = decodeURIComponent(uri?.split(',')[1] ?? '')
  expect(content).toContain('viewBox="0 0 100 100"')
  expect(content).toContain('fill="url(#local)"')
  expect(content).not.toMatch(
    /untrusted\.invalid|<image|<script|foreignObject|onload|response-data|<animate/
  )
})

test('retains normal SMIL and self-contained CSS animation in the image only', () => {
  const uri = diagnosticSvgImage(
    '<svg xmlns="http://www.w3.org/2000/svg"><style>@keyframes wheel { to { transform: rotate(360deg); } }.spin { animation: wheel 2s linear infinite; }</style><circle class="spin" r="8" style="transform-origin:center"><animate attributeName="r" from="7" to="8" dur="1s" repeatCount="indefinite"/></circle></svg>'
  )
  const content = decodeURIComponent(uri?.split(',')[1] ?? '')
  expect(content).toContain('@keyframes wheel')
  expect(content).toContain('transform-origin:center')
  expect(content).toContain('attributeName="r"')
})

test.each([
  'ordinary response without svg',
  '<svg><script>text</script></svg>',
  '<svg><rect/><!DOCTYPE svg SYSTEM "https://untrusted.invalid/external"></svg>',
  '<svg><rect/><?xml-stylesheet href="https://untrusted.invalid/style"?></svg>',
  '<svg><rect></svg>',
  `<svg>${'<g>'.repeat(50)}<rect/>${'</g>'.repeat(50)}</svg>`,
])(
  'does not present malformed, oversized-depth or untrusted-only content as an SVG preview',
  (answer) => {
    expect(diagnosticSvgImage(answer)).toBeNull()
  }
)
