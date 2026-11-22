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
import { ChevronDown, ChevronsUpDown, Search } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

import { endpointCategories } from '../content/endpoints'
import type { HttpMethod } from '../content/types'

function methodClass(method: HttpMethod): string {
  return method === 'GET'
    ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
    : 'border-blue-500/30 bg-blue-500/10 text-blue-700 dark:text-blue-300'
}

export function ApiEndpointTree({
  selectedId,
  onSelect,
}: {
  selectedId: string
  onSelect: (id: string) => void
}) {
  const { t } = useTranslation()
  const searchRef = useRef<HTMLInputElement>(null)
  const [query, setQuery] = useState('')
  const [openCategories, setOpenCategories] = useState(
    () => new Set(endpointCategories.map((category) => category.id))
  )

  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent) {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        searchRef.current?.focus()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [])

  const filteredCategories = useMemo(() => {
    const normalized = query.trim().toLowerCase()
    if (!normalized) {
      return endpointCategories
    }
    return endpointCategories
      .map((category) => ({
        ...category,
        endpoints: category.endpoints.filter((endpoint) =>
          [
            category.title,
            endpoint.name,
            endpoint.path,
            endpoint.description,
            endpoint.method,
          ]
            .join(' ')
            .toLowerCase()
            .includes(normalized)
        ),
      }))
      .filter((category) => category.endpoints.length > 0)
  }, [query])

  const allOpen = endpointCategories.every((category) =>
    openCategories.has(category.id)
  )

  function toggleAll() {
    setOpenCategories(
      allOpen
        ? new Set()
        : new Set(endpointCategories.map((category) => category.id))
    )
  }

  return (
    <div className='flex min-h-0 flex-col gap-3'>
      <div className='space-y-2'>
        <div className='relative'>
          <Search className='text-muted-foreground pointer-events-none absolute top-2 left-2.5 size-4' />
          <Input
            ref={searchRef}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={t('Search models, chat, audio, /v1/rerank...')}
            aria-label={t('Search API documentation')}
            className='pr-12 pl-8'
          />
          <kbd className='border-border bg-muted text-muted-foreground pointer-events-none absolute top-1.5 right-2 rounded border px-1.5 py-0.5 text-[10px]'>
            Ctrl K
          </kbd>
        </div>
        <div className='flex items-center justify-between'>
          <span className='text-muted-foreground text-xs'>
            {t('{{count}} endpoints', {
              count: endpointCategories.reduce(
                (sum, category) => sum + category.endpoints.length,
                0
              ),
            })}
          </span>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className='h-7 px-2 text-xs'
            onClick={toggleAll}
          >
            <ChevronsUpDown />
            {allOpen ? t('Collapse all') : t('Expand all')}
          </Button>
        </div>
      </div>

      <nav aria-label={t('API endpoints')} className='space-y-1'>
        {filteredCategories.length === 0 ? (
          <p className='text-muted-foreground px-2 py-8 text-center text-sm'>
            {t('No endpoints match your search.')}
          </p>
        ) : null}
        {filteredCategories.map((category) => {
          const open = Boolean(query.trim()) || openCategories.has(category.id)
          return (
            <Collapsible
              key={category.id}
              open={open}
              onOpenChange={(nextOpen) => {
                if (query.trim()) {
                  return
                }
                setOpenCategories((current) => {
                  const next = new Set(current)
                  if (nextOpen) {
                    next.add(category.id)
                  } else {
                    next.delete(category.id)
                  }
                  return next
                })
              }}
            >
              <CollapsibleTrigger className='hover:bg-muted/60 flex w-full items-center justify-between rounded-md px-2 py-1.5 text-sm font-medium transition-colors'>
                <span>{category.title}</span>
                <span className='text-muted-foreground flex items-center gap-1.5 text-xs'>
                  {category.endpoints.length}
                  <ChevronDown
                    className={cn(
                      'size-3.5 transition-transform',
                      open && 'rotate-180'
                    )}
                  />
                </span>
              </CollapsibleTrigger>
              <CollapsibleContent>
                <div className='mt-0.5 space-y-0.5 border-l pl-2'>
                  {category.endpoints.map((endpoint) => {
                    const selected = endpoint.id === selectedId
                    return (
                      <button
                        key={endpoint.id}
                        type='button'
                        onClick={() => onSelect(endpoint.id)}
                        className={cn(
                          'flex w-full items-center justify-between gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors',
                          selected
                            ? 'bg-muted text-foreground font-medium'
                            : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground'
                        )}
                      >
                        <span className='truncate'>{endpoint.name}</span>
                        <Badge
                          variant='outline'
                          className={cn(
                            'h-4 shrink-0 rounded px-1.5 font-mono text-[9px]',
                            methodClass(endpoint.method)
                          )}
                        >
                          {endpoint.method}
                        </Badge>
                      </button>
                    )
                  })}
                </div>
              </CollapsibleContent>
            </Collapsible>
          )
        })}
      </nav>
    </div>
  )
}
