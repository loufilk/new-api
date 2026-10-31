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

import { JSDOM } from 'jsdom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { getBootstrapTemplateParameters } from '../bootstrap-template'

const windows: JSDOM[] = []

function openShell(language = 'en') {
  const parameters = getBootstrapTemplateParameters(process.cwd())
  const template = readFileSync(resolve('index.html'), 'utf8')
  const html = template.replaceAll(
    /<%= (\w+) %>/g,
    (_, key: string) => parameters[key]
  )
  let expire = () => {}
  const dom = new JSDOM(html, {
    url: 'https://console.test/dashboard',
    runScripts: 'dangerously',
    beforeParse(window) {
      window.localStorage.setItem('i18nextLng', language)
      window.matchMedia = () => ({ matches: false }) as MediaQueryList
      window.setTimeout = ((callback: () => void) => {
        expire = callback
        return 1
      }) as typeof window.setTimeout
      window.clearTimeout = vi.fn()
    },
  })
  windows.push(dom)
  return { dom, expire: () => expire() }
}

afterEach(() => {
  for (const dom of windows) dom.window.close()
  windows.length = 0
})

describe('HTML startup while application resources are unavailable', () => {
  it('shows a loading status before any application script runs', () => {
    const { dom } = openShell('zh-CN')
    const shell = dom.window.document.querySelector('#app-bootstrap')
    expect(shell?.getAttribute('role')).toBe('status')
    expect(shell?.textContent).toContain('加载')
    expect(dom.window.document.querySelector('#root')?.childNodes.length).toBe(
      0
    )
  })

  it('offers recovery after a bounded wait without navigating automatically', () => {
    const { dom, expire } = openShell()
    expire()
    expect(
      dom.window.document.querySelector('#app-bootstrap')?.getAttribute('role')
    ).toBe('alert')
    expect(
      dom.window.document.querySelector<HTMLButtonElement>(
        '#app-bootstrap-retry'
      )?.hidden
    ).toBe(false)
    expect(dom.window.location.pathname).toBe('/dashboard')
  })

  it('offers recovery immediately if an application script fails to download', () => {
    const { dom } = openShell()
    const script = dom.window.document.createElement('script')
    script.src = '/static/js/unavailable.js'
    dom.window.document.head.append(script)
    script.dispatchEvent(new dom.window.Event('error'))
    expect(
      dom.window.document.querySelector('#app-bootstrap')?.getAttribute('role')
    ).toBe('alert')
  })

  it('removes the shell and its timer once the application renders', async () => {
    const { dom } = openShell()
    const content = dom.window.document.createElement('main')
    content.textContent = 'Ready'
    dom.window.document.querySelector('#root')?.append(content)
    await new Promise<void>((resolve) => dom.window.queueMicrotask(resolve))
    expect(dom.window.document.querySelector('#app-bootstrap')).toBeNull()
    expect(dom.window.clearTimeout).toHaveBeenCalled()
  })

  it.each([
    ['en', 'Loading...'],
    ['zhCN', '加载中...'],
    ['zhTW', '載入中...'],
    ['fr', 'Chargement...'],
    ['ru', 'Загрузка...'],
    ['ja', '読み込み中...'],
    ['vi', 'Đang tải...'],
    ['zh-Hant', '載入中...'],
    ['unknown', 'Loading...'],
  ])(
    'shows the selected language with English fallback for %s',
    (language, expected) => {
      const { dom } = openShell(language)
      expect(
        dom.window.document.querySelector('#app-bootstrap-message')?.textContent
      ).toBe(expected)
    }
  )
})
