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
import { Globe, Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Item,
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemTitle,
} from '@/components/ui/item'
import {
  Popover,
  PopoverContent,
  PopoverTitle,
  PopoverTrigger,
} from '@/components/ui/popover'

import type { useApiKeyAddresses } from '../hooks/use-api-key-addresses'
import { useApiKeys } from './api-keys-provider'
import { KeyDiagnosticButton } from './key-diagnostic-dialog'

type ApiKeyAddressProps = ReturnType<typeof useApiKeyAddresses>

export function ApiKeysAddressBar({ addresses, loading }: ApiKeyAddressProps) {
  const { t } = useTranslation()

  if (loading) return null

  return (
    <ul
      aria-label={t('API Addresses')}
      className='bg-muted/30 flex max-h-[min(9rem,25dvh)] shrink-0 flex-wrap items-start gap-2 overflow-y-auto rounded-lg border px-3 py-2'
    >
      {addresses.map((address) => (
        <li
          key={address.key}
          className='bg-background flex max-w-full min-w-0 items-center gap-1.5 rounded-md border px-2.5 py-1'
        >
          <span
            className='text-muted-foreground max-w-[40%] shrink-0 truncate text-xs font-medium'
            title={address.route}
          >
            {address.route}
          </span>
          <span aria-hidden='true' className='text-muted-foreground text-xs'>
            |
          </span>
          <code
            className='max-w-[200px] min-w-0 truncate text-xs select-text'
            title={address.url}
          >
            {address.url}
          </code>
          <CopyButton
            value={address.url}
            size='sm'
            className='size-6 p-0'
            iconClassName='size-3'
            tooltip={t('Copy API URL')}
            aria-label={`${t('Copy API URL')}: ${address.url}`}
          />
        </li>
      ))}
    </ul>
  )
}

export function ApiKeysPrimaryButtons({
  addresses,
  loading,
}: ApiKeyAddressProps) {
  const { t } = useTranslation()
  const { setOpen } = useApiKeys()

  return (
    <div className='flex flex-wrap gap-2'>
      <Popover>
        <PopoverTrigger render={<Button variant='outline' size='sm' />}>
          <Globe aria-hidden='true' />
          {t('API Addresses')}
        </PopoverTrigger>
        <PopoverContent
          align='end'
          className='max-h-[min(28rem,var(--available-height))] w-96 max-w-[calc(100vw-2rem)] overflow-y-auto'
        >
          <PopoverTitle>{t('API Addresses')}</PopoverTitle>
          {loading ? (
            <LoadingState inline size='sm' message={t('Loading...')} />
          ) : (
            <ItemGroup>
              {addresses.map((address) => (
                <Item
                  key={address.key}
                  role='listitem'
                  variant='muted'
                  size='xs'
                  className='flex-nowrap items-start'
                >
                  <ItemContent className='min-w-0 gap-1'>
                    <ItemTitle className='line-clamp-none break-all'>
                      {address.route}
                    </ItemTitle>
                    <code className='text-xs break-all select-text'>
                      {address.url}
                    </code>
                    {address.description && (
                      <ItemDescription className='line-clamp-none break-words'>
                        {address.description}
                      </ItemDescription>
                    )}
                  </ItemContent>
                  <ItemActions>
                    <CopyButton
                      value={address.url}
                      size='sm'
                      tooltip={t('Copy API URL')}
                      aria-label={`${t('Copy API URL')}: ${address.url}`}
                    />
                  </ItemActions>
                </Item>
              ))}
            </ItemGroup>
          )}
        </PopoverContent>
      </Popover>
      <KeyDiagnosticButton />
      <Button size='sm' onClick={() => setOpen('create')}>
        <Plus className='h-4 w-4' />
        {t('Create API Key')}
      </Button>
    </div>
  )
}
