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
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { API_BASE_URL } from '@/lib/api-url'

import {
  getEndpointDocId,
  getModelEndpoints,
  getModelPlayground,
  type ModelEndpointMap,
} from '../lib/model-endpoints'
import type { PricingModel } from '../types'

export function ModelApiActions(props: {
  model: PricingModel
  endpointMap: ModelEndpointMap
}) {
  const { t } = useTranslation()
  const endpoint = getModelEndpoints(props.model, props.endpointMap)[0]
  const path = endpoint?.path
  const documentation = endpoint ? getEndpointDocId(endpoint.type) : undefined
  const playground = getModelPlayground(props.model)
  const origin =
    API_BASE_URL ||
    (typeof window !== 'undefined' ? window.location.origin : '')
  return (
    <div className='flex min-w-0 flex-col gap-2'>
      {path && (
        <code
          className='text-muted-foreground text-sm [overflow-wrap:anywhere]'
          title={`${endpoint?.method ?? 'POST'} ${path}`}
        >
          {endpoint?.method ?? 'POST'} {path}
        </code>
      )}
      <div className='flex flex-wrap gap-1.5'>
        {path && (
          <CopyButton
            value={`${origin}${path}`}
            tooltip={t('Copy URL')}
            size='sm'
            variant='outline'
            className='h-9 text-sm'
          >
            {t('Copy URL')}
          </CopyButton>
        )}
        <Button
          role='link'
          variant='outline'
          className='h-9 text-sm'
          size='sm'
          render={
            documentation ? (
              <Link to='/help/api' search={{ endpoint: documentation }} />
            ) : (
              <Link to='/help' />
            )
          }
        >
          {t('API Reference')}
        </Button>
        {playground && (
          <Button
            role='link'
            variant='outline'
            className='h-9 text-sm'
            size='sm'
            render={<Link to={playground} />}
          >
            {t('Playground')}
          </Button>
        )}
      </div>
    </div>
  )
}
