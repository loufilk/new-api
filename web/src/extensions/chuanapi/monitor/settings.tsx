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
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { FormDirtyIndicator } from '@/features/system-settings/components/form-dirty-indicator'
import {
  SettingsControlGroup,
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '@/features/system-settings/components/settings-form-layout'
import { SettingsPageFormActions } from '@/features/system-settings/components/settings-page-context'
import { SettingsSection } from '@/features/system-settings/components/settings-section'

import {
  getMonitorSettings,
  getMonitorTargets,
  saveMonitorSettings,
} from './settings-api'
import type { MonitorConfig, MonitorSettingsData } from './types'

const settingsKey = ['model-monitor-settings']
const schema = z.object({
  enabled: z.boolean(),
  title: z.string().max(120, 'monitor.settings.invalid'),
  description: z.string().max(500, 'monitor.settings.invalid'),
  groups: z.record(z.string(), z.string().max(80, 'monitor.settings.invalid')),
  hidden_groups: z.record(z.string(), z.boolean()),
})

export function MonitorSettings() {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: settingsKey,
    queryFn: ({ signal }) => getMonitorSettings(signal),
    refetchOnWindowFocus: false,
    meta: { errorToast: false },
  })
  if (query.isPending) return <LoadingState />
  if (!query.data) {
    return (
      <ErrorState
        title={t('monitor.settings.loadFailed')}
        onRetry={() => void query.refetch()}
      />
    )
  }
  return <MonitorSettingsForm settings={query.data} />
}

function MonitorSettingsForm(props: { settings: MonitorSettingsData }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    defaultValues: props.settings.config,
  })
  const targets = useQuery({
    queryKey: ['model-monitor-settings-targets'],
    queryFn: ({ signal }) => getMonitorTargets(signal),
    enabled: props.settings.environment_enabled,
    refetchOnWindowFocus: false,
    retry: false,
    meta: { errorToast: false },
  })
  const resetForm = (config: MonitorConfig) => {
    // Keep mounted fields in the form defaults when the server omits blank
    // names and visible groups, so a successful save leaves the form clean.
    const current = form.getValues()
    form.reset({
      ...config,
      groups: {
        ...Object.fromEntries(
          Object.keys(current.groups).map((id) => [id, ''])
        ),
        ...config.groups,
      },
      hidden_groups: {
        ...Object.fromEntries(
          Object.keys(current.hidden_groups).map((id) => [id, false])
        ),
        ...config.hidden_groups,
      },
    })
  }
  const save = useMutation({
    mutationFn: saveMonitorSettings,
    onSuccess(config) {
      resetForm(config)
      queryClient.setQueryData(settingsKey, { ...props.settings, config })
      void queryClient.invalidateQueries({ queryKey: ['model-monitor'] })
      toast.success(t('monitor.settings.saved'))
    },
  })
  const groups = new Map(
    (targets.data ?? []).map((target) => [target.id, target])
  )
  for (const id of [
    ...Object.keys(props.settings.config.groups),
    ...Object.keys(props.settings.config.hidden_groups),
  ]) {
    if (!groups.has(id)) groups.set(id, { id, number: 0, model: '' })
  }
  const submit = form.handleSubmit((values) => save.mutate(values))

  return (
    <SettingsSection title={t('monitor.settings.title')}>
      <FormDirtyIndicator isDirty={form.formState.isDirty} />
      <Form {...form}>
        <SettingsForm onSubmit={submit}>
          <SettingsPageFormActions
            onSave={submit}
            onReset={() => resetForm(props.settings.config)}
            isSaving={save.isPending}
            isSaveDisabled={!form.formState.isDirty}
          />
          {!props.settings.environment_enabled && (
            <Alert>
              <AlertDescription>
                {t('monitor.settings.environmentDisabled')}
              </AlertDescription>
            </Alert>
          )}
          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('monitor.settings.enabled')}</FormLabel>
                  <FormDescription>
                    {t('monitor.settings.enabledHelp')}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    disabled={save.isPending}
                  />
                </FormControl>
                <FormMessage />
              </SettingsSwitchItem>
            )}
          />
          <FormField
            control={form.control}
            name='title'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('monitor.settings.pageTitle')}</FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    maxLength={120}
                    placeholder={t('monitor.title')}
                    disabled={save.isPending}
                  />
                </FormControl>
                <FormDescription>
                  {t('monitor.settings.defaultText')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='description'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('monitor.settings.description')}</FormLabel>
                <FormControl>
                  <Textarea
                    {...field}
                    maxLength={500}
                    placeholder={t('monitor.subtitle')}
                    disabled={save.isPending}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <div className='space-y-2'>
            <h3 className='font-medium'>{t('monitor.settings.groups')}</h3>
            <p className='text-muted-foreground text-sm'>
              {t('monitor.settings.groupsHelp')}
            </p>
          </div>
          {targets.isPending && targets.isFetching && <LoadingState />}
          {targets.isError && (
            <ErrorState
              title={t('monitor.settings.targetsFailed')}
              description={t('monitor.settings.namesPreserved')}
              onRetry={() => void targets.refetch()}
            />
          )}
          {[...groups.values()].map((target) => (
            <SettingsControlGroup key={target.id}>
              <FormField
                control={form.control}
                name={`hidden_groups.${target.id}`}
                defaultValue={false}
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>
                        {t('monitor.settings.showGroup', {
                          group: target.number
                            ? t('monitor.route', { number: target.number })
                            : t('monitor.settings.unlisted'),
                        })}
                      </FormLabel>
                      <FormDescription>
                        {t('monitor.settings.showGroupHelp')}
                      </FormDescription>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        checked={!field.value}
                        onCheckedChange={(checked) => field.onChange(!checked)}
                        disabled={save.isPending}
                      />
                    </FormControl>
                    <FormMessage />
                  </SettingsSwitchItem>
                )}
              />
              <FormField
                control={form.control}
                name={`groups.${target.id}`}
                defaultValue=''
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {target.number
                        ? t('monitor.route', { number: target.number })
                        : t('monitor.settings.unlisted')}{' '}
                      {target.model}
                    </FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        value={field.value ?? ''}
                        maxLength={80}
                        placeholder={t('monitor.settings.groupPlaceholder')}
                        disabled={save.isPending}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SettingsControlGroup>
          ))}
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
