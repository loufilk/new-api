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
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { render, screen } from '@testing-library/react'
import { describe, expect, test } from 'vitest'

import { Button } from '../button'

type Color = [number, number, number]
const css = readFileSync(
  resolve(import.meta.dirname, '../../../styles/theme.css'),
  'utf8'
)

// CSS Color 4 OKLab -> linear sRGB, followed by WCAG 2 relative luminance.
// Read the actual palette, rather than duplicating its expected color values.
function tokenRgb(block: string, name: string): Color {
  const token = block.match(new RegExp(`--${name}: oklch\\(([^)]+)\\)`))
  if (!token) throw new Error(`Missing OKLCH token ${name}`)
  const [lightness, chroma, hue] = token[1].split(' ').map(Number)
  const a = chroma * Math.cos((hue * Math.PI) / 180)
  const b = chroma * Math.sin((hue * Math.PI) / 180)
  const l = (lightness + 0.3963377774 * a + 0.2158037573 * b) ** 3
  const m = (lightness - 0.1055613458 * a - 0.0638541728 * b) ** 3
  const s = (lightness - 0.0894841775 * a - 1.291485548 * b) ** 3
  return [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ].map((value) => {
    const channel = Math.min(1, Math.max(0, value))
    return channel <= 0.0031308
      ? 12.92 * channel
      : 1.055 * channel ** (1 / 2.4) - 0.055
  }) as Color
}

function luminance(rgb: Color): number {
  const linear = rgb.map((channel) =>
    channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4
  )
  return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2]
}

function contrast(first: Color, second: Color): number {
  const values = [luminance(first), luminance(second)]
  return (Math.max(...values) + 0.05) / (Math.min(...values) + 0.05)
}

describe('default primary button contrast', () => {
  test.each([':root', '.dark'])(
    '%s normal and hovered small button text meets 4.5:1',
    (selector) => {
      const block = css.split(`${selector} {`)[1].split('}')[0]
      const primary = tokenRgb(block, 'primary')
      const foreground = tokenRgb(block, 'primary-foreground')
      expect(contrast(primary, foreground)).toBeGreaterThanOrEqual(4.5)
      expect(
        contrast(
          primary.map((c) => c * 0.95) as Color,
          foreground.map((c) => c * 0.95) as Color
        )
      ).toBeGreaterThanOrEqual(4.5)
    }
  )

  test.each([':root', '.dark'])(
    '%s focus ring contrasts with the separated page surface',
    (selector) => {
      const block = css.split(`${selector} {`)[1].split('}')[0]
      expect(
        contrast(tokenRgb(block, 'foreground'), tokenRgb(block, 'background'))
      ).toBeGreaterThanOrEqual(3)
    }
  )

  test('button and link rendering keep opaque primary backgrounds on hover and a separated focus ring', () => {
    render(
      <>
        <Button>Save</Button>
        <Button render={<a href='/sign-in' />}>Sign in</Button>
      </>
    )
    for (const control of [
      screen.getByRole('button', { name: 'Save' }),
      screen.getByRole('button', { name: 'Sign in' }),
    ]) {
      expect(control).toHaveClass(
        'bg-primary',
        'text-primary-foreground',
        'hover:brightness-95',
        'focus-visible:ring-foreground',
        'focus-visible:ring-offset-2',
        'focus-visible:ring-offset-background'
      )
      expect(control).not.toHaveClass('[a]:hover:bg-primary/80')
    }
  })

  test('disabled primary actions retain native disabled semantics and subdued styling', () => {
    render(<Button disabled>Save</Button>)
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Save' })).toHaveClass(
      'disabled:opacity-50'
    )
  })
})
