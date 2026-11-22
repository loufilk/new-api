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
import { Link } from '@tanstack/react-router'
import { ArrowLeft, ArrowRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

import { getAdjacentGuides } from '../content/nav'

export function PrevNextNav({ slug }: { slug: string }) {
  const { t } = useTranslation()
  const { previous, next } = getAdjacentGuides(slug)

  if (!previous && !next) {
    return null
  }

  return (
    <nav className='border-border mt-10 grid gap-3 border-t pt-6 sm:grid-cols-2'>
      {previous ? (
        <Link
          to='/help/guides/$slug'
          params={{ slug: previous.slug }}
          className='group border-border hover:border-primary/40 hover:bg-muted/40 flex flex-col gap-1 rounded-xl border p-4 transition-colors'
        >
          <span className='text-muted-foreground flex items-center gap-1 text-xs'>
            <ArrowLeft className='size-3.5 transition-transform group-hover:-translate-x-0.5' />
            {t('Previous')}
          </span>
          <span className='text-foreground text-sm font-medium'>
            {previous.title}
          </span>
        </Link>
      ) : (
        <span aria-hidden className='hidden sm:block' />
      )}
      {next ? (
        <Link
          to='/help/guides/$slug'
          params={{ slug: next.slug }}
          className={cn(
            'group border-border hover:border-primary/40 hover:bg-muted/40 flex flex-col gap-1 rounded-xl border p-4 text-right transition-colors',
            !previous && 'sm:col-start-2'
          )}
        >
          <span className='text-muted-foreground flex items-center justify-end gap-1 text-xs'>
            {t('Next')}
            <ArrowRight className='size-3.5 transition-transform group-hover:translate-x-0.5' />
          </span>
          <span className='text-foreground text-sm font-medium'>
            {next.title}
          </span>
        </Link>
      ) : null}
    </nav>
  )
}
