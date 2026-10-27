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
import { ArrowRight, Braces, Rocket } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'

import { AiDocsCallout } from '../components/ai-docs-callout'
import { DocsShell } from '../components/docs-shell'
import { FaqSection } from '../components/faq-section'
import { docsGroupIcons, docsGroups, getGuide } from '../content/nav'

export function DocsIndexPage() {
  const { t } = useTranslation()

  return (
    <DocsShell>
      <div className='space-y-10'>
        <header className='space-y-4'>
          <h1 className='text-foreground text-3xl font-bold tracking-tight'>
            {t('Documentation')}
          </h1>
          <p className='text-muted-foreground max-w-2xl text-sm leading-relaxed sm:text-base'>
            {t(
              'Everything you need to integrate: step-by-step tool guides, a full API reference, and troubleshooting for real errors.'
            )}
          </p>
          <div className='flex flex-wrap gap-2'>
            <Button
              render={
                <Link
                  to='/help/guides/$slug'
                  params={{ slug: 'quick-start' }}
                />
              }
            >
              <Rocket />
              {t('Quick Start')}
            </Button>
            <Button variant='outline' render={<Link to='/help/api' />}>
              <Braces />
              {t('API Reference')}
            </Button>
          </div>
        </header>

        {docsGroups.map((group) => {
          const GroupIcon = docsGroupIcons[group.id]
          return (
            <section key={group.id} className='space-y-3'>
              <h2 className='text-foreground flex items-center gap-2 text-lg font-semibold'>
                {GroupIcon ? (
                  <GroupIcon className='text-primary size-5' />
                ) : null}
                {group.title}
              </h2>
              <div className='grid gap-3 sm:grid-cols-2'>
                {group.slugs.map((slug) => {
                  const guide = getGuide(slug)
                  if (!guide) {
                    return null
                  }
                  return (
                    <Link
                      key={slug}
                      to='/help/guides/$slug'
                      params={{ slug }}
                      className='group border-border bg-card hover:border-primary/40 hover:bg-muted/40 relative flex flex-col gap-1.5 rounded-xl border p-4 transition-colors'
                    >
                      <div className='flex items-start justify-between gap-2'>
                        <span className='text-foreground flex flex-wrap items-center gap-2 text-sm font-semibold'>
                          {guide.title}
                          {guide.recommended ? (
                            <Badge
                              variant='secondary'
                              className='h-4 px-1.5 text-[10px]'
                            >
                              {t('Recommended')}
                            </Badge>
                          ) : null}
                        </span>
                        <ArrowRight className='text-muted-foreground size-4 shrink-0 transition-transform group-hover:translate-x-0.5' />
                      </div>
                      <p className='text-muted-foreground text-sm leading-relaxed'>
                        {guide.description}
                      </p>
                    </Link>
                  )
                })}
              </div>
            </section>
          )
        })}

        <AiDocsCallout />

        <FaqSection />
      </div>
    </DocsShell>
  )
}
