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
  ArrowRight,
  Check,
  Download,
  KeyRound,
  Layers3,
  Rocket,
  Terminal,
  Workflow,
} from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useApiInfo } from '@/features/dashboard/hooks/use-status-data'
import { EXCLUDED_GROUPS } from '@/features/pricing/constants'
import { usePricingData } from '@/features/pricing/hooks/use-pricing-data'
import {
  getEndpointDocId,
  getModelEndpoints,
  getModelPlayground,
} from '@/features/pricing/lib/model-endpoints'
import { getConfiguredGroupRatio } from '@/features/pricing/lib/model-helpers'
import { cn } from '@/lib/utils'

import { ApiCodeBlock } from '../help/components/api-code-block'
import { getDocsOrigin } from '../help/lib/docs-utils'
import {
  buildClientConfig,
  buildEnvironmentConfig,
  buildVerificationRequest,
  getClientEndpointType,
  getIntegrationClients,
  normalizeGatewayBaseUrl,
  type IntegrationClient,
  type IntegrationShell,
} from './lib/integration-config'

type IntegrationContext = { initialModel?: string; initialGroup?: string }

const CLIENT_LABELS: Record<IntegrationClient, string> = {
  api: 'API request',
  'claude-code': 'Claude Code',
  'codex-cli': 'Codex CLI',
}
const STEPS = [
  { title: 'Choose a model', icon: Layers3 },
  { title: 'Configure your client', icon: Terminal },
  { title: 'Verify your connection', icon: Workflow },
] as const

export function IntegrationWizard(
  props: IntegrationContext & {
    variant?: 'default' | 'outline' | 'secondary'
    size?: 'default' | 'sm'
    className?: string
  }
) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button
        variant={props.variant ?? 'outline'}
        size={props.size ?? 'sm'}
        className={props.className}
        onClick={() => setOpen(true)}
      >
        <Rocket aria-hidden='true' />
        {t('Integration wizard')}
      </Button>
      <Dialog
        open={open}
        onOpenChange={setOpen}
        title={t('Integration wizard')}
        description={t(
          'Choose a model, configure your client, and make your first request.'
        )}
        contentClassName='sm:max-w-4xl'
      >
        {open && (
          <IntegrationSetup
            initialModel={props.initialModel}
            initialGroup={props.initialGroup}
          />
        )}
      </Dialog>
    </>
  )
}

export function GettingStartedPage() {
  const { t } = useTranslation()
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Integration wizard')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t(
          'Choose a model, configure your client, and make your first request.'
        )}
      </SectionPageLayout.Description>
      <SectionPageLayout.Actions>
        <Button variant='outline' size='sm' render={<Link to='/help' />}>
          {t('Documentation')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='bg-card mx-auto max-w-5xl rounded-2xl border p-4 shadow-xs sm:p-6'>
          <IntegrationSetup />
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

export function IntegrationSetup(props: IntegrationContext) {
  const { t } = useTranslation()
  const id = useId()
  const pricing = usePricingData()
  const { items: apiInfo } = useApiInfo()
  const [step, setStep] = useState(0)
  const [chosenGroup, setChosenGroup] = useState(props.initialGroup ?? '')
  const [chosenProvider, setChosenProvider] = useState('all')
  const [chosenModel, setChosenModel] = useState(props.initialModel ?? '')
  const [chosenClient, setChosenClient] = useState<IntegrationClient>('api')
  const [chosenEndpoint, setChosenEndpoint] = useState('')
  const [chosenBase, setChosenBase] = useState('')
  const [shell, setShell] = useState<IntegrationShell>('posix')

  const providerNames = [
    ...new Set(
      pricing.models.map((model) => model.vendor_name || '__unassigned__')
    ),
  ]
  const provider = providerNames.includes(chosenProvider)
    ? chosenProvider
    : 'all'
  const providerModels = pricing.models.filter(
    (model) =>
      provider === 'all' || (model.vendor_name || '__unassigned__') === provider
  )
  const allGroups = Object.keys(pricing.usableGroup).filter(
    (group) =>
      !EXCLUDED_GROUPS.includes(group) &&
      providerModels.some((model) => model.enable_groups?.includes(group))
  )
  const initialModel = pricing.models.find(
    (model) => model.model_name === props.initialModel
  )
  const group = allGroups.includes(chosenGroup)
    ? chosenGroup
    : (initialModel?.enable_groups.find((item) => allGroups.includes(item)) ??
      allGroups[0] ??
      '')
  const availableModels = providerModels.filter((model) =>
    model.enable_groups?.includes(group)
  )
  const model =
    availableModels.find((item) => item.model_name === chosenModel) ??
    availableModels[0]
  const endpoints = model ? getModelEndpoints(model, pricing.endpointMap) : []
  const clients = model ? getIntegrationClients(model, pricing.endpointMap) : []
  const client = clients.includes(chosenClient) ? chosenClient : 'api'
  const preferredEndpoint = endpoints.some(
    (item) => item.type === chosenEndpoint
  )
    ? chosenEndpoint
    : (endpoints[0]?.type ?? '')
  const endpointType = getClientEndpointType(client, preferredEndpoint)
  const endpoint = endpoints.find((item) => item.type === endpointType)
  const lines = [
    {
      value: normalizeGatewayBaseUrl(getDocsOrigin()),
      label: t('Current gateway'),
    },
    ...apiInfo.map((item) => ({
      value: normalizeGatewayBaseUrl(item.url),
      label: item.route || item.description || item.url,
    })),
  ].filter(
    (item, index, list): item is { value: string; label: string } =>
      Boolean(item.value) &&
      list.findIndex((other) => other.value === item.value) === index
  )
  const base =
    lines.find((line) => line.value === chosenBase)?.value ??
    lines[0]?.value ??
    ''
  const config = model
    ? buildClientConfig(client, shell, base, model.model_name)
    : null
  const request = model
    ? buildVerificationRequest(
        shell,
        base,
        model,
        pricing.endpointMap,
        endpointType
      )
    : null
  const playground = model ? getModelPlayground(model) : undefined
  const docId = endpoint ? getEndpointDocId(endpoint.type) : undefined
  const canContinue = Boolean(model && endpoint && base)

  function downloadConfig() {
    if (!config) return
    const url = URL.createObjectURL(
      new Blob([`${config.code}\n`], { type: 'text/plain;charset=utf-8' })
    )
    const link = document.createElement('a')
    link.href = url
    link.download = config.filename
    link.click()
    URL.revokeObjectURL(url)
  }

  if (pricing.isLoading) {
    return (
      <div className='space-y-4'>
        <Skeleton className='h-16 w-full' />
        <Skeleton className='h-72 w-full' />
      </div>
    )
  }
  if (pricing.error) {
    return (
      <ErrorState
        title={t('Unable to load available models')}
        onRetry={() => void pricing.refetch()}
      />
    )
  }
  if (!allGroups.length || !pricing.models.length) {
    return (
      <EmptyState
        title={t('No available models')}
        description={t(
          'Models and calling groups will appear here when they are enabled for your account.'
        )}
        action={
          <Button variant='outline' render={<Link to='/pricing' />}>
            {t('Model Square')}
          </Button>
        }
      />
    )
  }

  return (
    <div className='space-y-6'>
      <ol
        aria-label={t('Integration progress')}
        className='grid grid-cols-3 gap-2 sm:gap-3'
      >
        {STEPS.map((item, index) => {
          const Icon = item.icon
          return (
            <li key={item.title}>
              <button
                type='button'
                aria-current={step === index ? 'step' : undefined}
                disabled={index > 0 && !canContinue}
                onClick={() => setStep(index)}
                className={cn(
                  'flex h-full w-full flex-col gap-2 rounded-xl border p-3 text-start transition-colors disabled:cursor-not-allowed disabled:opacity-50 sm:flex-row sm:items-center',
                  step === index
                    ? 'border-primary/40 bg-primary/5'
                    : 'border-border bg-muted/20 hover:bg-muted/40'
                )}
              >
                <span
                  className={cn(
                    'flex size-7 shrink-0 items-center justify-center rounded-full text-xs font-semibold',
                    step === index
                      ? 'bg-primary text-primary-foreground'
                      : 'bg-muted text-muted-foreground'
                  )}
                >
                  {step > index ? (
                    <Check className='size-4' />
                  ) : (
                    <Icon className='size-4' />
                  )}
                </span>
                <span className='text-xs leading-relaxed font-medium sm:text-sm'>
                  {index + 1}. {t(item.title)}
                </span>
              </button>
            </li>
          )
        })}
      </ol>

      {step === 0 && (
        <div className='grid gap-5 lg:grid-cols-[minmax(0,1fr)_minmax(0,.8fr)]'>
          <div className='space-y-5'>
            <div className='space-y-2'>
              <Label htmlFor={`${id}-provider`}>{t('Model provider')}</Label>
              <Combobox
                id={`${id}-provider`}
                value={provider}
                onValueChange={(value) => setChosenProvider(value ?? 'all')}
                options={[
                  { value: 'all', label: t('All providers') },
                  ...providerNames.map((name) => ({
                    value: name,
                    label: name === '__unassigned__' ? t('Other') : name,
                  })),
                ]}
                aria-label={t('Model provider')}
              />
            </div>
            <div className='space-y-2'>
              <Label htmlFor={`${id}-group`}>{t('Calling group')}</Label>
              <Combobox
                id={`${id}-group`}
                value={group}
                onValueChange={(value) => setChosenGroup(value ?? '')}
                options={allGroups.map((value) => ({
                  value,
                  label: value,
                  description: pricing.usableGroup[value]?.desc,
                }))}
                aria-label={t('Calling group')}
              />
              <p className='text-muted-foreground text-xs leading-relaxed'>
                {pricing.usableGroup[group]?.desc}
              </p>
            </div>
            <div className='space-y-2'>
              <Label htmlFor={`${id}-model`}>{t('Model')}</Label>
              <Combobox
                id={`${id}-model`}
                value={model?.model_name}
                onValueChange={(value) => setChosenModel(value ?? '')}
                options={availableModels.map((item) => ({
                  value: item.model_name,
                  label: item.model_name,
                  description: item.vendor_name,
                }))}
                aria-label={t('Model')}
                emptyText={t('No available models')}
              />
            </div>
            <div className='space-y-2'>
              <Label htmlFor={`${id}-base`}>{t('API address')}</Label>
              <Combobox
                id={`${id}-base`}
                value={base}
                onValueChange={(value) => setChosenBase(value ?? '')}
                options={lines.map((line) => ({
                  ...line,
                  description: line.value,
                }))}
                aria-label={t('API address')}
              />
              <div className='bg-muted/40 flex min-w-0 items-start justify-between gap-2 rounded-lg p-2.5'>
                <code className='text-xs leading-relaxed break-all'>
                  {base}
                </code>
                <CopyButton
                  value={base}
                  variant='ghost'
                  size='icon'
                  aria-label={t('Copy API address')}
                />
              </div>
            </div>
          </div>
          <div className='bg-muted/20 space-y-4 rounded-xl border p-4 sm:p-5'>
            <div className='flex items-center gap-2 text-sm font-semibold'>
              <KeyRound className='text-primary size-4' />
              {t('Prepare your API key')}
            </div>
            <p className='text-muted-foreground text-sm leading-relaxed'>
              {t(
                'Create or edit an API key with the calling group {{group}}. The key determines which group is used for billing.',
                { group }
              )}
            </p>
            <div className='bg-background rounded-lg border p-3'>
              <p className='text-muted-foreground text-xs'>
                {t('Your effective group multiplier')}
              </p>
              <p className='mt-1 font-mono text-2xl font-semibold tabular-nums'>
                {getConfiguredGroupRatio(pricing.groupRatio, group)}×
              </p>
              <p className='text-muted-foreground mt-2 text-xs leading-relaxed'>
                {t(
                  'The model catalog includes the group pricing available to your current account.'
                )}
              </p>
            </div>
            <Button
              variant='outline'
              className='w-full'
              render={<Link to='/keys' />}
            >
              <KeyRound />
              {t('Manage API keys')}
            </Button>
            <p className='text-muted-foreground text-xs leading-relaxed'>
              {t(
                'Keep the key in your local environment variable NEW_API_KEY. Generated files contain configuration only.'
              )}
            </p>
            {model && endpoints.length === 0 && (
              <p
                role='status'
                className='text-sm text-amber-600 dark:text-amber-400'
              >
                {t(
                  'This model has no published API endpoint. Check its model details before configuring a client.'
                )}
              </p>
            )}
          </div>
        </div>
      )}

      {step > 0 && model && (
        <div className='bg-muted/20 flex flex-wrap items-center gap-2 rounded-lg border px-3 py-2 text-xs'>
          <Badge variant='secondary'>{group}</Badge>
          <span className='font-mono break-all'>{model.model_name}</span>
          <span className='text-muted-foreground'>
            {getConfiguredGroupRatio(pricing.groupRatio, group)}×
          </span>
        </div>
      )}

      {step === 1 && (
        <div className='space-y-5'>
          <div className='grid gap-4 sm:grid-cols-2'>
            <div className='space-y-2'>
              <Label htmlFor={`${id}-client`}>{t('Client')}</Label>
              <Combobox
                id={`${id}-client`}
                value={client}
                onValueChange={(value: string | null) => {
                  if (clients.includes(value as IntegrationClient)) {
                    setChosenClient(value as IntegrationClient)
                  }
                }}
                options={clients.map((value) => ({
                  value,
                  label: t(CLIENT_LABELS[value]),
                }))}
                aria-label={t('Client')}
              />
            </div>
            <div className='space-y-2'>
              <Label>{t('Operating system')}</Label>
              <Tabs
                value={shell}
                onValueChange={(value) =>
                  setShell(value === 'powershell' ? 'powershell' : 'posix')
                }
              >
                <TabsList className='w-full' aria-label={t('Operating system')}>
                  <TabsTrigger value='posix' className='flex-1'>
                    macOS / Linux
                  </TabsTrigger>
                  <TabsTrigger value='powershell' className='flex-1'>
                    Windows
                  </TabsTrigger>
                </TabsList>
              </Tabs>
            </div>
          </div>
          {client === 'api' && (
            <div className='space-y-2'>
              <Label htmlFor={`${id}-protocol`}>{t('API protocol')}</Label>
              <Combobox
                id={`${id}-protocol`}
                value={preferredEndpoint}
                onValueChange={(value) => setChosenEndpoint(value ?? '')}
                options={endpoints.map((item) => ({
                  value: item.type,
                  label: item.type,
                  description: item.path,
                }))}
                aria-label={t('API protocol')}
              />
            </div>
          )}
          <div className='space-y-2'>
            <p className='text-sm font-medium'>
              {t('1. Set the API key in your terminal')}
            </p>
            <ApiCodeBlock
              code={buildEnvironmentConfig(shell)}
              label={shell === 'posix' ? 'Bash / Zsh' : 'PowerShell'}
            />
          </div>
          {config && (
            <div className='space-y-2'>
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <p className='text-sm font-medium'>
                  {client === 'codex-cli'
                    ? t('2. Merge this configuration into ~/.codex/config.toml')
                    : t('2. Run the client configuration in the same terminal')}
                </p>
                <Button size='sm' variant='outline' onClick={downloadConfig}>
                  <Download />
                  {t('Download configuration')}
                </Button>
              </div>
              <ApiCodeBlock code={config.code} label={config.filename} />
              {client === 'codex-cli' && (
                <p className='text-muted-foreground text-xs leading-relaxed'>
                  {t(
                    'Codex uses the Responses API. After saving, run codex in the terminal where NEW_API_KEY is set.'
                  )}
                </p>
              )}
            </div>
          )}
          {!config && request && (
            <div className='space-y-2'>
              <p className='text-sm font-medium'>
                {t('2. Copy the request for your selected protocol')}
              </p>
              <ApiCodeBlock
                code={request}
                label={shell === 'posix' ? 'cURL' : 'PowerShell'}
              />
            </div>
          )}
          {!config && !request && (
            <p className='text-muted-foreground text-sm'>
              {t(
                'Use the API reference for this protocol and its required request fields.'
              )}
            </p>
          )}
        </div>
      )}

      {step === 2 && (
        <div className='space-y-5'>
          <div className='space-y-2'>
            <h3 className='text-lg font-semibold'>
              {t('Make your first request')}
            </h3>
            <p className='text-muted-foreground text-sm leading-relaxed'>
              {t(
                'Run the request in your terminal, then open usage logs to confirm the model, calling group, and billed amount. Requests are charged at the selected model price.'
              )}
            </p>
          </div>
          {request && (
            <ApiCodeBlock
              code={request}
              label={shell === 'posix' ? 'cURL' : 'PowerShell'}
            />
          )}
          {!request && (
            <p className='text-muted-foreground bg-muted/20 rounded-lg border p-4 text-sm'>
              {t(
                'Use the API reference for this protocol and its required request fields.'
              )}
            </p>
          )}
          <div className='grid gap-3 sm:grid-cols-3'>
            {[
              {
                code: '401',
                title: t('Check the API key'),
                body: t(
                  'Confirm the key is active and NEW_API_KEY is set in this terminal.'
                ),
              },
              {
                code: '403 / 404',
                title: t('Check model access'),
                body: t(
                  'Check the model name, calling group, and the key model restrictions.'
                ),
              },
              {
                code: '429',
                title: t('Check limits and balance'),
                body: t(
                  'Review your available balance, request limits, and group availability.'
                ),
              },
            ].map((item) => (
              <div key={item.code} className='space-y-2 rounded-xl border p-3'>
                <Badge variant='outline'>{item.code}</Badge>
                <h4 className='text-sm font-medium'>{item.title}</h4>
                <p className='text-muted-foreground text-xs leading-relaxed'>
                  {item.body}
                </p>
              </div>
            ))}
          </div>
          <div className='flex flex-wrap gap-2'>
            <Button render={<Link to='/usage-logs' />}>
              {t('Open usage logs')}
              <ArrowRight />
            </Button>
            {playground && (
              <Button
                variant='outline'
                render={
                  <Link to={playground} search={{ model: model?.model_name }} />
                }
              >
                {t('Try in playground')}
              </Button>
            )}
            <Button
              variant='outline'
              render={<Link to='/help/api' search={{ endpoint: docId }} />}
            >
              {t('API Reference')}
            </Button>
          </div>
        </div>
      )}

      <div className='flex items-center justify-between gap-3 border-t pt-4'>
        <Button
          variant='ghost'
          disabled={step === 0}
          onClick={() => setStep(step - 1)}
        >
          <ArrowLeft />
          {t('Previous step')}
        </Button>
        {step < 2 ? (
          <Button disabled={!canContinue} onClick={() => setStep(step + 1)}>
            {t('Next step')}
            <ArrowRight />
          </Button>
        ) : (
          <Button variant='outline' onClick={() => setStep(0)}>
            {t('Configure another model')}
          </Button>
        )}
      </div>
    </div>
  )
}
