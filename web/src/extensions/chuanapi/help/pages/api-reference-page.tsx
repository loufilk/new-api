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
import {
  ArrowLeft,
  Braces,
  ChevronDown,
  CircleHelp,
  FileJson,
  KeyRound,
  Menu,
  Server,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { PublicLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { cn } from '@/lib/utils'

import { AiDocsCallout } from '../components/ai-docs-callout'
import { ApiCodeBlock } from '../components/api-code-block'
import { ApiEndpointTree } from '../components/api-endpoint-tree'
import { getEndpoint } from '../content/endpoints'
import type { EndpointDoc, HttpMethod } from '../content/types'
import { generateEndpointSamples, sampleLanguages } from '../lib/api-samples'
import { buildEndpointMarkdown, getDocsOrigin } from '../lib/docs-utils'

function methodClass(method: HttpMethod): string {
  return method === 'GET'
    ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
    : 'border-blue-500/30 bg-blue-500/10 text-blue-700 dark:text-blue-300'
}

function MethodBadge({ method }: { method: HttpMethod }) {
  return (
    <Badge
      variant='outline'
      className={cn('rounded px-2 font-mono text-[10px]', methodClass(method))}
    >
      {method}
    </Badge>
  )
}

function requestContentType(endpoint: EndpointDoc): string {
  if (endpoint.requestKind === 'multipart') {
    return 'multipart/form-data'
  }
  if (endpoint.requestKind === 'websocket') {
    return 'WebSocket upgrade'
  }
  if (endpoint.requestKind === 'json') {
    return 'application/json'
  }
  return 'none'
}

function authDescription(endpoint: EndpointDoc): string {
  if (endpoint.authHeader === 'x-api-key') {
    return 'x-api-key: API_TOKEN'
  }
  if (endpoint.authHeader === 'x-goog-api-key') {
    return 'x-goog-api-key: API_TOKEN'
  }
  return 'Authorization: Bearer API_TOKEN'
}

function ApiExamples({
  endpoint,
  samples,
}: {
  endpoint: EndpointDoc
  samples: ReturnType<typeof generateEndpointSamples>
}) {
  const { t } = useTranslation()
  const firstResponse = String(endpoint.responses[0]?.status ?? 200)

  return (
    <div className='space-y-6'>
      <section className='space-y-3'>
        <div>
          <h2 className='text-foreground text-base font-semibold'>
            {t('Code examples')}
          </h2>
          <p className='text-muted-foreground text-xs'>
            {t('Copy a request in your preferred language.')}
          </p>
        </div>
        <Tabs defaultValue='curl'>
          <TabsList variant='line' className='max-w-full overflow-x-auto'>
            {sampleLanguages.map((language) => (
              <TabsTrigger key={language.id} value={language.id}>
                {language.label}
              </TabsTrigger>
            ))}
          </TabsList>
          {sampleLanguages.map((language) => (
            <TabsContent key={language.id} value={language.id}>
              <ApiCodeBlock
                code={samples[language.id]}
                label={language.label}
              />
            </TabsContent>
          ))}
        </Tabs>
      </section>

      <section className='space-y-3'>
        <div>
          <h2 className='text-foreground text-base font-semibold'>
            {t('Response examples')}
          </h2>
          <p className='text-muted-foreground text-xs'>
            {t('Inspect common success and error response shapes.')}
          </p>
        </div>
        <Tabs defaultValue={firstResponse}>
          <TabsList variant='line'>
            {endpoint.responses.map((response) => (
              <TabsTrigger
                key={response.status}
                value={String(response.status)}
              >
                {response.status}
              </TabsTrigger>
            ))}
          </TabsList>
          {endpoint.responses.map((response) => (
            <TabsContent
              key={response.status}
              value={String(response.status)}
              className='space-y-2'
            >
              <div>
                <p className='text-foreground text-sm font-medium'>
                  {response.label}
                </p>
                <p className='text-muted-foreground text-xs leading-relaxed'>
                  {response.description}
                </p>
              </div>
              <ApiCodeBlock
                code={response.body}
                label={`${response.status} ${response.label}`}
              />
            </TabsContent>
          ))}
        </Tabs>
      </section>

      <section className='border-border bg-muted/20 space-y-2 rounded-xl border p-3'>
        <p className='text-foreground text-sm font-medium'>
          {t('On this page')}
        </p>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          className='h-7 w-full justify-start px-2 text-xs'
          onClick={() => document.querySelector('#overview')?.scrollIntoView()}
        >
          {t('Overview')}
        </Button>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          className='h-7 w-full justify-start px-2 text-xs'
          onClick={() => document.querySelector('#details')?.scrollIntoView()}
        >
          {t('Call details')}
        </Button>
      </section>
    </div>
  )
}

export function ApiReferencePage({
  endpointId,
  onEndpointChange,
}: {
  endpointId?: string
  onEndpointChange: (id: string) => void
}) {
  const { t } = useTranslation()
  const [mobileTreeOpen, setMobileTreeOpen] = useState(false)
  const endpoint = getEndpoint(endpointId)
  const origin = getDocsOrigin()
  const samples = useMemo(
    () => generateEndpointSamples(endpoint, origin),
    [endpoint, origin]
  )
  const endpointUrl = `${origin}${endpoint.path}`

  function selectEndpoint(id: string) {
    onEndpointChange(id)
    setMobileTreeOpen(false)
    window.scrollTo({ top: 0, behavior: 'smooth' })
  }

  return (
    <PublicLayout showMainContainer={false}>
      <main className='mx-auto min-h-svh w-full max-w-[1600px] pt-16'>
        <div className='border-border lg:grid lg:grid-cols-[260px_minmax(0,1fr)] xl:grid-cols-[260px_minmax(0,1fr)_380px]'>
          <aside className='border-border bg-muted/10 hidden border-r lg:block'>
            <div className='sticky top-16 h-[calc(100svh-4rem)] overflow-y-auto p-4'>
              <Button
                variant='ghost'
                size='sm'
                className='mb-4 h-8 px-2'
                render={<Link to='/help' />}
              >
                <ArrowLeft />
                {t('Back to docs center')}
              </Button>
              <ApiEndpointTree
                selectedId={endpoint.id}
                onSelect={selectEndpoint}
              />
            </div>
          </aside>

          <article
            id='content'
            className='min-w-0 px-4 py-6 sm:px-6 lg:px-8 lg:py-8'
          >
            <Collapsible
              open={mobileTreeOpen}
              onOpenChange={setMobileTreeOpen}
              className='mb-6 lg:hidden'
            >
              <CollapsibleTrigger className='border-border bg-card flex w-full items-center justify-between rounded-lg border px-3 py-2 text-sm font-medium'>
                <span className='flex items-center gap-2'>
                  <Menu className='size-4' />
                  {t('API endpoints')}
                </span>
                <ChevronDown
                  className={cn(
                    'size-4 transition-transform',
                    mobileTreeOpen && 'rotate-180'
                  )}
                />
              </CollapsibleTrigger>
              <CollapsibleContent>
                <div className='border-border bg-card mt-2 rounded-lg border p-3'>
                  <ApiEndpointTree
                    selectedId={endpoint.id}
                    onSelect={selectEndpoint}
                  />
                </div>
              </CollapsibleContent>
            </Collapsible>

            <section id='overview' className='scroll-mt-24 space-y-5'>
              <div className='space-y-3'>
                <MethodBadge method={endpoint.method} />
                <h1 className='text-foreground text-3xl font-bold tracking-tight'>
                  {endpoint.name}
                </h1>
                <p className='text-muted-foreground max-w-3xl text-sm leading-7 sm:text-base'>
                  {endpoint.description}
                </p>
                <CopyButton
                  value={buildEndpointMarkdown(endpoint, samples.curl, origin)}
                  variant='outline'
                  size='sm'
                  className='px-3'
                >
                  <FileJson />
                  {t('Copy Markdown')}
                </CopyButton>
              </div>

              <AiDocsCallout compact />

              <div className='border-border bg-card overflow-hidden rounded-xl border'>
                <div className='border-border bg-muted/30 flex items-center gap-2 border-b px-4 py-2.5'>
                  <Server className='text-muted-foreground size-4' />
                  <code className='text-muted-foreground min-w-0 truncate text-xs'>
                    {origin}
                  </code>
                </div>
                <div className='flex flex-wrap items-center gap-3 px-4 py-3'>
                  <MethodBadge method={endpoint.method} />
                  <code className='text-foreground min-w-0 flex-1 font-mono text-sm break-all'>
                    {endpoint.path}
                  </code>
                  <CopyButton
                    value={endpointUrl}
                    size='sm'
                    className='h-7 px-2 text-xs'
                  >
                    {t('Copy URL')}
                  </CopyButton>
                </div>
              </div>

              <div className='grid gap-3 sm:grid-cols-2'>
                <div className='border-border bg-card rounded-xl border p-4'>
                  <div className='text-foreground flex items-center gap-2 text-sm font-semibold'>
                    <KeyRound className='text-primary size-4' />
                    {t('Authentication')}
                  </div>
                  <code className='text-muted-foreground mt-2 block text-xs break-all'>
                    {authDescription(endpoint)}
                  </code>
                </div>
                <div className='border-border bg-card rounded-xl border p-4'>
                  <div className='text-foreground flex items-center gap-2 text-sm font-semibold'>
                    <Braces className='text-primary size-4' />
                    {t('Request body')}
                  </div>
                  <code className='text-muted-foreground mt-2 block text-xs'>
                    {requestContentType(endpoint)}
                  </code>
                </div>
              </div>
            </section>

            <section id='details' className='mt-10 scroll-mt-24 space-y-4'>
              <div>
                <h2 className='text-foreground text-2xl font-semibold'>
                  {t('Call details')}
                </h2>
                <p className='text-muted-foreground mt-1 text-sm'>
                  {t(
                    'These notes explain how the gateway handles authentication, relay format, and response shape.'
                  )}
                </p>
              </div>

              <div className='grid gap-3 md:grid-cols-3'>
                <div className='border-border bg-card rounded-xl border p-4'>
                  <p className='text-muted-foreground text-xs'>
                    {t('Authentication')}
                  </p>
                  <p className='text-foreground mt-1 text-sm font-medium'>
                    {authDescription(endpoint)}
                  </p>
                </div>
                <div className='border-border bg-card rounded-xl border p-4'>
                  <p className='text-muted-foreground text-xs'>
                    {t('Relay format')}
                  </p>
                  <p className='text-foreground mt-1 text-sm font-medium'>
                    {endpoint.relayFormat}
                  </p>
                </div>
                <div className='border-border bg-card rounded-xl border p-4'>
                  <p className='text-muted-foreground text-xs'>
                    {t('Response type')}
                  </p>
                  <p className='text-foreground mt-1 text-sm font-medium'>
                    {endpoint.responseType}
                  </p>
                </div>
              </div>

              <div className='space-y-2'>
                {endpoint.notes.map((note) => (
                  <div
                    key={note}
                    className='border-border bg-muted/20 text-muted-foreground rounded-lg border px-4 py-3 text-sm leading-relaxed'
                  >
                    {note}
                  </div>
                ))}
              </div>

              <div className='border-primary/20 bg-primary/5 flex gap-3 rounded-xl border p-4'>
                <CircleHelp className='text-primary mt-0.5 size-5 shrink-0' />
                <p className='text-muted-foreground text-sm leading-relaxed'>
                  {t(
                    'Endpoint availability depends on the model catalog and API key group.'
                  )}{' '}
                  <Link
                    to='/pricing'
                    className='text-primary font-medium hover:underline'
                  >
                    {t('Check the pricing catalog')}
                  </Link>
                </p>
              </div>
            </section>

            <div className='mt-10 xl:hidden'>
              <ApiExamples
                key={endpoint.id}
                endpoint={endpoint}
                samples={samples}
              />
            </div>
          </article>

          <aside className='border-border bg-muted/10 hidden border-l xl:block'>
            <div className='sticky top-16 h-[calc(100svh-4rem)] overflow-y-auto p-4'>
              <ApiExamples
                key={endpoint.id}
                endpoint={endpoint}
                samples={samples}
              />
            </div>
          </aside>
        </div>
      </main>
    </PublicLayout>
  )
}
