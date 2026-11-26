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
import { ChevronDown } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { cn } from '@/lib/utils'

import type { DocHeading } from '../lib/doc-headings'

function HeadingLinks({ headings }: { headings: readonly DocHeading[] }) {
  return (
    <ul className='space-y-2'>
      {headings.map((heading) => (
        <li key={heading.id} className={cn(heading.depth === 3 && 'pl-3')}>
          <a
            href={`#${heading.id}`}
            className='text-muted-foreground hover:text-foreground'
          >
            {heading.label}
          </a>
        </li>
      ))}
    </ul>
  )
}

export function ArticleToc({ headings }: { headings: readonly DocHeading[] }) {
  const { t } = useTranslation()
  if (!headings.length) return null
  return (
    <nav aria-label={t('On this page')} className='text-sm'>
      <Collapsible className='border-border rounded-lg border xl:hidden'>
        <CollapsibleTrigger className='flex w-full items-center justify-between gap-2 px-3 py-2 font-medium'>
          {t('On this page')}
          <ChevronDown className='size-4' aria-hidden='true' />
        </CollapsibleTrigger>
        <CollapsibleContent className='px-3 pb-3'>
          <HeadingLinks headings={headings} />
        </CollapsibleContent>
      </Collapsible>
      <div className='border-border sticky top-22 hidden max-h-[calc(100vh-6rem)] overflow-auto border-l pl-4 xl:block'>
        <p className='mb-3 font-medium'>{t('On this page')}</p>
        <HeadingLinks headings={headings} />
      </div>
    </nav>
  )
}
