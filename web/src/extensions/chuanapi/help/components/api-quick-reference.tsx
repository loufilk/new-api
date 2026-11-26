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

import { allEndpoints } from '../content/endpoints'

const quickEndpointIds = [
  'models-list',
  'chat-completions',
  'responses',
  'claude-messages',
  'image-generations',
  'gemini-generate-content',
]
const quickEndpoints = quickEndpointIds.flatMap((id) =>
  allEndpoints.filter((endpoint) => endpoint.id === id)
)

export function ApiQuickReference({ origin }: { origin: string }) {
  const { t } = useTranslation()
  const protocols = [
    {
      name: 'OpenAI',
      baseUrl: `${origin}/v1`,
      auth: 'Authorization: Bearer API_TOKEN',
    },
    { name: 'Anthropic', baseUrl: origin, auth: 'x-api-key: API_TOKEN' },
    { name: 'Gemini', baseUrl: origin, auth: 'x-goog-api-key: API_TOKEN' },
  ]

  return (
    <section
      aria-labelledby='api-quick-reference'
      className='mb-8 space-y-4 border-b pb-6'
    >
      <h1
        id='api-quick-reference'
        className='text-foreground text-3xl font-bold tracking-tight'
      >
        {t('API Reference')}
      </h1>
      <p className='text-muted-foreground text-sm'>
        {t('Choose the protocol supported by your model and API key.')}
      </p>
      <div className='grid gap-2 2xl:grid-cols-3'>
        {protocols.map((protocol) => (
          <div
            key={protocol.name}
            className='bg-card space-y-1 rounded-lg border p-3'
          >
            <h2 className='font-semibold'>{protocol.name}</h2>
            <div className='flex items-center justify-between gap-1'>
              <code className='min-w-0 text-xs break-all'>
                {protocol.baseUrl}
              </code>
              <CopyButton value={protocol.baseUrl} aria-label={t('Copy URL')} />
            </div>
            <code className='text-muted-foreground block text-xs break-all'>
              {protocol.auth}
            </code>
          </div>
        ))}
      </div>
      <h2 className='font-semibold'>{t('Common endpoints')}</h2>
      <ul className='grid gap-2 sm:grid-cols-2'>
        {quickEndpoints.map((endpoint) => (
          <li key={endpoint.id} className='min-w-0'>
            <Link
              to='/help/api'
              search={{ endpoint: endpoint.id }}
              className='hover:bg-muted block h-full rounded-lg border px-3 py-2'
            >
              <code className='block text-xs break-all'>
                {endpoint.method} {endpoint.path}
              </code>
              <span className='text-muted-foreground text-xs'>
                {endpoint.name}
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  )
}
