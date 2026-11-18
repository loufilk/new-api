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
import { ArrowRight, Braces, Rocket, Search, X } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { IntegrationWizard } from '@/extensions/chuanapi/onboarding/integration-wizard'

import { AiDocsCallout } from '../components/ai-docs-callout'
import { DocsShell } from '../components/docs-shell'
import { FaqSection } from '../components/faq-section'
import {
  docsGroupIcons,
  docsGroups,
  getGuide,
  orderedGuides,
} from '../content/nav'
import { searchGuides } from '../lib/search-guides'

export function DocsIndexPage() {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')
  const [category, setCategory] = useState('all')
  const matchingSlugs = new Set(
    searchGuides(orderedGuides, query).map((guide) => guide.slug)
  )
  const visibleGroups = docsGroups
    .filter((group) => category === 'all' || group.id === category)
    .map((group) => ({
      ...group,
      slugs: group.slugs.filter((slug) => matchingSlugs.has(slug)),
    }))
    .filter((group) => group.slugs.length > 0)
  const visibleCount = visibleGroups.reduce(
    (count, group) => count + group.slugs.length,
    0
  )

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
            <IntegrationWizard variant='default' size='default' />
            <Button
              variant='outline'
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

        <section
          className='bg-card space-y-4 rounded-2xl border p-4 sm:p-5'
          aria-label={t('Find a guide')}
        >
          <div className='relative'>
            <Search
              className='text-muted-foreground absolute top-2.5 left-3 size-4'
              aria-hidden='true'
            />
            <Input
              type='search'
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder={t(
                'Search guides, clients, protocols, or error codes...'
              )}
              aria-label={t('Search documentation')}
              className='pr-10 pl-9'
            />
            {query && (
              <Button
                variant='ghost'
                size='icon'
                className='absolute top-0 right-0'
                aria-label={t('Clear search')}
                onClick={() => setQuery('')}
              >
                <X className='size-4' />
              </Button>
            )}
          </div>
          <div
            className='flex flex-wrap gap-2'
            aria-label={t('Guide categories')}
          >
            <Button
              size='sm'
              variant={category === 'all' ? 'secondary' : 'ghost'}
              aria-pressed={category === 'all'}
              onClick={() => setCategory('all')}
            >
              {t('All guides')}
            </Button>
            {docsGroups.map((group) => (
              <Button
                key={group.id}
                size='sm'
                variant={category === group.id ? 'secondary' : 'ghost'}
                aria-pressed={category === group.id}
                onClick={() => setCategory(group.id)}
              >
                {group.title}
              </Button>
            ))}
          </div>
          <p role='status' className='text-muted-foreground text-xs'>
            {t('{{count}} guides found', { count: visibleCount })}
          </p>
        </section>

        {visibleGroups.length === 0 && (
          <EmptyState
            title={t('No matching guides')}
            description={t(
              'Try a model name, client name, protocol, or error code.'
            )}
            action={
              <Button
                variant='outline'
                onClick={() => {
                  setQuery('')
                  setCategory('all')
                }}
              >
                {t('Clear filters')}
              </Button>
            }
          />
        )}

        {visibleGroups.map((group) => {
          const GroupIcon = docsGroupIcons[group.id]
          return (
            <section key={group.id} className='space-y-3'>
              <h2 className='text-foreground flex items-center gap-2 text-lg font-semibold'>
                {GroupIcon ? (
                  <GroupIcon className='text-primary size-5' />
                ) : null}
                {group.title}
                <Badge variant='secondary'>{group.slugs.length}</Badge>
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
