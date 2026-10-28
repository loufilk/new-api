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
import { KeyRound, ScrollText, Zap } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { BundledLanguage } from 'shiki/bundle/web'

import {
  CodeBlock,
  CodeBlockCopyButton,
} from '@/components/ai-elements/code-block'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useStatus } from '@/hooks/use-status'
import { API_BASE_URL } from '@/lib/api-url'

import {
  buildModelCodeSample,
  type ModelSampleLanguage as Lang,
} from '../lib/model-code-samples'
import {
  getEndpointDocId,
  getModelEndpoints,
  type ModelEndpointMap,
} from '../lib/model-endpoints'
import type { PricingModel } from '../types'

// ---------------------------------------------------------------------------
// Code-sample registry
// ---------------------------------------------------------------------------
//
// Each sample is keyed by language and endpoint type. The endpoint type comes
// from the model's `supported_endpoint_types`; we render samples only for the
// types the model actually supports. This keeps copy-pasted code accurate and
// provider-shaped (OpenAI vs Anthropic vs Gemini, etc.).

const LANG_LABELS: Record<Lang, string> = {
  curl: 'cURL',
  python: 'Python',
  typescript: 'TypeScript',
  javascript: 'JavaScript',
}

const LANG_HIGHLIGHT: Record<Lang, BundledLanguage> = {
  curl: 'bash',
  python: 'python',
  typescript: 'typescript',
  javascript: 'javascript',
}

// ---------------------------------------------------------------------------
// Code samples section
// ---------------------------------------------------------------------------

function CodeSamplesSection(props: {
  model: PricingModel
  endpointMap: ModelEndpointMap
}) {
  const { t } = useTranslation()
  const { status } = useStatus()

  const baseUrl = useMemo(() => {
    // Copyable samples must always show the API origin. An explicitly
    // configured API base wins over the admin-configured server address,
    // which may be the console address in split deployments.
    if (API_BASE_URL) return API_BASE_URL
    const candidate =
      (status as Record<string, unknown> | null)?.server_address ??
      (status as Record<string, unknown> | null)?.serverAddress ??
      (status?.data as Record<string, unknown> | undefined)?.server_address ??
      (status?.data as Record<string, unknown> | undefined)?.serverAddress
    if (candidate && typeof candidate === 'string') {
      return candidate.replace(/\/$/, '')
    }
    if (typeof window !== 'undefined') return window.location.origin
    return 'https://api.example.com'
  }, [status])

  const endpoints = useMemo(
    () => getModelEndpoints(props.model, props.endpointMap),
    [props.model, props.endpointMap]
  )

  const [endpointType, setEndpointType] = useState<string>(
    endpoints[0]?.type ?? ''
  )
  const [lang, setLang] = useState<Lang>('curl')

  const activeEndpoint = useMemo(() => {
    return endpoints.find((e) => e.type === endpointType) ?? endpoints[0]
  }, [endpointType, endpoints])

  if (endpoints.length === 0 || !activeEndpoint) {
    return null
  }

  const code = buildModelCodeSample(lang, activeEndpoint.type, {
    baseUrl,
    apiKeyEnv: 'NEW_API_KEY',
    modelName: props.model.model_name || '',
    endpointType: activeEndpoint.type,
    endpointPath: activeEndpoint.path,
  })

  return (
    <section>
      <SectionTitle icon={ScrollText}>{t('Code samples')}</SectionTitle>

      <div className='flex flex-wrap items-center gap-2'>
        {endpoints.length > 1 && (
          <Tabs value={activeEndpoint.type} onValueChange={setEndpointType}>
            <TabsList className='bg-muted/40 h-8 p-0.5'>
              {endpoints.map((ep) => (
                <TabsTrigger
                  key={ep.type}
                  value={ep.type}
                  className='h-7 px-2.5 text-xs'
                >
                  {ep.type}
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
        )}

        <Tabs
          value={lang}
          onValueChange={(v) => setLang(v as Lang)}
          className='ml-auto'
        >
          <TabsList className='bg-muted/40 h-8 p-0.5'>
            {(Object.keys(LANG_LABELS) as Lang[]).map((l) => (
              <TabsTrigger key={l} value={l} className='h-7 px-2.5 text-xs'>
                {LANG_LABELS[l]}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      </div>

      <div className='mt-3'>
        {code ? (
          <CodeBlock code={code} language={LANG_HIGHLIGHT[lang]}>
            <CodeBlockCopyButton />
          </CodeBlock>
        ) : (
          <p className='text-muted-foreground text-sm'>
            {t('No verified code sample for this endpoint.')}
          </p>
        )}
        {getEndpointDocId(activeEndpoint.type) ? (
          <Link
            to='/help/api'
            search={{ endpoint: getEndpointDocId(activeEndpoint.type) }}
            className='text-primary mt-2 inline-block text-sm underline'
          >
            {t('API Reference')}
          </Link>
        ) : (
          <Link
            to='/help'
            className='text-primary mt-2 inline-block text-sm underline'
          >
            {t('API Reference')}
          </Link>
        )}
      </div>

      <p className='text-muted-foreground mt-2 text-xs'>
        {t('Replace')}{' '}
        <code className='bg-muted rounded px-1 py-0.5 font-mono text-[11px]'>
          {'<YOUR_API_KEY>'}
        </code>{' '}
        {t('with the API key from your token settings.')}
      </p>
    </section>
  )
}

// ---------------------------------------------------------------------------
// Authentication preview
// ---------------------------------------------------------------------------

function AuthSection() {
  const { t } = useTranslation()
  return (
    <section>
      <SectionTitle icon={KeyRound}>{t('Authentication')}</SectionTitle>
      <dl className='grid gap-3 text-xs sm:grid-cols-3'>
        <div className='flex flex-col gap-1'>
          <dt>OpenAI</dt>
          <dd>
            <code>Authorization: Bearer &lt;TOKEN&gt;</code>
          </dd>
        </div>
        <div className='flex flex-col gap-1'>
          <dt>Anthropic</dt>
          <dd>
            <code>x-api-key: &lt;TOKEN&gt;</code>
            <br />
            <code>anthropic-version: 2023-06-01</code>
          </dd>
        </div>
        <div className='flex flex-col gap-1'>
          <dt>Gemini</dt>
          <dd>
            <code>x-goog-api-key: &lt;TOKEN&gt;</code>
          </dd>
        </div>
      </dl>
      <p className='text-muted-foreground mt-3 text-xs'>
        {t(
          'Endpoint availability depends on the model catalog and API key group.'
        )}
      </p>
    </section>
  )
}

// ---------------------------------------------------------------------------
// Composite API tab
// ---------------------------------------------------------------------------

export function ModelDetailsApi(props: {
  model: PricingModel
  endpointMap: ModelEndpointMap
}) {
  return (
    <div className='space-y-6'>
      <CodeSamplesSection model={props.model} endpointMap={props.endpointMap} />
      <AuthSection />
    </div>
  )
}

// ---------------------------------------------------------------------------
// Local UI helpers
// ---------------------------------------------------------------------------

function SectionTitle(props: {
  children: React.ReactNode
  icon: React.ComponentType<{ className?: string }>
}) {
  const Icon = props.icon
  return (
    <h3 className='text-foreground mb-3 flex items-center gap-1.5 text-sm font-semibold'>
      <Icon className='text-muted-foreground/70 size-3.5' />
      {props.children}
    </h3>
  )
}

// Re-export so the parent can keep its own SectionTitle if it wants:
export { Zap as ApiTabIcon }
