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
import { Suspense, lazy } from 'react'
import { useTranslation } from 'react-i18next'

import { HtmlContent, type HtmlContentVariant } from '@/components/html-content'
import { cn } from '@/lib/utils'

const Markdown = lazy(() =>
  import('@/components/ui/markdown').then((mod) => ({ default: mod.Markdown }))
)

type RichContentMode = 'markdown' | 'html'

interface RichContentProps {
  content: string
  mode?: RichContentMode
  breaks?: boolean
  className?: string
  htmlVariant?: HtmlContentVariant
}

function MarkdownFallback(props: { className?: string }) {
  const { t } = useTranslation()
  return (
    <div
      className={cn(
        'prose prose-sm dark:prose-invert max-w-none min-h-6 animate-pulse',
        props.className
      )}
      role='status'
      aria-busy='true'
      aria-live='polite'
      aria-label={t('Loading...')}
    />
  )
}

export function RichContent(props: RichContentProps) {
  if (props.mode === 'html') {
    return (
      <HtmlContent
        content={props.content}
        className={props.className}
        variant={props.htmlVariant}
      />
    )
  }

  return (
    <Suspense fallback={<MarkdownFallback className={props.className} />}>
      <Markdown breaks={props.breaks} className={props.className}>
        {props.content}
      </Markdown>
    </Suspense>
  )
}
