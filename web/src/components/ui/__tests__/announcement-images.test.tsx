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
import { fireEvent, render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'

import { Markdown } from '../markdown'

test('announcement HTTPS images retain accessible descriptions and safe responsive loading', () => {
  const view = render(
    <Markdown imagePolicy='announcement'>
      {
        'Before\n\n![Maintenance schedule](https://images.example.test/a.png)\n\nAfter'
      }
    </Markdown>
  )
  const image = screen.getByRole('img', { name: 'Maintenance schedule' })
  expect(image).toHaveAttribute('src', 'https://images.example.test/a.png')
  expect(image).toHaveAttribute('referrerpolicy', 'no-referrer')
  expect(image).toHaveAttribute('loading', 'lazy')
  expect(image).toHaveAttribute('decoding', 'async')
  expect(image).toHaveClass('max-w-full', 'h-auto')
  expect(view.container).toHaveTextContent('Before')
  expect(view.container).toHaveTextContent('After')
})

test.each([
  'javascript:alert(1)',
  'data:image/svg+xml,<svg onload="alert(1)"/>',
  'http://images.example.test/a.png',
  '//images.example.test/a.png',
  '/private.png',
  'https://user:password@images.example.test/a.png',
  'https://images.example.test/a.svg',
])('announcement image rejects unsafe or unsupported source %s', (source) => {
  const view = render(
    <Markdown imagePolicy='announcement'>{`<img src='${source}' alt='Schedule'>`}</Markdown>
  )
  expect(view.container.querySelector('img[src]')).toBeNull()
  expect(view.container).toHaveTextContent('Schedule')
})

test('announcement raw HTML cannot bypass image policy or enable active content', () => {
  const view = render(
    <Markdown imagePolicy='announcement'>
      {
        '<script>alert(1)</script><iframe src="https://bad.example.test"></iframe><svg onload="alert(1)"><image href="https://bad.example.test/a" /></svg><picture><source srcset="http://bad.example.test/a"><img alt="Safe" src="https://images.example.test/a.png" srcset="http://bad.example.test/a 2x" onerror="alert(1)" style="width:99999px" width="99999" height="99999"></picture>'
      }
    </Markdown>
  )
  expect(
    view.container.querySelector('script, iframe, svg, source, picture')
  ).toBeNull()
  const image = screen.getByRole('img', { name: 'Safe' })
  for (const attribute of ['srcset', 'onerror', 'style', 'width', 'height'])
    expect(image).not.toHaveAttribute(attribute)
  expect(image).toHaveAttribute('referrerpolicy', 'no-referrer')
})

test('failed announcement images show a text fallback without removing surrounding content', () => {
  const view = render(
    <Markdown imagePolicy='announcement'>
      {
        'Before\n\n![Schedule](https://images.example.test/missing.png)\n\nAfter'
      }
    </Markdown>
  )
  fireEvent.error(screen.getByRole('img', { name: 'Schedule' }))
  expect(view.container.querySelector('img')).toBeNull()
  expect(view.container).toHaveTextContent('Image unavailable: Schedule')
  expect(view.container).toHaveTextContent('After')
})

test('announcement image links keep safe new-tab attributes without an image viewer', () => {
  render(
    <Markdown imagePolicy='announcement'>
      {
        '[![Schedule](https://images.example.test/a.png)](https://example.test/details)'
      }
    </Markdown>
  )
  const link = screen.getByRole('link', { name: 'Schedule' })
  expect(link).toHaveAttribute('target', '_blank')
  expect(link).toHaveAttribute('rel', 'noopener noreferrer')
})

test('existing Markdown consumers keep their image sources', () => {
  render(<Markdown>{'![Existing image](/existing.png)'}</Markdown>)
  expect(screen.getByRole('img', { name: 'Existing image' })).toHaveAttribute(
    'src',
    '/existing.png'
  )
})

test('raw image inputs cannot bypass the announcement image policy', () => {
  const view = render(
    <Markdown imagePolicy='announcement'>
      {
        '<input type="image" src="http://images.example.test/raw.png" alt="Bypass">Text stays'
      }
    </Markdown>
  )
  expect(view.container.querySelector('input[src]')).toBeNull()
  expect(view.container).toHaveTextContent('Text stays')
})

test('raw announcement styles cannot issue image requests or restyle the dashboard', () => {
  const view = render(
    <Markdown imagePolicy='announcement'>
      {
        '<div><style>@import url(http://images.example.test/styles.css); body {background-image:url(http://images.example.test/track.png)}</style>Notice</div>'
      }
    </Markdown>
  )
  expect(view.container.querySelector('style')).toBeNull()
  expect(view.container).toHaveTextContent('Notice')
})

test('MathML image glyphs cannot bypass the announcement image policy', () => {
  const view = render(
    <Markdown imagePolicy='announcement'>
      {
        '<math><mglyph src="http://images.example.test/track.png"></mglyph></math>Text stays'
      }
    </Markdown>
  )
  expect(view.container.querySelector('mglyph[src]')).toBeNull()
  expect(view.container).toHaveTextContent('Text stays')
})
