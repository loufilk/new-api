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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronDown } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import {
  SettingsControlGroup,
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '@/features/system-settings/components/settings-form-layout'
import { getServerErrorMessage } from '@/lib/server-error-message'

import {
  loadUsdtSettings,
  saveUsdtSettings,
  toUsdtSettingsForm,
  USDT_ADMIN_QUERY_KEY,
  USDT_NETWORKS,
  usdtSettingsFormSchema,
  type UsdtSettings,
  type UsdtSettingsForm,
} from './usdt-settings-api'

export function UsdtSettingsSection(props: { complianceConfirmed: boolean }) {
  const { t } = useTranslation()
  const [hasOpened, setHasOpened] = useState(false)
  const settings = useQuery({
    queryKey: USDT_ADMIN_QUERY_KEY,
    queryFn: ({ signal }) => loadUsdtSettings(signal),
    enabled: hasOpened,
    meta: { errorToast: false },
  })

  let content = null
  if (hasOpened) {
    if (settings.data) {
      content = (
        <UsdtSettingsEditor
          settings={settings.data}
          complianceConfirmed={props.complianceConfirmed}
        />
      )
    } else if (settings.isPending) {
      content = <LoadingState inline />
    } else {
      content = (
        <ErrorState
          className='min-h-32'
          title={t('Could not load USDT settings.')}
          onRetry={() => void settings.refetch()}
        />
      )
    }
  }

  return (
    <Collapsible
      className='mt-2 rounded-xl border'
      onOpenChange={(open) => {
        if (open) setHasOpened(true)
      }}
    >
      <h3>
        <CollapsibleTrigger
          render={<Button variant='ghost' type='button' />}
          className='group/usdt h-auto w-full justify-between px-4 py-3 text-base'
        >
          {t('USDT recharge')}
          <ChevronDown
            aria-hidden='true'
            className='size-4 shrink-0 transition-transform group-aria-expanded/usdt:rotate-180'
          />
        </CollapsibleTrigger>
      </h3>
      <CollapsibleContent keepMounted className='px-4 pb-4'>
        {content}
      </CollapsibleContent>
    </Collapsible>
  )
}

function UsdtSettingsEditor(props: {
  settings: UsdtSettings
  complianceConfirmed: boolean
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const form = useForm<UsdtSettingsForm>({
    resolver: zodResolver(usdtSettingsFormSchema),
    defaultValues: toUsdtSettingsForm(props.settings),
  })
  const { isDirty } = form.formState
  const { reset } = form
  const enabled = useWatch({ control: form.control, name: 'enabled' })
  const canEnable = props.settings.configured && props.complianceConfirmed
  useEffect(() => {
    if (!isDirty) reset(toUsdtSettingsForm(props.settings))
  }, [isDirty, props.settings, reset])

  const save = useMutation({
    mutationFn: saveUsdtSettings,
    retry: false,
    meta: { errorToast: false },
    onSuccess: (saved) => {
      queryClient.setQueryData(USDT_ADMIN_QUERY_KEY, saved)
      reset(toUsdtSettingsForm(saved))
      toast.success(t('USDT settings saved.'))
    },
  })

  return (
    <Form {...form}>
      <SettingsForm
        aria-label={t('USDT recharge')}
        className='gap-y-4'
        onSubmit={form.handleSubmit((values) => {
          if (values.enabled && !canEnable) return
          save.mutate(values)
        })}
      >
        <div className='space-y-2'>
          <Badge variant={props.settings.configured ? 'secondary' : 'outline'}>
            {props.settings.configured
              ? t('Gateway credentials configured')
              : t('Gateway configuration missing')}
          </Badge>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Set merchant credentials on the server. Configure receiving addresses and confirmations in the GM Pay gateway, never in this console.'
            )}
          </p>
          {!props.complianceConfirmed && (
            <p role='status' className='text-muted-foreground text-sm'>
              {t('Confirm the payment compliance terms before enabling USDT.')}
            </p>
          )}
        </div>

        <FormField
          control={form.control}
          name='enabled'
          render={({ field }) => (
            <SettingsSwitchItem>
              <SettingsSwitchContent>
                <FormLabel>{t('Enable USDT recharge')}</FormLabel>
                <FormMessage />
              </SettingsSwitchContent>
              <FormControl>
                <Switch
                  checked={field.value}
                  onCheckedChange={field.onChange}
                  disabled={save.isPending || (!canEnable && !field.value)}
                />
              </FormControl>
            </SettingsSwitchItem>
          )}
        />

        {(['min_amount', 'max_amount'] as const).map((name) => (
          <FormField
            key={name}
            control={form.control}
            name={name}
            render={({ field }) => (
              <FormItem>
                <FormLabel>
                  {name === 'min_amount'
                    ? t('Minimum recharge (CNY)')
                    : t('Maximum recharge (CNY)')}
                </FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    inputMode='decimal'
                    maxLength={10}
                    disabled={save.isPending}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        ))}

        <fieldset className='min-w-0 space-y-3'>
          <legend className='mb-2 text-sm font-medium'>
            {t('USDT networks')}
          </legend>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Bonuses are credited separately and do not reduce the payment amount.'
            )}
          </p>
          {USDT_NETWORKS.map((network) => (
            <SettingsControlGroup
              key={network.id}
              className='grid items-center space-y-0 gap-x-5 gap-y-2 sm:grid-cols-2'
            >
              <FormField
                control={form.control}
                name={`networks.${network.id}.enabled`}
                render={({ field }) => (
                  <SettingsSwitchItem className='px-0'>
                    <FormLabel>{network.label}</FormLabel>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                        disabled={save.isPending}
                      />
                    </FormControl>
                  </SettingsSwitchItem>
                )}
              />
              <FormField
                control={form.control}
                name={`networks.${network.id}.bonus`}
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {t('{{network}} bonus (%)', { network: network.label })}
                    </FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        inputMode='decimal'
                        maxLength={6}
                        disabled={save.isPending}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SettingsControlGroup>
          ))}
        </fieldset>

        {save.isError && (
          <Alert variant='destructive'>
            <AlertTitle>{t('Could not save USDT settings.')}</AlertTitle>
            <AlertDescription>
              {getServerErrorMessage(save.error)}
            </AlertDescription>
          </Alert>
        )}
        <div className='flex justify-end'>
          <Button
            type='submit'
            disabled={!isDirty || save.isPending || (enabled && !canEnable)}
          >
            {t('Save USDT settings')}
          </Button>
        </div>
      </SettingsForm>
    </Form>
  )
}
