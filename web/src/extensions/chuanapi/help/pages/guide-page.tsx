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
import { ArrowLeft, FileText } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'

import { DocsShell } from '../components/docs-shell'
import { DocsMarkdown } from '../components/guide-article'
import { PrevNextNav } from '../components/prev-next-nav'
import { getGuide, getGuideGroup } from '../content/nav'
import { buildGuideMarkdown } from '../lib/docs-utils'

export function GuidePage({ slug }: { slug: string }) {
  const { t } = useTranslation()
  const guide = getGuide(slug)

  if (!guide) {
    return (
      <DocsShell>
        <div className='space-y-4 py-12 text-center'>
          <h1 className='text-foreground text-2xl font-bold'>
            {t('Guide not found')}
          </h1>
          <p className='text-muted-foreground text-sm'>
            {t('The guide you are looking for does not exist or has moved.')}
          </p>
          <Button variant='outline' render={<Link to='/help' />}>
            <ArrowLeft />
            {t('Back to docs')}
          </Button>
        </div>
      </DocsShell>
    )
  }

  const group = getGuideGroup(slug)

  return (
    <DocsShell>
      <article className='space-y-6'>
        <header className='space-y-3'>
          {group ? (
            <p className='text-muted-foreground text-xs font-medium tracking-wide uppercase'>
              {group.title}
            </p>
          ) : null}
          <h1 className='text-foreground text-3xl font-bold tracking-tight'>
            {guide.title}
          </h1>
          <p className='text-muted-foreground text-sm leading-relaxed sm:text-base'>
            {guide.description}
          </p>
          <div className='flex flex-wrap items-center gap-2'>
            <CopyButton
              value={buildGuideMarkdown(guide)}
              variant='outline'
              size='sm'
              className='px-3'
            >
              <FileText />
              {t('Copy Markdown')}
            </CopyButton>
            {guide.updated ? (
              <span className='text-muted-foreground/70 text-xs'>
                {t('Last verified')}: {guide.updated}
              </span>
            ) : null}
          </div>
        </header>

        <DocsMarkdown body={guide.body} />

        <PrevNextNav slug={slug} />
      </article>
    </DocsShell>
  )
}
