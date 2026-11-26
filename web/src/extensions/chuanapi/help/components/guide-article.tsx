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
import { useLocation, useRouter } from '@tanstack/react-router'
import { useEffect, useMemo, useRef, type MouseEvent } from 'react'
import { useTranslation } from 'react-i18next'

import { Markdown } from '@/components/ui/markdown'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'

import type { DocHeading } from '../lib/doc-headings'
import { applyBaseUrl } from '../lib/docs-utils'

const COPY_ICON_SVG =
  '<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg>'
const CHECK_ICON_SVG =
  '<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>'

/**
 * 文档正文渲染：
 * - 替换 {{BASE_URL}} 占位符
 * - 为每个代码块注入复制按钮
 * - 拦截站内链接改为 SPA 导航（Markdown 组件默认给所有链接加 target=_blank）
 */
export function DocsMarkdown({
  body,
  headings,
}: {
  body: string
  headings: readonly DocHeading[]
}) {
  const { t } = useTranslation()
  const router = useRouter()
  const hash = useLocation({ select: (location) => location.hash })
  const containerRef = useRef<HTMLDivElement>(null)
  const rendered = useMemo(() => applyBaseUrl(body), [body])

  const { copyToClipboard } = useCopyToClipboard()

  const copyLabel = t('Copy code')
  const copiedLabel = t('Copied!')

  useEffect(() => {
    const container = containerRef.current
    container?.querySelectorAll('h2, h3').forEach((element, index) => {
      const heading = headings[index]
      if (!heading) return
      element.id = heading.id
      element.classList.add('scroll-mt-24')
    })
    if (!hash) return
    const section = headings.find(
      (heading) =>
        heading.id === hash || encodeURIComponent(heading.id) === hash
    )
    if (section) {
      container?.querySelector(`#${section.id}`)?.scrollIntoView?.()
    }
  }, [headings, hash])

  useEffect(() => {
    const container = containerRef.current
    if (!container) {
      return
    }

    const buttons: HTMLButtonElement[] = []
    container.querySelectorAll('pre').forEach((pre) => {
      if (pre.querySelector('[data-docs-copy]')) {
        return
      }
      pre.classList.add('relative')
      const button = document.createElement('button')
      button.type = 'button'
      button.dataset.docsCopy = 'true'
      button.className =
        'absolute top-2 right-2 inline-flex items-center gap-1 rounded-md border border-border bg-background/90 px-2 py-1 text-[11px] text-muted-foreground shadow-xs backdrop-blur transition-colors hover:text-foreground'
      button.innerHTML = `${COPY_ICON_SVG}<span></span>`
      const label = button.querySelector('span')
      if (label) label.textContent = copyLabel
      button.addEventListener('click', () => {
        const code = pre.querySelector('code')?.textContent ?? ''
        void copyToClipboard(code).then((success) => {
          if (!success || !button.isConnected) return
          button.innerHTML = `${CHECK_ICON_SVG}<span></span>`
          const successLabel = button.querySelector('span')
          if (successLabel) successLabel.textContent = copiedLabel
          window.setTimeout(() => {
            if (!button.isConnected) return
            button.innerHTML = `${COPY_ICON_SVG}<span></span>`
            const resetLabel = button.querySelector('span')
            if (resetLabel) resetLabel.textContent = copyLabel
          }, 2000)
        })
      })
      pre.appendChild(button)
      buttons.push(button)
    })

    return () => {
      buttons.forEach((button) => button.remove())
    }
  }, [rendered, copyLabel, copiedLabel, copyToClipboard])

  function handleClickCapture(event: MouseEvent<HTMLDivElement>) {
    if (
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey
    ) {
      return
    }
    const target = event.target as HTMLElement
    const anchor = target.closest('a')
    if (!anchor) {
      return
    }
    const href = anchor.getAttribute('href')
    if (!href || !href.startsWith('/') || href.startsWith('//')) {
      return
    }
    event.preventDefault()
    void router.history.push(href)
  }

  return (
    <div
      className='min-w-0'
      ref={containerRef}
      onClickCapture={handleClickCapture}
    >
      <Markdown className='prose-base text-[0.95rem] leading-7'>
        {rendered}
      </Markdown>
    </div>
  )
}
