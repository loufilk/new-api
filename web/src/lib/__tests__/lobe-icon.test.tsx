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
import { createRequire } from 'node:module'
import { resolve } from 'node:path'
import { runInNewContext } from 'node:vm'

import { act, render, screen } from '@testing-library/react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'

import { CatalogModelIcon } from '@/components/catalog-model-icon'
import { catalogModelPlatformIcon } from '@/features/pricing/lib/market-groups'

import { catalogIconID, catalogIconVariants } from '../catalog-icon-registry'
import { getLobeIcon, getLobeIconNames } from '../lobe-icon'

vi.mock(
  '../../../node_modules/@lobehub/icons/es/Mistral/components/Color.mjs',
  () => {
    throw new Error('Icon chunk unavailable')
  }
)

describe('Lobe icons', () => {
  it.each([
    '/build/web/node_modules/@lobehub/icons/es/',
    'D:\\a\\new-api\\web\\node_modules\\@lobehub\\icons\\es\\',
  ])('includes icon variants when bundling from %s', (root) => {
    // Vitest loads imports without applying Rspack's webpackInclude filter.
    const source = readFileSync(
      resolve(import.meta.dirname, '../lobe-icon.tsx'),
      'utf8'
    )
    const comment = source.match(/\/\*\s*(webpackInclude:[\s\S]*?)\*\//)
    expect(comment).not.toBeNull()
    const { webpackInclude } = runInNewContext(
      `({${comment?.[1]}})`,
      {},
      {
        timeout: 1000,
      }
    ) as { webpackInclude: RegExp }
    const separator = root.includes('\\') ? '\\' : '/'
    // Resolve the actual files shipped by the installed icon package: template
    // requests can still work in Vitest while a stale extension filters all
    // production chunks out of Rspack's filesystem context.
    const require = createRequire(import.meta.url)
    const files = [
      'OpenAI/components/Mono',
      'Claude/components/Color',
      'Gemini/components/Color',
      'Gemma/components/Simple',
      'LobeHub/components/Morden',
    ].map(
      (variant) =>
        require
          .resolve(`@lobehub/icons/es/${variant}.js`)
          .replaceAll('\\', '/')
          .split('/es/')
          .pop() ?? ''
    )
    files.push(
      'OpenAI/index.mjs',
      'OpenAI/components/Mono.d.mts',
      'OpenAI/components/Unknown.mjs'
    )
    const included = files.filter((file) =>
      webpackInclude.test(root + file.replaceAll('/', separator))
    )
    expect(included).toEqual(files.slice(0, 5))
  })

  it('loads a named variant with its configured size and accessibility props', async () => {
    render(
      getLobeIcon('Claude.Color.size={32}.role="img".aria-label="Claude icon"')
    )
    expect(screen.getByText('C')).toHaveStyle({ width: '32px', height: '32px' })
    const icon = await screen.findByRole('img', { name: 'Claude icon' })
    expect(icon.tagName.toLowerCase()).toBe('svg')
    expect(icon).toHaveAttribute('width', '32')
    expect(icon).toHaveAttribute('height', '32')
  })

  it('updates the displayed icon when the name changes', async () => {
    const { rerender } = render(
      getLobeIcon('OpenAI.role="img".aria-label="OpenAI icon"', 24)
    )
    expect(
      await screen.findByRole('img', { name: 'OpenAI icon' })
    ).toHaveAttribute('width', '24')
    rerender(
      getLobeIcon('Gemini.Color.role="img".aria-label="Gemini icon"', 28)
    )
    expect(
      await screen.findByRole('img', { name: 'Gemini icon' })
    ).toHaveAttribute('width', '28')
    expect(
      screen.queryByRole('img', { name: 'OpenAI icon' })
    ).not.toBeInTheDocument()
  })

  it('falls back to the base icon when the requested variant is unavailable', async () => {
    render(getLobeIcon('OpenAI.Unknown.role="img".aria-label="OpenAI icon"'))
    expect(
      await screen.findByRole('img', { name: 'OpenAI icon' })
    ).toBeVisible()
  })

  it.each(['Gemma.Simple', 'LobeHub.Morden'])(
    'loads the %s variant that is not listed in the standard catalog flags',
    async (name) => {
      render(getLobeIcon(`${name}.role="img".aria-label="Special icon"`, 24))
      expect(
        await screen.findByRole('img', { name: 'Special icon' })
      ).toHaveAttribute('height', '24')
    }
  )

  it('keeps a sized placeholder when a chunk fails and accepts later size changes', async () => {
    const { rerender } = render(getLobeIcon('Mistral.Color', 18))
    await act(async () => {
      await vi.dynamicImportSettled()
    })
    expect(screen.getByText('M')).toHaveStyle({ width: '18px', height: '18px' })
    rerender(getLobeIcon('Mistral.Color', 36))
    expect(screen.getByText('M')).toHaveStyle({ width: '36px', height: '36px' })
  })

  it('keeps placeholders for missing names and preserves custom icons', () => {
    const { rerender } = render(getLobeIcon('NotAnInstalledIcon', 30))
    expect(screen.getByText('N')).toHaveStyle({ width: '30px', height: '30px' })
    rerender(getLobeIcon('  '))
    expect(screen.getByText('?')).toBeVisible()
    rerender(getLobeIcon('SGLang', 24))
    expect(screen.getByRole('presentation', { hidden: true })).toHaveAttribute(
      'width',
      '24'
    )
    expect(getLobeIconNames()).toEqual(
      expect.arrayContaining([
        'OpenAI',
        'Claude.Color',
        'SGLang',
        'Sub2API',
        'Wan',
      ])
    )
  })
})

describe('catalog icon display', () => {
  it('publishes the same local registry as the installed picker', () => {
    const manifest = JSON.parse(
      readFileSync(
        resolve(import.meta.dirname, '../../../../model/catalog_icon_ids.json'),
        'utf8'
      )
    )
    expect(manifest).toEqual(Object.fromEntries(catalogIconVariants))
  })

  it.each([
    ['Claude.Color', 'Claude.Color'],
    [' OpenAI.Color ', 'OpenAI'],
    ["Claude.Avatar.type={'platform'}", 'Claude.Avatar'],
    ["Claude.Color.size={32}.aria-label='old icon'", 'Claude.Color'],
    ['Gemma.Simple', 'Gemma.Simple'],
    ['LobeHub.Morden', 'LobeHub.Morden'],
    ['Sub2API', 'Sub2API'],
    ['https://unavailable.invalid/logo.svg', ''],
    ['Claude.Color.href=https://unavailable.invalid', ''],
    ['data:image/svg+xml,<svg/>', ''],
    ['constructor', ''],
    ['NoSuchVendor', ''],
  ])('resolves stored key %s to local key %s', (raw, key) => {
    expect(catalogIconID(raw)).toBe(key)
  })

  it('uses valid vendor metadata for unsupported overrides, otherwise the model initial', async () => {
    const { rerender } = render(
      <CatalogModelIcon
        icon='bad-key'
        vendorIcon='Claude.Color'
        name='demo'
        size={21}
      />
    )
    expect((await screen.findByTitle('Claude')).parentElement).toHaveAttribute(
      'width',
      '21'
    )
    rerender(<CatalogModelIcon vendorIcon='bad-key' name='demo' size={21} />)
    expect(screen.getByText('D')).toBeVisible()
    expect(screen.queryByTitle('Claude')).not.toBeInTheDocument()
  })
})

describe('catalog brand source and first paint', () => {
  // No waitFor/lazy-import settling: a cold render, not just a later paint,
  // must have the actual bundled glyph. These match the public pricing shape.
  it.each([
    ['gpt-4o', 'OpenAI', 'OpenAI', '', 'OpenAI'],
    ['gpt-4o-mini', 'OpenAI', '', '', 'OpenAI'],
    ['gpt-4o-mini', 'OpenAI', '', 'OpenAI.Avatar', 'OpenAI'],
    ['gemini-2.5-flash', 'Google', '', 'Gemini.Avatar', 'Gemini'],
    ['gemini-2.5-flash', 'Google', 'Gemini.Color', '', 'Gemini'],
    ['gemini-2.5-pro', ' Google ', '', '', 'Gemini'],
    ['gemini-custom', 'Google', 'Gemini.Color', 'OpenAI.Color', 'OpenAI'],
    ['demo', 'OpenAI', 'Google.Color', 'invalid-key', 'Google'],
    [
      'demo',
      'Gemini',
      '',
      'https://signed.invalid/logo.svg?secret=x',
      'Gemini',
    ],
  ])(
    'paints %s (%s, %s, %s) as %s inline',
    (name, vendorName, vendorIcon, icon, brand) => {
      const markup = renderToStaticMarkup(
        <CatalogModelIcon
          {...{ name, vendorName, vendorIcon, icon }}
          size={28}
        />
      )
      const svg = new DOMParser()
        .parseFromString(markup, 'text/html')
        .querySelector('svg')
      expect(svg?.querySelector('title')?.textContent).toBe(brand)
      expect(svg?.querySelector('path')).not.toBeNull()
      expect(svg?.getAttribute('width')).toBe('28')
      expect(markup).not.toMatch(/<img|signed.invalid|secret=x/)
    }
  )

  const catalog = {
    blue: { platform: 'openai' },
    green: { platform: 'openai' },
    red: { platform: 'gemini' },
    reserved: { exclusive: true, platform: 'gemini' },
  }

  it.each([
    [['blue', 'green'], 'OpenAI'],
    [['red'], 'Gemini'],
    [['blue', 'red'], null],
    [['green', 'missing'], null],
    [['reserved'], null],
    [['constructor'], null],
    [[], null],
  ] as const)(
    'uses only explicit and unanimous platforms in allowed groups %j',
    (groups, brand) => {
      const platformIcon = catalogModelPlatformIcon({
        enable_groups: [...groups],
        group_catalog: catalog,
      })
      const markup = renderToStaticMarkup(
        <CatalogModelIcon
          name='gpt-gemini-demo'
          platformIcon={platformIcon}
          size={24}
        />
      )
      if (brand) expect(markup).toContain(`<title>${brand}</title>`)
      else {
        expect(markup).not.toContain('<svg')
        expect(markup).toContain('>G<')
      }
    }
  )

  it('does not substitute platform branding for an explicit unknown vendor or unsafe icon', () => {
    const { container } = render(
      <CatalogModelIcon
        name='openai/private'
        icon='<svg/>'
        vendorName='Unofficial OpenAI Relay'
        vendorIcon='data:image/svg+xml,<svg/>'
        platformIcon='Gemini.Color'
        size={20}
      />
    )
    expect(container.querySelector('svg, img')).toBeNull()
    expect(screen.getByText('O')).toBeVisible()
  })
})
