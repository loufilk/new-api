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
import { useState, useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getCCSwitchTokenModels } from '../../api'
import type { ApiKey, CCSwitchApp } from '../../types'
import {
  buildValidatedCCSwitchURL,
  ccSwitchModelsQueryKey,
} from './cc-switch-utils'

const APP_CONFIGS = {
  claude: {
    label: 'Claude',
    defaultName: 'My Claude',
    modelFields: [
      { key: 'model', labelKey: 'Primary Model', required: true },
      { key: 'haikuModel', labelKey: 'Haiku Model', required: false },
      { key: 'sonnetModel', labelKey: 'Sonnet Model', required: false },
      { key: 'opusModel', labelKey: 'Opus Model', required: false },
    ],
  },
  codex: {
    label: 'Codex',
    defaultName: 'My Codex',
    modelFields: [{ key: 'model', labelKey: 'Primary Model', required: true }],
  },
  gemini: {
    label: 'Gemini',
    defaultName: 'My Gemini',
    modelFields: [{ key: 'model', labelKey: 'Primary Model', required: true }],
  },
} as const

type AppType = CCSwitchApp

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  tokenKey: string
  token: ApiKey | null
  serverAddress?: string
}

export function CCSwitchDialog(props: Props) {
  const { t } = useTranslation()
  const [app, setApp] = useState<AppType>('claude')
  const [name, setName] = useState<string>(APP_CONFIGS.claude.defaultName)
  const [models, setModels] = useState<Record<string, string>>({})
  const [preparedUrl, setPreparedUrl] = useState('')
  const validation = useRef({ active: true })
  useEffect(() => {
    setPreparedUrl('')
    const attempt = { active: true }
    validation.current = attempt
    return () => {
      attempt.active = false
    }
  }, [
    props.open,
    props.token?.id,
    props.tokenKey,
    props.serverAddress,
    app,
    name,
    models,
  ])

  const {
    data: modelsData,
    isFetching,
    isError,
    refetch,
  } = useQuery({
    queryKey: ccSwitchModelsQueryKey(props.token, app),
    queryFn: async () => {
      if (!props.token) return { success: true, data: [] as string[] }
      return requireServerSuccess(
        await getCCSwitchTokenModels(props.token.id, app)
      )
    },
    enabled: props.open && !!props.token,
    staleTime: 5 * 60 * 1000,
  })

  const modelOptions = useMemo(() => {
    const items = modelsData?.data ?? []
    return items.map((modelName) => ({ value: modelName, label: modelName }))
  }, [modelsData?.data])

  useEffect(() => {
    if (props.open) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setModels({})
      setApp('claude')
      setName(APP_CONFIGS.claude.defaultName)
    }
  }, [props.open, props.token?.id])

  const currentConfig = APP_CONFIGS[app]
  const noModelsMessage = `${t('API Key')} ${t('Group')}: ${t('No models found')}`
  const hasNoModels = !isFetching && !isError && modelOptions.length === 0

  const handleAppChange = (val: string) => {
    const appVal = val as AppType
    setApp(appVal)
    setName(APP_CONFIGS[appVal].defaultName)
    setModels({})
  }

  const handleSubmit = async () => {
    if (preparedUrl) {
      window.open(preparedUrl, '_blank', 'noopener,noreferrer')
      setPreparedUrl('')
      props.onOpenChange(false)
      return
    }
    if (!models.model) {
      toast.warning(t('Please select a primary model'))
      return
    }
    if (!props.token || !props.tokenKey) return

    const attempt = validation.current
    const latest = await refetch()
    if (!attempt.active) return
    const availableModels = latest.data?.data ?? []
    if (latest.isError || availableModels.length === 0) {
      toast.warning(noModelsMessage)
      return
    }
    const url = buildValidatedCCSwitchURL(
      app,
      name,
      models,
      props.tokenKey,
      availableModels,
      props.serverAddress
    )
    if (!url) {
      toast.warning(t('The selected model is no longer available'))
      setModels({})
      return
    }
    setPreparedUrl(url)
  }

  const openDisabled =
    !props.token ||
    !props.tokenKey ||
    isFetching ||
    isError ||
    modelOptions.length === 0

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Import to CC Switch')}
      contentClassName='sm:max-w-md'
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button onClick={handleSubmit} disabled={openDisabled}>
            {preparedUrl ? t('Open CC Switch') : t('Continue')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <div className='space-y-2'>
          <Label>{t('Application')}</Label>
          <RadioGroup
            value={app}
            onValueChange={handleAppChange}
            className='flex gap-4'
          >
            {(
              Object.entries(APP_CONFIGS) as [
                AppType,
                (typeof APP_CONFIGS)[AppType],
              ][]
            ).map(([key, cfg]) => (
              <div key={key} className='flex items-center gap-2'>
                <RadioGroupItem value={key} id={`app-${key}`} />
                <Label htmlFor={`app-${key}`} className='cursor-pointer'>
                  {cfg.label}
                </Label>
              </div>
            ))}
          </RadioGroup>
        </div>

        <div className='space-y-2'>
          <Label htmlFor='cc-switch-name'>{t('Name')}</Label>
          <Input
            id='cc-switch-name'
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder={currentConfig.defaultName}
          />
        </div>

        {hasNoModels && (
          <p className='text-muted-foreground text-sm' role='status'>
            {noModelsMessage}
          </p>
        )}

        {currentConfig.modelFields.map((field) => (
          <div key={field.key} className='space-y-2'>
            <Label htmlFor={`cc-switch-${field.key}`} required={field.required}>
              {t(field.labelKey)}
            </Label>
            <Combobox
              id={`cc-switch-${field.key}`}
              aria-label={t(field.labelKey)}
              options={modelOptions}
              value={models[field.key] || ''}
              onValueChange={(v) =>
                setModels((prev) => ({ ...prev, [field.key]: v ?? '' }))
              }
              placeholder={t('Select or enter model name')}
              emptyText={t('No models found')}
            />
          </div>
        ))}
      </div>
    </Dialog>
  )
}
