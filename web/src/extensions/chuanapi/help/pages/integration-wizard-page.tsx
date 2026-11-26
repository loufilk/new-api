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
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import { ApiCodeBlock } from '../components/api-code-block'
import { DocsShell } from '../components/docs-shell'
import { getDocsOrigin } from '../lib/docs-utils'
import {
  buildWizardConfig,
  getWizardAddresses,
  getWizardCatalogue,
  lineModelProtocols,
  normalizeWizardAddress,
  wizardClients,
  wizardProtocols,
  type WizardOS,
} from '../lib/integration-wizard'

const TEMPLATE_MODEL = 'MODEL_ID'

export type IntegrationWizardProps = {
  publicLine?: string
  previewModel?: string
}

export function IntegrationWizardPage(props: IntegrationWizardProps) {
  return (
    <DocsShell>
      <IntegrationWizard key={JSON.stringify(props)} {...props} />
    </DocsShell>
  )
}

export function IntegrationWizard({
  publicLine,
  previewModel,
}: IntegrationWizardProps) {
  const { t } = useTranslation()
  const id = useId()
  const query = useQuery({
    queryKey: ['help', 'wizard-model-lines'],
    queryFn: ({ signal }) => getWizardCatalogue(signal),
    refetchInterval: 15_000,
  })
  const connectionQuery = useQuery({
    queryKey: ['help', 'wizard-addresses'],
    queryFn: ({ signal }) => getWizardAddresses(signal),
  })
  const [lineChoice, setLineChoice] = useState(publicLine)
  const [modelChoice, setModelChoice] = useState(previewModel)
  const [clientChoice, setClientChoice] = useState<string>()
  const [protocolChoice, setProtocolChoice] = useState<string>()
  const [addressChoice, setAddressChoice] = useState<string>()
  const [os, setOS] = useState<WizardOS>('unix')

  const published = query.isError ? undefined : query.data
  const models = published?.models ?? []
  const usableLines = (published?.public_lines ?? []).filter((line) =>
    models.some((model) =>
      lineModelProtocols(model, line.id).some((p) =>
        Object.hasOwn(wizardProtocols, p)
      )
    )
  )
  const currentLine =
    usableLines.find((line) => line.id === lineChoice) ?? usableLines[0]
  const availableModels = currentLine
    ? models.filter((model) =>
        lineModelProtocols(model, currentLine.id).some((p) =>
          Object.hasOwn(wizardProtocols, p)
        )
      )
    : []
  const currentModel =
    (currentLine && (!lineChoice || lineChoice === currentLine.id)
      ? availableModels.find((model) => model.model_name === modelChoice)
      : undefined) ?? availableModels[0]
  const eligibleProtocols = currentLine
    ? lineModelProtocols(currentModel, currentLine.id)
    : Object.keys(wizardProtocols)
  const availableClients = wizardClients.filter((client) =>
    client.protocols.some((protocol) => eligibleProtocols.includes(protocol))
  )
  const selectedClient =
    availableClients.find((client) => client.id === clientChoice) ??
    availableClients[0]
  const clientProtocols =
    selectedClient?.protocols.filter((p) => eligibleProtocols.includes(p)) ?? []
  const selectedProtocol =
    clientProtocols.find((protocol) => protocol === protocolChoice) ??
    clientProtocols[0]
  const configuredAddresses = !connectionQuery.isError
    ? connectionQuery.data?.addresses
    : undefined
  const fallbackRoot = !connectionQuery.isError
    ? (connectionQuery.data?.fallback ??
      normalizeWizardAddress(getDocsOrigin()))
    : normalizeWizardAddress(getDocsOrigin())
  const availableAddresses = (
    configuredAddresses?.length
      ? configuredAddresses
      : fallbackRoot
        ? [{ name: t('Default API address'), root: fallbackRoot }]
        : []
  ).map((entry, index) => ({
    ...entry,
    key: JSON.stringify([entry.root, entry.name, index]),
  }))
  const selectedAddress =
    availableAddresses.find((address) => address.key === addressChoice) ??
    availableAddresses[0]
  // Removed or tampered intents must not prefill a different real Key group.
  const unavailableIntent =
    (lineChoice && currentLine?.id !== lineChoice) ||
    (modelChoice && currentModel?.model_name !== modelChoice)
  const keySetup =
    currentLine && currentModel && !unavailableIntent
      ? {
          create: true as const,
          publicLine: currentLine.id,
          model: currentModel.model_name,
        }
      : {}
  const config =
    selectedClient && selectedProtocol && selectedAddress
      ? buildWizardConfig(
          selectedClient,
          selectedProtocol,
          selectedAddress.root,
          currentModel?.model_name ?? TEMPLATE_MODEL,
          os
        )
      : null

  return (
    <article className='space-y-6'>
      <header className='space-y-3'>
        <h1 className='text-foreground text-3xl font-bold tracking-tight'>
          {t('Client setup wizard')}
        </h1>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Choose a published model group, a model, a client and a connection address to generate setup instructions.'
          )}
        </p>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Public group names are for reference only. The group and permissions on your API Key determine actual routing.'
          )}
        </p>
      </header>

      {query.isPending && (
        <LoadingState inline size='sm' message={t('Loading...')} />
      )}
      {!query.isPending && !currentLine && (
        <div
          className='border-border bg-muted/40 space-y-2 rounded-lg border p-3 text-sm'
          role='status'
        >
          <p>
            {t(
              'No published group is available. The examples below use a placeholder for your model ID.'
            )}
          </p>
          {query.isError && (
            <Button
              size='sm'
              variant='outline'
              onClick={() => void query.refetch()}
            >
              {t('Retry')}
            </Button>
          )}
        </div>
      )}
      <section className='border-border bg-card grid min-w-0 gap-4 rounded-xl border p-4 sm:grid-cols-2'>
        {currentLine && (
          <>
            <div className='min-w-0 space-y-1.5'>
              <Label htmlFor={id + '-group'}>
                {t('Published model group')}
              </Label>
              <NativeSelect
                id={id + '-group'}
                className='w-full'
                value={currentLine.id}
                onChange={(event) => {
                  setLineChoice(event.target.value)
                  setModelChoice(undefined)
                  setProtocolChoice(undefined)
                }}
              >
                {usableLines.map((line) => (
                  <NativeSelectOption key={line.id} value={line.id}>
                    {line.label}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
            <div className='min-w-0 space-y-1.5'>
              <Label htmlFor={id + '-model'}>{t('Model')}</Label>
              <NativeSelect
                id={id + '-model'}
                className='w-full'
                value={currentModel?.model_name ?? ''}
                onChange={(event) => {
                  setModelChoice(event.target.value)
                  setProtocolChoice(undefined)
                }}
              >
                {availableModels.map((model) => (
                  <NativeSelectOption
                    key={model.model_name}
                    value={model.model_name}
                  >
                    {model.model_name}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
          </>
        )}
        <div className='min-w-0 space-y-1.5'>
          <Label htmlFor={id + '-client'}>{t('Client')}</Label>
          <NativeSelect
            id={id + '-client'}
            className='w-full'
            value={selectedClient?.id ?? ''}
            onChange={(event) => {
              setClientChoice(event.target.value)
              setProtocolChoice(undefined)
            }}
          >
            {availableClients.map((client) => (
              <NativeSelectOption key={client.id} value={client.id}>
                {client.name}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <div className='min-w-0 space-y-1.5'>
          <Label htmlFor={id + '-protocol'}>{t('Protocol')}</Label>
          <NativeSelect
            id={id + '-protocol'}
            className='w-full'
            value={selectedProtocol ?? ''}
            onChange={(event) => setProtocolChoice(event.target.value)}
          >
            {clientProtocols.map((protocol) => (
              <NativeSelectOption key={protocol} value={protocol}>
                {wizardProtocols[protocol]}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <div className='min-w-0 space-y-1.5'>
          <Label htmlFor={id + '-address'}>{t('Connection address')}</Label>
          <NativeSelect
            id={id + '-address'}
            className='w-full'
            disabled={!selectedAddress}
            value={selectedAddress?.key ?? ''}
            onChange={(event) => setAddressChoice(event.target.value)}
          >
            {!selectedAddress && (
              <NativeSelectOption value=''>
                {t('No verified connection address')}
              </NativeSelectOption>
            )}
            {availableAddresses.map((address) => (
              <NativeSelectOption key={address.key} value={address.key}>
                {address.name}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <div className='min-w-0 space-y-1.5'>
          <Label htmlFor={id + '-os'}>{t('Operating system')}</Label>
          <NativeSelect
            id={id + '-os'}
            className='w-full'
            value={os}
            onChange={(event) =>
              setOS(event.target.value === 'windows' ? 'windows' : 'unix')
            }
          >
            <NativeSelectOption value='unix'>macOS / Linux</NativeSelectOption>
            <NativeSelectOption value='windows'>Windows</NativeSelectOption>
          </NativeSelect>
        </div>
      </section>

      {!query.isPending && !connectionQuery.isPending && config && (
        <section className='space-y-3'>
          <h2 className='text-xl font-semibold'>{t('Setup instructions')}</h2>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Create or choose an API Key in the console. Set WANCHUAN_API_KEY locally for commands, or replace the placeholder in your client settings. Never paste your Key into this page.'
            )}
          </p>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Merge these settings with your existing configuration. Test in your client and check the request time, model and status in your usage logs. A model request may incur charges.'
            )}
          </p>
          <ApiCodeBlock label={config.label} code={config.setup} />
          <div className='flex flex-wrap gap-2'>
            <Button
              variant='outline'
              render={<Link to='/keys' search={keySetup} />}
            >
              {t('API Keys')}
            </Button>
            {selectedClient && (
              <Button
                variant='outline'
                render={
                  <Link
                    to='/help/guides/$slug'
                    params={{ slug: selectedClient.guide }}
                  />
                }
              >
                {t('Read the full guide')}
              </Button>
            )}
          </div>
        </section>
      )}
    </article>
  )
}
