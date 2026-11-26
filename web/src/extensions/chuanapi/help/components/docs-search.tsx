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
import { useNavigate } from '@tanstack/react-router'
import { Search } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'

import { searchDocs, type DocsSearchResult } from '../lib/docs-search'

export function DocsSearch() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')

  function changeOpen(next: boolean) {
    setOpen(next)
    if (!next) setQuery('')
  }

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (
        event.defaultPrevented ||
        event.repeat ||
        event.key.toLowerCase() !== 'k' ||
        (!event.ctrlKey && !event.metaKey)
      ) {
        return
      }
      event.preventDefault()
      setOpen(true)
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [])

  function selectResult(result: DocsSearchResult) {
    changeOpen(false)
    if (result.type === 'endpoint') {
      void navigate({ to: '/help/api', search: { endpoint: result.id } })
      return
    }
    void navigate({ to: '/help/guides/$slug', params: { slug: result.id } })
  }

  return (
    <>
      <Button
        type='button'
        variant='outline'
        className='w-full justify-start gap-2 sm:max-w-xs'
        aria-keyshortcuts='Control+k Meta+k'
        onClick={() => changeOpen(true)}
      >
        <Search />
        {t('Search documentation')}
      </Button>
      <CommandDialog
        open={open}
        onOpenChange={changeOpen}
        title={t('Search documentation')}
      >
        <Command shouldFilter={false}>
          <CommandInput
            aria-label={t('Search documentation')}
            placeholder={t('Search documentation')}
            value={query}
            onValueChange={setQuery}
          />
          <CommandList>
            {!query.trim() && (
              <div className='text-muted-foreground px-3 py-4 text-sm'>
                {t('Search by keyword, client, or API path.')}
              </div>
            )}
            {query.trim() && (
              <CommandEmpty>{t('No results found.')}</CommandEmpty>
            )}
            {searchDocs(query).map((result) => (
              <CommandItem
                key={`${result.type}:${result.id}`}
                value={`${result.type}:${result.id}`}
                onSelect={() => selectResult(result)}
                className='flex-col items-start gap-0.5'
              >
                <span className='font-medium'>{result.title}</span>
                <span className='text-muted-foreground text-xs'>
                  {result.description}
                </span>
              </CommandItem>
            ))}
          </CommandList>
        </Command>
      </CommandDialog>
    </>
  )
}
