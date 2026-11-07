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

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { SettingsSwitchField } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import {
  canEnableAutomaticTests,
  getModelTestSettings,
  modelTestSettingsSchema,
  saveModelTestSettings,
  validateModelTestTargets,
  type ModelTestSettings,
  type ModelTestSettingsInput,
} from './model-test-settings'
import { ModelTestTargetEditor } from './model-test-target-editor'

export function ModelTestSection() {
  const user = useAuthStore((state) => state.auth.user)
  if (!user || user.role < ROLE.ADMIN) return null
  return <AdminModelTestSettings key={user.id} userId={user.id} />
}

function AdminModelTestSettings(props: { userId: number }) {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['model-test-settings', props.userId],
    queryFn: ({ signal }) => getModelTestSettings(signal),
    staleTime: 0,
    refetchOnMount: 'always',
    refetchOnWindowFocus: false,
    retry: false,
    meta: { errorToast: false },
  })
  if (query.isError) {
    return (
      <ErrorState
        title={t('Failed to load settings')}
        onRetry={() => void query.refetch()}
      />
    )
  }
  if (!query.isFetchedAfterMount || !query.data) return <LoadingState />
  return (
    <ModelTestSettingsForm
      key={query.data.version}
      settings={query.data}
      userId={props.userId}
    />
  )
}

function ModelTestSettingsForm(props: {
  settings: ModelTestSettings
  userId: number
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const form = useForm<ModelTestSettingsInput>({
    resolver: zodResolver(modelTestSettingsSchema),
    defaultValues: modelTestSettingsSchema.parse(props.settings),
  })
  const values = form.watch()
  const { isDirty } = form.formState
  const valid =
    validateModelTestTargets(values, props.settings.catalogue) &&
    (!values.automatic_enabled || props.settings.engine_ready)
  const automaticReady =
    props.settings.engine_ready && valid && canEnableAutomaticTests(values)
  const mutation = useMutation({
    mutationFn: saveModelTestSettings,
    onSuccess: async (_, input) => {
      client.setQueryData<Record<string, unknown>>(
        ['status'],
        (previous) =>
          previous && { ...previous, model_test_enabled: input.enabled }
      )
      await client.invalidateQueries({ queryKey: ['model-tests'] })
      await client.invalidateQueries({
        queryKey: ['model-test-settings', props.userId],
      })
      toast.success(t('Setting updated successfully'))
    },
    onError: (error) => handleServerError(error, t('Failed to update setting')),
  })
  return (
    <SettingsSection title={t('intelligence.title')}>
      <form
        className='space-y-5'
        onSubmit={form.handleSubmit((input) => {
          if (valid) mutation.mutate(input)
        })}
      >
        <SettingsSwitchField
          controlId='model-test-enabled'
          label={t('Enable model intelligence monitoring')}
          description={t(
            'Turning monitoring off stops new tests. Active calls can still finish and incur charges.'
          )}
          checked={values.enabled}
          onCheckedChange={(enabled) => {
            form.setValue('enabled', enabled, { shouldDirty: true })
            if (!enabled) {
              form.setValue('automatic_enabled', false, { shouldDirty: true })
            }
          }}
          disabled={mutation.isPending}
        />
        <p className='text-muted-foreground text-sm'>
          {t(
            'Automatic model tests are off by default. Each run makes up to 3 paid calls using normal billing. Saving targets alone does not run tests.'
          )}
        </p>
        <div className='grid gap-4 sm:grid-cols-2'>
          <div className='space-y-2'>
            <Label htmlFor='model-test-period'>
              {t('Default interval (seconds)')}
            </Label>
            <Input
              id='model-test-period'
              type='number'
              min={60}
              max={604800}
              step={1}
              disabled={mutation.isPending}
              {...form.register('default_period_secs', { valueAsNumber: true })}
            />
          </div>
        </div>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Choose a group and a model within that group. Tests use normal group routing. The default interval is one hour. Each group and model can have only one target.'
          )}
        </p>
        {values.targets.map((target, index) => (
          <ModelTestTargetEditor
            key={target.id}
            target={target}
            catalogue={props.settings.catalogue}
            disabled={mutation.isPending}
            onChange={(next) =>
              form.setValue(
                'targets',
                values.targets.map((item, i) => (i === index ? next : item)),
                { shouldDirty: true }
              )
            }
            onRemove={() =>
              form.setValue(
                'targets',
                values.targets.filter((_, i) => i !== index),
                { shouldDirty: true }
              )
            }
          />
        ))}
        <Button
          type='button'
          variant='outline'
          disabled={mutation.isPending}
          onClick={() =>
            form.setValue(
              'targets',
              [
                ...values.targets,
                {
                  id: crypto.randomUUID(),
                  group: '',
                  model: '',
                  channel_id: 0,
                  enabled: true,
                  period_secs: 0,
                  max_output_tokens: 0,
                  max_run_quota: 0,
                },
              ],
              { shouldDirty: true }
            )
          }
        >
          {t('Add target')}
        </Button>
        {!props.settings.engine_ready && (
          <p role='status' className='text-muted-foreground text-sm'>
            {t(
              'Model reference data is not ready. Automatic tests cannot be enabled.'
            )}
          </p>
        )}
        <SettingsSwitchField
          controlId='model-test-automatic'
          label={t('Enable automatic model tests')}
          description={t(
            'Requires enabled tests and at least one enabled target.'
          )}
          checked={values.automatic_enabled}
          onCheckedChange={(enabled) =>
            form.setValue('automatic_enabled', enabled, { shouldDirty: true })
          }
          disabled={
            mutation.isPending || (!values.automatic_enabled && !automaticReady)
          }
        />
        {!valid && (
          <p role='alert' className='text-destructive text-sm'>
            {t(
              'Complete all target selections before saving. Intervals must be 60–604800 seconds, or 0 to inherit for a target.'
            )}
          </p>
        )}
        <SettingsPageFormActions
          onSave={() =>
            void form.handleSubmit((input) => {
              if (valid) mutation.mutate(input)
            })()
          }
          isSaving={mutation.isPending}
          isSaveDisabled={!valid || !isDirty}
        />
      </form>
    </SettingsSection>
  )
}
